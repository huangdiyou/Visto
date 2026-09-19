package workspacesettings

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

func (repository *SQLiteRepository) GetRegistration(
	ctx context.Context,
	workspaceID string,
	now time.Time,
) (RegistrationSettings, error) {
	item, err := repository.registrationByWorkspace(ctx, repository.db, workspaceID)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return RegistrationSettings{}, err
	}
	if err := repository.ensureRegistrationRow(ctx, workspaceID, now); err != nil {
		return RegistrationSettings{}, err
	}
	return repository.registrationByWorkspace(ctx, repository.db, workspaceID)
}

func (repository *SQLiteRepository) UpdateRegistration(
	ctx context.Context,
	record updateRegistrationRecord,
) (RegistrationSettings, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationSettings{}, fmt.Errorf("begin registration settings update: %w", err)
	}
	defer tx.Rollback()
	if err := ensureRegistrationRowTx(ctx, tx, record.WorkspaceID, record.Now); err != nil {
		return RegistrationSettings{}, err
	}
	var currentRevision int
	if err := tx.QueryRowContext(ctx, `
		SELECT revision
		FROM workspace_registration_settings
		WHERE workspace_id = ?
	`, record.WorkspaceID).Scan(&currentRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RegistrationSettings{}, ErrNotFound
		}
		return RegistrationSettings{}, fmt.Errorf("read registration revision: %w", err)
	}
	if currentRevision != record.Revision {
		return RegistrationSettings{}, ErrRevisionConflict
	}
	timestamp := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_registration_settings
		SET team_name = ?,
			registration_enabled = ?,
			email_verification_required = ?,
			default_workspace_role = 'member',
			revision = revision + 1,
			updated_by = ?,
			updated_at = ?
		WHERE workspace_id = ? AND revision = ?
	`, record.TeamName,
		boolInt(record.RegistrationEnabled),
		boolInt(record.EmailVerificationRequired),
		record.UpdatedBy,
		timestamp,
		record.WorkspaceID,
		record.Revision); err != nil {
		return RegistrationSettings{}, fmt.Errorf("update registration settings: %w", err)
	}
	item, err := repository.registrationByWorkspace(ctx, tx, record.WorkspaceID)
	if err != nil {
		return RegistrationSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegistrationSettings{}, fmt.Errorf("commit registration settings update: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) PublicRegistration(
	ctx context.Context,
) (PublicRegistration, error) {
	var item PublicRegistration
	var enabled, verification int
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			workspaces.id,
			workspaces.name,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			COALESCE(settings.registration_enabled, 0),
			COALESCE(settings.email_verification_required, 0),
			COALESCE(settings.default_workspace_role, 'member')
		FROM workspaces
		LEFT JOIN workspace_registration_settings settings
			ON settings.workspace_id = workspaces.id
		WHERE workspaces.status = 'active'
			AND workspaces.deleted_at IS NULL
		ORDER BY workspaces.created_at
		LIMIT 1
	`).Scan(
		&item.WorkspaceID,
		&item.WorkspaceName,
		&item.TeamName,
		&enabled,
		&verification,
		&item.DefaultWorkspaceRole,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicRegistration{}, ErrNotFound
	}
	if err != nil {
		return PublicRegistration{}, fmt.Errorf("read public registration settings: %w", err)
	}
	item.RegistrationEnabled = enabled == 1
	item.EmailVerificationRequired = verification == 1
	return item, nil
}

func (repository *SQLiteRepository) ensureRegistrationRow(
	ctx context.Context,
	workspaceID string,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin registration settings creation: %w", err)
	}
	defer tx.Rollback()
	if err := ensureRegistrationRowTx(ctx, tx, workspaceID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registration settings creation: %w", err)
	}
	return nil
}

func ensureRegistrationRowTx(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	now time.Time,
) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM workspaces
		WHERE id = ? AND status = 'active' AND deleted_at IS NULL
	`, workspaceID).Scan(&exists); err != nil {
		return fmt.Errorf("validate settings workspace: %w", err)
	}
	if exists == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO workspace_registration_settings (
			workspace_id, registration_enabled, email_verification_required,
			default_workspace_role, updated_at
		) VALUES (?, 0, 0, 'member', ?)
	`, workspaceID, formatTime(now)); err != nil {
		return fmt.Errorf("create default registration settings: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) registrationByWorkspace(
	ctx context.Context,
	queryer rowQueryer,
	workspaceID string,
) (RegistrationSettings, error) {
	var item RegistrationSettings
	var enabled, verification int
	var updatedBy sql.NullString
	var updatedAt string
	err := queryer.QueryRowContext(ctx, `
		SELECT
			settings.workspace_id,
			workspaces.name,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			settings.registration_enabled,
			settings.email_verification_required,
			settings.default_workspace_role,
			settings.revision,
			settings.updated_by,
			settings.updated_at
		FROM workspace_registration_settings settings
		JOIN workspaces ON workspaces.id = settings.workspace_id
		WHERE settings.workspace_id = ?
			AND workspaces.status = 'active'
			AND workspaces.deleted_at IS NULL
	`, workspaceID).Scan(
		&item.WorkspaceID,
		&item.WorkspaceName,
		&item.TeamName,
		&enabled,
		&verification,
		&item.DefaultWorkspaceRole,
		&item.Revision,
		&updatedBy,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistrationSettings{}, ErrNotFound
	}
	if err != nil {
		return RegistrationSettings{}, fmt.Errorf("read registration settings: %w", err)
	}
	item.RegistrationEnabled = enabled == 1
	item.EmailVerificationRequired = verification == 1
	if updatedBy.Valid {
		item.UpdatedBy = &updatedBy.String
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return RegistrationSettings{}, fmt.Errorf("parse registration settings time: %w", err)
	}
	item.UpdatedAt = parsed
	return item, nil
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
