package media

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryBreakerStore struct {
	state   EncoderBreakerState
	saves   int
	loadErr error
	saveErr error
}

func (store *memoryBreakerStore) LoadEncoderBreaker(context.Context) (EncoderBreakerState, error) {
	if store.loadErr != nil {
		return EncoderBreakerState{}, store.loadErr
	}
	return store.state, nil
}

func (store *memoryBreakerStore) SaveEncoderBreaker(_ context.Context, state EncoderBreakerState) error {
	if store.saveErr != nil {
		return store.saveErr
	}
	store.state = state
	store.saves++
	return nil
}

func breakerFixture(t *testing.T) (*EncoderBreaker, *memoryBreakerStore) {
	t.Helper()
	store := &memoryBreakerStore{}
	breaker := NewEncoderBreaker(store)
	breaker.now = func() time.Time {
		return time.Date(2026, time.September, 24, 9, 0, 0, 0, time.UTC)
	}
	if err := breaker.Load(context.Background()); err != nil {
		t.Fatalf("load breaker: %v", err)
	}
	return breaker, store
}

var h264Candidates = []string{"h264_videotoolbox", "libopenh264", "libx264"}

func TestBreakerDoesNotOverrideTheCallerUntilSomethingFails(t *testing.T) {
	t.Parallel()

	breaker, _ := breakerFixture(t)
	if got := breaker.ActiveEncoder("h264_videotoolbox"); got != "h264_videotoolbox" {
		t.Fatalf("active encoder = %q, want the caller's default", got)
	}
}

func TestBreakerCountsConsecutiveFailuresAndTripsAtTheThreshold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, _ := breakerFixture(t)

	for attempt := 1; attempt < EncoderBreakerThreshold; attempt++ {
		outcome, err := breaker.RecordFailure(ctx, "h264_videotoolbox", "device busy", h264Candidates)
		if err != nil {
			t.Fatalf("record failure %d: %v", attempt, err)
		}
		if outcome.Tripped {
			t.Fatalf("breaker tripped after %d failures, threshold is %d", attempt, EncoderBreakerThreshold)
		}
		if outcome.FailureCount != attempt {
			t.Fatalf("failure count = %d, want %d", outcome.FailureCount, attempt)
		}
	}

	outcome, err := breaker.RecordFailure(ctx, "h264_videotoolbox", "device busy", h264Candidates)
	if err != nil {
		t.Fatalf("record tripping failure: %v", err)
	}
	if !outcome.Tripped {
		t.Fatal("breaker did not trip at the threshold")
	}
	if outcome.PreviousEncoder != "h264_videotoolbox" || outcome.ActiveEncoder != "libopenh264" {
		t.Fatalf("unexpected trip outcome: %#v", outcome)
	}
	// The count restarts against the new encoder instead of staying at the
	// threshold, otherwise the next single failure would trip again immediately.
	if outcome.FailureCount != 0 {
		t.Fatalf("failure count after trip = %d, want 0", outcome.FailureCount)
	}
	if !strings.Contains(outcome.Reason, "h264_videotoolbox") ||
		!strings.Contains(outcome.Reason, "device busy") {
		t.Fatalf("trip reason must name the encoder and the cause, got %q", outcome.Reason)
	}
	if got := breaker.ActiveEncoder("h264_videotoolbox"); got != "libopenh264" {
		t.Fatalf("active encoder after trip = %q, want libopenh264", got)
	}
}

func TestBreakerSuccessResetsTheCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, _ := breakerFixture(t)
	for attempt := 0; attempt < EncoderBreakerThreshold-1; attempt++ {
		if _, err := breaker.RecordFailure(ctx, "libopenh264", "busy", h264Candidates); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	if err := breaker.RecordSuccess(ctx, "libopenh264"); err != nil {
		t.Fatalf("record success: %v", err)
	}
	// A success anywhere in between clears the streak, so another full threshold
	// is needed before the breaker moves.
	outcome, err := breaker.RecordFailure(ctx, "libopenh264", "busy", h264Candidates)
	if err != nil {
		t.Fatalf("record failure after success: %v", err)
	}
	if outcome.Tripped || outcome.FailureCount != 1 {
		t.Fatalf("count was not reset by the success: %#v", outcome)
	}
}

// A success reported for an encoder that is not the one being counted must not
// clear the streak of the encoder that is actually failing.
func TestBreakerSuccessForAnotherEncoderIsIgnored(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, _ := breakerFixture(t)
	if _, err := breaker.RecordFailure(ctx, "libopenh264", "busy", h264Candidates); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if err := breaker.RecordSuccess(ctx, "libx264"); err != nil {
		t.Fatalf("record unrelated success: %v", err)
	}
	if got := breaker.Snapshot().FailureCount; got != 1 {
		t.Fatalf("failure count = %d, want 1", got)
	}
}

