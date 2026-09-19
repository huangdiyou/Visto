package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"review-studio.local/core/internal/identity"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (repository *SQLiteRepository) Create(
	ctx context.Context,
	record createRecord,
) (Invitation, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, fmt.Errorf("begin invitation creation: %w", err)
	}
	defer tx.Rollback()
	var memberCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memberships
		JOIN users ON users.id = memberships.user_id
		WHERE memberships.workspace_id = ? AND lower(users.email) = lower(?)
			AND memberships.status IN ('active', 'invited')
	`, record.WorkspaceID, record.Email).Scan(&memberCount); err != nil {
		return Invitation{}, fmt.Errorf("check invited membership: %w", err)
	}
	if memberCount > 0 {
		return Invitation{}, ErrEmailInUse
	}
	var pendingCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workspace_invitations
		WHERE workspace_id = ? AND lower(email) = lower(?) AND status = 'pending'
	`, record.WorkspaceID, record.Email).Scan(&pendingCount); err != nil {
		return Invitation{}, fmt.Errorf("check pending invitation: %w", err)
	}
	if pendingCount > 0 {
		return Invitation{}, ErrPendingExists
	}
	now := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_invitations (
			id, workspace_id, email, role_key, token_digest, token_prefix,
			status, expires_at, invited_by, send_count, last_sent_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, 1, ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.Email, record.Role,
		record.TokenDigest, record.TokenPrefix, formatTime(record.ExpiresAt),
		record.InvitedBy, now, now, now); err != nil {
		return Invitation{}, fmt.Errorf("create invitation: %w", err)
	}
	item, err := scanInvitation(tx.QueryRowContext(ctx, invitationSelect+`
		WHERE workspace_invitations.id = ?
	`, record.ID))
	if err != nil {
		return Invitation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Invitation{}, fmt.Errorf("commit invitation creation: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) List(
	ctx context.Context,
	workspaceID string,
	now time.Time,
) ([]Invitation, error) {
	if _, err := repository.db.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET status = 'expired', updated_at = ?
		WHERE workspace_id = ? AND status = 'pending' AND expires_at <= ?
	`, formatTime(now), workspaceID, formatTime(now)); err != nil {
		return nil, fmt.Errorf("expire invitations: %w", err)
	}
	rows, err := repository.db.QueryContext(ctx, invitationSelect+`
		WHERE workspace_invitations.workspace_id = ?
		ORDER BY workspace_invitations.created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()
	items := make([]Invitation, 0)
	for rows.Next() {
		item, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitations: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) Rotate(
	ctx context.Context,
	record rotateRecord,
) (Invitation, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET token_digest = ?, token_prefix = ?, expires_at = ?,
			send_count = send_count + 1, last_sent_at = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND status = 'pending'
	`, record.TokenDigest, record.TokenPrefix, formatTime(record.ExpiresAt),
		formatTime(record.Now), formatTime(record.Now), record.ID,
		record.WorkspaceID)
	if err != nil {
		return Invitation{}, fmt.Errorf("rotate invitation token: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return Invitation{}, err
	}
	return scanInvitation(repository.db.QueryRowContext(ctx, invitationSelect+`
		WHERE workspace_invitations.id = ?
			AND workspace_invitations.workspace_id = ?
	`, record.ID, record.WorkspaceID))
}

func (repository *SQLiteRepository) Revoke(
	ctx context.Context,
	workspaceID string,
	id string,
	now time.Time,
) (Invitation, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET status = 'revoked', revoked_at = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND status = 'pending'
	`, formatTime(now), formatTime(now), id, workspaceID)
	if err != nil {
		return Invitation{}, fmt.Errorf("revoke invitation: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return Invitation{}, err
	}
	return scanInvitation(repository.db.QueryRowContext(ctx, invitationSelect+`
		WHERE workspace_invitations.id = ?
			AND workspace_invitations.workspace_id = ?
	`, id, workspaceID))
}

func (repository *SQLiteRepository) Preview(
	ctx context.Context,
	tokenDigest string,
	now time.Time,
) (Invitation, error) {
	item, err := scanInvitation(repository.db.QueryRowContext(ctx, invitationSelect+`
		WHERE workspace_invitations.token_digest = ?
	`, tokenDigest))
	if errors.Is(err, ErrNotFound) {
		return Invitation{}, ErrUnavailable
	}
	if err != nil {
		return Invitation{}, err
	}
	if item.Status != "pending" {
		return Invitation{}, ErrUnavailable
	}
	if !item.ExpiresAt.After(now) {
		_, _ = repository.db.ExecContext(ctx, `
			UPDATE workspace_invitations
			SET status = 'expired', updated_at = ?
			WHERE id = ? AND status = 'pending'
		`, formatTime(now), item.ID)
		return Invitation{}, ErrExpired
	}
	return item, nil
}

func (repository *SQLiteRepository) Accept(
	ctx context.Context,
	record acceptRecord,
) (acceptedRecord, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return acceptedRecord{}, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer tx.Rollback()
	item, err := scanInvitation(tx.QueryRowContext(ctx, invitationSelect+`
		WHERE workspace_invitations.token_digest = ?
	`, record.TokenDigest))
	if errors.Is(err, ErrNotFound) || (err == nil && item.Status != "pending") {
		return acceptedRecord{}, ErrUnavailable
	}
	if err != nil {
		return acceptedRecord{}, err
	}
	if !item.ExpiresAt.After(record.Now) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE workspace_invitations
			SET status = 'expired', updated_at = ?
			WHERE id = ? AND status = 'pending'
		`, formatTime(record.Now), item.ID); err != nil {
			return acceptedRecord{}, fmt.Errorf("expire invitation: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return acceptedRecord{}, fmt.Errorf("commit invitation expiry: %w", err)
		}
		return acceptedRecord{}, ErrExpired
	}
	var existingUsers int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users WHERE lower(email) = lower(?)
	`, item.Email).Scan(&existingUsers); err != nil {
		return acceptedRecord{}, fmt.Errorf("check invitation email: %w", err)
	}
	if existingUsers > 0 {
		return acceptedRecord{}, ErrEmailInUse
	}
	now := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, email, display_name, status, locale, created_at, updated_at
		) VALUES (?, ?, ?, 'active', ?, ?, ?)
	`, record.Material.UserID, item.Email, record.DisplayName, record.Locale,
		now, now); err != nil {
		return acceptedRecord{}, fmt.Errorf("create invited user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, joined_at,
			created_at, updated_at, revision
		) VALUES (?, ?, ?, ?, 'active', ?, ?, ?, 1)
	`, record.Material.MembershipID, item.WorkspaceID, record.Material.UserID,
		item.Role, now, now, now); err != nil {
		return acceptedRecord{}, fmt.Errorf("create invited membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_credentials (user_id, password_hash, updated_at)
		VALUES (?, ?, ?)
	`, record.Material.UserID, record.Material.PasswordHash, now); err != nil {
		return acceptedRecord{}, fmt.Errorf("create invited credential: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (
			id, user_id, workspace_id, token_digest, expires_at,
			last_seen_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, record.Material.SessionID, record.Material.UserID, item.WorkspaceID,
		record.Material.SessionDigest, formatTime(record.Material.SessionExpires),
		now, now); err != nil {
		return acceptedRecord{}, fmt.Errorf("create invited session: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE workspace_invitations
		SET status = 'accepted', accepted_by_user_id = ?, accepted_at = ?,
			updated_at = ?
		WHERE id = ? AND status = 'pending'
	`, record.Material.UserID, now, now, item.ID)
	if err != nil {
		return acceptedRecord{}, fmt.Errorf("accept invitation: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return acceptedRecord{}, ErrUnavailable
	}
	var workspace identity.Workspace
	if err := tx.QueryRowContext(ctx, `
		SELECT
			workspaces.id,
			workspaces.name,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			workspaces.timezone
		FROM workspaces
		LEFT JOIN workspace_registration_settings settings
			ON settings.workspace_id = workspaces.id
		WHERE workspaces.id = ? AND workspaces.status = 'active'
	`, item.WorkspaceID).Scan(
		&workspace.ID,
		&workspace.Name,
		&workspace.TeamName,
		&workspace.Timezone,
	); err != nil {
		return acceptedRecord{}, fmt.Errorf("read invited workspace: %w", err)
	}
	acceptedAt := record.Now
	item.Status = "accepted"
	item.AcceptedByUserID = &record.Material.UserID
	item.AcceptedAt = &acceptedAt
	item.UpdatedAt = record.Now
	if err := tx.Commit(); err != nil {
		return acceptedRecord{}, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	email := item.Email
	return acceptedRecord{
		Invitation: item,
		Workspace:  workspace,
		User: identity.User{
			ID:          record.Material.UserID,
			Email:       &email,
			DisplayName: record.DisplayName,
			Locale:      record.Locale,
		},
		Role:          item.Role,
		SessionID:     record.Material.SessionID,
		SessionExpiry: record.Material.SessionExpires,
	}, nil
}

const invitationSelect = `
	SELECT workspace_invitations.id, workspace_invitations.workspace_id,
		COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
		workspace_invitations.email,
		workspace_invitations.role_key, workspace_invitations.token_prefix,
		workspace_invitations.status, workspace_invitations.expires_at,
		workspace_invitations.invited_by,
		workspace_invitations.accepted_by_user_id,
		workspace_invitations.send_count, workspace_invitations.last_sent_at,
		workspace_invitations.accepted_at, workspace_invitations.revoked_at,
		workspace_invitations.created_at, workspace_invitations.updated_at
	FROM workspace_invitations
	JOIN workspaces ON workspaces.id = workspace_invitations.workspace_id
	LEFT JOIN workspace_registration_settings settings
		ON settings.workspace_id = workspaces.id
`

type scanner interface {
	Scan(...any) error
}

func scanInvitation(row scanner) (Invitation, error) {
	var item Invitation
	var acceptedBy, acceptedAt, revokedAt sql.NullString
	var expiresAt, lastSentAt, createdAt, updatedAt string
	if err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.WorkspaceName,
		&item.Email,
		&item.Role,
		&item.TokenPrefix,
		&item.Status,
		&expiresAt,
		&item.InvitedBy,
		&acceptedBy,
		&item.SendCount,
		&lastSentAt,
		&acceptedAt,
		&revokedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Invitation{}, ErrNotFound
		}
		return Invitation{}, fmt.Errorf("scan invitation: %w", err)
	}
	item.AcceptedByUserID = nullableString(acceptedBy)
	var err error
	item.ExpiresAt, err = parseTime(expiresAt)
	if err != nil {
		return Invitation{}, err
	}
	item.LastSentAt, err = parseTime(lastSentAt)
	if err != nil {
		return Invitation{}, err
	}
	item.AcceptedAt, err = nullableTime(acceptedAt)
	if err != nil {
		return Invitation{}, err
	}
	item.RevokedAt, err = nullableTime(revokedAt)
	if err != nil {
		return Invitation{}, err
	}
	item.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Invitation{}, err
	}
	item.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Invitation{}, err
	}
	return item, nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read invitation update result: %w", err)
	}
	if affected == 0 {
		return ErrUnavailable
	}
	return nil
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
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse invitation time: %w", err)
	}
	return parsed, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
