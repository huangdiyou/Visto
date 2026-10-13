package main

import (
	"context"
	"fmt"
	"log/slog"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/notification"
)

// encoderEventRecorder turns an encoder event into the two things the design asks
// for: an in-app notification for the workspace managers and an audit record
// (docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.4).
//
// Both are best effort. The encode already produced output, so a notification or
// an audit row that cannot be written is a reporting problem, not a media one;
// the error is logged and swallowed rather than failing the rendition.
type encoderEventRecorder struct {
	notifications *notification.Service
	audit         *audit.Service
	logger        *slog.Logger
}

func (recorder encoderEventRecorder) RecordEncoderEvent(
	ctx context.Context,
	event media.EncoderEvent,
) error {
	title, body := encoderEventMessage(event)
	if err := recorder.notifications.CreateForWorkspaceManagers(
		ctx,
		notification.CreateForWorkspaceInput{
			WorkspaceID:  event.WorkspaceID,
			Type:         event.Kind,
			ResourceType: "rendition",
			ResourceID:   event.RenditionID,
			Title:        title,
			Body:         body,
		},
	); err != nil {
		recorder.logger.Warn(
			"media encoder notification failed",
			"type", event.Kind,
			"error", err,
		)
	}
	if _, err := recorder.audit.RecordLog(ctx, audit.RecordLogInput{
		WorkspaceID:  event.WorkspaceID,
		ActorType:    "system",
		ActorID:      encoderBreakerActor,
		Action:       event.Kind,
		ResourceType: "rendition",
		ResourceID:   event.RenditionID,
		After: map[string]any{
			"previousEncoder": event.PreviousEncoder,
			"activeEncoder":   event.ActiveEncoder,
			"failureCount":    event.FailureCount,
		},
	}); err != nil {
		recorder.logger.Warn(
			"media encoder audit recording failed",
			"type", event.Kind,
			"error", err,
		)
	}
	return nil
}

// encoderEventMessage is what the Owner actually reads. A trip has to answer
// "why did the encoder change", so it names the encoder, the count and the cause;
// the earlier fallback note only has to say that the job kept going.
func encoderEventMessage(event media.EncoderEvent) (string, string) {
	if event.Kind == media.EncoderEventTripped {
		return "媒体编码器已自动切换", fmt.Sprintf(
			"%s 连续失败 %d 次，已切换到 %s。原因：%s",
			event.PreviousEncoder,
			media.EncoderBreakerThreshold,
			event.ActiveEncoder,
			event.Reason,
		)
	}
	return "媒体编码已回退", fmt.Sprintf(
		"%s 编码失败，已自动改用 %s 继续处理。",
		event.PreviousEncoder,
		event.ActiveEncoder,
	)
}
