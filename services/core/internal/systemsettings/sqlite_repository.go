package systemsettings

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

func (repository *SQLiteRepository) GetNetwork(
	ctx context.Context,
	now time.Time,
) (NetworkSettings, error) {
	item, err := repository.network(ctx, repository.db)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return NetworkSettings{}, err
	}
	if err := repository.ensureNetworkRow(ctx, now); err != nil {
		return NetworkSettings{}, err
	}
	return repository.network(ctx, repository.db)
}

func (repository *SQLiteRepository) UpdateNetwork(
	ctx context.Context,
	record updateNetworkRecord,
) (NetworkUpdate, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return NetworkUpdate{}, fmt.Errorf("begin system network settings update: %w", err)
	}
	defer tx.Rollback()

	if err := ensureNetworkRowTx(ctx, tx, record.Now); err != nil {
		return NetworkUpdate{}, err
	}

	previous, err := repository.network(ctx, tx)
	if err != nil {
		return NetworkUpdate{}, err
	}
	if previous.Revision != record.Revision {
		return NetworkUpdate{}, ErrRevisionConflict
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE system_network_settings
		SET require_remote_https = ?,
			revision = revision + 1,
			updated_by = ?,
			updated_at = ?
		WHERE id = 1 AND revision = ?
	`, boolInt(record.RequireRemoteHTTPS), record.UpdatedBy,
		formatTime(record.Now), record.Revision)
	if err != nil {
		return NetworkUpdate{}, fmt.Errorf("update system network settings: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return NetworkUpdate{}, fmt.Errorf("check system network settings update: %w", err)
	} else if affected != 1 {
		return NetworkUpdate{}, ErrRevisionConflict
	}

	item, err := repository.network(ctx, tx)
	if err != nil {
		return NetworkUpdate{}, err
	}
	if err := tx.Commit(); err != nil {
		return NetworkUpdate{}, fmt.Errorf("commit system network settings update: %w", err)
	}
	return NetworkUpdate{Previous: previous, Current: item}, nil
}

func (repository *SQLiteRepository) ensureNetworkRow(
	ctx context.Context,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system network settings creation: %w", err)
	}
	defer tx.Rollback()
	if err := ensureNetworkRowTx(ctx, tx, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit system network settings creation: %w", err)
	}
	return nil
}

func ensureNetworkRowTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO system_network_settings (
			id, require_remote_https, revision, updated_at
		) VALUES (1, 0, 1, ?)
	`, formatTime(now)); err != nil {
		return fmt.Errorf("create default system network settings: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) network(
	ctx context.Context,
	queryer rowQueryer,
) (NetworkSettings, error) {
	var item NetworkSettings
	var requireRemoteHTTPS int
	var updatedBy sql.NullString
	var updatedAt string
	if err := queryer.QueryRowContext(ctx, `
		SELECT require_remote_https, revision, updated_by, updated_at
		FROM system_network_settings
		WHERE id = 1
	`).Scan(&requireRemoteHTTPS, &item.Revision, &updatedBy, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NetworkSettings{}, ErrNotFound
		}
		return NetworkSettings{}, fmt.Errorf("read system network settings: %w", err)
	}
	item.RequireRemoteHTTPS = requireRemoteHTTPS == 1
	if updatedBy.Valid {
		item.UpdatedBy = &updatedBy.String
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return NetworkSettings{}, fmt.Errorf("parse system network settings time: %w", err)
	}
	item.UpdatedAt = parsed
	return item, nil
}

func (repository *SQLiteRepository) GetHostAccess(
	ctx context.Context,
	now time.Time,
) (HostAccessSettings, error) {
	item, err := repository.hostAccess(ctx, repository.db)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, ErrHostAccessNotFound) {
		return HostAccessSettings{}, err
	}
	if err := repository.ensureHostAccessRow(ctx, now); err != nil {
		return HostAccessSettings{}, err
	}
	return repository.hostAccess(ctx, repository.db)
}

