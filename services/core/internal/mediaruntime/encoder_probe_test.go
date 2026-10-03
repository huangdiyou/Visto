package mediaruntime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// ffmpegEncodersTable is a trimmed `ffmpeg -hide_banner -encoders` table. The
// probe must read the second whitespace-separated field, not a substring: the
// table below deliberately contains libx264rgb next to libx264.
const ffmpegEncodersTable = `Encoders:
 V..... = Video
 ------
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 V....D libx264rgb           libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 RGB (codec h264)
 V....D libopenh264          OpenH264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 V....D h264_videotoolbox    VideoToolbox H.264 Encoder (codec h264)
 V....D h264_vaapi           H.264/AVC (VAAPI) (codec h264)
`

type encoderProbeRecorder struct {
	calls       [][]string
	encodedWith []string
}

// fakeEncoderProbeDeps returns dependencies whose run function answers the encoder listing
// and then fails exactly the encoders in failing.
func fakeEncoderProbeDeps(t *testing.T, listing string, failing map[string]bool, device string) (encoderProbeDeps, *encoderProbeRecorder) {
	t.Helper()
	fake := &encoderProbeRecorder{}
	deps := encoderProbeDeps{
		run: func(_ context.Context, file string, args ...string) (string, error) {
			fake.calls = append(fake.calls, args)
			if reflect.DeepEqual(args, []string{"-hide_banner", "-encoders"}) {
				return listing, nil
			}
			name := probeEncoderArg(args)
			if name == "" {
				t.Fatalf("probe ran an unrecognised command: %v", args)
			}
			fake.encodedWith = append(fake.encodedWith, name)
			if failing[name] {
				return "", errors.New("media capability command failed")
			}
			return "", nil
		},
	}
	return deps, fake
}

// probeEncoderArg returns the value after -c:v.
func probeEncoderArg(args []string) string {
	for i, arg := range args {
		if arg == "-c:v" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestEncoderCandidatesKeepTheDocumentedFallbackOrder(t *testing.T) {
	cases := map[string][]string{
		"darwin":  {"h264_videotoolbox", "libopenh264", "libx264"},
		"windows": {"h264_nvenc", "h264_qsv", "h264_amf", "libopenh264", "libx264"},
		"linux":   {"h264_nvenc", "h264_qsv", "libopenh264", "libx264"},
	}
	for platform, want := range cases {
		if got := H264EncoderCandidates(platform); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s candidates = %v, want %v", platform, got, want)
		}
	}
}

func TestProbeRecommendsTheFirstEncoderThatActuallyEncodes(t *testing.T) {
	deps, fake := fakeEncoderProbeDeps(t, ffmpegEncodersTable, map[string]bool{"h264_videotoolbox": true}, "")
	result, err := probeH264Encoders(context.Background(), "/runtime/ffmpeg", "darwin", deps)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	// The listing contains the encoder but the idle encode fails: a compiled-in
	// encoder is not an available one.
	if result.Recommended != "libopenh264" {
		t.Fatalf("recommended = %q, want libopenh264", result.Recommended)
	}
	if !reflect.DeepEqual(result.Available, []string{"libopenh264", "libx264"}) {
		t.Fatalf("available = %v", result.Available)
	}
	reason := probeReasonFor(result, "h264_videotoolbox")
	if reason == "" {
		t.Fatal("an unavailable encoder must carry a reason")
	}
	if strings.Contains(reason, "/") {
		t.Fatalf("reason leaks a path: %q", reason)
	}
	if !reflect.DeepEqual(fake.encodedWith, []string{"h264_videotoolbox", "libopenh264", "libx264"}) {
		t.Fatalf("encoded with %v", fake.encodedWith)
	}
}

func TestProbeSkipsEncodersTheRuntimeDoesNotCarry(t *testing.T) {
	deps, fake := fakeEncoderProbeDeps(t, ffmpegEncodersTable, nil, "")
	result, err := probeH264Encoders(context.Background(), "/runtime/ffmpeg", "windows", deps)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	for _, name := range []string{"h264_nvenc", "h264_qsv", "h264_amf"} {
		if probeReasonFor(result, name) != "encoder is not built into this runtime" {
			t.Fatalf("%s should be reported as not built in", name)
		}
	}
	// A candidate that is not in the table must never be executed.
	for _, name := range fake.encodedWith {
		if name == "h264_nvenc" || name == "h264_qsv" || name == "h264_amf" {
			t.Fatalf("probe executed %s even though the runtime does not carry it", name)
		}
	}
	if result.Recommended != "libopenh264" {
		t.Fatalf("recommended = %q", result.Recommended)
	}
}

func TestProbeDoesNotMatchAnEncoderNameAsASubstring(t *testing.T) {
	deps, fake := fakeEncoderProbeDeps(t, " V....D libx264rgb   libx264 H.264 RGB (codec h264)\n", nil, "")
	result, err := probeH264Encoders(context.Background(), "/runtime/ffmpeg", "darwin", deps)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if len(fake.encodedWith) != 0 {
		t.Fatalf("libx264rgb must not satisfy libx264, ran %v", fake.encodedWith)
	}
	if result.Recommended != "" {
		t.Fatalf("recommended = %q, want empty", result.Recommended)
	}
}

func TestProbeExcludesVAAPIEvenWhenListedAndDevicePresent(t *testing.T) {
	deps, recorder := fakeEncoderProbeDeps(t, ffmpegEncodersTable, nil, "/dev/dri/renderD128")
	result, err := probeH264Encoders(context.Background(), "/runtime/ffmpeg", "linux", deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.Recommended != "libopenh264" {
		t.Fatalf("recommendation: %s", result.Recommended)
	}
	for _, name := range recorder.encodedWith {
		if name == "h264_vaapi" {
			t.Fatal("unsupported processor path was probed")
		}
	}
}

func TestProbeReportsNoRecommendationWhenNothingEncodes(t *testing.T) {
	deps, _ := fakeEncoderProbeDeps(t, ffmpegEncodersTable, map[string]bool{
		"h264_videotoolbox": true,
		"libopenh264":       true,
		"libx264":           true,
	}, "")
	result, err := probeH264Encoders(context.Background(), "/runtime/ffmpeg", "darwin", deps)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if result.Recommended != "" || len(result.Available) != 0 {
		t.Fatalf("expected no recommendation, got %+v", result)
	}
	if result.Platform != "darwin" || result.ProbedAt.IsZero() {
		t.Fatalf("result must record platform and time: %+v", result)
	}
}

func TestProbeFailsWhenTheEncoderListingCannotBeRead(t *testing.T) {
	deps := encoderProbeDeps{
		run: func(context.Context, string, ...string) (string, error) {
			return "", errors.New("media capability command failed")
		},
	}
	if _, err := probeH264Encoders(context.Background(), "/runtime/ffmpeg", "darwin", deps); err == nil {
		t.Fatal("a runtime that cannot list encoders must fail the probe")
	}
}

func probeReasonFor(result EncoderProbeResult, name string) string {
	for _, candidate := range result.Candidates {
		if candidate.Name == name {
			return candidate.Reason
		}
	}
	return ""
}
