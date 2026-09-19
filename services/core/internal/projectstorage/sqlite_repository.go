package projectstorage

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

func (repository *SQLiteRepository) ListGrants(
	ctx context.Context,
	workspaceID string,
) ([]Grant, error) {
	rows, err := repository.db.QueryContext(ctx, grantSelect()+`
		WHERE g.workspace_id = ?
		ORDER BY g.updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list project storage grants: %w", err)
	}
	defer rows.Close()
	items := make([]Grant, 0)
	for rows.Next() {
		item, scanErr := scanGrant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListAvailableGrants returns the grants a project may see. The full workspace
// grant list stays Owner-only because it exposes storage deliberately withheld
// from projects, such as workspace-only review-upload buckets.
func (repository *SQLiteRepository) ListAvailableGrants(
	ctx context.Context,
	workspaceID string,
) ([]Grant, error) {
	rows, err := repository.db.QueryContext(ctx, grantSelect()+`
		WHERE g.workspace_id = ?
			AND (
				-- Legacy authorized roots and remote providers have no managed
				-- bucket row and stay visible to projects.
				b.id IS NULL
				-- Managed buckets are only offered to projects when explicitly
				-- marked available.
				OR (b.status = 'active' AND b.project_available = 1)
			)
		ORDER BY g.updated_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list available project storage grants: %w", err)
	}
	defer rows.Close()
	items := make([]Grant, 0)
	for rows.Next() {
		item, scanErr := scanGrant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repository *SQLiteRepository) SetGrant(
	ctx context.Context,
	record grantRecord,
) (Grant, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, fmt.Errorf("begin project storage grant: %w", err)
	}
	defer tx.Rollback()

	if err := validateProviderAndRoot(
		ctx,
		tx,
		record.WorkspaceID,
		record.StorageProviderID,
		record.AuthorizedRootID,
	); err != nil {
		return Grant{}, err
	}

	var existingID string
	rootKey := ""
	if record.AuthorizedRootID != nil {
		rootKey = *record.AuthorizedRootID
	}
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM project_storage_grants
		WHERE workspace_id = ?
			AND storage_provider_id = ?
			AND COALESCE(authorized_root_id, '') = ?
	`, record.WorkspaceID, record.StorageProviderID, rootKey).Scan(&existingID)
	now := formatTime(record.Now)
	if err == nil {
		if _, updateErr := tx.ExecContext(ctx, `
			UPDATE project_storage_grants
			SET status = ?, granted_by = ?, updated_at = ?
			WHERE id = ? AND workspace_id = ?
		`, record.Status, record.GrantedBy, now, existingID,
			record.WorkspaceID); updateErr != nil {
			return Grant{}, fmt.Errorf("update project storage grant: %w", updateErr)
		}
		record.ID = existingID
	} else if errors.Is(err, sql.ErrNoRows) {
		if _, insertErr := tx.ExecContext(ctx, `
			INSERT INTO project_storage_grants (
				id, workspace_id, storage_provider_id, authorized_root_id,
				status, granted_by, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, record.ID, record.WorkspaceID, record.StorageProviderID,
			record.AuthorizedRootID, record.Status, record.GrantedBy,
			now, now); insertErr != nil {
			return Grant{}, fmt.Errorf("create project storage grant: %w", insertErr)
		}
	} else {
		return Grant{}, fmt.Errorf("read project storage grant: %w", err)
	}
	item, err := scanGrant(tx.QueryRowContext(
		ctx,
		grantSelect()+` WHERE g.id = ? AND g.workspace_id = ?`,
		record.ID,
		record.WorkspaceID,
	))
	if err != nil {
		return Grant{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, fmt.Errorf("commit project storage grant: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) ListSelections(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]Selection, error) {
	rows, err := repository.db.QueryContext(ctx, selectionSelect()+`
		WHERE s.workspace_id = ?
			AND s.project_id = ?
		ORDER BY s.purpose
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project storage selections: %w", err)
	}
	defer rows.Close()
	items := make([]Selection, 0)
	for rows.Next() {
		item, scanErr := scanSelection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repository *SQLiteRepository) Select(
	ctx context.Context,
	record selectionRecord,
) (Selection, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Selection{}, fmt.Errorf("begin project storage selection: %w", err)
	}
	defer tx.Rollback()

	if err := validateActiveProject(ctx, tx, record.WorkspaceID, record.ProjectID); err != nil {
		return Selection{}, err
	}
	if err := validateActiveGrant(
		ctx,
		tx,
		record.WorkspaceID,
		record.GrantID,
		record.Purpose,
	); err != nil {
		return Selection{}, err
	}

	now := formatTime(record.Now)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO project_storage_selections (
			id, workspace_id, project_id, grant_id, purpose,
			selected_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, purpose) DO UPDATE SET
			grant_id = excluded.grant_id,
			selected_by = excluded.selected_by,
			updated_at = excluded.updated_at
	`, record.ID, record.WorkspaceID, record.ProjectID, record.GrantID,
		record.Purpose, record.SelectedBy, now, now)
	if err != nil {
		return Selection{}, fmt.Errorf("select project storage grant: %w", err)
	}
	item, err := scanSelection(tx.QueryRowContext(
		ctx,
		selectionSelect()+`
		WHERE s.workspace_id = ?
			AND s.project_id = ?
			AND s.purpose = ?
	`, record.WorkspaceID, record.ProjectID, record.Purpose))
	if err != nil {
		return Selection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Selection{}, fmt.Errorf("commit project storage selection: %w", err)
	}
	return item, nil
}

func grantSelect() string {
	return `
		SELECT
			g.id, g.workspace_id, g.storage_provider_id, g.authorized_root_id,
			p.name, p.kind, p.status, r.display_name, r.status, b.id, b.purpose,
			g.status, g.created_at, g.updated_at
		FROM project_storage_grants g
		JOIN storage_providers p
			ON p.id = g.storage_provider_id
			AND p.workspace_id = g.workspace_id
			AND p.deleted_at IS NULL
		LEFT JOIN authorized_roots r
			ON r.id = g.authorized_root_id
			AND r.workspace_id = g.workspace_id
			AND r.deleted_at IS NULL
		LEFT JOIN local_managed_buckets b
			ON b.authorized_root_id = g.authorized_root_id
			AND b.workspace_id = g.workspace_id
			AND b.deleted_at IS NULL
	`
}

func selectionSelect() string {
	return `
		SELECT
			s.id, s.workspace_id, s.project_id, s.grant_id, s.purpose,
			s.selected_by, s.created_at, s.updated_at,
			g.id, g.workspace_id, g.storage_provider_id, g.authorized_root_id,
			p.name, p.kind, p.status, r.display_name, r.status, b.id, b.purpose,
			g.status, g.created_at, g.updated_at
		FROM project_storage_selections s
		JOIN project_storage_grants g
			ON g.id = s.grant_id
			AND g.workspace_id = s.workspace_id
		JOIN storage_providers p
			ON p.id = g.storage_provider_id
			AND p.workspace_id = g.workspace_id
			AND p.deleted_at IS NULL
		LEFT JOIN authorized_roots r
			ON r.id = g.authorized_root_id
			AND r.workspace_id = g.workspace_id
			AND r.deleted_at IS NULL
		LEFT JOIN local_managed_buckets b
			ON b.authorized_root_id = g.authorized_root_id
			AND b.workspace_id = g.workspace_id
			AND b.deleted_at IS NULL
	`
}

func scanGrant(scanner interface {
	Scan(dest ...any) error
}) (Grant, error) {
	var item Grant
	var rootID, rootName, rootStatus, bucketID, bucketPurpose sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.StorageProviderID,
		&rootID,
		&item.ProviderName,
		&item.ProviderKind,
		&item.ProviderStatus,
		&rootName,
		&rootStatus,
		&bucketID,
		&bucketPurpose,
		&item.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Grant{}, ErrNotFound
		}
		return Grant{}, fmt.Errorf("scan project storage grant: %w", err)
	}
	if rootID.Valid {
		item.AuthorizedRootID = &rootID.String
	}
	if rootName.Valid {
		item.RootName = &rootName.String
	}
	if rootStatus.Valid {
		item.RootStatus = &rootStatus.String
	}
	if bucketID.Valid {
		item.LocalManagedBucketID = &bucketID.String
	}
	if bucketPurpose.Valid {
		item.BucketPurpose = &bucketPurpose.String
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Grant{}, err
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Grant{}, err
	}
	return item, nil
}