// SetHostAccess records the wizard's one-time choice. It is intentionally a
// plain write with no revision handshake: the value is never editable from the
// web, so there is no concurrent editor to reconcile with.
func (repository *SQLiteRepository) SetHostAccess(
	ctx context.Context,
	record setHostAccessRecord,
) (HostAccessUpdate, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return HostAccessUpdate{}, fmt.Errorf("begin system host access write: %w", err)
	}
	defer tx.Rollback()

	if err := ensureHostAccessRowTx(ctx, tx, record.Now); err != nil {
		return HostAccessUpdate{}, err
	}
	previous, err := repository.hostAccess(ctx, tx)
	if err != nil {
		return HostAccessUpdate{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_host_access_settings
		SET allow_web_host_paths = ?,
			revision = revision + 1,
			updated_by = ?,
			updated_at = ?
		WHERE id = 1
	`, boolInt(record.AllowWebHostPaths), record.UpdatedBy,
		formatTime(record.Now)); err != nil {
		return HostAccessUpdate{}, fmt.Errorf("write system host access settings: %w", err)
	}
	current, err := repository.hostAccess(ctx, tx)
	if err != nil {
		return HostAccessUpdate{}, err
	}
	if err := tx.Commit(); err != nil {
		return HostAccessUpdate{}, fmt.Errorf("commit system host access write: %w", err)
	}
	return HostAccessUpdate{Previous: previous, Current: current}, nil
}

func (repository *SQLiteRepository) ensureHostAccessRow(
	ctx context.Context,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system host access settings creation: %w", err)
	}
	defer tx.Rollback()
	if err := ensureHostAccessRowTx(ctx, tx, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit system host access settings creation: %w", err)
	}
	return nil
}

func ensureHostAccessRowTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO system_host_access_settings (
			id, allow_web_host_paths, revision, updated_at
		) VALUES (1, 0, 1, ?)
	`, formatTime(now)); err != nil {
		return fmt.Errorf("create default system host access settings: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) hostAccess(
	ctx context.Context,
	queryer rowQueryer,
) (HostAccessSettings, error) {
	var item HostAccessSettings
	var allowWebHostPaths int
	var updatedBy sql.NullString
	var updatedAt string
	if err := queryer.QueryRowContext(ctx, `
		SELECT allow_web_host_paths, revision, updated_by, updated_at
		FROM system_host_access_settings
		WHERE id = 1
	`).Scan(&allowWebHostPaths, &item.Revision, &updatedBy, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HostAccessSettings{}, ErrHostAccessNotFound
		}
		return HostAccessSettings{}, fmt.Errorf("read system host access settings: %w", err)
	}
	item.AllowWebHostPaths = allowWebHostPaths == 1
	if updatedBy.Valid {
		item.UpdatedBy = &updatedBy.String
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return HostAccessSettings{}, fmt.Errorf("parse system host access settings time: %w", err)
	}
	item.UpdatedAt = parsed
	return item, nil
}

// GetMediaEncoding reads the encoder singleton, materialising the default row on
// first use so an upgraded instance can read it without a migration step.
func (repository *SQLiteRepository) GetMediaEncoding(
	ctx context.Context,
	now time.Time,
) (MediaEncodingSettings, error) {
	item, err := repository.mediaEncoding(ctx, repository.db)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, ErrMediaEncodingNotFound) {
		return MediaEncodingSettings{}, err
	}
	if err := repository.ensureMediaEncodingRow(ctx, now); err != nil {
		return MediaEncodingSettings{}, err
	}
	return repository.mediaEncoding(ctx, repository.db)
}

