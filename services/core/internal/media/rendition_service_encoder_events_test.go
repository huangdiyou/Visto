package media

import (
	"context"
	"errors"
	"testing"
)

type recordingEncoderEvents struct {
	events []EncoderEvent
	err    error
}

func (recorder *recordingEncoderEvents) RecordEncoderEvent(
	_ context.Context,
	event EncoderEvent,
) error {
	recorder.events = append(recorder.events, event)
	return recorder.err
}

func TestReportEncoderEventCarriesTheWorkspaceAndAsset(t *testing.T) {
	t.Parallel()

	recorder := &recordingEncoderEvents{}
	service := &RenditionService{}
	service.SetEncoderEventRecorder(recorder)

	service.reportEncoderEvent(
		context.Background(),
		&EncoderEvent{Kind: EncoderEventFallback, PreviousEncoder: "h264_qsv", FailureCount: 1},
		"ws-1", "asset-1", "rendition-1",
	)

	if len(recorder.events) != 1 {
		t.Fatalf("events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.WorkspaceID != "ws-1" || event.AssetID != "asset-1" || event.RenditionID != "rendition-1" {
		t.Fatalf("identity was not attached: %#v", event)
	}
	if event.Kind != EncoderEventFallback || event.PreviousEncoder != "h264_qsv" {
		t.Fatalf("event = %#v", event)
	}
}

// A service without a recorder must behave exactly as before the events existed.
func TestReportEncoderEventWithoutARecorderIsANoOp(t *testing.T) {
	t.Parallel()

	service := &RenditionService{}
	service.reportEncoderEvent(
		context.Background(),
		&EncoderEvent{Kind: EncoderEventTripped},
		"ws-1", "asset-1", "rendition-1",
	)
}

// Most encodes have no encoder news, and that must not reach the recorder.
func TestReportEncoderEventSkipsAnEmptyEvent(t *testing.T) {
	t.Parallel()

	recorder := &recordingEncoderEvents{}
	service := &RenditionService{}
	service.SetEncoderEventRecorder(recorder)

	service.reportEncoderEvent(context.Background(), nil, "ws-1", "asset-1", "rendition-1")
	if len(recorder.events) != 0 {
		t.Fatalf("events = %d, want 0", len(recorder.events))
	}
}

// Delivery failing must never surface: the encode already produced output.
func TestReportEncoderEventSwallowsDeliveryFailures(t *testing.T) {
	t.Parallel()

	recorder := &recordingEncoderEvents{err: errors.New("notification store unavailable")}
	service := &RenditionService{}
	service.SetEncoderEventRecorder(recorder)

	service.reportEncoderEvent(
		context.Background(),
		&EncoderEvent{Kind: EncoderEventTripped},
		"ws-1", "asset-1", "rendition-1",
	)
	if len(recorder.events) != 1 {
		t.Fatalf("events = %d, want 1", len(recorder.events))
	}
}
