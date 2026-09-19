package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"review-studio.local/core/internal/storage"
)

const maxSynchronousProbes = 100

type Prober interface {
	Probe(
		ctx context.Context,
		reader io.Reader,
		hint ProbeHint,
	) (ProbeResult, error)
}

type Storage interface {
	ListObjects(
		ctx context.Context,
		workspaceID string,
		rootID string,
	) ([]storage.StoredObject, error)
	Adapter(
		ctx context.Context,
		workspaceID string,
		rootID string,
	) (storage.Adapter, error)
	Object(
		ctx context.Context,
		workspaceID string,
		objectID string,
	) (storage.StoredObject, error)
}

type Service struct {
	repository Repository
	storage    Storage
	prober     Prober
	clock      func() time.Time
}

func NewService(
	repository Repository,
	storageService Storage,
	prober Prober,
) *Service {
	return &Service{
		repository: repository,
		storage:    storageService,
		prober:     prober,
		clock:      time.Now,
	}
}

func (service *Service) ListRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]Metadata, error) {
	if _, err := service.storage.ListObjects(ctx, workspaceID, rootID); err != nil {
		return nil, err
	}
	return service.repository.ListRoot(ctx, workspaceID, rootID)
}

func (service *Service) ProbeRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (ProbeBatch, error) {
	return service.probeRoot(
		ctx,
		workspaceID,
		rootID,
		maxSynchronousProbes,
		nil,
	)
}

func (service *Service) ProbeRootForJob(
	ctx context.Context,
	workspaceID string,
	rootID string,
	progress func(current, total int64) error,
) (ProbeBatch, error) {
	return service.probeRoot(ctx, workspaceID, rootID, 0, progress)
}

func (service *Service) ProbeStorageObject(
	ctx context.Context,
	workspaceID string,
	objectID string,
) (Metadata, error) {
	object, err := service.storage.Object(ctx, workspaceID, objectID)
	if err != nil {
		return Metadata{}, err
	}
	if object.Status != "available" {
		return Metadata{}, storage.ErrObjectNotFound
	}
	adapter, err := service.storage.Adapter(
		ctx,
		workspaceID,
		object.AuthorizedRootID,
	)
	if err != nil {
		return Metadata{}, err
	}
	metadata := service.probeObject(ctx, adapter, object)
	if err := service.repository.Upsert(context.WithoutCancel(ctx), metadata); err != nil {
		return Metadata{}, err
	}
	if metadata.Status != "succeeded" {
		message := "media probe failed"
		if metadata.ErrorMessage != nil {
			message = *metadata.ErrorMessage
		}
		return metadata, fmt.Errorf("%w: %s", ErrProbeFailed, message)
	}
	return metadata, nil
}

func (service *Service) probeRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
	limit int,
	progress func(current, total int64) error,
) (ProbeBatch, error) {
	objects, err := service.storage.ListObjects(ctx, workspaceID, rootID)
	if err != nil {
		return ProbeBatch{}, err
	}
	existing, err := service.repository.ListRoot(ctx, workspaceID, rootID)
	if err != nil {
		return ProbeBatch{}, err
	}
	existingByObject := make(map[string]Metadata, len(existing))
	for _, item := range existing {
		existingByObject[item.StorageObjectID] = item
	}

	batch := ProbeBatch{RootID: rootID}
	candidates := make([]storage.StoredObject, 0)
	for _, object := range objects {
		if object.Status != "available" {
			continue
		}
		batch.Available++
		previous, ok := existingByObject[object.ID]
		if ok && previous.Status == "succeeded" &&
			previous.SourceFingerprint == object.QuickFingerprint {
			batch.Skipped++
			continue
		}
		candidates = append(candidates, object)
	}
	if limit > 0 && len(candidates) > limit {
		batch.Remaining = len(candidates) - limit
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		batch.Items = existing
		return batch, nil
	}

	adapter, err := service.storage.Adapter(ctx, workspaceID, rootID)
	if err != nil {
		return ProbeBatch{}, err
	}
	for _, object := range candidates {
		if err := ctx.Err(); err != nil {
			return ProbeBatch{}, err
		}
		batch.Attempted++
		metadata := service.probeObject(ctx, adapter, object)
		if err := service.repository.Upsert(
			context.WithoutCancel(ctx),
			metadata,
		); err != nil {
			return ProbeBatch{}, err
		}
		if metadata.Status == "succeeded" {
			batch.Succeeded++
		} else {
			batch.Failed++
		}
		if progress != nil {
			if err := progress(
				int64(batch.Attempted),
				int64(len(candidates)),
			); err != nil {
				return ProbeBatch{}, err
			}
		}
	}

	batch.Items, err = service.repository.ListRoot(ctx, workspaceID, rootID)
	return batch, err
}

func (service *Service) probeObject(
	ctx context.Context,
	adapter storage.Adapter,
	object storage.StoredObject,
) Metadata {
	metadata := Metadata{
		StorageObjectID:   object.ID,
		WorkspaceID:       object.WorkspaceID,
		Status:            "failed",
		SourceFingerprint: object.QuickFingerprint,
		MediaType: classifyMedia(
			ProbeHint{ObjectKey: object.ObjectKey, MIMEType: object.MIMEType},
			nil,
		),
		ProbedAt: service.clock().UTC(),
	}
	reader, _, err := adapter.OpenRange(
		ctx,
		object.ObjectKey,
		storage.ByteRange{},
	)
	if err != nil {
		return failedMetadata(metadata, "probe.open_failed", err)
	}
	defer reader.Close()

	result, err := service.prober.Probe(ctx, reader, ProbeHint{
		ObjectKey: object.ObjectKey,
		MIMEType:  object.MIMEType,
	})
	if err != nil {
		code := "probe.failed"
		if errors.Is(err, ErrProbeUnavailable) {
			code = "probe.unavailable"
		} else if errors.Is(err, ErrProbeOutputLimit) {
			code = "probe.output_limit"
		}
		return failedMetadata(metadata, code, err)
	}

	metadata.Status = "succeeded"
	metadata.MediaType = result.MediaType
	metadata.FormatName = result.FormatName
	metadata.FormatLongName = result.FormatLongName
	metadata.DurationUS = result.DurationUS
	metadata.BitRate = result.BitRate
	metadata.Width = result.Width
	metadata.Height = result.Height
	metadata.RotationDegrees = result.RotationDegrees
	metadata.FrameRate = result.FrameRate
	metadata.VideoCodec = result.VideoCodec
	metadata.AudioCodec = result.AudioCodec
	metadata.RawMetadataJSON = pointer(result.RawMetadataJSON)
	return metadata
}

func failedMetadata(
	metadata Metadata,
	code string,
	err error,
) Metadata {
	message := strings.TrimSpace(err.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	metadata.ErrorCode = pointer(code)
	metadata.ErrorMessage = pointer(message)
	return metadata
}