// UpdateMediaEncoding writes the Owner's explicit choice. Unlike the host access
// choice this one IS editable from the web, so it carries the same revision
// handshake as the network settings: two sessions must not silently overwrite
// each other.
func (repository *SQLiteRepository) UpdateMediaEncoding(
	ctx context.Context,
	record updateMediaEncodingRecord,
) (MediaEncodingUpdate, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("begin system media encoding write: %w", err)
	}
	defer tx.Rollback()

	if err := ensureMediaEncodingRowTx(ctx, tx, record.Now); err != nil {
		return MediaEncodingUpdate{}, err
	}
	previous, err := repository.mediaEncoding(ctx, tx)
	if err != nil {
		return MediaEncodingUpdate{}, err
	}
	if previous.Revision != record.Revision {
		return MediaEncodingUpdate{}, ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_media_encoding_settings
		SET preferred_encoder = ?,
			revision = revision + 1,
			updated_by = ?,
			updated_at = ?
		WHERE id = 1
	`, record.PreferredEncoder, record.UpdatedBy, formatTime(record.Now)); err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("write system media encoding settings: %w", err)
	}
	current, err := repository.mediaEncoding(ctx, tx)
	if err != nil {
		return MediaEncodingUpdate{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("commit system media encoding write: %w", err)
	}
	return MediaEncodingUpdate{Previous: previous, Current: current}, nil
}

// RecordMediaEncodingRuntime writes the columns the encoder job owns. It does
// NOT bump revision: revision guards the Owner's edit of preferred_encoder, and
// letting a background encode invalidate it would make the Owner's next save
// fail with a conflict they cannot explain. updated_at still moves, so the page
// can show when the breaker last acted.
func (repository *SQLiteRepository) RecordMediaEncodingRuntime(
	ctx context.Context,
	record recordMediaEncodingRuntimeRecord,
) (MediaEncodingUpdate, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("begin system media encoding runtime write: %w", err)
	}
	defer tx.Rollback()

	if err := ensureMediaEncodingRowTx(ctx, tx, record.Now); err != nil {
		return MediaEncodingUpdate{}, err
	}
	previous, err := repository.mediaEncoding(ctx, tx)
	if err != nil {
		return MediaEncodingUpdate{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_media_encoding_settings
		SET active_encoder = ?,
			failure_count = ?,
			tripped_encoder = ?,
			tripped_reason = ?,
			tripped_at = ?,
			updated_by = ?,
			updated_at = ?
		WHERE id = 1
	`, record.ActiveEncoder, record.FailureCount,
		nullString(record.TrippedEncoder), nullString(record.TrippedReason),
		nullTime(record.TrippedAt),
		record.UpdatedBy, formatTime(record.Now)); err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("write system media encoding runtime: %w", err)
	}
	current, err := repository.mediaEncoding(ctx, tx)
	if err != nil {
		return MediaEncodingUpdate{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("commit system media encoding runtime write: %w", err)
	}
	return MediaEncodingUpdate{Previous: previous, Current: current}, nil
}

