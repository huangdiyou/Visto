package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

const localSourceProviderName = "本地源文件"

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (repository *SQLiteRepository) RegisterLocalRoot(
	ctx context.Context,
	record registerRootRecord,
) (AuthorizedRoot, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthorizedRoot{}, fmt.Errorf("begin local root registration: %w", err)
	}
	defer tx.Rollback()

	providerID, err := ensureLocalProvider(ctx, tx, record)
	if err != nil {
		return AuthorizedRoot{}, err
	}

	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_path_secrets (
			id, workspace_id, path_text, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
	`,
		record.PathSecretID,
		record.WorkspaceID,
		record.LocalPath,
		now,
		now,
	); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("store local path secret: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO authorized_roots (
			id, workspace_id, storage_provider_id, display_name,
			display_path, path_secret_ref, mode, scan_enabled,
			status, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'available', 1, ?, ?)
	`,
		record.RootID,
		record.WorkspaceID,
		providerID,
		record.DisplayName,
		record.DisplayPath,
		record.PathSecretID,
		record.Mode,
		boolInt(record.ScanEnabled),
		now,
		now,
	); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("create authorized root: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("commit local root registration: %w", err)
	}
	return repository.Root(ctx, record.WorkspaceID, record.RootID)
}

func (repository *SQLiteRepository) ListRoots(
	ctx context.Context,
	workspaceID string,
) ([]AuthorizedRoot, error) {
	rows, err := repository.db.QueryContext(ctx, `
			SELECT
				id, workspace_id, storage_provider_id, display_name,
				display_path, mode, scan_enabled, status, revision,
				created_at, updated_at, last_scan_at, last_scan_status,
				last_scan_summary_json
		FROM authorized_roots
		WHERE workspace_id = ?
			AND purpose IN ('library', 'managed_versions')
			AND deleted_at IS NULL
		ORDER BY
			CASE purpose WHEN 'managed_versions' THEN 1 ELSE 0 END,
			created_at,
			display_name
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list authorized roots: %w", err)
	}
	defer rows.Close()

	roots := make([]AuthorizedRoot, 0)
	for rows.Next() {
		root, err := scanAuthorizedRoot(rows)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorized roots: %w", err)
	}
	return roots, nil
}

func (repository *SQLiteRepository) Root(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (AuthorizedRoot, error) {
	root, err := scanAuthorizedRoot(repository.db.QueryRowContext(ctx, `
			SELECT
				id, workspace_id, storage_provider_id, display_name,
				display_path, mode, scan_enabled, status, revision,
				created_at, updated_at, last_scan_at, last_scan_status,
				last_scan_summary_json
		FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthorizedRoot{}, ErrRootNotFound
	}
	return root, err
}

