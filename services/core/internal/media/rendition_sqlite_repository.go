package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SQLiteRenditionRepository struct {
	db *sql.DB
}

func NewSQLiteRenditionRepository(db *sql.DB) *SQLiteRenditionRepository {
	return &SQLiteRenditionRepository{db: db}
}

func (repository *SQLiteRenditionRepository) TargetRoot(
	ctx context.Context,
	workspaceID string,
	sourceStorageObjectID string,
) (*string, error) {
	var rootID sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		WITH candidate_projects AS (
			SELECT project_id, 0 AS priority, created_at
			FROM upload_checks
			WHERE workspace_id = ?
				AND storage_object_id = ?
				AND project_id IS NOT NULL
			UNION ALL
			SELECT pa.project_id, 1 AS priority, pa.created_at
			FROM version_files vf
			JOIN asset_versions av ON av.id = vf.asset_version_id
			JOIN project_assets pa
				ON pa.asset_id = av.asset_id
				AND pa.workspace_id = av.workspace_id
				AND pa.status = 'active'
			WHERE vf.storage_object_id = ?
				AND av.workspace_id = ?
				AND av.deleted_at IS NULL
		)
		SELECT grant.authorized_root_id
		FROM candidate_projects candidate
		JOIN project_storage_selections selection
			ON selection.project_id = candidate.project_id
			AND selection.workspace_id = ?
			AND selection.purpose = 'default_rendition'
		JOIN project_storage_grants grant
			ON grant.id = selection.grant_id
			AND grant.workspace_id = selection.workspace_id
		ORDER BY candidate.priority, candidate.created_at
		LIMIT 1
	`, workspaceID, sourceStorageObjectID, sourceStorageObjectID,
		workspaceID, workspaceID).Scan(&rootID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve rendition target root: %w", err)
	}
	if !rootID.Valid {
		return nil, nil
	}
	return &rootID.String, nil
}

func (repository *SQLiteRenditionRepository) ListRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]Rendition, error) {
	rows, err := repository.db.QueryContext(ctx, renditionSelect+`
		JOIN storage_objects
			ON storage_objects.id = renditions.source_storage_object_id
		WHERE renditions.workspace_id = ?
			AND storage_objects.authorized_root_id = ?
			AND storage_objects.deleted_at IS NULL
		ORDER BY storage_objects.object_key, renditions.kind
	`, workspaceID, rootID)
	if err != nil {
		return nil, fmt.Errorf("list renditions: %w", err)
	}
	defer rows.Close()

	items := make([]Rendition, 0)
	for rows.Next() {
		item, err := scanRendition(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate renditions: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRenditionRepository) Get(
	ctx context.Context,
	workspaceID string,
	renditionID string,
) (Rendition, error) {
	item, err := scanRendition(repository.db.QueryRowContext(
		ctx,
		renditionSelect+" WHERE renditions.id = ? AND renditions.workspace_id = ?",
		renditionID,
		workspaceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Rendition{}, ErrRenditionNotFound
	}
	return item, err
}

func (repository *SQLiteRenditionRepository) Begin(
	ctx context.Context,
	rendition Rendition,
	now time.Time,
) (Rendition, error) {
	timestamp := formatTime(now)
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO renditions (
			id, workspace_id, source_storage_object_id, authorized_root_id,
			asset_version_id,
			kind, profile_key, profile_version, source_fingerprint,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'processing', ?, ?)
		ON CONFLICT (
			source_storage_object_id, kind, profile_key, profile_version
		) DO UPDATE SET
			source_fingerprint = excluded.source_fingerprint,
			authorized_root_id = excluded.authorized_root_id,
			status = 'processing',
			object_key = NULL,
			mime_type = NULL,
			width = NULL,
			height = NULL,
			duration_us = NULL,
			size_bytes = NULL,
			metadata_json = '{}',
			error_code = NULL,
			error_message = NULL,
			updated_at = excluded.updated_at,
			completed_at = NULL
	`,
		rendition.ID, rendition.WorkspaceID,
		rendition.SourceStorageObjectID, rendition.AuthorizedRootID,
		rendition.AssetVersionID,
		rendition.Kind, rendition.ProfileKey, rendition.ProfileVersion,
		rendition.SourceFingerprint, timestamp, timestamp,
	)
	if err != nil {
		return Rendition{}, fmt.Errorf("begin rendition: %w", err)
	}
	return repository.findProfile(
		ctx,
		rendition.WorkspaceID,
		rendition.SourceStorageObjectID,
		rendition.Kind,
		rendition.ProfileKey,
		rendition.ProfileVersion,
	)
}

