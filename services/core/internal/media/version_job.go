package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"review-studio.local/core/internal/job"
)

const ProcessAssetVersionJobType = "media.process_asset_version"

type ProcessAssetVersionPayload struct {
	VersionID         string `json:"versionId"`
	StorageObjectID   string `json:"storageObjectId"`
	SourceFingerprint string `json:"sourceFingerprint"`
}

type ProcessAssetVersionExecutor struct {
	Media      *Service
	Renditions *RenditionService
	Library    *LibraryService
	Jobs       *job.Service
}

func (executor ProcessAssetVersionExecutor) Execute(
	ctx context.Context,
	item job.Job,
	reporter job.Reporter,
) (runErr error) {
	var payload ProcessAssetVersionPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return codedRenditionError{code: "asset_version.payload_invalid", err: err}
	}
	if payload.VersionID == "" ||
		payload.StorageObjectID == "" ||
		payload.SourceFingerprint == "" ||
		item.SubjectType != "storageObject" ||
		item.SubjectID != payload.StorageObjectID {
		return codedRenditionError{
			code: "asset_version.subject_invalid",
			err:  errors.New("asset version job subject does not match payload"),
		}
	}
	if err := executor.Library.SetVersionProcessingStatus(
		context.WithoutCancel(ctx),
		item.WorkspaceID,
		payload.VersionID,
		"processing",
	); err != nil {
		return err
	}
	defer func() {
		if runErr != nil {
			_ = executor.Library.SetVersionProcessingStatus(
				context.WithoutCancel(ctx),
				item.WorkspaceID,
				payload.VersionID,
				"failed",
			)
		}
	}()

	metadata, err := executor.Media.ProbeStorageObject(
		ctx,
		item.WorkspaceID,
		payload.StorageObjectID,
	)
	if err != nil {
		return err
	}
	if err := reporter.SetProgress(ctx, 1, processingTotal(metadata.MediaType), "steps"); err != nil {
		return err
	}

	switch metadata.MediaType {
	case "image":
		err = executor.Renditions.Generate(
			ctx,
			item.WorkspaceID,
			payload.StorageObjectID,
			payload.SourceFingerprint,
			func(current, total int64) error {
				return reporter.SetProgress(ctx, current+1, total+1, "steps")
			},
		)
	case "video":
		err = executor.Renditions.GenerateVideoCore(
			ctx,
			item.WorkspaceID,
			payload.StorageObjectID,
			payload.SourceFingerprint,
			func(current, total int64) error {
				return reporter.SetProgress(ctx, current+1, total+1, "steps")
			},
		)
	}
	if err != nil {
		return err
	}
	status := "ready"
	if metadata.MediaType == "video" {
		status = "partial"
	}
	if err := executor.Library.SetVersionProcessingStatus(
		context.WithoutCancel(ctx),
		item.WorkspaceID,
		payload.VersionID,
		status,
	); err != nil {
		return err
	}
	if metadata.MediaType == "video" {
		_ = executor.enqueueVideoEnhancements(
			context.WithoutCancel(ctx),
			item.WorkspaceID,
			payload,
		)
	}
	return nil
}

func (executor ProcessAssetVersionExecutor) enqueueVideoEnhancements(
	ctx context.Context,
	workspaceID string,
	payload ProcessAssetVersionPayload,
) error {
	if executor.Jobs == nil {
		return nil
	}
	jobPayload, err := json.Marshal(GenerateImageRenditionsPayload{
		StorageObjectID:   payload.StorageObjectID,
		SourceFingerprint: payload.SourceFingerprint,
	})
	if err != nil {
		return err
	}
	idempotencyKey := fmt.Sprintf(
		"video-enhancements:%s:%s:v1",
		payload.VersionID,
		payload.SourceFingerprint,
	)
	_, _, err = executor.Jobs.Create(ctx, job.CreateInput{
		WorkspaceID:    workspaceID,
		Type:           GenerateVideoEnhancementsJobType,
		Priority:       VideoEnhancementPriority,
		IdempotencyKey: &idempotencyKey,
		SubjectType:    "storageObject",
		SubjectID:      payload.StorageObjectID,
		Payload:        jobPayload,
	})
	if err != nil {
		return codedRenditionError{
			code: "asset_version.enhancement_enqueue_failed",
			err:  err,
		}
	}
	return nil
}

func processingTotal(mediaType string) int64 {
	switch mediaType {
	case "image":
		return 3
	case "video":
		return 3
	default:
		return 1
	}
}
