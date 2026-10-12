package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"review-studio.local/core/internal/job"
)

const (
	CopyObjectJobType = "storage.copy_object"
	copyChunkBytes    = int64(8 << 20)
)

type CopyTaskPayload struct {
	TaskID string `json:"taskId"`
}

func (service *Service) CreateCopyTask(
	ctx context.Context,
	input CreateCopyTaskInput,
) (CopyTask, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SourceStorageObjectID = strings.TrimSpace(input.SourceStorageObjectID)
	input.SourceAssetID = strings.TrimSpace(input.SourceAssetID)
	input.SourceAssetVersionID = strings.TrimSpace(input.SourceAssetVersionID)
	input.TargetRootID = strings.TrimSpace(input.TargetRootID)
	input.TargetObjectKey = strings.TrimSpace(input.TargetObjectKey)
	sourceSelectors := 0
	for _, value := range []string{
		input.SourceStorageObjectID,
		input.SourceAssetID,
		input.SourceAssetVersionID,
	} {
		if value != "" {
			sourceSelectors++
		}
	}
	if input.WorkspaceID == "" || sourceSelectors != 1 || input.TargetRootID == "" {
		return CopyTask{}, ErrInvalidRootInput
	}
	source, err := service.copySourceObject(ctx, input)
	if err != nil {
		return CopyTask{}, err
	}
	if source.Status != "available" {
		return CopyTask{}, ErrRootUnavailable
	}
	if source.AuthorizedRootID == "" {
		return CopyTask{}, ErrInvalidRootInput
	}
	targetRoot, err := service.repository.Root(
		ctx,
		input.WorkspaceID,
		input.TargetRootID,
	)
	if err != nil {
		return CopyTask{}, err
	}
	if targetRoot.Status != "available" {
		return CopyTask{}, ErrRootUnavailable
	}
	targetKey := input.TargetObjectKey
	if targetKey == "" {
		targetKey = source.ObjectKey
	}
	targetKey, err = NormalizeObjectKey(targetKey)
	if err != nil {
		return CopyTask{}, err
	}
	id, err := newID()
	if err != nil {
		return CopyTask{}, err
	}
	return service.repository.CreateCopyTask(
		ctx,
		createCopyTaskRecord{
			ID: id, WorkspaceID: input.WorkspaceID,
			Source: source, TargetRoot: targetRoot, TargetKey: targetKey,
			TempFile: id + ".part", Now: service.clock().UTC(),
		},
	)
}

