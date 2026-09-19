package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (repository *SQLiteRepository) ListMemberships(
	ctx context.Context,
	workspaceID string,
) ([]Membership, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT memberships.id, memberships.workspace_id, memberships.user_id,
			users.email, users.display_name, memberships.role_key,
			memberships.status, memberships.revision, memberships.joined_at,
			memberships.created_at, memberships.updated_at, memberships.disabled_at
		FROM memberships
		JOIN users ON users.id = memberships.user_id
		WHERE memberships.workspace_id = ?
		ORDER BY
			CASE memberships.role_key
				WHEN 'owner' THEN 0
				WHEN 'admin' THEN 1
				WHEN 'member' THEN 2
				ELSE 3
			END,
			memberships.created_at
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	defer rows.Close()
	items := make([]Membership, 0)
	for rows.Next() {
		item, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memberships: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) UpdateMembership(
	ctx context.Context,
	input UpdateMembershipInput,
	now time.Time,
) (Membership, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, fmt.Errorf("begin membership update: %w", err)
	}
	defer tx.Rollback()
	var currentRole, currentStatus string
	var currentRevision int
	err = tx.QueryRowContext(ctx, `
		SELECT role_key, status, revision
		FROM memberships
		WHERE id = ? AND workspace_id = ?
	`, input.ID, input.WorkspaceID).Scan(
		&currentRole,
		&currentStatus,
		&currentRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("read membership for update: %w", err)
	}
	if currentRevision != input.Revision {
		return Membership{}, ErrRevisionConflict
	}
	removesActiveOwner := currentRole == "owner" && currentStatus == "active" &&
		(input.Role != "owner" || input.Status != "active")
	if removesActiveOwner {
		var ownerCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM memberships
			WHERE workspace_id = ? AND role_key = 'owner' AND status = 'active'
		`, input.WorkspaceID).Scan(&ownerCount); err != nil {
			return Membership{}, fmt.Errorf("count active owners: %w", err)
		}
		if ownerCount <= 1 {
			return Membership{}, ErrLastOwner
		}
	}
	disabledAt := any(nil)
	if input.Status == "disabled" {
		disabledAt = formatTime(now)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE memberships
		SET role_key = ?, status = ?, revision = revision + 1,
			disabled_at = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
	`, input.Role, input.Status, disabledAt, formatTime(now), input.ID,
		input.WorkspaceID, input.Revision)
	if err != nil {
		return Membership{}, fmt.Errorf("update membership: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Membership{}, fmt.Errorf("read membership update result: %w", err)
	}
	if affected == 0 {
		return Membership{}, ErrRevisionConflict
	}
	if input.Status == "disabled" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions
			SET revoked_at = ?
			WHERE workspace_id = ?
				AND user_id = (
					SELECT user_id FROM memberships WHERE id = ?
				)
				AND revoked_at IS NULL
		`, formatTime(now), input.WorkspaceID, input.ID); err != nil {
			return Membership{}, fmt.Errorf("revoke disabled member sessions: %w", err)
		}
	}
	item, err := scanMembership(tx.QueryRowContext(ctx, `
		SELECT memberships.id, memberships.workspace_id, memberships.user_id,
			users.email, users.display_name, memberships.role_key,
			memberships.status, memberships.revision, memberships.joined_at,
			memberships.created_at, memberships.updated_at, memberships.disabled_at
		FROM memberships
		JOIN users ON users.id = memberships.user_id
		WHERE memberships.id = ? AND memberships.workspace_id = ?
	`, input.ID, input.WorkspaceID))
	if err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(); err != nil {
		return Membership{}, fmt.Errorf("commit membership update: %w", err)
	}
	return item, nil
}

type scanner interface {
	Scan(...any) error
}

func scanMembership(row scanner) (Membership, error) {
	var item Membership
	var email, joinedAt, disabledAt sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.UserID,
		&email,
		&item.DisplayName,
		&item.Role,
		&item.Status,
		&item.Revision,
		&joinedAt,
		&createdAt,
		&updatedAt,
		&disabledAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Membership{}, ErrNotFound
		}
		return Membership{}, fmt.Errorf("scan membership: %w", err)
	}
	item.Email = nullableString(email)
	var err error
	item.JoinedAt, err = nullableTime(joinedAt)
	if err != nil {
		return Membership{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Membership{}, fmt.Errorf("parse membership creation time: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Membership{}, fmt.Errorf("parse membership update time: %w", err)
	}
	item.DisabledAt, err = nullableTime(disabledAt)
	if err != nil {
		return Membership{}, err
	}
	return item, nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse membership time: %w", err)
	}
	return &parsed, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