func (repository *SQLiteRenditionRepository) Ready(
	ctx context.Context,
	renditionID string,
	result RenditionResult,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rendition completion: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE renditions
		SET status = 'ready', object_key = ?, mime_type = ?,
			width = ?, height = ?, duration_us = ?, size_bytes = ?,
			metadata_json = ?,
			error_code = NULL, error_message = NULL,
			updated_at = ?, completed_at = ?
		WHERE id = ?
	`,
		result.ObjectKey, result.MIMEType, result.Width, result.Height,
		result.DurationUS, result.SizeBytes, defaultJSON(result.MetadataJSON),
		timestamp, timestamp, renditionID,
	); err != nil {
		return fmt.Errorf("complete rendition: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"DELETE FROM rendition_segments WHERE rendition_id = ?",
		renditionID,
	); err != nil {
		return fmt.Errorf("replace rendition segments: %w", err)
	}
	for _, segment := range result.Segments {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rendition_segments (
				id, rendition_id, role, sequence, file_name,
				object_key, mime_type, size_bytes, duration_us, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			segment.ID, renditionID, segment.Role, segment.Sequence,
			segment.FileName, segment.ObjectKey, segment.MIMEType,
			segment.SizeBytes, segment.DurationUS, timestamp,
		); err != nil {
			return fmt.Errorf("store rendition segment: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rendition completion: %w", err)
	}
	return nil
}

func (repository *SQLiteRenditionRepository) Fail(
	ctx context.Context,
	renditionID string,
	code string,
	message string,
	now time.Time,
) error {
	_, err := repository.db.ExecContext(ctx, `
		UPDATE renditions
		SET status = 'failed', error_code = ?, error_message = ?,
			updated_at = ?, completed_at = ?
		WHERE id = ?
	`, code, message, formatTime(now), formatTime(now), renditionID)
	if err != nil {
		return fmt.Errorf("fail rendition: %w", err)
	}
	return nil
}

func (repository *SQLiteRenditionRepository) findProfile(
	ctx context.Context,
	workspaceID string,
	sourceID string,
	kind string,
	profileKey string,
	profileVersion int,
) (Rendition, error) {
	return scanRendition(repository.db.QueryRowContext(ctx, renditionSelect+`
		WHERE renditions.workspace_id = ?
			AND renditions.source_storage_object_id = ?
			AND renditions.kind = ?
			AND renditions.profile_key = ?
			AND renditions.profile_version = ?
	`, workspaceID, sourceID, kind, profileKey, profileVersion))
}

func (repository *SQLiteRenditionRepository) Segment(
	ctx context.Context,
	workspaceID string,
	renditionID string,
	fileName string,
) (RenditionSegment, error) {
	var item RenditionSegment
	var createdAt string
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			rendition_segments.id, rendition_segments.rendition_id,
			renditions.authorized_root_id,
			rendition_segments.role, rendition_segments.sequence,
			rendition_segments.file_name, rendition_segments.object_key,
			rendition_segments.mime_type, rendition_segments.size_bytes,
			rendition_segments.duration_us, rendition_segments.created_at
		FROM rendition_segments
		JOIN renditions ON renditions.id = rendition_segments.rendition_id
		WHERE rendition_segments.rendition_id = ?
			AND rendition_segments.file_name = ?
			AND renditions.workspace_id = ?
			AND renditions.status = 'ready'
	`, renditionID, fileName, workspaceID).Scan(
		&item.ID, &item.RenditionID, &item.AuthorizedRootID,
		&item.Role, &item.Sequence,
		&item.FileName, &item.ObjectKey, &item.MIMEType,
		&item.SizeBytes, &item.DurationUS, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RenditionSegment{}, ErrRenditionNotFound
	}
	if err != nil {
		return RenditionSegment{}, fmt.Errorf("read rendition segment: %w", err)
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return RenditionSegment{}, fmt.Errorf("parse segment creation time: %w", err)
	}
	return item, nil
}

const renditionSelect = `
	SELECT
		renditions.id, renditions.workspace_id,
		renditions.source_storage_object_id, renditions.authorized_root_id,
		renditions.asset_version_id,
		renditions.kind, renditions.profile_key, renditions.profile_version,
		renditions.source_fingerprint, renditions.status,
		renditions.object_key, renditions.mime_type,
		renditions.width, renditions.height, renditions.duration_us,
		renditions.size_bytes, renditions.metadata_json,
		renditions.error_code, renditions.error_message,
		renditions.created_at, renditions.updated_at, renditions.completed_at
	FROM renditions
`

func scanRendition(scanner rowScanner) (Rendition, error) {
	var item Rendition
	var createdAt string
	var updatedAt string
	var completedAt sql.NullString
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.SourceStorageObjectID,
		&item.AuthorizedRootID, &item.AssetVersionID,
		&item.Kind, &item.ProfileKey,
		&item.ProfileVersion, &item.SourceFingerprint, &item.Status,
		&item.ObjectKey, &item.MIMEType, &item.Width, &item.Height,
		&item.DurationUS, &item.SizeBytes, &item.MetadataJSON,
		&item.ErrorCode, &item.ErrorMessage,
		&createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		return Rendition{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Rendition{}, fmt.Errorf("parse rendition creation time: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Rendition{}, fmt.Errorf("parse rendition update time: %w", err)
	}
	if completedAt.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, completedAt.String)
		if parseErr != nil {
			return Rendition{}, fmt.Errorf("parse rendition completion time: %w", parseErr)
		}
		item.CompletedAt = &value
	}
	return item, nil
}

func defaultJSON(value string) string {
	if value == "" {
		return "{}"
	}
	return value
}
