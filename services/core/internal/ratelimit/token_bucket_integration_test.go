package ratelimit

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
)

// TestConsumeTokenAllowsBurstThenRefillsSmoothly pins the two properties that a
// fixed window does not provide: a full burst is available immediately, and a
// blocked client recovers as time passes instead of waiting for a shared clock
// boundary.
func TestConsumeTokenAllowsBurstThenRefillsSmoothly(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "token-bucket.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := New(db)
	now := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }

	const limit = 50
	for attempt := 1; attempt <= limit; attempt++ {
		decision, err := service.ConsumeToken(ctx, "text", "visitor-1", limit, time.Minute)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if !decision.Allowed {
			t.Fatalf("attempt %d denied, want allowed", attempt)
		}
	}
	denied, err := service.ConsumeToken(ctx, "text", "visitor-1", limit, time.Minute)
	if err != nil {
		t.Fatalf("attempt %d: %v", limit+1, err)
	}
	if denied.Allowed {
		t.Fatalf("attempt %d allowed, want denied", limit+1)
	}
	if denied.RetryAfter < time.Second {
		t.Fatalf("retry after = %s, want at least 1s", denied.RetryAfter)
	}

	// A different visitor has an independent budget.
	other, err := service.ConsumeToken(ctx, "text", "visitor-2", limit, time.Minute)
	if err != nil {
		t.Fatalf("other visitor: %v", err)
	}
	if !other.Allowed {
		t.Fatal("other visitor denied, want allowed")
	}

	// 50 tokens per minute refills 1 token about every 1.2s; after 5s a couple of
	// requests are available again without waiting for the whole window.
	now = now.Add(5 * time.Second)
	recovered, err := service.ConsumeToken(ctx, "text", "visitor-1", limit, time.Minute)
	if err != nil {
		t.Fatalf("recovered attempt: %v", err)
	}
	if !recovered.Allowed {
		t.Fatal("token bucket did not refill within the window")
	}
}

// TestConsumeTokenIsAtomicAndSurvivesRestart proves the budget cannot be reset by
// concurrency, by a reopened database, or by a new limiter instance, which is
// what a visitor creating a fresh share session would produce.
func TestConsumeTokenIsAtomicAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "token-bucket-restart.db")
	db, err := database.Open(ctx, database.Config{Path: path})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	service := New(db)
	now := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }

	const limit = 5
	const requests = 12
	var allowed int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			decision, consumeErr := service.ConsumeToken(ctx, "text", "visitor-1", limit, time.Hour)
			if consumeErr != nil {
				t.Errorf("consume: %v", consumeErr)
				return
			}
			if decision.Allowed {
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
	restarted := New(reopened)
	restarted.clock = func() time.Time { return now }
	decision, err := restarted.ConsumeToken(ctx, "text", "visitor-1", limit, time.Hour)
	if err != nil {
		t.Fatalf("restarted consume: %v", err)
	}
	if decision.Allowed {
		t.Fatal("restarting Core reset the token budget")
	}
}

// TestConsumeTokenEnforcesSustainedHourlyCap covers the second tier of the guest
// budget: the per-minute allowance refills, but the hourly allowance is what
// stops a script from posting all night.
func TestConsumeTokenEnforcesSustainedHourlyCap(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "token-bucket-hourly.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := New(db)
	now := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }

	// 500 per hour, spent in ten batches of 50 one minute apart. A minute of
	// refill adds about 8 tokens, so the hourly budget still runs out.
	const hourlyLimit = 500
	spent := 0
	for batch := 0; batch < 10; batch++ {
		for index := 0; index < 50; index++ {
			decision, err := service.ConsumeToken(ctx, "text-hour", "visitor-1", hourlyLimit, time.Hour)
			if err != nil {
				t.Fatalf("batch %d attempt %d: %v", batch, index, err)
			}
			if decision.Allowed {
				spent++
			}
		}
		now = now.Add(time.Minute)
	}
	if spent < hourlyLimit {
		t.Fatalf("spent = %d, want at least the hourly cap %d", spent, hourlyLimit)
	}

	// Nine more minutes of refill cannot restore a full hour of budget, so a
	// sustained flood stays blocked well inside the hour.
	blocked := 0
	for index := 0; index < 200; index++ {
		decision, err := service.ConsumeToken(ctx, "text-hour", "visitor-1", hourlyLimit, time.Hour)
		if err != nil {
			t.Fatalf("sustained attempt %d: %v", index, err)
		}
		if !decision.Allowed {
			blocked++
		}
	}
	if blocked == 0 {
		t.Fatal("sustained flood was never blocked by the hourly cap")
	}

	// A full hour of idle time restores the whole budget.
	now = now.Add(time.Hour)
	decision, err := service.ConsumeToken(ctx, "text-hour", "visitor-1", hourlyLimit, time.Hour)
	if err != nil {
		t.Fatalf("post-idle attempt: %v", err)
	}
	if !decision.Allowed {
		t.Fatal("hourly budget did not recover after an idle hour")
	}
}

// TestCleanupRemovesStaleTokenBuckets keeps the new table inside the existing
// retention job so persistent limiting cannot grow the database forever.
func TestCleanupRemovesStaleTokenBuckets(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "token-bucket-cleanup.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := New(db)
	now := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now.Add(-72 * time.Hour) }
	if _, err := service.ConsumeToken(ctx, "text", "old-visitor", 5, time.Minute); err != nil {
		t.Fatalf("consume old: %v", err)
	}
	service.clock = func() time.Time { return now }
	if _, err := service.ConsumeToken(ctx, "text", "current-visitor", 5, time.Minute); err != nil {
		t.Fatalf("consume current: %v", err)
	}

	removed, err := service.Cleanup(ctx, now.Add(-48*time.Hour), 100)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	var remaining int
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM rate_limit_token_buckets",
	).Scan(&remaining); err != nil {
		t.Fatalf("count token buckets: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}