func (repository *SQLiteRepository) ResolvedRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (rootRecord, error) {
	var record rootRecord
	var scanEnabled int
	var createdAt string
	var updatedAt string
	var lastScanAt sql.NullString
	var lastScanStatus sql.NullString
	var lastScanSummary sql.NullString
	var providerConfigJSON string
	var providerSecretRef sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			authorized_roots.id,
			authorized_roots.workspace_id,
			authorized_roots.storage_provider_id,
			storage_providers.kind,
			storage_providers.config_json,
			storage_providers.secret_ref,
			authorized_roots.path_secret_ref,
			authorized_roots.display_name,
			authorized_roots.display_path,
			local_path_secrets.path_text,
			authorized_roots.mode,
			authorized_roots.scan_enabled,
			authorized_roots.status,
				authorized_roots.revision,
				authorized_roots.created_at,
				authorized_roots.updated_at,
				authorized_roots.last_scan_at,
				authorized_roots.last_scan_status,
				authorized_roots.last_scan_summary_json
		FROM authorized_roots
		JOIN storage_providers
			ON storage_providers.id = authorized_roots.storage_provider_id
			AND storage_providers.workspace_id = authorized_roots.workspace_id
		JOIN local_path_secrets
			ON local_path_secrets.id = authorized_roots.path_secret_ref
			AND local_path_secrets.workspace_id = authorized_roots.workspace_id
		WHERE authorized_roots.id = ?
			AND authorized_roots.workspace_id = ?
			AND authorized_roots.deleted_at IS NULL
	`, rootID, workspaceID).Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.StorageProviderID,
		&record.ProviderKind,
		&providerConfigJSON,
		&providerSecretRef,
		&record.PathSecretID,
		&record.DisplayName,
		&record.DisplayPath,
		&record.LocalPath,
		&record.Mode,
		&scanEnabled,
		&record.Status,
		&record.Revision,
		&createdAt,
		&updatedAt,
		&lastScanAt,
		&lastScanStatus,
		&lastScanSummary,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return rootRecord{}, ErrRootNotFound
	}
	if err != nil {
		return rootRecord{}, fmt.Errorf("resolve authorized root: %w", err)
	}

	record.ScanEnabled = scanEnabled != 0
	if err := json.Unmarshal(
		[]byte(providerConfigJSON),
		&record.ProviderConfig,
	); err != nil {
		return rootRecord{}, fmt.Errorf("decode root provider config: %w", err)
	}
	if providerSecretRef.Valid {
		record.ProviderSecretRef = providerSecretRef.String
	}
	record.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return rootRecord{}, fmt.Errorf("parse authorized root creation time: %w", err)
	}
	record.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return rootRecord{}, fmt.Errorf("parse authorized root update time: %w", err)
	}
	record.LastScanAt, record.LastScanStatus, record.LastScanSummary, err =
		parseScanState(lastScanAt, lastScanStatus, lastScanSummary)
	if err != nil {
		return rootRecord{}, err
	}
	return record, nil
}

func (repository *SQLiteRepository) UpdateRoot(
	ctx context.Context,
	input UpdateRootInput,
	now time.Time,
) (AuthorizedRoot, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE authorized_roots
		SET display_name = ?,
			scan_enabled = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`,
		input.DisplayName,
		boolInt(input.ScanEnabled),
		formatDatabaseTime(now),
		input.ID,
		input.WorkspaceID,
		input.Revision,
	)
	if err != nil {
		return AuthorizedRoot{}, fmt.Errorf("update authorized root: %w", err)
	}
	if err := requireChangedRoot(
		ctx,
		repository.db,
		result,
		input.WorkspaceID,
		input.ID,
	); err != nil {
		return AuthorizedRoot{}, err
	}
	return repository.Root(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) DeleteRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
	revision int,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin authorized root deletion: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatDatabaseTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET deleted_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`, timestamp, timestamp, rootID, workspaceID, revision)
	if err != nil {
		return fmt.Errorf("delete authorized root: %w", err)
	}
	if err := requireChangedRoot(ctx, tx, result, workspaceID, rootID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE storage_objects
		SET status = 'missing',
			missing_since = COALESCE(missing_since, ?),
			updated_at = ?
		WHERE workspace_id = ?
			AND authorized_root_id = ?
			AND status = 'available'
			AND deleted_at IS NULL
	`, timestamp, timestamp, workspaceID, rootID); err != nil {
		return fmt.Errorf("mark deleted root objects missing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit authorized root deletion: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ListLocalManagedBuckets(
	ctx context.Context,
	workspaceID string,
) ([]LocalManagedBucket, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			local_managed_buckets.id,
			local_managed_buckets.workspace_id,
			local_managed_buckets.authorized_root_id,
			authorized_roots.storage_provider_id,
			local_managed_buckets.display_name,
			authorized_roots.display_path,
			local_managed_buckets.purpose,
			local_managed_buckets.quota_bytes,
			COALESCE((
				SELECT SUM(size_bytes)
				FROM storage_objects
				WHERE storage_objects.workspace_id = local_managed_buckets.workspace_id
					AND storage_objects.authorized_root_id = local_managed_buckets.authorized_root_id
					AND storage_objects.status = 'available'
					AND storage_objects.deleted_at IS NULL
			), local_managed_buckets.used_bytes_estimate, 0),
			local_managed_buckets.upload_security_policy,
			local_managed_buckets.project_available,
			local_managed_buckets.status,
			local_managed_buckets.created_by,
			local_managed_buckets.revision,
			local_managed_buckets.created_at,
			local_managed_buckets.updated_at
		FROM local_managed_buckets
		JOIN authorized_roots
			ON authorized_roots.id = local_managed_buckets.authorized_root_id
			AND authorized_roots.workspace_id = local_managed_buckets.workspace_id
			AND authorized_roots.deleted_at IS NULL
		WHERE local_managed_buckets.workspace_id = ?
			AND local_managed_buckets.deleted_at IS NULL
		ORDER BY
			CASE local_managed_buckets.status
				WHEN 'active' THEN 0
				WHEN 'error' THEN 1
				ELSE 2
			END,
			local_managed_buckets.created_at,
			lower(local_managed_buckets.display_name)
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list local managed buckets: %w", err)
	}
	defer rows.Close()

	buckets := make([]LocalManagedBucket, 0)
	for rows.Next() {
		bucket, err := scanLocalManagedBucket(rows)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local managed buckets: %w", err)
	}
	return buckets, nil
}

