package identity

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

func (repository *SQLiteRepository) IsSetupComplete(ctx context.Context) (bool, error) {
	var count int
	if err := repository.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM memberships WHERE role_key = 'owner' AND status = 'active'",
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check local setup: %w", err)
	}

	return count > 0, nil
}

func (repository *SQLiteRepository) Setup(
	ctx context.Context,
	record setupRecord,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin local setup: %w", err)
	}
	defer tx.Rollback()

	var ownerCount int
	if err := tx.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM memberships WHERE role_key = 'owner' AND status = 'active'",
	).Scan(&ownerCount); err != nil {
		return fmt.Errorf("check existing owner: %w", err)
	}
	if ownerCount > 0 {
		return ErrSetupAlreadyCompleted
	}

	now := formatDatabaseTime(record.Now)
	expiresAt := formatDatabaseTime(record.SessionExpires)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, email, display_name, status, locale, created_at, updated_at
		) VALUES (?, ?, ?, 'active', ?, ?, ?)
	`, record.UserID, record.OwnerEmail, record.OwnerName, record.Locale, now, now); err != nil {
		return fmt.Errorf("create owner user: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (
			id, name, status, default_locale, timezone,
			settings_json, created_at, updated_at
		) VALUES (?, ?, 'active', ?, ?, '{}', ?, ?)
	`, record.WorkspaceID, record.WorkspaceName, record.Locale, record.Timezone, now, now); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status,
			joined_at, created_at, updated_at
		) VALUES (?, ?, ?, 'owner', 'active', ?, ?, ?)
	`, record.MembershipID, record.WorkspaceID, record.UserID, now, now, now); err != nil {
		return fmt.Errorf("create owner membership: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_credentials (user_id, password_hash, updated_at)
		VALUES (?, ?, ?)
	`, record.UserID, record.PasswordHash, now); err != nil {
		return fmt.Errorf("create owner credential: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (
			id, user_id, workspace_id, token_digest,
			expires_at, last_seen_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, record.SessionID, record.UserID, record.WorkspaceID, record.SessionDigest, expiresAt, now, now); err != nil {
		return fmt.Errorf("create initial session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit local setup: %w", err)
	}

	return nil
}

func (repository *SQLiteRepository) LegacyOwnerCredential(
	ctx context.Context,
) (credentialRecord, error) {
	record, err := scanCredential(repository.db.QueryRowContext(ctx, `
		SELECT
			users.id,
			workspaces.id,
			users.email,
			users.display_name,
			workspaces.name,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			users.locale,
			workspaces.timezone,
			memberships.role_key,
			user_credentials.password_hash
		FROM memberships
		JOIN users ON users.id = memberships.user_id
		JOIN workspaces ON workspaces.id = memberships.workspace_id
		LEFT JOIN workspace_registration_settings settings
			ON settings.workspace_id = workspaces.id
		JOIN user_credentials ON user_credentials.user_id = users.id
		WHERE memberships.role_key = 'owner'
			AND memberships.status = 'active'
			AND users.status = 'active'
			AND workspaces.status = 'active'
			AND users.email IS NULL
		ORDER BY memberships.created_at
		LIMIT 1
	`))
	if errors.Is(err, ErrInvalidCredentials) {
		complete, completeErr := repository.IsSetupComplete(ctx)
		if completeErr != nil {
			return credentialRecord{}, completeErr
		}
		if !complete {
			return credentialRecord{}, ErrSetupRequired
		}
	}
	return record, err
}

func (repository *SQLiteRepository) CredentialByEmail(
	ctx context.Context,
	email string,
) (credentialRecord, error) {
	return scanCredential(repository.db.QueryRowContext(ctx, `
		SELECT
			users.id,
			workspaces.id,
			users.email,
			users.display_name,
			workspaces.name,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			users.locale,
			workspaces.timezone,
			memberships.role_key,
			user_credentials.password_hash
		FROM memberships
		JOIN users ON users.id = memberships.user_id
		JOIN workspaces ON workspaces.id = memberships.workspace_id
		LEFT JOIN workspace_registration_settings settings
			ON settings.workspace_id = workspaces.id
		JOIN user_credentials ON user_credentials.user_id = users.id
		WHERE lower(users.email) = lower(?)
			AND memberships.status = 'active'
			AND users.status = 'active'
			AND workspaces.status = 'active'
		ORDER BY memberships.created_at
		LIMIT 1
	`, email))
}

