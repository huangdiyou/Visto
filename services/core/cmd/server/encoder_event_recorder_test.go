package main

import (
	"strings"
	"testing"

	"review-studio.local/core/internal/media"
)

// The trip notification is the only place the Owner learns why the encoder
// changed, so it has to name the old encoder, how often it failed, where the
// instance moved to and the cause.
func TestEncoderEventMessageExplainsTheTrip(t *testing.T) {
	t.Parallel()

	title, body := encoderEventMessage(media.EncoderEvent{
		Kind:            media.EncoderEventTripped,
		PreviousEncoder: "h264_qsv",
		ActiveEncoder:   "libopenh264",
		Reason:          "h264_qsv failed 3 times in a row: no device",
	})
	if title == "" || body == "" {
		t.Fatal("a trip must produce both a title and a body")
	}
	for _, want := range []string{
		"h264_qsv",
		"libopenh264",
		"no device",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("trip body %q does not mention %q", body, want)
		}
	}
	if !strings.Contains(body, "3") {
		t.Fatalf("trip body %q does not state the failure count", body)
	}
}

// The earlier fallback note only has to say that the job kept going.
func TestEncoderEventMessageExplainsTheFallback(t *testing.T) {
	t.Parallel()

	_, body := encoderEventMessage(media.EncoderEvent{
		Kind:            media.EncoderEventFallback,
		PreviousEncoder: "h264_qsv",
		ActiveEncoder:   "libopenh264",
		FailureCount:    1,
	})
	for _, want := range []string{"h264_qsv", "libopenh264"} {
		if !strings.Contains(body, want) {
			t.Fatalf("fallback body %q does not mention %q", body, want)
		}
	}
}
