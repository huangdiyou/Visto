package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// The re-probe starts real FFmpeg processes on the host, so it is Owner-only like
// the rest of the media encoding settings.
func TestMediaEncodingReprobeRequiresOwner(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	_, workspaceID := setupInstance(t, handler)
	member := memberCookie(t, handler, config, workspaceID)

	response := performJSONRequest(
		handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", member)
	if response.Code == http.StatusAccepted {
		t.Fatal("a member must not start an encoder probe")
	}
}

// A build that cannot probe says so instead of accepting a request it will never
// carry out.
func TestMediaEncodingReprobeIsUnavailableWithoutAProbe(t *testing.T) {
	handler, cookie := mediaEncodingHandler(t)
	response := performJSONRequest(
		handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", cookie)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", response.Code, response.Body.String())
	}
}

// The sweep is handed the Owner who asked for it, so the activity record names a
// person rather than an anonymous system job.
func TestMediaEncodingReprobeRunsAsTheRequestingOwner(t *testing.T) {
	config := testConfig(t)
	actors := make(chan string, 1)
	config.RunMediaEncodingProbe = func(_ context.Context, actorID string) error {
		actors <- actorID
		return nil
	}
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)
	ownerID := ownerUserID(t, handler, cookie)

	response := performJSONRequest(
		handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", cookie)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", response.Code, response.Body.String())
	}
	select {
	case actor := <-actors:
		if actor != ownerID {
			t.Fatalf("actor = %q, want the Owner who asked", actor)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep was never handed over")
	}
}

// ownerUserID reads the signed-in Owner's id from the session endpoint.
func ownerUserID(t *testing.T, handler http.Handler, cookie *http.Cookie) string {
	t.Helper()
	response := performJSONRequest(
		handler, http.MethodGet, "/api/v1/session", "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("read session: status %d: %s", response.Code, response.Body.String())
	}
	var session sessionResponse
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return session.User.ID
}

// The guard exists because each sweep starts real encoders. Two sweeps at once
// would double the load for the same answer, so the second request is refused
// rather than queued.
func TestMediaEncodingReprobeRefusesAConcurrentSweep(t *testing.T) {
	config := testConfig(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	config.RunMediaEncodingProbe = func(context.Context, string) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	first := performJSONRequest(
		handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", cookie)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first sweep: status %d, want 202", first.Code)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first sweep never started")
	}
	second := performJSONRequest(
		handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", cookie)
	close(release)
	if second.Code != http.StatusConflict {
		t.Fatalf("second sweep: status %d, want 409", second.Code)
	}
	waitForProbeIdle(t, handler, cookie)
}

// A probe that cannot run is a diagnosis that failed, not a server fault: the
// request is already answered, and the guard has to be released so the Owner can
// try again instead of being locked out until a restart.
func TestMediaEncodingReprobeRecoversFromAProbeFailure(t *testing.T) {
	config := testConfig(t)
	calls := make(chan int, 4)
	var mu sync.Mutex
	attempt := 0
	config.RunMediaEncodingProbe = func(context.Context, string) error {
		mu.Lock()
		attempt++
		current := attempt
		mu.Unlock()
		calls <- current
		return errors.New("ffmpeg is not installed")
	}
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	if response := performJSONRequest(
		handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", cookie,
	); response.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", response.Code)
	}
	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("the failing sweep never ran")
	}
	waitForProbeIdle(t, handler, cookie)
}

// waitForProbeIdle retries the re-probe until the guard accepts it again, which
// is how the tests observe that the background sweep finished.
func waitForProbeIdle(t *testing.T, handler http.Handler, cookie *http.Cookie) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := performJSONRequest(
			handler, http.MethodPost, "/api/v1/system/media-encoding/reprobe", "", cookie)
		if response.Code == http.StatusAccepted {
			return
		}
		if response.Code != http.StatusConflict {
			t.Fatalf("retry: status %d, want 202 once the guard is released", response.Code)
		}
		if time.Now().After(deadline) {
			t.Fatal("the guard was never released")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