func (repository *SQLiteRepository) LocalManagedBucket(
	ctx context.Context,
	workspaceID string,
	bucketID string,
) (LocalManagedBucket, error) {
	bucket, err := scanLocalManagedBucket(repository.db.QueryRowContext(ctx, `
		SELECT
			local_managed_buckets.id,
			local_managed_buckets.workspace_id,
			local_managed_buckets.authorized_root_id,
			authorized_roots.storage_provider_id,
			local_managed_buckets.display_name,
			authorized_roots.display_path,
			local_managed_buckets.purpose,
			local_managed_buckets.quota_bytes,
			COALESCE((
				SELECT SUM(size_bytes)
				FROM storage_objects
				WHERE storage_objects.workspace_id = local_managed_buckets.workspace_id
					AND storage_objects.authorized_root_id = local_managed_buckets.authorized_root_id
					AND storage_objects.status = 'available'
					AND storage_objects.deleted_at IS NULL
			), local_managed_buckets.used_bytes_estimate, 0),
			local_managed_buckets.upload_security_policy,
			local_managed_buckets.project_available,
			local_managed_buckets.status,
			local_managed_buckets.created_by,
			local_managed_buckets.revision,
			local_managed_buckets.created_at,
			local_managed_buckets.updated_at
		FROM local_managed_buckets
		JOIN authorized_roots
			ON authorized_roots.id = local_managed_buckets.authorized_root_id
			AND authorized_roots.workspace_id = local_managed_buckets.workspace_id
			AND authorized_roots.deleted_at IS NULL
		WHERE local_managed_buckets.id = ?
			AND local_managed_buckets.workspace_id = ?
			AND local_managed_buckets.deleted_at IS NULL
	`, bucketID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return LocalManagedBucket{}, ErrBucketNotFound
	}
	return bucket, err
}

func (repository *SQLiteRepository) CreateLocalManagedBucket(
	ctx context.Context,
	record createLocalManagedBucketRecord,
) (LocalManagedBucket, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return LocalManagedBucket{}, fmt.Errorf("begin local bucket creation: %w", err)
	}
	defer tx.Rollback()

	providerID, err := ensureLocalProvider(ctx, tx, registerRootRecord{
		ProviderID:  record.ProviderID,
		WorkspaceID: record.WorkspaceID,
		Now:         record.Now,
	})
	if err != nil {
		return LocalManagedBucket{}, err
	}

	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_path_secrets (
			id, workspace_id, path_text, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
	`,
		record.PathSecretID,
		record.WorkspaceID,
		record.LocalPath,
		now,
		now,
	); err != nil {
		return LocalManagedBucket{}, fmt.Errorf("store local bucket path secret: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO authorized_roots (
			id, workspace_id, storage_provider_id, display_name,
			display_path, path_secret_ref, mode, scan_enabled,
			status, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'managed', 0, 'available', 1, ?, ?)
	`,
		record.RootID,
		record.WorkspaceID,
		providerID,
		record.DisplayName,
		record.DisplayPath,
		record.PathSecretID,
		now,
		now,
	); err != nil {
		return LocalManagedBucket{}, fmt.Errorf("create bucket authorized root: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_managed_buckets (
			id, workspace_id, authorized_root_id, display_name, purpose,
			quota_bytes, used_bytes_estimate, upload_security_policy,
			project_available, status, created_by, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, NULLIF(?, ''), 1, ?, ?)
	`,
		record.BucketID,
		record.WorkspaceID,
		record.RootID,
		record.DisplayName,
		record.Purpose,
		record.QuotaBytes,
		record.UploadSecurityPolicy,
		boolInt(record.ProjectAvailable),
		record.Status,
		record.CreatedBy,
		now,
		now,
	); err != nil {
		return LocalManagedBucket{}, fmt.Errorf("create local managed bucket: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return LocalManagedBucket{}, fmt.Errorf("commit local bucket creation: %w", err)
	}
	return repository.LocalManagedBucket(ctx, record.WorkspaceID, record.BucketID)
}

func (repository *SQLiteRepository) UpdateLocalManagedBucket(
	ctx context.Context,
	input UpdateLocalManagedBucketInput,
	now time.Time,
) (LocalManagedBucket, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return LocalManagedBucket{}, fmt.Errorf("begin local bucket update: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatDatabaseTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE local_managed_buckets
		SET display_name = ?,
			purpose = ?,
			quota_bytes = ?,
			upload_security_policy = ?,
			project_available = ?,
			status = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`,
		input.DisplayName,
		input.Purpose,
		input.QuotaBytes,
		input.UploadSecurityPolicy,
		boolInt(input.ProjectAvailable),
		input.Status,
		timestamp,
		input.ID,
		input.WorkspaceID,
		input.Revision,
	)
	if err != nil {
		return LocalManagedBucket{}, fmt.Errorf("update local managed bucket: %w", err)
	}
	if err := requireChangedBucket(
		ctx,
		tx,
		result,
		input.WorkspaceID,
		input.ID,
	); err != nil {
		return LocalManagedBucket{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET display_name = ?,
			updated_at = ?
		WHERE id = (
			SELECT authorized_root_id
			FROM local_managed_buckets
			WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
		)
			AND workspace_id = ?
			AND deleted_at IS NULL
	`,
		input.DisplayName,
		timestamp,
		input.ID,
		input.WorkspaceID,
		input.WorkspaceID,
	); err != nil {
		return LocalManagedBucket{}, fmt.Errorf("sync local bucket root: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return LocalManagedBucket{}, fmt.Errorf("commit local bucket update: %w", err)
	}
	return repository.LocalManagedBucket(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) StartScan(
	ctx context.Context,
	workspaceID string,
	rootID string,
	scanID string,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin directory scan: %w", err)
	}
	defer tx.Rollback()

	var rootCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootID, workspaceID).Scan(&rootCount); err != nil {
		return fmt.Errorf("validate scan root: %w", err)
	}
	if rootCount == 0 {
		return ErrRootNotFound
	}

	timestamp := formatDatabaseTime(now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO directory_scans (
			id, workspace_id, authorized_root_id, status, started_at
		) VALUES (?, ?, ?, 'running', ?)
	`, scanID, workspaceID, rootID, timestamp); err != nil {
		return fmt.Errorf("create directory scan: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET last_scan_status = 'running', updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, timestamp, rootID, workspaceID); err != nil {
		return fmt.Errorf("mark root scan running: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit directory scan start: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ApplyScan(
	ctx context.Context,
	workspaceID string,
	rootID string,
	scanID string,
	observed []ObservedFile,
	now time.Time,
) (ScanSummary, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanSummary{}, fmt.Errorf("begin scan reconciliation: %w", err)
	}
	defer tx.Rollback()

	var providerID string
	if err := tx.QueryRowContext(ctx, `
		SELECT storage_provider_id
		FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootID, workspaceID).Scan(&providerID); errors.Is(err, sql.ErrNoRows) {
		return ScanSummary{}, ErrRootNotFound
	} else if err != nil {
		return ScanSummary{}, fmt.Errorf("read scan root provider: %w", err)
	}

	existing, err := loadStoredObjects(ctx, tx, workspaceID, rootID)
	if err != nil {
		return ScanSummary{}, err
	}

	observedKeys := make(map[string]struct{}, len(observed))
	for _, file := range observed {
		observedKeys[file.ObjectKey] = struct{}{}
	}
	byKey := make(map[string]*existingObject, len(existing))
	byFingerprint := make(map[string][]*existingObject)
	for index := range existing {
		object := &existing[index]
		byKey[object.ObjectKey] = object
		if _, remainsAtSameKey := observedKeys[object.ObjectKey]; !remainsAtSameKey &&
			object.QuickFingerprint != "" {
			byFingerprint[object.QuickFingerprint] = append(
				byFingerprint[object.QuickFingerprint],
				object,
			)
		}
	}

	summary := ScanSummary{Discovered: len(observed)}
	matched := make(map[string]bool, len(existing))
	observedObjectIDs := make(map[string]string, len(observed))
	timestamp := formatDatabaseTime(now)
	for _, file := range observed {
		if object := byKey[file.ObjectKey]; object != nil && !matched[object.ID] {
			switch {
			case object.Status == "missing":
				summary.Recovered++
			case object.QuickFingerprint != file.QuickFingerprint ||
				object.SizeBytes != file.SizeBytes:
				summary.Modified++
			default:
				summary.Unchanged++
			}
			if err := updateObservedObject(
				ctx,
				tx,
				object.ID,
				file,
				scanID,
				timestamp,
			); err != nil {
				return ScanSummary{}, err
			}
			matched[object.ID] = true
			observedObjectIDs[file.ObjectKey] = object.ID
			continue
		}

		moved := uniqueUnmatched(
			byFingerprint[file.QuickFingerprint],
			matched,
		)
		if moved != nil {
			if err := updateObservedObject(
				ctx,
				tx,
				moved.ID,
				file,
				scanID,
				timestamp,
			); err != nil {
				return ScanSummary{}, err
			}
			matched[moved.ID] = true
			observedObjectIDs[file.ObjectKey] = moved.ID
			summary.Moved++
			continue
		}

		objectID, err := newID()
		if err != nil {
			return ScanSummary{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id,
				object_key, kind, status, size_bytes, modified_at,
				quick_fingerprint, mime_type, first_discovered_at,
				last_seen_at, last_seen_scan_id, created_at, updated_at
			) VALUES (
				?, ?, ?, ?, ?, 'source', 'available', ?, ?, ?, ?, ?, ?, ?, ?, ?
			)
		`,
			objectID,
			workspaceID,
			providerID,
			rootID,
			file.ObjectKey,
			file.SizeBytes,
			formatDatabaseTime(file.ModifiedAt),
			file.QuickFingerprint,
			file.MIMEType,
			timestamp,
			timestamp,
			scanID,
			timestamp,
			timestamp,
		); err != nil {
			return ScanSummary{}, fmt.Errorf("create discovered storage object: %w", err)
		}
		observedObjectIDs[file.ObjectKey] = objectID
		summary.New++
	}

	for _, file := range observed {
		if err := syncLogicalAsset(
			ctx,
			tx,
			workspaceID,
			observedObjectIDs[file.ObjectKey],
			file,
			timestamp,
		); err != nil {
			return ScanSummary{}, err
		}
	}

	for index := range existing {
		object := &existing[index]
		if matched[object.ID] || object.Status != "available" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE storage_objects
			SET status = 'missing',
				missing_since = ?,
				updated_at = ?
			WHERE id = ? AND status = 'available' AND deleted_at IS NULL
		`, timestamp, timestamp, object.ID); err != nil {
			return ScanSummary{}, fmt.Errorf("mark storage object missing: %w", err)
		}
		summary.Missing++
	}

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return ScanSummary{}, fmt.Errorf("encode scan summary: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE directory_scans
		SET status = 'succeeded',
			discovered_count = ?,
			new_count = ?,
			modified_count = ?,
			moved_count = ?,
			missing_count = ?,
			recovered_count = ?,
			unchanged_count = ?,
			completed_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND authorized_root_id = ?
			AND status = 'running'
	`,
		summary.Discovered,
		summary.New,
		summary.Modified,
		summary.Moved,
		summary.Missing,
		summary.Recovered,
		summary.Unchanged,
		timestamp,
		scanID,
		workspaceID,
		rootID,
	); err != nil {
		return ScanSummary{}, fmt.Errorf("complete directory scan: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET status = 'available',
			last_scan_at = ?,
			last_scan_status = 'succeeded',
			last_scan_summary_json = ?,
			updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, timestamp, string(summaryJSON), timestamp, rootID, workspaceID); err != nil {
		return ScanSummary{}, fmt.Errorf("update authorized root scan state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ScanSummary{}, fmt.Errorf("commit scan reconciliation: %w", err)
	}
	return summary, nil
}

func syncLogicalAsset(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	objectID string,
	file ObservedFile,
	timestamp string,
) error {
	var assetID string
	var currentVersionID string
	var currentFingerprint string
	var versionNumber int
	err := tx.QueryRowContext(ctx, `
		SELECT
			assets.id,
			asset_versions.id,
			asset_versions.source_fingerprint,
			asset_versions.version_number
		FROM version_files
		JOIN asset_versions
			ON asset_versions.id = version_files.asset_version_id
		JOIN assets
			ON assets.id = asset_versions.asset_id
			AND assets.current_version_id = asset_versions.id
		WHERE version_files.storage_object_id = ?
			AND version_files.role = 'primary'
			AND asset_versions.deleted_at IS NULL
			AND assets.deleted_at IS NULL
	`, objectID).Scan(
		&assetID,
		&currentVersionID,
		&currentFingerprint,
		&versionNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return createLogicalAsset(
			ctx,
			tx,
			workspaceID,
			objectID,
			file,
			timestamp,
		)
	}
	if err != nil {
		return fmt.Errorf("read logical asset for storage object: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET name = ?, type = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, path.Base(file.ObjectKey), assetType(file.MIMEType), timestamp, assetID, workspaceID); err != nil {
		return fmt.Errorf("refresh logical asset metadata: %w", err)
	}
	if currentFingerprint == file.QuickFingerprint {
		return nil
	}

	nextVersionID, err := newID()
	if err != nil {
		return err
	}
	versionFileID, err := newID()
	if err != nil {
		return err
	}
	var createdBy string
	if err := tx.QueryRowContext(ctx, `
		SELECT created_by FROM assets
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, assetID, workspaceID).Scan(&createdBy); err != nil {
		return fmt.Errorf("read logical asset creator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions (
			id, workspace_id, asset_id, version_number, processing_status,
			source_filename, source_mime, source_size_bytes,
			source_fingerprint, media_metadata_json, created_by, created_at
		) VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?, '{}', ?, ?)
	`, nextVersionID, workspaceID, assetID, versionNumber+1,
		path.Base(file.ObjectKey), file.MIMEType, file.SizeBytes,
		file.QuickFingerprint, createdBy, timestamp,
	); err != nil {
		return fmt.Errorf("create logical asset version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO version_files (
			id, workspace_id, asset_version_id, storage_object_id, role, created_at
		) VALUES (?, ?, ?, ?, 'primary', ?)
	`, versionFileID, workspaceID, nextVersionID, objectID, timestamp); err != nil {
		return fmt.Errorf("link logical asset version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET current_version_id = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, nextVersionID, timestamp, assetID, workspaceID); err != nil {
		return fmt.Errorf("activate logical asset version: %w", err)
	}
	return nil
}

func createLogicalAsset(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	objectID string,
	file ObservedFile,
	timestamp string,
) error {
	assetID, err := newID()
	if err != nil {
		return err
	}
	versionID, err := newID()
	if err != nil {
		return err
	}
	versionFileID, err := newID()
	if err != nil {
		return err
	}
	var createdBy string
	if err := tx.QueryRowContext(ctx, `
		SELECT user_id
		FROM memberships
		WHERE workspace_id = ? AND role_key = 'owner' AND status = 'active'
		ORDER BY created_at
		LIMIT 1
	`, workspaceID).Scan(&createdBy); err != nil {
		return fmt.Errorf("read logical asset owner: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO assets (
			id, workspace_id, type, name, current_version_id,
			origin_storage_object_id, status,
			created_by, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, 1, ?, ?)
	`, assetID, workspaceID, assetType(file.MIMEType), path.Base(file.ObjectKey),
		versionID, objectID, createdBy, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("create logical asset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions (
			id, workspace_id, asset_id, version_number, processing_status,
			source_filename, source_mime, source_size_bytes,
			source_fingerprint, media_metadata_json, created_by, created_at
		) VALUES (?, ?, ?, 1, 'pending', ?, ?, ?, ?, '{}', ?, ?)
	`, versionID, workspaceID, assetID, path.Base(file.ObjectKey),
		file.MIMEType, file.SizeBytes, file.QuickFingerprint, createdBy, timestamp,
	); err != nil {
		return fmt.Errorf("create first logical asset version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO version_files (
			id, workspace_id, asset_version_id, storage_object_id, role, created_at
		) VALUES (?, ?, ?, ?, 'primary', ?)
	`, versionFileID, workspaceID, versionID, objectID, timestamp); err != nil {
		return fmt.Errorf("link first logical asset version: %w", err)
	}
	return nil
}

func assetType(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case mimeType == "application/pdf":
		return "pdf"
	default:
		return "other"
	}
}

func (repository *SQLiteRepository) FailScan(
	ctx context.Context,
	workspaceID string,
	rootID string,
	scanID string,
	code string,
	message string,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin failed scan update: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatDatabaseTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE directory_scans
		SET status = 'failed',
			error_code = ?,
			error_message = ?,
			completed_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND authorized_root_id = ?
			AND status = 'running'
	`, code, message, timestamp, scanID, workspaceID, rootID); err != nil {
		return fmt.Errorf("fail directory scan: %w", err)
	}

	rootStatus := "available"
	switch code {
	case "scan.root_missing":
		rootStatus = "missing"
	case "scan.permission_lost":
		rootStatus = "permission_lost"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET status = ?,
			last_scan_at = ?,
			last_scan_status = 'failed',
			last_scan_summary_json = NULL,
			updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootStatus, timestamp, timestamp, rootID, workspaceID); err != nil {
		return fmt.Errorf("update failed root scan state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed scan update: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ListObjects(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]StoredObject, error) {
	var rootCount int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootID, workspaceID).Scan(&rootCount); err != nil {
		return nil, fmt.Errorf("validate object root: %w", err)
	}
	if rootCount == 0 {
		return nil, ErrRootNotFound
	}

	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			id, workspace_id, storage_provider_id, authorized_root_id,
			object_key, status, size_bytes, modified_at,
			quick_fingerprint, mime_type, first_discovered_at,
			last_seen_at, missing_since, created_at, updated_at
		FROM storage_objects
		WHERE workspace_id = ?
			AND authorized_root_id = ?
			AND deleted_at IS NULL
		ORDER BY
			CASE status WHEN 'available' THEN 0 WHEN 'missing' THEN 1 ELSE 2 END,
			object_key
	`, workspaceID, rootID)
	if err != nil {
		return nil, fmt.Errorf("list storage objects: %w", err)
	}
	defer rows.Close()

	objects := make([]StoredObject, 0)
	for rows.Next() {
		object, err := scanStoredObject(rows)
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage objects: %w", err)
	}
	return objects, nil
}

func (repository *SQLiteRepository) Object(
	ctx context.Context,
	workspaceID string,
	objectID string,
) (StoredObject, error) {
	object, err := scanStoredObject(repository.db.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, storage_provider_id, authorized_root_id,
			object_key, status, size_bytes, modified_at,
			quick_fingerprint, mime_type, first_discovered_at,
			last_seen_at, missing_since, created_at, updated_at
		FROM storage_objects
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, objectID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredObject{}, ErrObjectNotFound
	}
	return object, err
}

func (repository *SQLiteRepository) ObjectForAsset(
	ctx context.Context,
	workspaceID string,
	assetID string,
) (StoredObject, error) {
	object, err := scanStoredObject(repository.db.QueryRowContext(ctx, `
		SELECT
			so.id, so.workspace_id, so.storage_provider_id,
			so.authorized_root_id, so.object_key, so.status, so.size_bytes,
			so.modified_at, so.quick_fingerprint, so.mime_type,
			so.first_discovered_at, so.last_seen_at, so.missing_since,
			so.created_at, so.updated_at
		FROM assets a
		JOIN asset_versions av ON av.id = a.current_version_id
			AND av.deleted_at IS NULL
		JOIN version_files vf ON vf.asset_version_id = av.id
			AND vf.role = 'primary'
		JOIN storage_objects so ON so.id = vf.storage_object_id
			AND so.deleted_at IS NULL
		WHERE a.id = ? AND a.workspace_id = ?
			AND a.deleted_at IS NULL
	`, assetID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredObject{}, ErrObjectNotFound
	}
	return object, err
}

func (repository *SQLiteRepository) ObjectForAssetVersion(
	ctx context.Context,
	workspaceID string,
	versionID string,
) (StoredObject, error) {
	object, err := scanStoredObject(repository.db.QueryRowContext(ctx, `
		SELECT
			so.id, so.workspace_id, so.storage_provider_id,
			so.authorized_root_id, so.object_key, so.status, so.size_bytes,
			so.modified_at, so.quick_fingerprint, so.mime_type,
			so.first_discovered_at, so.last_seen_at, so.missing_since,
			so.created_at, so.updated_at
		FROM asset_versions av
		JOIN version_files vf ON vf.asset_version_id = av.id
			AND vf.role = 'primary'
		JOIN storage_objects so ON so.id = vf.storage_object_id
			AND so.deleted_at IS NULL
		WHERE av.id = ? AND av.workspace_id = ?
			AND av.deleted_at IS NULL
	`, versionID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredObject{}, ErrObjectNotFound
	}
	return object, err
}

func (repository *SQLiteRepository) ProjectArchivePlan(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (ProjectArchivePlan, error) {
	var targetRootID sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT grant.authorized_root_id
		FROM project_storage_selections selection
		JOIN project_storage_grants grant
			ON grant.id = selection.grant_id
			AND grant.workspace_id = selection.workspace_id
		WHERE selection.workspace_id = ?
			AND selection.project_id = ?
			AND selection.purpose = 'archive'
			AND grant.status = 'active'
	`, workspaceID, projectID).Scan(&targetRootID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectArchivePlan{}, nil
	}
	if err != nil {
		return ProjectArchivePlan{}, fmt.Errorf("resolve project archive target: %w", err)
	}
	if !targetRootID.Valid {
		return ProjectArchivePlan{}, nil
	}

	rows, err := repository.db.QueryContext(ctx, `
		SELECT DISTINCT
			object.id, object.workspace_id, object.storage_provider_id,
			object.authorized_root_id, object.object_key, object.status,
			object.size_bytes, object.modified_at, object.quick_fingerprint,
			object.mime_type, object.first_discovered_at, object.last_seen_at,
			object.missing_since, object.created_at, object.updated_at
		FROM project_assets project_asset
		JOIN asset_versions version
			ON version.asset_id = project_asset.asset_id
			AND version.workspace_id = project_asset.workspace_id
			AND version.deleted_at IS NULL
		JOIN version_files file ON file.asset_version_id = version.id
		JOIN storage_objects object
			ON object.id = file.storage_object_id
			AND object.workspace_id = file.workspace_id
			AND object.deleted_at IS NULL
		WHERE project_asset.workspace_id = ?
			AND project_asset.project_id = ?
			AND project_asset.status = 'active'
			AND object.status = 'available'
		ORDER BY object.created_at
	`, workspaceID, projectID)
	if err != nil {
		return ProjectArchivePlan{}, fmt.Errorf("list project archive objects: %w", err)
	}
	defer rows.Close()

	plan := ProjectArchivePlan{TargetRootID: targetRootID.String}
	for rows.Next() {
		object, scanErr := scanStoredObject(rows)
		if scanErr != nil {
			return ProjectArchivePlan{}, scanErr
		}
		plan.Objects = append(plan.Objects, object)
	}
	if err := rows.Err(); err != nil {
		return ProjectArchivePlan{}, fmt.Errorf("iterate project archive objects: %w", err)
	}
	return plan, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAuthorizedRoot(scanner rowScanner) (AuthorizedRoot, error) {
	var root AuthorizedRoot
	var scanEnabled int
	var createdAt string
	var updatedAt string
	var lastScanAt sql.NullString
	var lastScanStatus sql.NullString
	var lastScanSummary sql.NullString
	if err := scanner.Scan(
		&root.ID,
		&root.WorkspaceID,
		&root.StorageProviderID,
		&root.DisplayName,
		&root.DisplayPath,
		&root.Mode,
		&scanEnabled,
		&root.Status,
		&root.Revision,
		&createdAt,
		&updatedAt,
		&lastScanAt,
		&lastScanStatus,
		&lastScanSummary,
	); err != nil {
		return AuthorizedRoot{}, err
	}
	root.ScanEnabled = scanEnabled != 0

	var err error
	root.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return AuthorizedRoot{}, fmt.Errorf("parse authorized root creation time: %w", err)
	}
	root.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return AuthorizedRoot{}, fmt.Errorf("parse authorized root update time: %w", err)
	}
	root.LastScanAt, root.LastScanStatus, root.LastScanSummary, err =
		parseScanState(lastScanAt, lastScanStatus, lastScanSummary)
	if err != nil {
		return AuthorizedRoot{}, err
	}
	return root, nil
}

func scanLocalManagedBucket(scanner rowScanner) (LocalManagedBucket, error) {
	var bucket LocalManagedBucket
	var quotaBytes sql.NullInt64
	var usedBytes sql.NullInt64
	var projectAvailable int
	var createdBy sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&bucket.ID,
		&bucket.WorkspaceID,
		&bucket.AuthorizedRootID,
		&bucket.StorageProviderID,
		&bucket.DisplayName,
		&bucket.DisplayPath,
		&bucket.Purpose,
		&quotaBytes,
		&usedBytes,
		&bucket.UploadSecurityPolicy,
		&projectAvailable,
		&bucket.Status,
		&createdBy,
		&bucket.Revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return LocalManagedBucket{}, err
	}
	if quotaBytes.Valid {
		bucket.QuotaBytes = &quotaBytes.Int64
	}
	if usedBytes.Valid {
		bucket.UsedBytesEstimate = &usedBytes.Int64
	}
	bucket.ProjectAvailable = projectAvailable != 0
	if createdBy.Valid {
		bucket.CreatedBy = &createdBy.String
	}

	var err error
	bucket.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return LocalManagedBucket{}, fmt.Errorf("parse local bucket creation time: %w", err)
	}
	bucket.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return LocalManagedBucket{}, fmt.Errorf("parse local bucket update time: %w", err)
	}
	return bucket, nil
}

type rootWriteQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func requireChangedRoot(
	ctx context.Context,
	queryer rootWriteQueryer,
	result sql.Result,
	workspaceID string,
	rootID string,
) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read authorized root write result: %w", err)
	}
	if changed > 0 {
		return nil
	}

	var revision int
	err = queryer.QueryRowContext(ctx, `
		SELECT revision
		FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootID, workspaceID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRootNotFound
	}
	if err != nil {
		return fmt.Errorf("classify authorized root write: %w", err)
	}
	return ErrRevisionConflict
}

func requireChangedBucket(
	ctx context.Context,
	queryer rootWriteQueryer,
	result sql.Result,
	workspaceID string,
	bucketID string,
) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read local bucket write result: %w", err)
	}
	if changed > 0 {
		return nil
	}

	var revision int
	err = queryer.QueryRowContext(ctx, `
		SELECT revision
		FROM local_managed_buckets
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, bucketID, workspaceID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBucketNotFound
	}
	if err != nil {
		return fmt.Errorf("classify local bucket write: %w", err)
	}
	return ErrBucketRevisionConflict
}

type existingObject struct {
	ID               string
	ObjectKey        string
	Status           string
	SizeBytes        int64
	QuickFingerprint string
}

func loadStoredObjects(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	rootID string,
) ([]existingObject, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, object_key, status, COALESCE(size_bytes, 0),
			COALESCE(quick_fingerprint, '')
		FROM storage_objects
		WHERE workspace_id = ?
			AND authorized_root_id = ?
			AND deleted_at IS NULL
	`, workspaceID, rootID)
	if err != nil {
		return nil, fmt.Errorf("load storage objects for scan: %w", err)
	}
	defer rows.Close()

	objects := make([]existingObject, 0)
	for rows.Next() {
		var object existingObject
		if err := rows.Scan(
			&object.ID,
			&object.ObjectKey,
			&object.Status,
			&object.SizeBytes,
			&object.QuickFingerprint,
		); err != nil {
			return nil, fmt.Errorf("scan existing storage object: %w", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing storage objects: %w", err)
	}
	return objects, nil
}

func updateObservedObject(
	ctx context.Context,
	tx *sql.Tx,
	objectID string,
	file ObservedFile,
	scanID string,
	timestamp string,
) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE storage_objects
		SET object_key = ?,
			status = 'available',
			size_bytes = ?,
			modified_at = ?,
			quick_fingerprint = ?,
			mime_type = ?,
			last_seen_at = ?,
			last_seen_scan_id = ?,
			missing_since = NULL,
			updated_at = ?
		WHERE id = ? AND deleted_at IS NULL
	`,
		file.ObjectKey,
		file.SizeBytes,
		formatDatabaseTime(file.ModifiedAt),
		file.QuickFingerprint,
		file.MIMEType,
		timestamp,
		scanID,
		timestamp,
		objectID,
	); err != nil {
		return fmt.Errorf("update discovered storage object: %w", err)
	}
	return nil
}

func uniqueUnmatched(
	candidates []*existingObject,
	matched map[string]bool,
) *existingObject {
	var result *existingObject
	for _, candidate := range candidates {
		if matched[candidate.ID] {
			continue
		}
		if result != nil {
			return nil
		}
		result = candidate
	}
	return result
}

func scanStoredObject(scanner rowScanner) (StoredObject, error) {
	var object StoredObject
	var sizeBytes sql.NullInt64
	var modifiedAt sql.NullString
	var fingerprint sql.NullString
	var mimeType sql.NullString
	var firstDiscoveredAt string
	var lastSeenAt sql.NullString
	var missingSince sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&object.ID,
		&object.WorkspaceID,
		&object.StorageProviderID,
		&object.AuthorizedRootID,
		&object.ObjectKey,
		&object.Status,
		&sizeBytes,
		&modifiedAt,
		&fingerprint,
		&mimeType,
		&firstDiscoveredAt,
		&lastSeenAt,
		&missingSince,
		&createdAt,
		&updatedAt,
	); err != nil {
		return StoredObject{}, err
	}
	if sizeBytes.Valid {
		object.SizeBytes = sizeBytes.Int64
	}
	if fingerprint.Valid {
		object.QuickFingerprint = fingerprint.String
	}
	if mimeType.Valid {
		object.MIMEType = mimeType.String
	}

	var err error
	if modifiedAt.Valid {
		object.ModifiedAt, err = parseDatabaseTime(modifiedAt.String)
		if err != nil {
			return StoredObject{}, fmt.Errorf("parse object modification time: %w", err)
		}
	}
	object.FirstDiscoveredAt, err = parseDatabaseTime(firstDiscoveredAt)
	if err != nil {
		return StoredObject{}, fmt.Errorf("parse object discovery time: %w", err)
	}
	object.LastSeenAt, err = nullableDatabaseTime(lastSeenAt)
	if err != nil {
		return StoredObject{}, fmt.Errorf("parse object last seen time: %w", err)
	}
	object.MissingSince, err = nullableDatabaseTime(missingSince)
	if err != nil {
		return StoredObject{}, fmt.Errorf("parse object missing time: %w", err)
	}
	object.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return StoredObject{}, fmt.Errorf("parse object creation time: %w", err)
	}
	object.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return StoredObject{}, fmt.Errorf("parse object update time: %w", err)
	}
	return object, nil
}

func ensureLocalProvider(
	ctx context.Context,
	tx *sql.Tx,
	record registerRootRecord,
) (string, error) {
	var providerID string
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM storage_providers
		WHERE workspace_id = ?
			AND kind = 'local'
			AND name = ?
			AND deleted_at IS NULL
		LIMIT 1
	`, record.WorkspaceID, localSourceProviderName).Scan(&providerID)
	if err == nil {
		return providerID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("find local storage provider: %w", err)
	}

	capabilities, err := json.Marshal(map[string]bool{
		"read":      true,
		"rangeRead": true,
		"list":      true,
		"write":     false,
		"delete":    false,
	})
	if err != nil {
		return "", fmt.Errorf("encode local provider capabilities: %w", err)
	}
	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO storage_providers (
			id, workspace_id, kind, name, status,
			config_json, capabilities_json, created_at, updated_at
		) VALUES (?, ?, 'local', ?, 'active', '{}', ?, ?, ?)
	`,
		record.ProviderID,
		record.WorkspaceID,
		localSourceProviderName,
		string(capabilities),
		now,
		now,
	); err != nil {
		return "", fmt.Errorf("create local storage provider: %w", err)
	}
	return record.ProviderID, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatDatabaseTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000Z")
}

func parseDatabaseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func nullableDatabaseTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseDatabaseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseScanState(
	lastScanAt sql.NullString,
	lastScanStatus sql.NullString,
	lastScanSummary sql.NullString,
) (*time.Time, *string, *ScanSummary, error) {
	parsedAt, err := nullableDatabaseTime(lastScanAt)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse root last scan time: %w", err)
	}

	var status *string
	if lastScanStatus.Valid {
		status = &lastScanStatus.String
	}

	var summary *ScanSummary
	if lastScanSummary.Valid {
		var parsed ScanSummary
		if err := json.Unmarshal([]byte(lastScanSummary.String), &parsed); err != nil {
			return nil, nil, nil, fmt.Errorf("parse root scan summary: %w", err)
		}
		summary = &parsed
	}
	return parsedAt, status, summary, nil
}

// ListLocalRootLocations returns every live local root together with the
// absolute path stored for it.
//
// Startup uses it to spot roots still pointing at a different instance's data
// directory: restore swaps the data directory but does not rewrite the paths in
// local_path_secrets, so a restore into a different directory would keep reading
// and writing another instance's files without any visible symptom.
func (repository *SQLiteRepository) ListLocalRootLocations(
	ctx context.Context,
) ([]LocalRootLocation, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			authorized_roots.id,
			authorized_roots.workspace_id,
			authorized_roots.display_name,
			local_path_secrets.path_text
		FROM authorized_roots
		JOIN storage_providers
			ON storage_providers.id = authorized_roots.storage_provider_id
			AND storage_providers.workspace_id = authorized_roots.workspace_id
		JOIN local_path_secrets
			ON local_path_secrets.id = authorized_roots.path_secret_ref
			AND local_path_secrets.workspace_id = authorized_roots.workspace_id
		WHERE authorized_roots.deleted_at IS NULL
			AND storage_providers.kind = 'local'
		ORDER BY authorized_roots.display_name, authorized_roots.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list local root locations: %w", err)
	}
	defer rows.Close()

	locations := make([]LocalRootLocation, 0, 8)
	for rows.Next() {
		var location LocalRootLocation
		if err := rows.Scan(
			&location.RootID,
			&location.WorkspaceID,
			&location.DisplayName,
			&location.PathText,
		); err != nil {
			return nil, fmt.Errorf("read local root location: %w", err)
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read local root locations: %w", err)
	}
	return locations, nil
}