func TestBreakerEndsOnSoftwareWhenTheListRunsOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, _ := breakerFixture(t)
	if err := breaker.Trip(ctx, "h264_videotoolbox", "gone", "libopenh264"); err != nil {
		t.Fatalf("trip: %v", err)
	}
	for attempt := 0; attempt < EncoderBreakerThreshold; attempt++ {
		if _, err := breaker.RecordFailure(ctx, "libopenh264", "busy", h264Candidates); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	// libx264 is the final entry, so the breaker stays there rather than walking
	// off the end of the list.
	if got := breaker.ActiveEncoder("libopenh264"); got != "libx264" {
		t.Fatalf("active encoder = %q, want libx264", got)
	}
}

func TestBreakerPersistsAndReloads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, store := breakerFixture(t)
	for attempt := 0; attempt < EncoderBreakerThreshold; attempt++ {
		if _, err := breaker.RecordFailure(ctx, "h264_videotoolbox", "no device", h264Candidates); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	if store.saves == 0 {
		t.Fatal("breaker state was never persisted")
	}

	// A fresh breaker over the same store must remember the trip; forgetting it
	// is what would make the Owner see the same failure cycle again after a
	// restart.
	reloaded := NewEncoderBreaker(store)
	if err := reloaded.Load(ctx); err != nil {
		t.Fatalf("reload breaker: %v", err)
	}
	if got := reloaded.ActiveEncoder("h264_videotoolbox"); got != "libopenh264" {
		t.Fatalf("reloaded active encoder = %q, want libopenh264", got)
	}
	snapshot := reloaded.Snapshot()
	if snapshot.TrippedEncoder != "h264_videotoolbox" || snapshot.TrippedAt.IsZero() {
		t.Fatalf("reloaded trip state = %#v", snapshot)
	}
}

func TestBreakerResetClearsTheTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, store := breakerFixture(t)
	if err := breaker.Trip(ctx, "h264_videotoolbox", "gone", "libopenh264"); err != nil {
		t.Fatalf("trip: %v", err)
	}
	if err := breaker.Reset(ctx, "h264_videotoolbox"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	snapshot := breaker.Snapshot()
	if snapshot.TrippedEncoder != "" || snapshot.FailureCount != 0 {
		t.Fatalf("reset left state behind: %#v", snapshot)
	}
	if store.state.ActiveEncoder != "h264_videotoolbox" {
		t.Fatalf("reset did not persist the chosen encoder: %#v", store.state)
	}
}

// A failure reported for an encoder that is not the one in use belongs to an
// earlier cycle and must not be counted against the current encoder.
func TestBreakerIgnoresFailuresFromAnotherCycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	breaker, _ := breakerFixture(t)
	if err := breaker.Trip(ctx, "h264_videotoolbox", "gone", "libopenh264"); err != nil {
		t.Fatalf("trip: %v", err)
	}
	outcome, err := breaker.RecordFailure(ctx, "h264_videotoolbox", "stale", h264Candidates)
	if err != nil {
		t.Fatalf("record stale failure: %v", err)
	}
	if outcome.FailureCount != 0 || outcome.Tripped {
		t.Fatalf("stale failure was counted: %#v", outcome)
	}
	if got := breaker.ActiveEncoder("h264_videotoolbox"); got != "libopenh264" {
		t.Fatalf("active encoder changed to %q", got)
	}
}

func TestBreakerReportsStoreFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := &memoryBreakerStore{}
	breaker := NewEncoderBreaker(store)
	if err := breaker.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	store.saveErr = errors.New("disk full")
	if _, err := breaker.RecordFailure(ctx, "libx264", "busy", h264Candidates); err == nil {
		t.Fatal("a failed persist must be reported")
	}
	store.saveErr = nil
	store.loadErr = errors.New("disk unreadable")
	if err := NewEncoderBreaker(store).Load(ctx); err == nil {
		t.Fatal("a failed load must be reported")
	}
}

func TestNextEncoderCandidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		candidates []string
		current    string
		want       string
	}{
		{"middle", h264Candidates, "h264_videotoolbox", "libopenh264"},
		{"last stays last", h264Candidates, "libx264", "libx264"},
		{"unknown falls back to the last entry", h264Candidates, "h264_nvenc", "libx264"},
		{"empty list keeps the encoder", nil, "libx264", "libx264"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := nextEncoderCandidate(testCase.candidates, testCase.current); got != testCase.want {
				t.Fatalf("nextEncoderCandidate(%v, %q) = %q, want %q",
					testCase.candidates, testCase.current, got, testCase.want)
			}
		})
	}
}
