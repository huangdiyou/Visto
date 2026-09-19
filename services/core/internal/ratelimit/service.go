package ratelimit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Service struct {
	db    *sql.DB
	clock func() time.Time
}

func New(db *sql.DB) *Service {
	return &Service{db: db, clock: time.Now}
}

func (service *Service) Consume(
	ctx context.Context,
	namespace string,
	key string,
	limit int,
	window time.Duration,
) (bool, error) {
	if service == nil || service.db == nil || strings.TrimSpace(namespace) == "" ||
		strings.TrimSpace(key) == "" || limit < 1 || window <= 0 {
		return false, errors.New("invalid rate limit input")
	}
	now := service.clock().UTC()
	cutoff := now.Add(-window).Format(time.RFC3339Nano)
	var count int
	err := service.db.QueryRowContext(ctx, `
		INSERT INTO rate_limit_buckets (
			key_digest, attempt_count, window_started_at, updated_at
		) VALUES (?, 1, ?, ?)
		ON CONFLICT(key_digest) DO UPDATE SET
			attempt_count = CASE
				WHEN window_started_at <= ? THEN 1
				ELSE attempt_count + 1
			END,
			window_started_at = CASE
				WHEN window_started_at <= ? THEN excluded.window_started_at
				ELSE window_started_at
			END,
			updated_at = excluded.updated_at
		RETURNING attempt_count
	`, digestKey(namespace, key), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), cutoff, cutoff).Scan(&count)
	if err != nil {
		return false, err
	}
	return count <= limit, nil
}

func (service *Service) Clear(
	ctx context.Context,
	namespace string,
	key string,
) error {
	if service == nil || service.db == nil {
		return nil
	}
	_, err := service.db.ExecContext(ctx,
		"DELETE FROM rate_limit_buckets WHERE key_digest = ?",
		digestKey(namespace, key),
	)
	return err
}

// Cleanup removes inactive buckets in bounded batches so persistent rate limiting
// cannot grow the SQLite database indefinitely. Both the fixed-window and the
// token-bucket tables are covered, and the batch limit applies to each table so
// one busy table cannot starve the other.
func (service *Service) Cleanup(ctx context.Context, before time.Time, limit int) (int, error) {
	if service == nil || service.db == nil {
		return 0, nil
	}
	if limit < 1 {
		return 0, errors.New("invalid rate limit cleanup input")
	}

	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	removed := 0
	for _, table := range []string{"rate_limit_buckets", "rate_limit_token_buckets"} {
		keys, err := staleBucketKeys(ctx, tx, table, before, limit)
		if err != nil {
			return 0, err
		}
		for _, key := range keys {
			if _, err := tx.ExecContext(
				ctx,
				"DELETE FROM "+table+" WHERE key_digest = ?",
				key,
			); err != nil {
				return 0, err
			}
		}
		removed += len(keys)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}

// staleBucketKeys reads one bounded batch of expired keys. The table name comes
// from a fixed internal list, never from request input.
func staleBucketKeys(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	before time.Time,
	limit int,
) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT key_digest
		FROM `+table+`
		WHERE updated_at < ?
		ORDER BY updated_at ASC
		LIMIT ?
	`, before.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func digestKey(namespace string, key string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(namespace) + "|" + strings.TrimSpace(key)))
	return hex.EncodeToString(digest[:])
}
