package ratelimit

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
)

func TestConsumePersistsAndIsAtomic(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rate-limit.db")
	db, err := database.Open(ctx, database.Config{Path: path})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	service := New(db)

	const requests = 12
	const limit = 5
	var allowed int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ok, consumeErr := service.Consume(ctx, "login", "account@example.com", limit, time.Minute)
			if consumeErr != nil {
				t.Errorf("consume: %v", consumeErr)
				return
			}
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	if allowed != limit {
		t.Fatalf("allowed = %d, want %d", allowed, limit)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := database.Open(ctx, database.Config{Path: path})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	if ok, err := New(reopened).Consume(ctx, "login", "account@example.com", limit, time.Minute); err != nil || ok {
		t.Fatalf("reopened limiter allowed=%t err=%v, want denied", ok, err)
	}
}

func TestCleanupRemovesOnlyExpiredBucketsWithinLimit(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{Path: filepath.Join(t.TempDir(), "cleanup.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := New(db)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now.Add(-72 * time.Hour) }
	for _, key := range []string{"old-one", "old-two", "old-three"} {
		if _, err := service.Consume(ctx, "test", key, 5, time.Minute); err != nil {
			t.Fatalf("consume %s: %v", key, err)
		}
	}
	service.clock = func() time.Time { return now }
	if _, err := service.Consume(ctx, "test", "current", 5, time.Minute); err != nil {
		t.Fatalf("consume current: %v", err)
	}

	removed, err := service.Cleanup(ctx, now.Add(-48*time.Hour), 2)
	if err != nil {
		t.Fatalf("cleanup first batch: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	removed, err = service.Cleanup(ctx, now.Add(-48*time.Hour), 2)
	if err != nil {
		t.Fatalf("cleanup second batch: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rate_limit_buckets").Scan(&remaining); err != nil {
		t.Fatalf("count buckets: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}
