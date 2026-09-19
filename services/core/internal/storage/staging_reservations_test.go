package storage

import (
	"errors"
	"testing"
)

func TestStagingReservationTrackerSharesVolumeBudget(t *testing.T) {
	tracker := NewStagingReservationTracker()
	const (
		available = int64(1_000)
		margin    = int64(100)
	)
	if err := tracker.Reserve("volume:test", available, margin, 500); err != nil {
		t.Fatalf("reserve first operation: %v", err)
	}
	if err := tracker.Reserve("volume:test", available, margin, 400); err != nil {
		t.Fatalf("reserve second operation: %v", err)
	}
	if err := tracker.Reserve("volume:test", available, margin, 1); !errors.Is(err, ErrStagingDiskSpaceLow) {
		t.Fatalf("reserve beyond shared safety margin error = %v, want disk space error", err)
	}
	if got := tracker.ReservedBytes("volume:test"); got != 900 {
		t.Fatalf("reserved bytes = %d, want 900", got)
	}
	tracker.Release("volume:test", 500)
	if err := tracker.Reserve("volume:test", available, margin, 1); err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
}

func TestStagingReservationTrackerLimitsConcurrentOperations(t *testing.T) {
	tracker := NewStagingReservationTracker()
	releases := make([]func(), 0, DefaultMaxConcurrentStagingOperations)
	for range DefaultMaxConcurrentStagingOperations {
		release, err := tracker.Acquire("volume:test", DefaultMaxConcurrentStagingOperations)
		if err != nil {
			t.Fatalf("acquire staging operation: %v", err)
		}
		releases = append(releases, release)
	}
	if _, err := tracker.Acquire("volume:test", DefaultMaxConcurrentStagingOperations); !errors.Is(err, ErrStagingConcurrencyLimit) {
		t.Fatalf("acquire beyond concurrency limit error = %v, want concurrency error", err)
	}
	if got := tracker.ActiveOperations("volume:test"); got != DefaultMaxConcurrentStagingOperations {
		t.Fatalf("active operations = %d, want %d", got, DefaultMaxConcurrentStagingOperations)
	}
	releases[0]()
	if _, err := tracker.Acquire("volume:test", DefaultMaxConcurrentStagingOperations); err != nil {
		t.Fatalf("acquire after operation release: %v", err)
	}
}

func TestStagingReservationTrackerLimitsOperationsPerActor(t *testing.T) {
	tracker := NewStagingReservationTracker()
	first, err := tracker.AcquireForActor("volume:test", "member-a", 4, 2)
	if err != nil {
		t.Fatalf("acquire first actor operation: %v", err)
	}
	defer first()
	second, err := tracker.AcquireForActor("volume:test", "member-a", 4, 2)
	if err != nil {
		t.Fatalf("acquire second actor operation: %v", err)
	}
	defer second()
	if _, err := tracker.AcquireForActor("volume:test", "member-a", 4, 2); !errors.Is(err, ErrStagingConcurrencyLimit) {
		t.Fatalf("acquire beyond actor limit error = %v, want concurrency error", err)
	}
	thirdActor, err := tracker.AcquireForActor("volume:test", "member-b", 4, 2)
	if err != nil {
		t.Fatalf("acquire another actor operation: %v", err)
	}
	defer thirdActor()
}
