package media

import (
	"errors"
	"io"
	"sync"

	"review-studio.local/core/internal/storage"
)

const (
	DefaultMaxUploadFileBytes int64 = 20 << 30
	DefaultMinimumFreeBytes   int64 = 512 << 20
)

var (
	ErrLibraryUploadFileTooLarge  = errors.New("media library upload file is too large")
	ErrLibraryUploadQuotaExceeded = errors.New("media library upload quota exceeded")
	ErrLibraryUploadDiskSpaceLow  = errors.New("media library upload disk space is low")
	ErrLibraryUploadBusy          = errors.New("media library upload staging is busy")
)

type UploadLimitError struct {
	Reason string
	Limit  int64
}

func (err *UploadLimitError) Error() string {
	if err == nil || err.Reason == "" {
		return "media library upload limit exceeded"
	}
	return "media library upload limit exceeded: " + err.Reason
}

func (err *UploadLimitError) Unwrap() error {
	switch err.Reason {
	case "file_too_large":
		return ErrLibraryUploadFileTooLarge
	case "quota_exceeded":
		return ErrLibraryUploadQuotaExceeded
	case "disk_space_low":
		return ErrLibraryUploadDiskSpaceLow
	case "staging_busy":
		return ErrLibraryUploadBusy
	default:
		return ErrLibraryUploadInvalid
	}
}

func (err *UploadLimitError) UserMessage() string {
	if err == nil {
		return "上传文件超过当前限制"
	}
	switch err.Reason {
	case "file_too_large":
		return "文件超过单次上传大小上限"
	case "quota_exceeded":
		return "上传会超过当前存储桶容量上限"
	case "disk_space_low":
		return "磁盘剩余空间不足，上传已停止"
	case "staging_busy":
		return "上传暂存繁忙，请稍后重试"
	default:
		return "上传文件超过当前限制"
	}
}

type uploadReservationTracker struct {
	mu       sync.Mutex
	reserved map[string]int64
}

func newUploadReservationTracker() *uploadReservationTracker {
	return &uploadReservationTracker{reserved: make(map[string]int64)}
}

