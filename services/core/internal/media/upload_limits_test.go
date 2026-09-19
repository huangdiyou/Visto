package media

import (
	"errors"
	"strings"
	"testing"

	"review-studio.local/core/internal/storage"
)

func TestUploadLimitReaderSharesStagingDiskReservations(t *testing.T) {
	available := int64(1_000)
	service := NewLibraryService(nil)
	service.stagingReservations = storage.NewStagingReservationTracker()
	target := &UploadTarget{
		DiskAvailableBytes: &available,
		MinimumFreeBytes:   100,
		ReservationKey:     "volume:test",
	}

	first, releaseFirst := service.limitUploadReader(strings.NewReader(strings.Repeat("a", 500)), target)
	buffer := make([]byte, 500)
	if read, err := first.Read(buffer); read != 500 || err != nil {
		t.Fatalf("read first upload = (%d, %v), want (500, nil)", read, err)
	}

	second, releaseSecond := service.limitUploadReader(strings.NewReader(strings.Repeat("b", 401)), target)
	defer releaseSecond()
	buffer = make([]byte, 401)
	if read, err := second.Read(buffer); read != 0 || !errors.Is(err, ErrLibraryUploadDiskSpaceLow) {
		t.Fatalf("read second upload = (%d, %v), want disk space rejection", read, err)
	}

	releaseFirst()
	third, releaseThird := service.limitUploadReader(strings.NewReader("c"), target)
	defer releaseThird()
	if read, err := third.Read(make([]byte, 1)); read != 1 || err != nil {
		t.Fatalf("read after release = (%d, %v), want (1, nil)", read, err)
	}
}

func TestUploadLimitReaderLimitsConcurrentStagingOperations(t *testing.T) {
	available := int64(10_000)
	service := NewLibraryService(nil)
	service.stagingReservations = storage.NewStagingReservationTracker()
	target := &UploadTarget{
		DiskAvailableBytes: &available,
		MinimumFreeBytes:   100,
		ReservationKey:     "volume:test",
	}
	releases := make([]func(), 0, storage.DefaultMaxConcurrentStagingOperations)
	for range storage.DefaultMaxConcurrentStagingOperations {
		reader, release := service.limitUploadReader(strings.NewReader("x"), target)
		if read, err := reader.Read(make([]byte, 1)); read != 1 || err != nil {
			t.Fatalf("read accepted upload = (%d, %v), want (1, nil)", read, err)
		}
		releases = append(releases, release)
	}
	blocked, releaseBlocked := service.limitUploadReader(strings.NewReader("x"), target)
	defer releaseBlocked()
	if read, err := blocked.Read(make([]byte, 1)); read != 0 || !errors.Is(err, ErrLibraryUploadBusy) {
		t.Fatalf("read beyond concurrency limit = (%d, %v), want busy error", read, err)
	}
	for _, release := range releases {
		release()
	}
}

func TestUploadLimitReaderLimitsStagingOperationsPerActor(t *testing.T) {
	available := int64(10_000)
	service := NewLibraryService(nil)
	service.stagingReservations = storage.NewStagingReservationTracker()
	target := &UploadTarget{
		DiskAvailableBytes: &available,
		MinimumFreeBytes:   100,
		ReservationKey:     "volume:test",
		StagingActorID:     "member-a",
	}
	releases := make([]func(), 0, storage.DefaultMaxConcurrentStagingOperationsPerActor)
	for range storage.DefaultMaxConcurrentStagingOperationsPerActor {
		reader, release := service.limitUploadReader(strings.NewReader("x"), target)
		if read, err := reader.Read(make([]byte, 1)); read != 1 || err != nil {
			t.Fatalf("read accepted actor upload = (%d, %v), want (1, nil)", read, err)
		}
		releases = append(releases, release)
	}
	blocked, releaseBlocked := service.limitUploadReader(strings.NewReader("x"), target)
	defer releaseBlocked()
	if read, err := blocked.Read(make([]byte, 1)); read != 0 || !errors.Is(err, ErrLibraryUploadBusy) {
		t.Fatalf("read beyond actor limit = (%d, %v), want busy error", read, err)
	}
	for _, release := range releases {
		release()
	}
}
