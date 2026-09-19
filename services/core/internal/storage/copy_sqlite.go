package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type copyTaskQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (repository *SQLiteRepository) CreateCopyTask(
	ctx context.Context,
	record createCopyTaskRecord,
) (CopyTask, error) {
	timestamp := formatDatabaseTime(record.Now)
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO storage_copy_tasks (
			id, workspace_id, source_storage_object_id, source_root_id,
			source_object_key, target_root_id, target_object_key, status,
			total_bytes, copied_bytes, temp_file_name, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', ?, 0, ?, ?, ?)
	`,
		record.ID,
		record.WorkspaceID,
		record.Source.ID,
		record.Source.AuthorizedRootID,
		record.Source.ObjectKey,
		record.TargetRoot.ID,
		record.TargetKey,
		record.Source.SizeBytes,
		record.TempFile,
		timestamp,
		timestamp,
	)
	if err != nil {
		return CopyTask{}, fmt.Errorf("create storage copy task: %w", err)
	}
	return repository.CopyTask(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) AttachCopyTaskJob(
	ctx context.Context,
	workspaceID string,
	taskID string,
	jobID string,
	now time.Time,
) (CopyTask, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_copy_tasks
		SET job_id = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND job_id IS NULL
	`,
		jobID,
		formatDatabaseTime(now),
		taskID,
		workspaceID,
	)
	if err := requireCopyTaskChanged(ctx, repository.db, result, err, workspaceID, taskID); err != nil {
		return CopyTask{}, err
	}
	return repository.CopyTask(ctx, workspaceID, taskID)
}

func (repository *SQLiteRepository) CopyTask(
	ctx context.Context,
	workspaceID string,
	taskID string,
) (CopyTask, error) {
	return scanCopyTask(repository.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, source_storage_object_id, source_root_id,
			source_object_key, target_root_id, target_object_key,
			target_storage_object_id, job_id, status, total_bytes, copied_bytes,
			content_hash, content_hash_algorithm, temp_file_name, error_code,
			error_message, revision, created_at, updated_at, completed_at
		FROM storage_copy_tasks
		WHERE id = ? AND workspace_id = ?
	`, taskID, workspaceID))
}

func (repository *SQLiteRepository) UpdateCopyTaskRunning(
	ctx context.Context,
	workspaceID string,
	taskID string,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_copy_tasks
		SET status = 'running', error_code = NULL, error_message = NULL,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ?
			AND status IN ('queued', 'failed', 'running')
	`, formatDatabaseTime(now), taskID, workspaceID)
	return requireCopyTaskChanged(ctx, repository.db, result, err, workspaceID, taskID)
}

func (repository *SQLiteRepository) UpdateCopyTaskProgress(
	ctx context.Context,
	workspaceID string,
	taskID string,
	copiedBytes int64,
	totalBytes int64,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_copy_tasks
		SET copied_bytes = ?, total_bytes = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ?
			AND status IN ('queued', 'running', 'failed')
	`,
		copiedBytes,
		totalBytes,
		formatDatabaseTime(now),
		taskID,
		workspaceID,
	)
	return requireCopyTaskChanged(ctx, repository.db, result, err, workspaceID, taskID)
}

func (repository *SQLiteRepository) CompleteCopyTask(
	ctx context.Context,
	workspaceID string,
	taskID string,
	targetObjectID string,
	contentHash string,
	algorithm string,
	now time.Time,
) (CopyTask, error) {
	timestamp := formatDatabaseTime(now)
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_copy_tasks
		SET target_storage_object_id = ?, status = 'succeeded',
			copied_bytes = total_bytes, content_hash = ?,
			content_hash_algorithm = ?, error_code = NULL,
			error_message = NULL, revision = revision + 1,
			updated_at = ?, completed_at = ?
		WHERE id = ? AND workspace_id = ?
			AND status IN ('queued', 'running', 'failed')
	`,
		targetObjectID,
		nullableText(contentHash),
		nullableText(algorithm),
		timestamp,
		timestamp,
		taskID,
		workspaceID,
	)
	if err := requireCopyTaskChanged(ctx, repository.db, result, err, workspaceID, taskID); err != nil {
		return CopyTask{}, err
	}
	return repository.CopyTask(ctx, workspaceID, taskID)
}

