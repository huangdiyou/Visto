package projectaccess

import (
	"context"
	"database/sql"
	"encoding/json"
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

func (repository *SQLiteRepository) ActiveMembership(
	ctx context.Context,
	workspaceID string,
	projectID string,
	userID string,
	now time.Time,
) (Membership, error) {
	var membership Membership
	var permissionsJSON string
	var expiresAt sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			id,
			workspace_id,
			project_id,
			user_id,
			role_key,
			permissions_json,
			status,
			expires_at
		FROM project_memberships
		WHERE workspace_id = ?
			AND project_id = ?
			AND user_id = ?
			AND status = 'active'
			AND (expires_at IS NULL OR expires_at > ?)
		LIMIT 1
	`,
		workspaceID,
		projectID,
		userID,
		formatDatabaseTime(now),
	).Scan(
		&membership.ID,
		&membership.WorkspaceID,
		&membership.ProjectID,
		&membership.UserID,
		&membership.RoleKey,
		&permissionsJSON,
		&membership.Status,
		&expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, nil
	}
	if err != nil {
		return Membership{}, fmt.Errorf("read project membership: %w", err)
	}
	permissions, err := parsePermissions(permissionsJSON)
	if err != nil {
		return Membership{}, err
	}
	membership.Permissions = permissions
	if expiresAt.Valid {
		parsed, err := parseDatabaseTime(expiresAt.String)
		if err != nil {
			return Membership{}, fmt.Errorf("parse membership expiry: %w", err)
		}
		membership.ExpiresAt = &parsed
	}
	return membership, nil
}

func parsePermissions(value string) (map[Permission]bool, error) {
	var raw map[string]bool
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, fmt.Errorf("parse project permissions: %w", err)
	}
	permissions := make(map[Permission]bool, len(raw))
	for key, allowed := range raw {
		permissions[Permission(key)] = allowed
	}
	return permissions, nil
}

func formatDatabaseTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseDatabaseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
