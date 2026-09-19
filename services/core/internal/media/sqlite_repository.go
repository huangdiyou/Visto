package media

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

func (repository *SQLiteRepository) Get(
	ctx context.Context,
	workspaceID string,
	storageObjectID string,
) (Metadata, error) {
	return scanMetadata(repository.db.QueryRowContext(ctx, `
		SELECT
			storage_object_id, workspace_id, status, source_fingerprint,
			media_type, format_name, format_long_name, duration_us,
			bit_rate, width, height, rotation_degrees, frame_rate,
			video_codec, audio_codec, raw_metadata_json, error_code,
			error_message, probed_at
		FROM media_probes
		WHERE storage_object_id = ? AND workspace_id = ?
	`, storageObjectID, workspaceID))
}

func (repository *SQLiteRepository) ListRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]Metadata, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			media_probes.storage_object_id,
			media_probes.workspace_id,
			media_probes.status,
			media_probes.source_fingerprint,
			media_probes.media_type,
			media_probes.format_name,
			media_probes.format_long_name,
			media_probes.duration_us,
			media_probes.bit_rate,
			media_probes.width,
			media_probes.height,
			media_probes.rotation_degrees,
			media_probes.frame_rate,
			media_probes.video_codec,
			media_probes.audio_codec,
			media_probes.raw_metadata_json,
			media_probes.error_code,
			media_probes.error_message,
			media_probes.probed_at
		FROM media_probes
		JOIN storage_objects
			ON storage_objects.id = media_probes.storage_object_id
		WHERE media_probes.workspace_id = ?
			AND storage_objects.authorized_root_id = ?
			AND storage_objects.deleted_at IS NULL
		ORDER BY storage_objects.object_key
	`, workspaceID, rootID)
	if err != nil {
		return nil, fmt.Errorf("list media probes: %w", err)
	}
	defer rows.Close()

	items := make([]Metadata, 0)
	for rows.Next() {
		item, err := scanMetadata(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media probes: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) Upsert(
	ctx context.Context,
	metadata Metadata,
) error {
	timestamp := formatTime(metadata.ProbedAt)
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO media_probes (
			storage_object_id, workspace_id, status, source_fingerprint,
			media_type, format_name, format_long_name, duration_us,
			bit_rate, width, height, rotation_degrees, frame_rate,
			video_codec, audio_codec, raw_metadata_json, error_code,
			error_message, probed_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
		ON CONFLICT(storage_object_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			status = excluded.status,
			source_fingerprint = excluded.source_fingerprint,
			media_type = excluded.media_type,
			format_name = excluded.format_name,
			format_long_name = excluded.format_long_name,
			duration_us = excluded.duration_us,
			bit_rate = excluded.bit_rate,
			width = excluded.width,
			height = excluded.height,
			rotation_degrees = excluded.rotation_degrees,
			frame_rate = excluded.frame_rate,
			video_codec = excluded.video_codec,
			audio_codec = excluded.audio_codec,
			raw_metadata_json = excluded.raw_metadata_json,
			error_code = excluded.error_code,
			error_message = excluded.error_message,
			probed_at = excluded.probed_at,
			updated_at = excluded.updated_at
	`,
		metadata.StorageObjectID,
		metadata.WorkspaceID,
		metadata.Status,
		metadata.SourceFingerprint,
		metadata.MediaType,
		metadata.FormatName,
		metadata.FormatLongName,
		metadata.DurationUS,
		metadata.BitRate,
		metadata.Width,
		metadata.Height,
		metadata.RotationDegrees,
		metadata.FrameRate,
		metadata.VideoCodec,
		metadata.AudioCodec,
		metadata.RawMetadataJSON,
		metadata.ErrorCode,
		metadata.ErrorMessage,
		timestamp,
		timestamp,
	)
	if err != nil {
		return fmt.Errorf("upsert media probe: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMetadata(scanner rowScanner) (Metadata, error) {
	var item Metadata
	var probedAt string
	err := scanner.Scan(
		&item.StorageObjectID,
		&item.WorkspaceID,
		&item.Status,
		&item.SourceFingerprint,
		&item.MediaType,
		&item.FormatName,
		&item.FormatLongName,
		&item.DurationUS,
		&item.BitRate,
		&item.Width,
		&item.Height,
		&item.RotationDegrees,
		&item.FrameRate,
		&item.VideoCodec,
		&item.AudioCodec,
		&item.RawMetadataJSON,
		&item.ErrorCode,
		&item.ErrorMessage,
		&probedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, ErrObjectNotFound
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("scan media probe: %w", err)
	}
	item.ProbedAt, err = time.Parse(time.RFC3339Nano, probedAt)
	if err != nil {
		return Metadata{}, fmt.Errorf("parse media probe time: %w", err)
	}
	return item, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