func (repository *SQLiteRepository) CreateAccount(
	ctx context.Context,
	record accountRecord,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account creation: %w", err)
	}
	defer tx.Rollback()
	var workspaceCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workspaces WHERE id = ? AND status = 'active'
	`, record.WorkspaceID).Scan(&workspaceCount); err != nil {
		return fmt.Errorf("validate account workspace: %w", err)
	}
	if workspaceCount == 0 {
		return ErrWorkspaceNotFound
	}
	var emailCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users WHERE lower(email) = lower(?)
	`, record.Email).Scan(&emailCount); err != nil {
		return fmt.Errorf("check account email: %w", err)
	}
	if emailCount > 0 {
		return ErrEmailInUse
	}
	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (
			id, email, display_name, status, locale, created_at, updated_at
		) VALUES (?, ?, ?, 'active', ?, ?, ?)
	`, record.UserID, record.Email, record.DisplayName, record.Locale, now, now); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status,
			joined_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'active', ?, ?, ?)
	`, record.MembershipID, record.WorkspaceID, record.UserID, record.Role,
		now, now, now); err != nil {
		return fmt.Errorf("create membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_credentials (user_id, password_hash, updated_at)
		VALUES (?, ?, ?)
	`, record.UserID, record.PasswordHash, now); err != nil {
		return fmt.Errorf("create user credential: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account creation: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) UpdateProfile(
	ctx context.Context,
	userID string,
	displayName string,
	locale string,
	now time.Time,
) (User, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE users
		SET display_name = ?, locale = ?, updated_at = ?
		WHERE id = ? AND status = 'active'
	`, displayName, locale, formatDatabaseTime(now), userID)
	if err != nil {
		return User{}, fmt.Errorf("update user profile: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("read updated user profile count: %w", err)
	}
	if affected == 0 {
		return User{}, ErrUserNotFound
	}

	var user User
	if err := repository.db.QueryRowContext(ctx, `
		SELECT id, email, display_name, locale
		FROM users
		WHERE id = ? AND status = 'active'
	`, userID).Scan(
		&user.ID,
		newNullableStringTarget(&user.Email),
		&user.DisplayName,
		&user.Locale,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("read updated user profile: %w", err)
	}
	return user, nil
}

func (repository *SQLiteRepository) CreateSession(
	ctx context.Context,
	sessionID string,
	userID string,
	workspaceID string,
	tokenDigest string,
	now time.Time,
	expiresAt time.Time,
) error {
	if _, err := repository.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, user_id, workspace_id, token_digest,
			expires_at, last_seen_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID,
		userID,
		workspaceID,
		tokenDigest,
		formatDatabaseTime(expiresAt),
		formatDatabaseTime(now),
		formatDatabaseTime(now),
	); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	return nil
}

func (repository *SQLiteRepository) SessionByDigest(
	ctx context.Context,
	tokenDigest string,
	now time.Time,
	idleCutoff time.Time,
) (sessionRecord, error) {
	var record sessionRecord
	var expiresAt string
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			sessions.id,
			users.id,
			workspaces.id,
			users.email,
			users.display_name,
			workspaces.name,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			users.locale,
			workspaces.timezone,
			memberships.role_key,
			sessions.expires_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		JOIN workspaces ON workspaces.id = sessions.workspace_id
		LEFT JOIN workspace_registration_settings settings
			ON settings.workspace_id = workspaces.id
		JOIN memberships
			ON memberships.user_id = sessions.user_id
			AND memberships.workspace_id = sessions.workspace_id
		WHERE sessions.token_digest = ?
			AND sessions.revoked_at IS NULL
			AND sessions.expires_at > ?
			AND sessions.last_seen_at > ?
			AND users.status = 'active'
			AND workspaces.status = 'active'
			AND memberships.status = 'active'
		LIMIT 1
	`, tokenDigest, formatDatabaseTime(now), formatDatabaseTime(idleCutoff)).Scan(
		&record.SessionID,
		&record.UserID,
		&record.WorkspaceID,
		newNullableStringTarget(&record.Email),
		&record.DisplayName,
		&record.WorkspaceName,
		&record.WorkspaceTeamName,
		&record.Locale,
		&record.Timezone,
		&record.Role,
		&expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return sessionRecord{}, fmt.Errorf("read session: %w", err)
	}

	record.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return sessionRecord{}, fmt.Errorf("parse session expiry: %w", err)
	}

	return record, nil
}

func (repository *SQLiteRepository) TouchSession(
	ctx context.Context,
	sessionID string,
	now time.Time,
) error {
	if _, err := repository.db.ExecContext(ctx, `
		UPDATE sessions SET last_seen_at = ? WHERE id = ?
	`, formatDatabaseTime(now), sessionID); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}

	return nil
}

func (repository *SQLiteRepository) RevokeSession(
	ctx context.Context,
	tokenDigest string,
	now time.Time,
) error {
	if _, err := repository.db.ExecContext(ctx, `
		UPDATE sessions
		SET revoked_at = ?
		WHERE token_digest = ? AND revoked_at IS NULL
	`, formatDatabaseTime(now), tokenDigest); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanCredential(row scanner) (credentialRecord, error) {
	var record credentialRecord
	var email sql.NullString
	if err := row.Scan(
		&record.UserID,
		&record.WorkspaceID,
		&email,
		&record.DisplayName,
		&record.WorkspaceName,
		&record.WorkspaceTeamName,
		&record.Locale,
		&record.Timezone,
		&record.Role,
		&record.PasswordHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return credentialRecord{}, ErrInvalidCredentials
		}
		return credentialRecord{}, fmt.Errorf("read credential: %w", err)
	}
	record.Email = nullableString(email)
	return record, nil
}

func newNullableStringTarget(destination **string) *nullableStringScanner {
	return &nullableStringScanner{destination: destination}
}

type nullableStringScanner struct {
	destination **string
}

func (target *nullableStringScanner) Scan(value any) error {
	if value == nil {
		*target.destination = nil
		return nil
	}
	text, ok := value.(string)
	if !ok {
		bytes, bytesOK := value.([]byte)
		if !bytesOK {
			return fmt.Errorf("scan nullable string from %T", value)
		}
		text = string(bytes)
	}
	*target.destination = &text
	return nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func formatDatabaseTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000Z")
}
