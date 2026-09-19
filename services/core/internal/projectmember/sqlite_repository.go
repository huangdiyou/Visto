package projectmember

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (repository *SQLiteRepository) List(
	ctx context.Context,
	workspaceID string,
	projectID string,
	now time.Time,
) ([]Member, error) {
	rows, err := repository.db.QueryContext(ctx, memberSelect+`
		WHERE project_memberships.workspace_id = ?
			AND project_memberships.project_id = ?
			AND project_memberships.status != 'removed'
		ORDER BY
			CASE project_memberships.role_key
				WHEN 'primary_owner' THEN 0
				WHEN 'supervisor' THEN 1
				WHEN 'member' THEN 2
				ELSE 3
			END,
			project_memberships.created_at
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project members: %w", err)
	}
	defer rows.Close()
	items := make([]Member, 0)
	for rows.Next() {
		item, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		applyExpiry(&item, now)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project members: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) Add(
	ctx context.Context,
	record addRecord,
) (Member, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, fmt.Errorf("begin project member add: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveWorkspaceMember(
		ctx,
		tx,
		record.WorkspaceID,
		record.UserID,
	); err != nil {
		return Member{}, err
	}
	if err := requireProject(ctx, tx, record.WorkspaceID, record.ProjectID); err != nil {
		return Member{}, err
	}
	timestamp := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_memberships (
			id, workspace_id, project_id, user_id, role_key, permissions_json,
			status, created_by, expires_at, joined_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)
	`,
		record.ID,
		record.WorkspaceID,
		record.ProjectID,
		record.UserID,
		record.RoleKey,
		record.Permissions,
		record.CreatedBy,
		nullableTime(record.ExpiresAt),
		timestamp,
		timestamp,
		timestamp,
	); err != nil {
		if isUniqueConstraint(err) {
			return Member{}, ErrAlreadyMember
		}
		return Member{}, fmt.Errorf("add project member: %w", err)
	}
	item, err := repository.get(ctx, tx, record.WorkspaceID, record.ProjectID, record.ID, record.Now)
	if err != nil {
		return Member{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, fmt.Errorf("commit project member add: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) CreateGuest(
	ctx context.Context,
	record createGuestRecord,
) (Member, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, fmt.Errorf("begin project guest creation: %w", err)
	}
	defer tx.Rollback()
	if err := requireProject(ctx, tx, record.WorkspaceID, record.ProjectID); err != nil {
		return Member{}, err
	}
	if record.Email != nil {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM users
			WHERE lower(email) = lower(?)
		`, *record.Email).Scan(&count); err != nil {
			return Member{}, fmt.Errorf("check project guest email: %w", err)
		}
		if count > 0 {
			return Member{}, ErrEmailInUse
		}
	}
	timestamp := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, email, display_name, status, locale, created_at, updated_at
		) VALUES (?, ?, ?, 'active', ?, ?, ?)
	`, record.UserID, record.Email, record.DisplayName, record.Locale,
		timestamp, timestamp); err != nil {
		return Member{}, fmt.Errorf("create project guest user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status,
			joined_at, created_at, updated_at
		) VALUES (?, ?, ?, 'guest', 'active', ?, ?, ?)
	`, record.MembershipID, record.WorkspaceID, record.UserID,
		timestamp, timestamp, timestamp); err != nil {
		return Member{}, fmt.Errorf("create project guest workspace membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_memberships (
			id, workspace_id, project_id, user_id, role_key, permissions_json,
			status, created_by, expires_at, joined_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'guest', ?, 'active', ?, ?, ?, ?, ?)
	`, record.ProjectMemberID, record.WorkspaceID, record.ProjectID,
		record.UserID, record.Permissions, record.CreatedBy,
		formatTime(record.ExpiresAt), timestamp, timestamp, timestamp); err != nil {
		return Member{}, fmt.Errorf("create project guest membership: %w", err)
	}
	item, err := repository.get(
		ctx,
		tx,
		record.WorkspaceID,
		record.ProjectID,
		record.ProjectMemberID,
		record.Now,
	)
	if err != nil {
		return Member{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, fmt.Errorf("commit project guest creation: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) Update(
	ctx context.Context,
	record updateRecord,
) (Member, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, fmt.Errorf("begin project member update: %w", err)
	}
	defer tx.Rollback()
	current, err := repository.get(
		ctx,
		tx,
		record.WorkspaceID,
		record.ProjectID,
		record.ID,
		record.Now,
	)
	if err != nil {
		return Member{}, err
	}
	if current.RoleKey == "primary_owner" {
		return Member{}, ErrPrimaryOwnerProtected
	}
	if current.Revision != record.Revision {
		return Member{}, ErrRevisionConflict
	}
	timestamp := formatTime(record.Now)
	result, err := tx.ExecContext(ctx, `
		UPDATE project_memberships
		SET role_key = ?,
			permissions_json = ?,
			status = ?,
			expires_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND project_id = ?
			AND revision = ?
			AND status != 'removed'
	`,
		record.RoleKey,
		record.Permissions,
		record.Status,
		nullableTime(record.ExpiresAt),
		timestamp,
		record.ID,
		record.WorkspaceID,
		record.ProjectID,
		record.Revision,
	)
	if err != nil {
		return Member{}, fmt.Errorf("update project member: %w", err)
	}
	if err := requireRowsAffected(result); err != nil {
		return Member{}, err
	}
	item, err := repository.get(ctx, tx, record.WorkspaceID, record.ProjectID, record.ID, record.Now)
	if err != nil {
		return Member{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, fmt.Errorf("commit project member update: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) Remove(
	ctx context.Context,
	input RemoveInput,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin project member removal: %w", err)
	}
	defer tx.Rollback()
	current, err := repository.get(
		ctx,
		tx,
		input.WorkspaceID,
		input.ProjectID,
		input.ID,
		now,
	)
	if err != nil {
		return err
	}
	if current.RoleKey == "primary_owner" {
		return ErrPrimaryOwnerProtected
	}
	if current.Revision != input.Revision {
		return ErrRevisionConflict
	}
	timestamp := formatTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE project_memberships
		SET status = 'removed',
			removed_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND project_id = ?
			AND revision = ?
			AND status != 'removed'
	`, timestamp, timestamp, input.ID, input.WorkspaceID, input.ProjectID, input.Revision)
	if err != nil {
		return fmt.Errorf("remove project member: %w", err)
	}
	if err := requireRowsAffected(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project member removal: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) Transfer(
	ctx context.Context,
	record transferRecord,
) (Member, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, fmt.Errorf("begin project transfer: %w", err)
	}
	defer tx.Rollback()
	if err := requireProject(ctx, tx, record.WorkspaceID, record.ProjectID); err != nil {
		return Member{}, err
	}
	if err := requireActiveWorkspaceMember(
		ctx,
		tx,
		record.WorkspaceID,
		record.NewPrimaryUserID,
	); err != nil {
		return Member{}, err
	}
	var currentMembershipID, currentUserID string
	err = tx.QueryRowContext(ctx, `
		SELECT id, user_id
		FROM project_memberships
		WHERE workspace_id = ?
			AND project_id = ?
			AND role_key = 'primary_owner'
			AND status = 'active'
	`, record.WorkspaceID, record.ProjectID).Scan(
		&currentMembershipID,
		&currentUserID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("read current primary owner: %w", err)
	}
	if record.ActorWorkspaceRole != "owner" && record.ActorUserID != currentUserID {
		return Member{}, ErrTransferForbidden
	}
	timestamp := formatTime(record.Now)
	if currentUserID != record.NewPrimaryUserID {
		role, status := formerOwnerRoleAndStatus(record.FormerOwnerAction)
		var removedAt any
		if status == "removed" {
			removedAt = timestamp
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE project_memberships
			SET role_key = ?,
				status = ?,
				removed_at = ?,
				revision = revision + 1,
				updated_at = ?
			WHERE id = ?
				AND workspace_id = ?
				AND project_id = ?
		`, role, status, removedAt, timestamp, currentMembershipID,
			record.WorkspaceID, record.ProjectID); err != nil {
			return Member{}, fmt.Errorf("update former primary owner: %w", err)
		}
		if err := repository.upsertPrimaryOwner(ctx, tx, record); err != nil {
			return Member{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE projects
		SET primary_owner_user_id = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, record.NewPrimaryUserID, timestamp, record.ProjectID, record.WorkspaceID); err != nil {
		return Member{}, fmt.Errorf("update project primary owner: %w", err)
	}
	item, err := repository.getByUser(
		ctx,
		tx,
		record.WorkspaceID,
		record.ProjectID,
		record.NewPrimaryUserID,
		record.Now,
	)
	if err != nil {
		return Member{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, fmt.Errorf("commit project transfer: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) upsertPrimaryOwner(
	ctx context.Context,
	tx *sql.Tx,
	record transferRecord,
) error {
	var existingID string
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM project_memberships
		WHERE workspace_id = ?
			AND project_id = ?
			AND user_id = ?
			AND status != 'removed'
	`, record.WorkspaceID, record.ProjectID, record.NewPrimaryUserID).Scan(&existingID)
	timestamp := formatTime(record.Now)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO project_memberships (
				id, workspace_id, project_id, user_id, role_key,
				permissions_json, status, created_by, joined_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'primary_owner', ?, 'active', ?, ?, ?, ?)
		`, record.NewMembershipID, record.WorkspaceID, record.ProjectID,
			record.NewPrimaryUserID, record.Permissions, record.ActorUserID,
			timestamp, timestamp, timestamp)
		if err != nil {
			return fmt.Errorf("insert new primary owner: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read new primary owner membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_memberships
		SET role_key = 'primary_owner',
			permissions_json = ?,
			status = 'active',
			expires_at = NULL,
			removed_at = NULL,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND project_id = ?
	`, record.Permissions, timestamp, existingID, record.WorkspaceID,
		record.ProjectID); err != nil {
		return fmt.Errorf("promote primary owner membership: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) get(
	ctx context.Context,
	queryer rowQueryer,
	workspaceID string,
	projectID string,
	id string,
	now time.Time,
) (Member, error) {
	item, err := scanMember(queryer.QueryRowContext(ctx, memberSelect+`
		WHERE project_memberships.id = ?
			AND project_memberships.workspace_id = ?
			AND project_memberships.project_id = ?
	`, id, workspaceID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, err
	}
	applyExpiry(&item, now)
	return item, nil
}

func (repository *SQLiteRepository) getByUser(
	ctx context.Context,
	queryer rowQueryer,
	workspaceID string,
	projectID string,
	userID string,
	now time.Time,
) (Member, error) {
	item, err := scanMember(queryer.QueryRowContext(ctx, memberSelect+`
		WHERE project_memberships.user_id = ?
			AND project_memberships.workspace_id = ?
			AND project_memberships.project_id = ?
			AND project_memberships.status != 'removed'
	`, userID, workspaceID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, err
	}
	applyExpiry(&item, now)
	return item, nil
}

func requireProject(
	ctx context.Context,
	queryer rowQueryer,
	workspaceID string,
	projectID string,
) error {
	var status string
	err := queryer.QueryRowContext(ctx, `
		SELECT status
		FROM projects
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, projectID, workspaceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read project: %w", err)
	}
	if status != "active" {
		return ErrInvalidInput
	}
	return nil
}

func requireActiveWorkspaceMember(
	ctx context.Context,
	queryer rowQueryer,
	workspaceID string,
	userID string,
) error {
	var exists int
	if err := queryer.QueryRowContext(ctx, `
		SELECT 1
		FROM memberships
		WHERE workspace_id = ?
			AND user_id = ?
			AND status = 'active'
	`, workspaceID, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkspaceMemberNeeded
		}
		return fmt.Errorf("read workspace member: %w", err)
	}
	return nil
}

func formerOwnerRoleAndStatus(action string) (string, string) {
	switch action {
	case "keep_supervisor":
		return "supervisor", "active"
	case "demote_member":
		return "member", "active"
	default:
		return "member", "removed"
	}
}

func applyExpiry(item *Member, now time.Time) {
	if item.Status == "active" && item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
		item.Status = "expired"
	}
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type rowScanner interface {
	Scan(...any) error
}

const memberSelect = `
	SELECT
		project_memberships.id,
		project_memberships.workspace_id,
		project_memberships.project_id,
		project_memberships.user_id,
		users.email,
		users.display_name,
		project_memberships.role_key,
		project_memberships.permissions_json,
		project_memberships.status,
		project_memberships.expires_at,
		project_memberships.joined_at,
		project_memberships.removed_at,
		project_memberships.revision,
		project_memberships.created_at,
		project_memberships.updated_at
	FROM project_memberships
	JOIN users ON users.id = project_memberships.user_id
`

func scanMember(scanner rowScanner) (Member, error) {
	var item Member
	var email, expiresAt, joinedAt, removedAt sql.NullString
	var permissionsJSON, createdAt, updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ProjectID,
		&item.UserID,
		&email,
		&item.DisplayName,
		&item.RoleKey,
		&permissionsJSON,
		&item.Status,
		&expiresAt,
		&joinedAt,
		&removedAt,
		&item.Revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Member{}, err
	}
	item.Email = nullableString(email)
	permissions := map[string]bool{}
	if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
		return Member{}, fmt.Errorf("parse project member permissions: %w", err)
	}
	item.Permissions = permissions
	var err error
	item.ExpiresAt, err = parseNullableTime(expiresAt)
	if err != nil {
		return Member{}, err
	}
	item.JoinedAt, err = parseNullableTime(joinedAt)
	if err != nil {
		return Member{}, err
	}
	item.RemovedAt, err = parseNullableTime(removedAt)
	if err != nil {
		return Member{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Member{}, fmt.Errorf("parse project member creation time: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Member{}, fmt.Errorf("parse project member update time: %w", err)
	}
	return item, nil
}

func requireRowsAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read rows affected: %w", err)
	}
	if affected == 0 {
		return ErrRevisionConflict
	}
	return nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse project member time: %w", err)
	}
	return &parsed, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "constraint failed")
}