func (repository *SQLiteRepository) FailCopyTask(
	ctx context.Context,
	workspaceID string,
	taskID string,
	code string,
	message string,
	cancelled bool,
	now time.Time,
) error {
	status := "failed"
	if cancelled {
		status = "cancelled"
	}
	timestamp := formatDatabaseTime(now)
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_copy_tasks
		SET status = ?, error_code = ?, error_message = ?,
			revision = revision + 1, updated_at = ?,
			completed_at = CASE WHEN ? THEN ? ELSE completed_at END
		WHERE id = ? AND workspace_id = ?
			AND status IN ('queued', 'running', 'failed')
	`,
		status,
		nullableText(code),
		nullableText(message),
		timestamp,
		cancelled,
		timestamp,
		taskID,
		workspaceID,
	)
	return requireCopyTaskChanged(ctx, repository.db, result, err, workspaceID, taskID)
}

func (repository *SQLiteRepository) UpsertCopiedObject(
	ctx context.Context,
	record copiedObjectRecord,
) (StoredObject, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return StoredObject{}, fmt.Errorf("begin copied object upsert: %w", err)
	}
	defer tx.Rollback()

	var providerID string
	if err := tx.QueryRowContext(ctx, `
		SELECT storage_provider_id
		FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, record.RootID, record.WorkspaceID).Scan(&providerID); errors.Is(err, sql.ErrNoRows) {
		return StoredObject{}, ErrRootNotFound
	} else if err != nil {
		return StoredObject{}, fmt.Errorf("read target root provider: %w", err)
	}

	timestamp := formatDatabaseTime(record.Now)
	modifiedAt := formatDatabaseTime(record.Observed.ModifiedAt)
	var objectID string
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM storage_objects
		WHERE workspace_id = ? AND authorized_root_id = ?
			AND object_key = ? AND deleted_at IS NULL
	`, record.WorkspaceID, record.RootID, record.Observed.ObjectKey).Scan(&objectID)
	if errors.Is(err, sql.ErrNoRows) {
		objectID, err = newID()
		if err != nil {
			return StoredObject{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id,
				object_key, kind, status, size_bytes, modified_at,
				quick_fingerprint, content_hash, content_hash_algorithm,
				mime_type, first_discovered_at, last_seen_at, created_at,
				updated_at
			) VALUES (
				?, ?, ?, ?, ?, 'source', 'available', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			)
		`,
			objectID,
			record.WorkspaceID,
			providerID,
			record.RootID,
			record.Observed.ObjectKey,
			record.Observed.SizeBytes,
			modifiedAt,
			record.Observed.QuickFingerprint,
			nullableText(record.ContentHash),
			nullableText(record.Algorithm),
			record.Observed.MIMEType,
			timestamp,
			timestamp,
			timestamp,
			timestamp,
		); err != nil {
			return StoredObject{}, fmt.Errorf("insert copied object: %w", err)
		}
	} else if err != nil {
		return StoredObject{}, fmt.Errorf("find copied object: %w", err)
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE storage_objects
		SET storage_provider_id = ?, status = 'available', size_bytes = ?,
			modified_at = ?, quick_fingerprint = ?, content_hash = ?,
			content_hash_algorithm = ?, mime_type = ?,
			last_seen_at = ?, missing_since = NULL, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`,
		providerID,
		record.Observed.SizeBytes,
		modifiedAt,
		record.Observed.QuickFingerprint,
		nullableText(record.ContentHash),
		nullableText(record.Algorithm),
		record.Observed.MIMEType,
		timestamp,
		timestamp,
		objectID,
		record.WorkspaceID,
	); err != nil {
		return StoredObject{}, fmt.Errorf("update copied object: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return StoredObject{}, fmt.Errorf("commit copied object upsert: %w", err)
	}
	return repository.Object(ctx, record.WorkspaceID, objectID)
}

func scanCopyTask(scanner rowScanner) (CopyTask, error) {
	var item CopyTask
	var targetObjectID sql.NullString
	var jobID sql.NullString
	var contentHash sql.NullString
	var contentHashAlgorithm sql.NullString
	var errorCode sql.NullString
	var errorMessage sql.NullString
	var createdAt string
	var updatedAt string
	var completedAt sql.NullString
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.SourceStorageObjectID,
		&item.SourceRootID,
		&item.SourceObjectKey,
		&item.TargetRootID,
		&item.TargetObjectKey,
		&targetObjectID,
		&jobID,
		&item.Status,
		&item.TotalBytes,
		&item.CopiedBytes,
		&contentHash,
		&contentHashAlgorithm,
		&item.TempFileName,
		&errorCode,
		&errorMessage,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CopyTask{}, ErrObjectNotFound
	}
	if err != nil {
		return CopyTask{}, fmt.Errorf("scan storage copy task: %w", err)
	}
	if targetObjectID.Valid {
		item.TargetStorageObjectID = &targetObjectID.String
	}
	if jobID.Valid {
		item.JobID = &jobID.String
	}
	if contentHash.Valid {
		item.ContentHash = &contentHash.String
	}
	if contentHashAlgorithm.Valid {
		item.ContentHashAlgorithm = &contentHashAlgorithm.String
	}
	if errorCode.Valid {
		item.ErrorCode = &errorCode.String
	}
	if errorMessage.Valid {
		item.ErrorMessage = &errorMessage.String
	}
	var parseErr error
	item.CreatedAt, parseErr = parseDatabaseTime(createdAt)
	if parseErr != nil {
		return CopyTask{}, fmt.Errorf("parse copy task creation time: %w", parseErr)
	}
	item.UpdatedAt, parseErr = parseDatabaseTime(updatedAt)
	if parseErr != nil {
		return CopyTask{}, fmt.Errorf("parse copy task update time: %w", parseErr)
	}
	item.CompletedAt, parseErr = nullableDatabaseTime(completedAt)
	if parseErr != nil {
		return CopyTask{}, fmt.Errorf("parse copy task completion time: %w", parseErr)
	}
	return item, nil
}

func requireCopyTaskChanged(
	ctx context.Context,
	db copyTaskQueryer,
	result sql.Result,
	err error,
	workspaceID string,
	taskID string,
) error {
	if err != nil {
		return fmt.Errorf("update storage copy task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read copy task update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM storage_copy_tasks
		WHERE id = ? AND workspace_id = ?
	`, taskID, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("read copy task update conflict: %w", err)
	}
	if count == 0 {
		return ErrObjectNotFound
	}
	return ErrRevisionConflict
}
