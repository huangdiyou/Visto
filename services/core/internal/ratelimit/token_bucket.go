package ratelimit

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
)

// Decision is the outcome of a token-bucket attempt. RetryAfter is only
// meaningful when Allowed is false.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// ConsumeToken takes one token from a persistent bucket that holds `limit`
// tokens and refills at `limit` tokens per `window`.
//
// This is a sliding budget rather than a fixed window: a client that hits the
// cap starts recovering immediately instead of waiting for a shared clock
// boundary, and a burst is never released to everyone at the same instant.
//
// The whole read-refill-decide-write cycle is a single atomic UPSERT so
// concurrent requests for the same key cannot both pass on the last token. A
// denied attempt does not subtract anything, so a flood cannot build a deficit
// that keeps a legitimate client out after it stops; the balance never goes
// below zero.
func (service *Service) ConsumeToken(
	ctx context.Context,
	namespace string,
	key string,
	limit int,
	window time.Duration,
) (Decision, error) {
	if service == nil || service.db == nil || strings.TrimSpace(namespace) == "" ||
		strings.TrimSpace(key) == "" || limit < 1 || window <= 0 {
		return Decision{}, errors.New("invalid rate limit input")
	}
	now := service.clock().UTC()
	capacity := float64(limit)
	refillPerSecond := capacity / window.Seconds()
	if refillPerSecond <= 0 {
		return Decision{}, errors.New("invalid rate limit refill rate")
	}
	nowEpoch := float64(now.UnixNano()) / float64(time.Second)

	// refilled is inlined three times because SQLite has no statement-local
	// bindings in an UPSERT; the arguments are repeated to match.
	const refilled = `MIN(
		burst_capacity,
		token_balance + MAX(0.0, ? - updated_at_epoch) * refill_per_second
	)`
	var allowed int
	var balance float64
	err := service.db.QueryRowContext(ctx, `
		INSERT INTO rate_limit_token_buckets (
			key_digest, token_balance, burst_capacity, refill_per_second,
			last_allowed, updated_at, updated_at_epoch
		) VALUES (?, ? - 1.0, ?, ?, 1, ?, ?)
		ON CONFLICT(key_digest) DO UPDATE SET
			token_balance = CASE
				WHEN `+refilled+` >= 1.0 THEN `+refilled+` - 1.0
				ELSE `+refilled+`
			END,
			last_allowed = CASE WHEN `+refilled+` >= 1.0 THEN 1 ELSE 0 END,
			burst_capacity = excluded.burst_capacity,
			refill_per_second = excluded.refill_per_second,
			updated_at = excluded.updated_at,
			updated_at_epoch = excluded.updated_at_epoch
		RETURNING last_allowed, token_balance
	`,
		digestKey(namespace, key),
		capacity, capacity, refillPerSecond,
		now.Format(time.RFC3339Nano), nowEpoch,
		nowEpoch, nowEpoch, nowEpoch, nowEpoch,
	).Scan(&allowed, &balance)
	if err != nil {
		return Decision{}, err
	}
	if allowed == 1 {
		return Decision{Allowed: true}, nil
	}
	return Decision{
		Allowed:    false,
		RetryAfter: retryAfterForDeficit(balance, refillPerSecond),
	}, nil
}

// ClearToken drops a token bucket, used when a caller wants a clean budget.
func (service *Service) ClearToken(
	ctx context.Context,
	namespace string,
	key string,
) error {
	if service == nil || service.db == nil {
		return nil
	}
	_, err := service.db.ExecContext(ctx,
		"DELETE FROM rate_limit_token_buckets WHERE key_digest = ?",
		digestKey(namespace, key),
	)
	return err
}

// retryAfterForDeficit reports how long until one whole token is available.
func retryAfterForDeficit(balance float64, refillPerSecond float64) time.Duration {
	missing := 1.0 - balance
	if missing <= 0 || refillPerSecond <= 0 {
		return time.Second
	}
	seconds := math.Ceil(missing / refillPerSecond)
	if seconds < 1 {
		seconds = 1
	}
	return time.Duration(seconds) * time.Second
}