// RecordMediaEncodingProbe writes the sweep result and nothing else. It does NOT
// bump revision, and it deliberately cannot reach active_encoder, failure_count or
// the trip columns: a sweep reports which encoders exist, and letting it write the
// encoder in use would clear a choice or a trip it knows nothing about.
func (repository *SQLiteRepository) RecordMediaEncodingProbe(
	ctx context.Context,
	record recordMediaEncodingProbeRecord,
) (MediaEncodingUpdate, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("begin system media encoding probe write: %w", err)
	}
	defer tx.Rollback()

	if err := ensureMediaEncodingRowTx(ctx, tx, record.Now); err != nil {
		return MediaEncodingUpdate{}, err
	}
	previous, err := repository.mediaEncoding(ctx, tx)
	if err != nil {
		return MediaEncodingUpdate{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_media_encoding_settings
		SET detected_encoders = ?,
			detected_at = ?,
			updated_by = ?,
			updated_at = ?
		WHERE id = 1
	`, detectedJSON(record.DetectedEncoders), formatTime(record.Now),
		record.UpdatedBy, formatTime(record.Now)); err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("write system media encoding probe: %w", err)
	}
	current, err := repository.mediaEncoding(ctx, tx)
	if err != nil {
		return MediaEncodingUpdate{}, err
	}
	if err := tx.Commit(); err != nil {
		return MediaEncodingUpdate{}, fmt.Errorf("commit system media encoding probe write: %w", err)
	}
	return MediaEncodingUpdate{Previous: previous, Current: current}, nil
}

// detectedJSON renders a probe result for storage.
func detectedJSON(value []string) any {
	if value == nil {
		return "[]"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		// The list is a slice of strings, so an encoding failure is impossible;
		// storing an empty list is better than writing invalid JSON.
		return "[]"
	}
	return string(encoded)
}

// nullString stores an empty breaker field as NULL, so a reader can tell "not
// tripped" from "tripped with an empty reason".
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullTime stores an absent timestamp as NULL.
func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func (repository *SQLiteRepository) ensureMediaEncodingRow(
	ctx context.Context,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system media encoding settings creation: %w", err)
	}
	defer tx.Rollback()
	if err := ensureMediaEncodingRowTx(ctx, tx, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit system media encoding settings creation: %w", err)
	}
	return nil
}

func ensureMediaEncodingRowTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO system_media_encoding_settings (
			id, preferred_encoder, detected_encoders, active_encoder, failure_count, revision, updated_at
		) VALUES (1, '', '[]', '', 0, 1, ?)
	`, formatTime(now)); err != nil {
		return fmt.Errorf("create default system media encoding settings: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) mediaEncoding(
	ctx context.Context,
	queryer rowQueryer,
) (MediaEncodingSettings, error) {
	var item MediaEncodingSettings
	var detectedRaw string
	var detectedAt, trippedEncoder, trippedReason, trippedAt, updatedBy sql.NullString
	var updatedAt string
	if err := queryer.QueryRowContext(ctx, `
		SELECT preferred_encoder, detected_encoders, detected_at, active_encoder,
			failure_count, tripped_encoder, tripped_reason, tripped_at,
			revision, updated_by, updated_at
		FROM system_media_encoding_settings
		WHERE id = 1
	`).Scan(&item.PreferredEncoder, &detectedRaw, &detectedAt, &item.ActiveEncoder,
		&item.FailureCount, &trippedEncoder, &trippedReason, &trippedAt,
		&item.Revision, &updatedBy, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MediaEncodingSettings{}, ErrMediaEncodingNotFound
		}
		return MediaEncodingSettings{}, fmt.Errorf("read system media encoding settings: %w", err)
	}
	// A malformed list is a storage fault, not "no encoders", so it is reported
	// instead of being flattened into an empty probe result.
	detected := []string{}
	if detectedRaw != "" {
		if err := json.Unmarshal([]byte(detectedRaw), &detected); err != nil {
			return MediaEncodingSettings{}, fmt.Errorf("parse detected media encoders: %w", err)
		}
	}
	item.DetectedEncoders = detected
	detectedAtValue, err := nullableTime(detectedAt)
	if err != nil {
		return MediaEncodingSettings{}, err
	}
	item.DetectedAt = detectedAtValue
	trippedAtValue, err := nullableTime(trippedAt)
	if err != nil {
		return MediaEncodingSettings{}, err
	}
	item.TrippedAt = trippedAtValue
	if trippedEncoder.Valid {
		item.TrippedEncoder = &trippedEncoder.String
	}
	if trippedReason.Valid {
		item.TrippedReason = &trippedReason.String
	}
	if updatedBy.Valid {
		item.UpdatedBy = &updatedBy.String
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return MediaEncodingSettings{}, fmt.Errorf("parse system media encoding settings time: %w", err)
	}
	item.UpdatedAt = parsed
	return item, nil
}

// nullableTime parses an optional timestamp column.
func nullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse optional settings time: %w", err)
	}
	return &parsed, nil
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