func scanSelection(scanner interface {
	Scan(dest ...any) error
}) (Selection, error) {
	var item Selection
	var createdAt, updatedAt string
	var grantRootID, grantRootName, grantRootStatus sql.NullString
	var grantBucketID, grantBucketPurpose sql.NullString
	var grantCreatedAt, grantUpdatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ProjectID,
		&item.GrantID,
		&item.Purpose,
		&item.SelectedBy,
		&createdAt,
		&updatedAt,
		&item.Grant.ID,
		&item.Grant.WorkspaceID,
		&item.Grant.StorageProviderID,
		&grantRootID,
		&item.Grant.ProviderName,
		&item.Grant.ProviderKind,
		&item.Grant.ProviderStatus,
		&grantRootName,
		&grantRootStatus,
		&grantBucketID,
		&grantBucketPurpose,
		&item.Grant.Status,
		&grantCreatedAt,
		&grantUpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Selection{}, ErrNotFound
		}
		return Selection{}, fmt.Errorf("scan project storage selection: %w", err)
	}
	if grantRootID.Valid {
		item.Grant.AuthorizedRootID = &grantRootID.String
	}
	if grantRootName.Valid {
		item.Grant.RootName = &grantRootName.String
	}
	if grantRootStatus.Valid {
		item.Grant.RootStatus = &grantRootStatus.String
	}
	if grantBucketID.Valid {
		item.Grant.LocalManagedBucketID = &grantBucketID.String
	}
	if grantBucketPurpose.Valid {
		item.Grant.BucketPurpose = &grantBucketPurpose.String
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Selection{}, err
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Selection{}, err
	}
	item.Grant.CreatedAt, err = time.Parse(time.RFC3339Nano, grantCreatedAt)
	if err != nil {
		return Selection{}, err
	}
	item.Grant.UpdatedAt, err = time.Parse(time.RFC3339Nano, grantUpdatedAt)
	if err != nil {
		return Selection{}, err
	}
	return item, nil
}