func (service *Service) CreateProjectArchiveTasks(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]CopyTask, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	if workspaceID == "" || projectID == "" {
		return nil, ErrInvalidRootInput
	}
	plan, err := service.repository.ProjectArchivePlan(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil || plan.TargetRootID == "" {
		return nil, err
	}
	if err := projectArchivePlanLimitError(len(plan.Objects)); err != nil {
		return nil, err
	}
	tasks := make([]CopyTask, 0, len(plan.Objects))
	for _, object := range plan.Objects {
		if object.AuthorizedRootID == plan.TargetRootID {
			continue
		}
		task, err := service.CreateCopyTask(ctx, CreateCopyTaskInput{
			WorkspaceID:           workspaceID,
			SourceStorageObjectID: object.ID,
			TargetRootID:          plan.TargetRootID,
			TargetObjectKey: path.Join(
				"projects",
				projectID,
				"archive",
				object.ID,
				path.Base(object.ObjectKey),
			),
		})
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func projectArchivePlanLimitError(objectCount int) error {
	if objectCount > maxProjectArchiveObjects {
		return ErrProjectArchiveLimitExceeded
	}
	return nil
}

func (service *Service) copySourceObject(
	ctx context.Context,
	input CreateCopyTaskInput,
) (StoredObject, error) {
	switch {
	case input.SourceStorageObjectID != "":
		return service.repository.Object(
			ctx,
			input.WorkspaceID,
			input.SourceStorageObjectID,
		)
	case input.SourceAssetID != "":
		return service.repository.ObjectForAsset(
			ctx,
			input.WorkspaceID,
			input.SourceAssetID,
		)
	default:
		return service.repository.ObjectForAssetVersion(
			ctx,
			input.WorkspaceID,
			input.SourceAssetVersionID,
		)
	}
}

func (service *Service) AttachCopyTaskJob(
	ctx context.Context,
	workspaceID string,
	taskID string,
	jobID string,
) (CopyTask, error) {
	return service.repository.AttachCopyTaskJob(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(taskID),
		strings.TrimSpace(jobID),
		service.clock().UTC(),
	)
}

func (service *Service) CopyTask(
	ctx context.Context,
	workspaceID string,
	taskID string,
) (CopyTask, error) {
	return service.repository.CopyTask(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(taskID),
	)
}

type CopyTaskExecutor struct {
	Service *Service
}

func (executor CopyTaskExecutor) Execute(
	ctx context.Context,
	item job.Job,
	reporter job.Reporter,
) error {
	if executor.Service == nil {
		return copyTaskError{code: "storage.copy.unavailable", message: "storage service is unavailable"}
	}
	var payload CopyTaskPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil ||
		strings.TrimSpace(payload.TaskID) == "" {
		return copyTaskError{code: "storage.copy.payload_invalid", message: "copy task payload is invalid"}
	}
	task, err := executor.Service.CopyTask(
		ctx,
		item.WorkspaceID,
		payload.TaskID,
	)
	if err != nil {
		return err
	}
	err = executor.Service.runCopyTask(ctx, task, reporter)
	if err != nil {
		_ = executor.Service.repository.FailCopyTask(
			context.WithoutCancel(ctx),
			task.WorkspaceID,
			task.ID,
			copyErrorCode(err),
			err.Error(),
			errors.Is(err, context.Canceled),
			executor.Service.clock().UTC(),
		)
	}
	return err
}

func (service *Service) runCopyTask(
	ctx context.Context,
	task CopyTask,
	reporter job.Reporter,
) error {
	if task.Status == "succeeded" {
		return nil
	}
	if err := os.MkdirAll(service.copyTempDir, 0o700); err != nil {
		return fmt.Errorf("prepare copy staging directory: %w", err)
	}
	if err := service.repository.UpdateCopyTaskRunning(
		ctx,
		task.WorkspaceID,
		task.ID,
		service.clock().UTC(),
	); err != nil {
		return err
	}
	source, err := service.repository.Object(
		ctx,
		task.WorkspaceID,
		task.SourceStorageObjectID,
	)
	if err != nil {
		return err
	}
	sourceAdapter, err := service.Adapter(ctx, task.WorkspaceID, task.SourceRootID)
	if err != nil {
		return err
	}
	targetAdapter, err := service.Adapter(ctx, task.WorkspaceID, task.TargetRootID)
	if err != nil {
		return err
	}
	sourceInfo, err := sourceAdapter.Stat(ctx, task.SourceObjectKey)
	if err != nil {
		return err
	}
	if sourceInfo.Kind != "file" {
		return ErrNotRegularFile
	}
	if sourceInfo.SizeBytes != source.SizeBytes {
		return fmt.Errorf("%w: source size changed", ErrPathChanged)
	}

	tempPath := filepath.Join(service.copyTempDir, task.TempFileName)
	releaseStaging, err := service.reserveCopyStaging(tempPath, sourceInfo.SizeBytes)
	if err != nil {
		return err
	}
	defer releaseStaging()
	releaseTargetCapacity, err := service.reserveCopyTargetCapacity(ctx, task, sourceInfo.SizeBytes)
	if err != nil {
		return err
	}
	defer releaseTargetCapacity()
	sourceHash, err := service.stageCopySource(
		ctx,
		task,
		sourceAdapter,
		sourceInfo,
		tempPath,
		reporter,
	)
	if err != nil {
		return err
	}

	if existing, err := targetAdapter.Stat(ctx, task.TargetObjectKey); err == nil {
		if existing.Kind != "file" || existing.SizeBytes != sourceInfo.SizeBytes {
			return fmt.Errorf("%w: target already exists with different size", ErrPathChanged)
		}
		targetHash, err := hashAdapterObject(ctx, targetAdapter, task.TargetObjectKey, existing.SizeBytes)
		if err != nil {
			return err
		}
		if targetHash != sourceHash {
			return fmt.Errorf("%w: target already exists with different content", ErrPathChanged)
		}
		return service.completeCopyTask(
			ctx,
			task,
			targetAdapter,
			task.TargetObjectKey,
			sourceHash,
			reporter,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tempKey, err := copyTempObjectKey(task)
	if err != nil {
		return err
	}
	file, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("open staged copy: %w", err)
	}
	if _, err := targetAdapter.Put(
		ctx,
		tempKey,
		file,
		sourceInfo.SizeBytes,
		sourceInfo.MIMEType,
	); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		if !errors.Is(err, os.ErrClosed) {
			return fmt.Errorf("close staged copy: %w", err)
		}
	}
	defer targetAdapter.Delete(context.WithoutCancel(ctx), tempKey)

	targetHash, err := hashAdapterObject(ctx, targetAdapter, tempKey, sourceInfo.SizeBytes)
	if err != nil {
		return err
	}
	if targetHash != sourceHash {
		return copyTaskError{code: "storage.copy.hash_mismatch", message: "uploaded copy hash does not match source"}
	}
	if targetAdapter.Capabilities()["move"] {
		if err := targetAdapter.Move(ctx, tempKey, task.TargetObjectKey); err != nil {
			return err
		}
	} else {
		if err := targetAdapter.Copy(ctx, tempKey, task.TargetObjectKey); err != nil {
			return err
		}
		if err := targetAdapter.Delete(ctx, tempKey); err != nil {
			return err
		}
	}
	return service.completeCopyTask(
		ctx,
		task,
		targetAdapter,
		task.TargetObjectKey,
		sourceHash,
		reporter,
	)
}

// reserveCopyTargetCapacity reserves local managed bucket quota and the target
// volume before bytes are written. A point-in-time capacity check is not enough:
// concurrent copies must share an atomic process-local reservation budget.
func (service *Service) reserveCopyTargetCapacity(
	ctx context.Context,
	task CopyTask,
	size int64,
) (func(), error) {
	if size <= 0 {
		return func() {}, nil
	}
	capacity, err := service.LocalManagedBucketCapacityForRoot(
		ctx,
		task.WorkspaceID,
		task.TargetRootID,
	)
	if errors.Is(err, ErrBucketNotFound) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	tracker := service.StagingReservations()
	releases := make([]func(), 0, 2)
	releaseAll := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	if capacity.QuotaAvailableBytes != nil {
		key := "bucket:" + capacity.Bucket.ID
		if err := tracker.ReserveCapacity(key, *capacity.QuotaAvailableBytes, size); err != nil {
			return nil, copyTaskError{code: "storage.copy.quota_exceeded", message: "target bucket quota exceeded"}
		}
		releases = append(releases, func() { tracker.Release(key, size) })
	}
	root, err := service.repository.ResolvedRoot(ctx, task.WorkspaceID, task.TargetRootID)
	if err != nil {
		releaseAll()
		return nil, err
	}
	diskKey, available, err := StagingDiskReservation(root.LocalPath)
	if err != nil {
		releaseAll()
		return nil, fmt.Errorf("inspect target copy disk: %w", err)
	}
	if err := tracker.Reserve(diskKey, available, DefaultStagingMinimumFreeBytes, size); err != nil {
		releaseAll()
		return nil, copyTaskError{code: "storage.copy.disk_space_low", message: "target volume does not have enough free space"}
	}
	releases = append(releases, func() { tracker.Release(diskKey, size) })
	return releaseAll, nil
}

func (service *Service) reserveCopyStaging(tempPath string, sourceSize int64) (func(), error) {
	if sourceSize <= 0 {
		return func() {}, nil
	}
	key, available, err := StagingDiskReservation(service.copyTempDir)
	if err != nil {
		return nil, fmt.Errorf("inspect copy staging disk: %w", err)
	}
	alreadyStaged := int64(0)
	if info, statErr := os.Stat(tempPath); statErr == nil {
		alreadyStaged = info.Size()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect copy staging file: %w", statErr)
	}
	remaining := sourceSize - alreadyStaged
	if remaining <= 0 {
		return func() {}, nil
	}
	tracker := service.StagingReservations()
	releaseOperation, err := tracker.Acquire(key, DefaultMaxConcurrentStagingOperations)
	if err != nil {
		if errors.Is(err, ErrStagingConcurrencyLimit) {
			return nil, copyTaskError{
				code:    "storage.copy.staging_busy",
				message: "copy staging is busy; retry later",
			}
		}
		return nil, err
	}
	if err := tracker.Reserve(key, available, DefaultStagingMinimumFreeBytes, remaining); err != nil {
		releaseOperation()
		if errors.Is(err, ErrStagingDiskSpaceLow) {
			return nil, copyTaskError{
				code:    "storage.copy.staging_space_low",
				message: "copy staging disk does not have enough free space",
			}
		}
		return nil, err
	}
	return func() {
		tracker.Release(key, remaining)
		releaseOperation()
	}, nil
}

func (service *Service) stageCopySource(
	ctx context.Context,
	task CopyTask,
	adapter Adapter,
	source FileInfo,
	tempPath string,
	reporter job.Reporter,
) (string, error) {
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("open copy staging file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat copy staging file: %w", err)
	}
	offset := info.Size()
	if offset > source.SizeBytes {
		if err := file.Truncate(0); err != nil {
			return "", fmt.Errorf("truncate stale copy staging file: %w", err)
		}
		offset = 0
	}
	if offset > 0 {
		ok, err := verifyStagedPrefix(
			ctx,
			adapter,
			source.ObjectKey,
			tempPath,
			offset,
		)
		if err != nil {
			return "", err
		}
		if !ok {
			if err := file.Truncate(0); err != nil {
				return "", fmt.Errorf("reset corrupt copy staging file: %w", err)
			}
			offset = 0
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek copy staging file: %w", err)
	}
	totalForProgress := source.SizeBytes
	if totalForProgress == 0 {
		totalForProgress = 1
	}
	for offset < source.SizeBytes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		length := min64(copyChunkBytes, source.SizeBytes-offset)
		reader, _, err := adapter.OpenRange(
			ctx,
			source.ObjectKey,
			ByteRange{Offset: offset, Length: length},
		)
		if err != nil {
			return "", err
		}
		written, copyErr := io.Copy(file, io.LimitReader(reader, length))
		closeErr := reader.Close()
		if copyErr != nil {
			return "", fmt.Errorf("stage copy chunk: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close copy chunk: %w", closeErr)
		}
		if written != length {
			return "", io.ErrUnexpectedEOF
		}
		offset += written
		if err := service.repository.UpdateCopyTaskProgress(
			ctx,
			task.WorkspaceID,
			task.ID,
			offset,
			source.SizeBytes,
			service.clock().UTC(),
		); err != nil {
			return "", err
		}
		if reporter != nil {
			if err := reporter.SetProgress(
				ctx,
				offset,
				totalForProgress,
				"bytes",
			); err != nil {
				return "", err
			}
		}
	}
	hash, err := hashLocalFile(tempPath)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func verifyStagedPrefix(
	ctx context.Context,
	adapter Adapter,
	objectKey string,
	tempPath string,
	length int64,
) (bool, error) {
	if length == 0 {
		return true, nil
	}
	local, err := os.Open(tempPath)
	if err != nil {
		return false, fmt.Errorf("open staged prefix: %w", err)
	}
	defer local.Close()
	remote, _, err := adapter.OpenRange(
		ctx,
		objectKey,
		ByteRange{Offset: 0, Length: length},
	)
	if err != nil {
		return false, err
	}
	defer remote.Close()
	localBuffer := make([]byte, 256*1024)
	remoteBuffer := make([]byte, len(localBuffer))
	remaining := length
	for remaining > 0 {
		chunk := int64(len(localBuffer))
		if remaining < chunk {
			chunk = remaining
		}
		chunkSize := int(chunk)
		if _, err := io.ReadFull(local, localBuffer[:chunkSize]); err != nil {
			return false, nil
		}
		if _, err := io.ReadFull(remote, remoteBuffer[:chunkSize]); err != nil {
			return false, err
		}
		if !bytes.Equal(localBuffer[:chunkSize], remoteBuffer[:chunkSize]) {
			return false, nil
		}
		remaining -= chunk
	}
	return true, nil
}

func (service *Service) completeCopyTask(
	ctx context.Context,
	task CopyTask,
	adapter Adapter,
	targetKey string,
	contentHash string,
	reporter job.Reporter,
) error {
	finalInfo, err := adapter.Observe(ctx, targetKey)
	if err != nil {
		return err
	}
	finalHash, err := hashAdapterObject(
		ctx,
		adapter,
		targetKey,
		finalInfo.SizeBytes,
	)
	if err != nil {
		return err
	}
	if finalHash != contentHash {
		return copyTaskError{code: "storage.copy.hash_mismatch", message: "final copy hash does not match source"}
	}
	targetObject, err := service.repository.UpsertCopiedObject(
		ctx,
		copiedObjectRecord{
			WorkspaceID: task.WorkspaceID,
			RootID:      task.TargetRootID,
			Observed:    finalInfo,
			ContentHash: contentHash,
			Algorithm:   "sha256",
			Now:         service.clock().UTC(),
		},
	)
	if err != nil {
		return err
	}
	if _, err := service.repository.CompleteCopyTask(
		ctx,
		task.WorkspaceID,
		task.ID,
		targetObject.ID,
		contentHash,
		"sha256",
		service.clock().UTC(),
	); err != nil {
		return err
	}
	if reporter != nil {
		total := finalInfo.SizeBytes
		if total == 0 {
			total = 1
		}
		return reporter.SetProgress(ctx, total, total, "bytes")
	}
	return nil
}

func copyTempObjectKey(task CopyTask) (string, error) {
	base := ".review-studio-copy-" + task.ID + ".tmp"
	dir := path.Dir(task.TargetObjectKey)
	if dir == "." || dir == "/" {
		return NormalizeObjectKey(base)
	}
	return NormalizeObjectKey(path.Join(dir, base))
}

func hashLocalFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open staged copy for hashing: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash staged copy: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashAdapterObject(
	ctx context.Context,
	adapter Adapter,
	objectKey string,
	size int64,
) (string, error) {
	hash := sha256.New()
	if size == 0 {
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	var offset int64
	for offset < size {
		length := min64(copyChunkBytes, size-offset)
		reader, _, err := adapter.OpenRange(
			ctx,
			objectKey,
			ByteRange{Offset: offset, Length: length},
		)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(hash, io.LimitReader(reader, length)); err != nil {
			reader.Close()
			return "", err
		}
		if err := reader.Close(); err != nil {
			return "", err
		}
		offset += length
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyErrorCode(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return coded.Code()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "storage.copy.cancelled"
	case errors.Is(err, ErrPathChanged):
		return "storage.copy.path_changed"
	case errors.Is(err, ErrRootUnavailable):
		return "storage.copy.root_unavailable"
	default:
		return "storage.copy.failed"
	}
}

type copyTaskError struct {
	code    string
	message string
}

func (err copyTaskError) Error() string {
	return err.message
}

func (err copyTaskError) Code() string {
	return err.code
}
