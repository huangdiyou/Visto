package media

import (
	"strings"
	"testing"
)

// The debounce the design asks for: while an encoder is on its way to tripping,
// the Owner hears about the failure once, not once per attempt.
func TestEncoderEventFiresOncePerFailureCycle(t *testing.T) {
	t.Parallel()

	first := encoderEventFor(EncoderBreakerOutcome{
		PreviousEncoder: "h264_qsv",
		ActiveEncoder:   "h264_qsv",
		FailureCount:    1,
	}, true)
	if first == nil {
		t.Fatal("the first failure of a cycle must be reported")
	}
	if first.Kind != EncoderEventFallback {
		t.Fatalf("kind = %q, want %q", first.Kind, EncoderEventFallback)
	}
	if first.PreviousEncoder != "h264_qsv" || first.FailureCount != 1 {
		t.Fatalf("event = %#v", first)
	}

	for _, count := range []int{2, 3} {
		if event := encoderEventFor(EncoderBreakerOutcome{
			PreviousEncoder: "h264_qsv",
			ActiveEncoder:   "h264_qsv",
			FailureCount:    count,
		}, true); event != nil {
			t.Fatalf("failure %d produced %#v; only the first failure of a cycle is reported", count, event)
		}
	}
}

// A failure with nowhere to fall back to means the whole rendition failed, which
// is already reported through the job. It is not an encoder-selection event.
func TestEncoderEventIsSilentWhenThereIsNoFallback(t *testing.T) {
	t.Parallel()

	if event := encoderEventFor(EncoderBreakerOutcome{
		PreviousEncoder: "libx264",
		ActiveEncoder:   "libx264",
		FailureCount:    1,
	}, false); event != nil {
		t.Fatalf("event = %#v, want none", event)
	}
}

// Tripping is what explains to the Owner why the encoder changed, so it is
// reported even though the failure count has just been reset.
func TestEncoderEventReportsTheTripAndWhereItMovedTo(t *testing.T) {
	t.Parallel()

	event := encoderEventFor(EncoderBreakerOutcome{
		Tripped:         true,
		PreviousEncoder: "h264_qsv",
		ActiveEncoder:   "libopenh264",
		FailureCount:    0,
		Reason:          "h264_qsv failed 3 times in a row: no device",
	}, false)
	if event == nil {
		t.Fatal("a trip must always be reported")
	}
	if event.Kind != EncoderEventTripped {
		t.Fatalf("kind = %q, want %q", event.Kind, EncoderEventTripped)
	}
	if event.PreviousEncoder != "h264_qsv" || event.ActiveEncoder != "libopenh264" {
		t.Fatalf("event = %#v", event)
	}
	// The reason is what tells the Owner *why* the encoder changed, so it has to
	// survive into the event.
	if !strings.Contains(event.Reason, "no device") {
		t.Fatalf("reason = %q", event.Reason)
	}
}

// A trip is the more important message: an Owner reading only the fallback note
// would not learn that the instance moved off the encoder entirely.
func TestEncoderEventPrefersTheTripOverTheFallback(t *testing.T) {
	t.Parallel()

	event := encoderEventFor(EncoderBreakerOutcome{
		Tripped:         true,
		PreviousEncoder: "h264_qsv",
		ActiveEncoder:   "libopenh264",
		FailureCount:    0,
		Reason:          "tripped",
	}, true)
	if event == nil || event.Kind != EncoderEventTripped {
		t.Fatalf("event = %#v, want the trip", event)
	}
}

// Without a breaker nothing is counted, so nothing is reported.
func TestEncoderEventIsSilentWithoutABreakerOutcome(t *testing.T) {
	t.Parallel()

	if event := encoderEventFor(EncoderBreakerOutcome{}, true); event != nil {
		t.Fatalf("event = %#v, want none", event)
	}
}