func validateProviderAndRoot(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	providerID string,
	rootID *string,
) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM storage_providers
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, providerID, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("validate storage provider: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	if rootID == nil {
		return nil
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM authorized_roots
		WHERE id = ?
			AND workspace_id = ?
			AND storage_provider_id = ?
			AND deleted_at IS NULL
	`, *rootID, workspaceID, providerID).Scan(&count); err != nil {
		return fmt.Errorf("validate authorized root: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func validateActiveProject(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projectID string,
) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM projects
		WHERE id = ?
			AND workspace_id = ?
			AND status = 'active'
			AND deleted_at IS NULL
	`, projectID, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("validate project: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func validateActiveGrant(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	grantID string,
	purpose string,
) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM project_storage_grants
		WHERE id = ?
			AND workspace_id = ?
			AND status = 'active'
	`, grantID, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("validate project storage grant: %w", err)
	}
	if count != 1 {
		return ErrGrantUnavailable
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM project_storage_grants g
		JOIN storage_providers p
			ON p.id = g.storage_provider_id
			AND p.workspace_id = g.workspace_id
			AND p.deleted_at IS NULL
		LEFT JOIN authorized_roots r
			ON r.id = g.authorized_root_id
			AND r.workspace_id = g.workspace_id
			AND r.deleted_at IS NULL
		LEFT JOIN local_managed_buckets b
			ON b.workspace_id = g.workspace_id
			AND b.authorized_root_id = g.authorized_root_id
			AND b.deleted_at IS NULL
		WHERE g.id = ?
			AND g.workspace_id = ?
			AND g.status = 'active'
			AND (
				(
					b.status = 'active'
					AND b.project_available = 1
					AND (? != 'upload' OR b.purpose = 'upload')
				)
				OR (
					g.authorized_root_id IS NOT NULL
					AND p.status = 'active'
					AND p.kind IN ('webdav', 's3')
					AND r.status = 'available'
				)
			)
	`, grantID, workspaceID, purpose).Scan(&count); err != nil {
		return fmt.Errorf("validate project storage target: %w", err)
	}
	if count != 1 {
		return ErrGrantUnavailable
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
