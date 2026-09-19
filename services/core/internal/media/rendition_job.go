package media

import (
	"context"
	"encoding/json"
	"errors"

	"review-studio.local/core/internal/job"
)

const GenerateImageRenditionsJobType = "media.generate_image_renditions"
const GenerateVideoRenditionsJobType = "media.generate_video_renditions"
const GenerateVideoEnhancementsJobType = "media.generate_video_enhancements"

const (
	AssetVersionProcessingPriority = 12
	ImageRenditionPriority         = 5
	VideoRenditionPriority         = 4
	VideoEnhancementPriority       = 2
)

type GenerateImageRenditionsPayload struct {
	StorageObjectID   string `json:"storageObjectId"`
	SourceFingerprint string `json:"sourceFingerprint"`
}

type GenerateImageRenditionsExecutor struct {
	Service *RenditionService
}

type GenerateVideoRenditionsExecutor struct {
	Service *RenditionService
}

type GenerateVideoEnhancementsExecutor struct {
	Service *RenditionService
}

func (executor GenerateVideoRenditionsExecutor) Execute(
	ctx context.Context,
	item job.Job,
	reporter job.Reporter,
) error {
	var payload GenerateImageRenditionsPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return codedRenditionError{
			code: "rendition.payload_invalid",
			err:  err,
		}
	}
	if payload.StorageObjectID == "" ||
		payload.SourceFingerprint == "" ||
		item.SubjectType != "storageObject" ||
		item.SubjectID != payload.StorageObjectID {
		return codedRenditionError{
			code: "rendition.subject_invalid",
			err:  errors.New("video rendition job subject does not match payload"),
		}
	}
	return executor.Service.GenerateVideo(
		ctx,
		item.WorkspaceID,
		payload.StorageObjectID,
		payload.SourceFingerprint,
		func(current, total int64) error {
			return reporter.SetProgress(ctx, current, total, "renditions")
		},
	)
}

func (executor GenerateVideoEnhancementsExecutor) Execute(
	ctx context.Context,
	item job.Job,
	reporter job.Reporter,
) error {
	var payload GenerateImageRenditionsPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return codedRenditionError{
			code: "rendition.payload_invalid",
			err:  err,
		}
	}
	if payload.StorageObjectID == "" ||
		payload.SourceFingerprint == "" ||
		item.SubjectType != "storageObject" ||
		item.SubjectID != payload.StorageObjectID {
		return codedRenditionError{
			code: "rendition.subject_invalid",
			err:  errors.New("video enhancement job subject does not match payload"),
		}
	}
	return executor.Service.GenerateVideoEnhancements(
		ctx,
		item.WorkspaceID,
		payload.StorageObjectID,
		payload.SourceFingerprint,
		func(current, total int64) error {
			return reporter.SetProgress(ctx, current, total, "enhancements")
		},
	)
}

func (executor GenerateImageRenditionsExecutor) Execute(
	ctx context.Context,
	item job.Job,
	reporter job.Reporter,
) error {
	var payload GenerateImageRenditionsPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return codedRenditionError{
			code: "rendition.payload_invalid",
			err:  err,
		}
	}
	if payload.StorageObjectID == "" ||
		payload.SourceFingerprint == "" ||
		item.SubjectType != "storageObject" ||
		item.SubjectID != payload.StorageObjectID {
		return codedRenditionError{
			code: "rendition.subject_invalid",
			err:  errors.New("rendition job subject does not match payload"),
		}
	}
	return executor.Service.Generate(
		ctx,
		item.WorkspaceID,
		payload.StorageObjectID,
		payload.SourceFingerprint,
		func(current, total int64) error {
			return reporter.SetProgress(ctx, current, total, "renditions")
		},
	)
}
