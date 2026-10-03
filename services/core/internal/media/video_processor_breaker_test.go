package media

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func breakerProcessor(t *testing.T, store *memoryBreakerStore) *FFmpegVideoProcessor {
	t.Helper()
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	breaker := NewEncoderBreaker(store)
	if err := breaker.Load(context.Background()); err != nil {
		t.Fatalf("load breaker: %v", err)
	}
	processor.SetEncoderBreaker(breaker)
	return processor
}

func TestAccelerationModeForEncoder(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"h264_nvenc":        VideoAccelerationNVENC,
		"h264_qsv":          VideoAccelerationQSV,
		"h264_amf":          VideoAccelerationAMF,
		"h264_videotoolbox": VideoAccelerationVideoToolbox,
		"libopenh264":       VideoAccelerationSoftware,
		"libx264":           VideoAccelerationSoftware,
	}
	for encoder, want := range cases {
		got, ok := accelerationModeForEncoder(encoder)
		if !ok || got != want {
			t.Fatalf("accelerationModeForEncoder(%q) = %q/%v, want %q", encoder, got, ok, want)
		}
	}
	if _, ok := accelerationModeForEncoder("h264_bogus"); ok {
		t.Fatal("an unknown encoder must not map to a mode")
	}
}

// Without a breaker the plans must be exactly what they always were; that is what
// keeps an upgraded instance's encoding path unchanged.
func TestEncodingPlansAreUnchangedWithoutABreakerOpinion(t *testing.T) {
	t.Parallel()

	baseline := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	baseline.SetAccelerationMode("qsv")
	want := baseline.encodingPlans(VideoProfile{Kind: RenditionProxy})

	withBreaker := breakerProcessor(t, &memoryBreakerStore{})
	withBreaker.SetAccelerationMode("qsv")
	got := withBreaker.encodingPlans(VideoProfile{Kind: RenditionProxy})

	if len(got) != len(want) {
		t.Fatalf("plan count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("plan %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestEncodingPlansFollowATrippedBreakerToSoftware(t *testing.T) {
	t.Parallel()

	processor := breakerProcessor(t, &memoryBreakerStore{})
	processor.SetAccelerationMode("qsv")
	if err := processor.breaker.Trip(context.Background(), "h264_qsv", "no device", "libopenh264"); err != nil {
		t.Fatalf("trip: %v", err)
	}
	plans := processor.encodingPlans(VideoProfile{Kind: RenditionProxy})
	if len(plans) != 1 {
		t.Fatalf("plan count = %d, want 1", len(plans))
	}
	if plans[0].mode != VideoAccelerationSoftware || plans[0].encoder != "libopenh264" {
		t.Fatalf("plan = %#v, want the software encoder the breaker chose", plans[0])
	}
}

func TestEncodingPlansFollowATrippedBreakerToHardware(t *testing.T) {
	t.Parallel()

	processor := breakerProcessor(t, &memoryBreakerStore{})
	// The configured mode is software, but the breaker moved the instance onto a
	// hardware encoder (for example after a probe recommended one).
	if err := processor.breaker.Trip(context.Background(), "libx264", "slow", "h264_qsv"); err != nil {
		t.Fatalf("trip: %v", err)
	}
	plans := processor.encodingPlans(VideoProfile{Kind: RenditionProxy})
	if len(plans) != 2 {
		t.Fatalf("plan count = %d, want 2", len(plans))
	}
	if plans[0].mode != VideoAccelerationQSV || plans[0].encoder != "h264_qsv" {
		t.Fatalf("first plan = %#v, want qsv", plans[0])
	}
	// The software encoder must stay behind the breaker's choice, otherwise a
	// second failure has nowhere to go.
	if plans[1].mode != VideoAccelerationSoftware || !plans[1].fallback {
		t.Fatalf("second plan = %#v, want a software fallback", plans[1])
	}
}

// A number exported for the probe, or a name that only exists in some build, must
// not become a plan. Falling back to software is the safe answer.
func TestEncodingPlansFallBackWhenTheBreakerNamesAnUnknownEncoder(t *testing.T) {
	t.Parallel()

	processor := breakerProcessor(t, &memoryBreakerStore{})
	if err := processor.breaker.Trip(context.Background(), "libx264", "gone", "h264_bogus"); err != nil {
		t.Fatalf("trip: %v", err)
	}
	plans := processor.encodingPlans(VideoProfile{Kind: RenditionProxy})
	if len(plans) != 1 || plans[0].mode != VideoAccelerationSoftware {
		t.Fatalf("plans = %#v, want a single software plan", plans)
	}
}

func TestProcessorRecordsFailuresAgainstTheEncoderThatFailed(t *testing.T) {
	t.Parallel()

	store := &memoryBreakerStore{}
	processor := breakerProcessor(t, store)
	processor.SetAccelerationMode("qsv")

	processor.recordEncoderFailure(context.Background(), "h264_qsv", errors.New("no device"))
	if got := processor.breaker.Snapshot().FailureCount; got != 1 {
		t.Fatalf("failure count = %d, want 1", got)
	}
	if store.saves != 1 {
		t.Fatalf("saves = %d, want 1", store.saves)
	}

	// The third failure trips, and the breaker moves to the software encoder
	// because that is the only other step in the fallback order.
	processor.recordEncoderFailure(context.Background(), "h264_qsv", errors.New("no device"))
	outcome, _ := processor.breaker.RecordFailure(
		context.Background(), "h264_qsv", "no device", processor.encoderCandidates())
	if !outcome.Tripped || outcome.ActiveEncoder != defaultSoftwareVideoEncoder {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestProcessorSuccessClearsTheStreak(t *testing.T) {
	t.Parallel()

	processor := breakerProcessor(t, &memoryBreakerStore{})
	processor.SetAccelerationMode("qsv")
	processor.recordEncoderFailure(context.Background(), "h264_qsv", errors.New("no device"))
	processor.recordEncoderSuccess(context.Background(), "h264_qsv")
	if got := processor.breaker.Snapshot().FailureCount; got != 0 {
		t.Fatalf("failure count = %d, want 0", got)
	}
}

// A processor with no breaker must not panic or record anything.
func TestProcessorWithoutABreakerRecordsNothing(t *testing.T) {
	t.Parallel()

	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.recordEncoderFailure(context.Background(), "h264_qsv", errors.New("no device"))
	processor.recordEncoderSuccess(context.Background(), "h264_qsv")
}

func TestBreakerReasonIsASingleBoundedLine(t *testing.T) {
	t.Parallel()

	if got := breakerReason(errors.New("first\n\nsecond   third")); got != "first second third" {
		t.Fatalf("reason = %q", got)
	}
	if got := breakerReason(nil); got != "" {
		t.Fatalf("nil reason = %q", got)
	}
	long := strings.Repeat("é", 500)
	got := breakerReason(errors.New(long))
	if len([]rune(got)) != 200 {
		t.Fatalf("reason length = %d runes, want 200", len([]rune(got)))
	}
	if !strings.HasPrefix(long, got) {
		t.Fatal("truncation did not keep a prefix of the message")
	}
}
