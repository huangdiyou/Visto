package systemsettings

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