func (tracker *uploadReservationTracker) reserve(
	target *UploadTarget,
	addBytes int64,
) error {
	if tracker == nil || target == nil || addBytes <= 0 {
		return nil
	}
	key := target.uploadReservationKey()
	if key == "" {
		return nil
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	current := tracker.reserved[key]
	if target.QuotaAvailableBytes != nil &&
		current+addBytes > *target.QuotaAvailableBytes {
		return uploadLimitExceeded("quota_exceeded", *target.QuotaAvailableBytes)
	}
	tracker.reserved[key] = current + addBytes
	return nil
}

func (tracker *uploadReservationTracker) release(
	target *UploadTarget,
	bytes int64,
) {
	if tracker == nil || target == nil || bytes <= 0 {
		return
	}
	key := target.uploadReservationKey()
	if key == "" {
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

type uploadLimitReader struct {
	reader         io.Reader
	target         *UploadTarget
	reservations   *uploadReservationTracker
	staging        *storage.StagingReservationTracker
	stagingRelease func()
	readBytes      int64
	released       bool
}

func (service *LibraryService) limitUploadReader(
	reader io.Reader,
	target *UploadTarget,
) (io.Reader, func()) {
	if service.uploadReservations == nil {
		service.uploadReservations = newUploadReservationTracker()
	}
	if service.stagingReservations == nil {
		service.stagingReservations = storage.NewStagingReservationTracker()
	}
	limited := &uploadLimitReader{
		reader:       reader,
		target:       target,
		reservations: service.uploadReservations,
		staging:      service.stagingReservations,
	}
	return limited, limited.release
}

func (reader *uploadLimitReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return reader.reader.Read(buffer)
	}
	maxBytes := reader.maxFileBytes()
	remaining := maxBytes - reader.readBytes
	if remaining <= 0 {
		var probe [1]byte
		read, err := reader.reader.Read(probe[:])
		if err == io.EOF {
			return 0, io.EOF
		}
		if err != nil && read == 0 {
			return 0, err
		}
		return 0, uploadLimitExceeded("file_too_large", maxBytes)
	}
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}
	read, err := reader.reader.Read(buffer)
	if read > 0 {
		reservedBytes := int64(read)
		if reserveErr := reader.reservations.reserve(reader.target, reservedBytes); reserveErr != nil {
			return 0, reserveErr
		}
		if reserveErr := reader.reserveStaging(reservedBytes); reserveErr != nil {
			reader.reservations.release(reader.target, reservedBytes)
			return 0, reserveErr
		}
		reader.readBytes += reservedBytes
	}
	return read, err
}

func (reader *uploadLimitReader) release() {
	if reader == nil || reader.released {
		return
	}
	reader.released = true
	reader.reservations.release(reader.target, reader.readBytes)
	if reader.target != nil && reader.target.DiskAvailableBytes != nil {
		reader.staging.Release(reader.target.uploadReservationKey(), reader.readBytes)
	}
	if reader.stagingRelease != nil {
		reader.stagingRelease()
	}
}

func (reader *uploadLimitReader) reserveStaging(bytes int64) error {
	if reader == nil || reader.target == nil || reader.target.DiskAvailableBytes == nil {
		return nil
	}
	if reader.stagingRelease == nil {
		release, err := reader.staging.AcquireForActor(
			reader.target.uploadReservationKey(),
			reader.target.StagingActorID,
			storage.DefaultMaxConcurrentStagingOperations,
			storage.DefaultMaxConcurrentStagingOperationsPerActor,
		)
		if err != nil {
			if errors.Is(err, storage.ErrStagingConcurrencyLimit) {
				return uploadLimitExceeded("staging_busy", storage.DefaultMaxConcurrentStagingOperations)
			}
			return err
		}
		reader.stagingRelease = release
	}
	minimumFree := reader.target.MinimumFreeBytes
	if minimumFree <= 0 {
		minimumFree = DefaultMinimumFreeBytes
	}
	if err := reader.staging.Reserve(
		reader.target.uploadReservationKey(),
		*reader.target.DiskAvailableBytes,
		minimumFree,
		bytes,
	); err != nil {
		if errors.Is(err, storage.ErrStagingDiskSpaceLow) {
			available := *reader.target.DiskAvailableBytes - minimumFree
			if available < 0 {
				available = 0
			}
			return uploadLimitExceeded("disk_space_low", available)
		}
		return err
	}
	return nil
}

func (reader *uploadLimitReader) maxFileBytes() int64 {
	if reader.target != nil && reader.target.MaxSingleFileBytes > 0 {
		return reader.target.MaxSingleFileBytes
	}
	return DefaultMaxUploadFileBytes
}

func (target *UploadTarget) uploadReservationKey() string {
	if target == nil {
		return ""
	}
	if target.ReservationKey != "" {
		return target.ReservationKey
	}
	if target.AuthorizedRootID == "" {
		return ""
	}
	return target.AuthorizedRootID
}

func uploadLimitExceeded(reason string, limit int64) *UploadLimitError {
	return &UploadLimitError{Reason: reason, Limit: limit}
}

func (target *UploadTarget) ValidateUploadSize(sizeBytes int64) error {
	if sizeBytes < 0 {
		return ErrLibraryUploadInvalid
	}
	maxFileBytes := DefaultMaxUploadFileBytes
	if target != nil && target.MaxSingleFileBytes > 0 {
		maxFileBytes = target.MaxSingleFileBytes
	}
	if sizeBytes > maxFileBytes {
		return uploadLimitExceeded("file_too_large", maxFileBytes)
	}
	if target == nil {
		return nil
	}
	if target.QuotaAvailableBytes != nil && sizeBytes > *target.QuotaAvailableBytes {
		return uploadLimitExceeded("quota_exceeded", *target.QuotaAvailableBytes)
	}
	if target.DiskAvailableBytes != nil {
		minimumFree := target.MinimumFreeBytes
		if minimumFree <= 0 {
			minimumFree = DefaultMinimumFreeBytes
		}
		availableForUpload := *target.DiskAvailableBytes - minimumFree
		if availableForUpload < 0 {
			availableForUpload = 0
		}
		if sizeBytes > availableForUpload {
			return uploadLimitExceeded("disk_space_low", availableForUpload)
		}
	}
	return nil
}
