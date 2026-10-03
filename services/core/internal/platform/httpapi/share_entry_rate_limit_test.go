package httpapi

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareEntryBudgetIsPersistentAndDoesNotStoreRawToken(t *testing.T) {
	key := shareEntryRateLimitKey("secret-entry-token", "203.0.113.1")
	if strings.Contains(key, "secret-entry-token") {
		t.Fatal("raw token stored")
	}
	path := filepath.Join(t.TempDir(), "limits.db")
	h := newGuestTextRateLimitHandler(t, path)
	for i := 0; i < 20; i++ {
		if !h.consumeRateLimit(httptest.NewRecorder(), httptest.NewRequest("POST", "/share-api/v1/entry/token", nil), "share-entry", key, 20, 5*time.Minute) {
			t.Fatal("early rejection")
		}
	}
	// A new handler and connection on the same database must not reset quota.
	h = newGuestTextRateLimitHandler(t, path)
	response := httptest.NewRecorder()
	// Nil share service proves the route returns before session creation.
	req := httptest.NewRequest("POST", "/share-api/v1/entry/token", nil)
	req.SetPathValue("token", "secret-entry-token")
	req.RemoteAddr = "203.0.113.1:54321"
	h.handleOpenShareEntry(response, req)
	if response.Code != 429 || response.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
}
