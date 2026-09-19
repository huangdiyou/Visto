package storage

import (
	"errors"
	"sync"
)

// DefaultStagingMinimumFreeBytes is kept free on a local volume while Core
// stages uploads, copies, or archive work.
const DefaultStagingMinimumFreeBytes int64 = 512 << 20

const DefaultMaxConcurrentStagingOperations = 4
const DefaultMaxConcurrentStagingOperationsPerActor = 2

var (
	ErrStagingDiskSpaceLow     = errors.New("storage staging disk space is low")
	ErrStagingConcurrencyLimit = errors.New("storage staging concurrency limit reached")
)

// StagingReservationTracker coordinates all work that writes temporary files
// to the same local filesystem. The key is a physical-volume identifier, not a
// logical storage root, so independent WebDAV/S3 roots cannot overcommit one
// Core disk.
type StagingReservationTracker struct {
	mu       sync.Mutex
	reserved map[string]int64
	active   map[string]int
	actors   map[string]int
}

func NewStagingReservationTracker() *StagingReservationTracker {
	return &StagingReservationTracker{
		reserved: make(map[string]int64),
		active:   make(map[string]int),
		actors:   make(map[string]int),
	}
}

func (tracker *StagingReservationTracker) Acquire(key string, maximum int) (func(), error) {
	return tracker.AcquireForActor(key, "", maximum, 0)
}

func (tracker *StagingReservationTracker) AcquireForActor(
	key string,
	actor string,
	maximum int,
	maximumPerActor int,
) (func(), error) {
	if tracker == nil || key == "" {
		return func() {}, nil
	}
	if maximum <= 0 {
		maximum = DefaultMaxConcurrentStagingOperations
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.active[key] >= maximum {
		return nil, ErrStagingConcurrencyLimit
	}
	actorKey := key + "\x00" + actor
	if actor != "" && maximumPerActor > 0 && tracker.actors[actorKey] >= maximumPerActor {
		return nil, ErrStagingConcurrencyLimit
	}
	tracker.active[key]++
	if actor != "" {
		tracker.actors[actorKey]++
	}
	released := false
	return func() {
		tracker.mu.Lock()
		defer tracker.mu.Unlock()
		if released {
			return
		}
		released = true
		if tracker.active[key] <= 1 {
			delete(tracker.active, key)
		} else {
			tracker.active[key]--
		}
		if actor != "" {
			if tracker.actors[actorKey] <= 1 {
				delete(tracker.actors, actorKey)
			} else {
				tracker.actors[actorKey]--
			}
		}
	}, nil
}

func (tracker *StagingReservationTracker) Reserve(
	key string,
	availableBytes int64,
	minimumFreeBytes int64,
	bytes int64,
) error {
	if minimumFreeBytes <= 0 {
		minimumFreeBytes = DefaultStagingMinimumFreeBytes
	}
	return tracker.reserve(key, availableBytes, minimumFreeBytes, bytes)
}

// ReserveCapacity reserves a logical capacity budget such as a storage bucket
// quota. Unlike disk space, this budget has no implicit free-space margin.
func (tracker *StagingReservationTracker) ReserveCapacity(
	key string,
	availableBytes int64,
	bytes int64,
) error {
	return tracker.reserve(key, availableBytes, 0, bytes)
}

func (tracker *StagingReservationTracker) reserve(
	key string,
	availableBytes int64,
	minimumFreeBytes int64,
	bytes int64,
) error {
	if tracker == nil || key == "" || bytes <= 0 {
		return nil
	}
	availableForWork := availableBytes - minimumFreeBytes
	if availableForWork < 0 {
		availableForWork = 0
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.reserved[key]+bytes > availableForWork {
		return ErrStagingDiskSpaceLow
	}
	tracker.reserved[key] += bytes
	return nil
}

func (tracker *StagingReservationTracker) Release(key string, bytes int64) {
	if tracker == nil || key == "" || bytes <= 0 {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	next := tracker.reserved[key] - bytes
	if next <= 0 {
		delete(tracker.reserved, key)
		return
	}
	tracker.reserved[key] = next
}

func (tracker *StagingReservationTracker) ReservedBytes(key string) int64 {
	if tracker == nil || key == "" {
		return 0
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.reserved[key]
}

func (tracker *StagingReservationTracker) ActiveOperations(key string) int {
	if tracker == nil || key == "" {
		return 0
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.active[key]
}
