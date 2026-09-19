package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/ratelimit"
)

// newGuestTextRateLimitHandler builds a handler wired only with what the guest
// text-change budget needs, plus a real SQLite limiter so persistence is real.
func newGuestTextRateLimitHandler(t *testing.T, path string) *handler {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{Path: path})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return &handler{
		rateLimits: ratelimit.New(db),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func attemptGuestTextChange(
	h *handler,
	clientAddr string,
	shareID string,
	visitorID string,
) (*httptest.ResponseRecorder, bool) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://visto.test/share-api/v1/items/item-1/threads",
		nil,
	)
	request.RemoteAddr = clientAddr
	allowed := h.consumeGuestTextChangeBudget(response, request, shareID, visitorID)
	return response, allowed
}

// TestGuestTextChangeBudgetPerVisitorMinuteTier covers the per-visitor tier: a
// reviewer can submit a long burst of change notes, the 51st in the same minute
// is rejected with a Retry-After hint, and another visitor on the same share is
// untouched.
func TestGuestTextChangeBudgetPerVisitorMinuteTier(t *testing.T) {
	h := newGuestTextRateLimitHandler(
		t,
		filepath.Join(t.TempDir(), "guest-text-visitor.db"),
	)
	const client = "203.0.113.40:5522"

	for index := 1; index <= guestTextChangesPerMinute; index++ {
		if _, allowed := attemptGuestTextChange(h, client, "share-1", "visitor-1"); !allowed {
			t.Fatalf("attempt %d rejected before the visitor budget was spent", index)
		}
	}
	response, allowed := attemptGuestTextChange(h, client, "share-1", "visitor-1")
	if allowed {
		t.Fatalf("attempt %d allowed, want rejected", guestTextChangesPerMinute+1)
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d, want 429", response.Code)
	}
	retryAfter := response.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("rate limited response is missing Retry-After")
	}
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil || seconds < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer", retryAfter)
	}

	// A second visitor has its own budget and must not be punished.
	if _, allowed := attemptGuestTextChange(h, client, "share-1", "visitor-2"); !allowed {
		t.Fatal("a different visitor was rejected by another visitor's budget")
	}
}

// TestGuestTextChangeBudgetShareClientTierSurvivesNewSessionAndRestart covers the
// abuse case the per-visitor tier cannot: a script that keeps creating fresh
// visitor sessions. The share+client tier is charged regardless of the visitor
// identity and is persistent, so neither a new session nor a Core restart
// restores the budget.
func TestGuestTextChangeBudgetShareClientTierSurvivesNewSessionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guest-text-client.db")
	h := newGuestTextRateLimitHandler(t, path)
	const client = "203.0.113.41:5522"

	// Four visitors spending their full per-minute allowance exhausts the
	// per-share client allowance exactly.
	visitors := shareClientTextChangesPerMin / guestTextChangesPerMinute
	for visitor := 1; visitor <= visitors; visitor++ {
		visitorID := "visitor-" + strconv.Itoa(visitor)
		for index := 1; index <= guestTextChangesPerMinute; index++ {
			if _, allowed := attemptGuestTextChange(h, client, "share-1", visitorID); !allowed {
				t.Fatalf("%s attempt %d rejected before the budgets were spent", visitorID, index)
			}
		}
	}

	// A brand-new visitor session has an untouched visitor budget, but the
	// share+client budget is gone.
	response, allowed := attemptGuestTextChange(h, client, "share-1", "visitor-fresh")
	if allowed {
		t.Fatal("re-creating a visitor session reset the share client budget")
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("share client rate limited status = %d, want 429", response.Code)
	}

	// A different client network is unaffected.
	if _, allowed := attemptGuestTextChange(h, "203.0.113.99:5522", "share-1", "visitor-other"); !allowed {
		t.Fatal("a different client was rejected by another client's budget")
	}

	// Restarting Core rebuilds the handler and the limiter from the same database.
	restarted := newGuestTextRateLimitHandler(t, path)
	restartedResponse, restartedAllowed := attemptGuestTextChange(
		restarted, client, "share-1", "visitor-after-restart",
	)
	if restartedAllowed {
		t.Fatal("restarting Core reset the share client budget")
	}
	if restartedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("post-restart status = %d, want 429", restartedResponse.Code)
	}

	// A different share on the same client keeps its own budget, so one busy
	// share cannot lock a client out of every other review.
	if _, allowed := attemptGuestTextChange(h, client, "share-2", "visitor-1"); !allowed {
		t.Fatal("a different share was rejected by another share's budget")
	}
}
