package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFFmpegVideoProcessorCreatesBrowserRenditions(t *testing.T) {
	requireMediaTools(t)
	sourcePath := createVideoFixture(t)
	width := 640
	height := 360
	duration := int64(3_000_000)
	metadata := Metadata{
		MediaType: "video", Width: &width, Height: &height,
		DurationUS: &duration,
	}
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.SetSoftwareEncoder(os.Getenv("REVIEW_STUDIO_SOFTWARE_VIDEO_ENCODER"))

	for _, profile := range defaultVideoProfiles {
		t.Run(profile.Kind, func(t *testing.T) {
			source, err := os.Open(sourcePath)
			if err != nil {
				t.Fatalf("open video fixture: %v", err)
			}
			defer source.Close()
			artifact, err := processor.Process(
				context.Background(),
				source,
				metadata,
				profile,
			)
			if err != nil {
				t.Fatalf("process %s: %v", profile.Kind, err)
			}
			defer artifact.Cleanup()
			if len(artifact.Files) == 0 {
				t.Fatal("expected output files")
			}
			for _, file := range artifact.Files {
				info, err := os.Stat(file.Path)
				if err != nil {
					t.Fatalf("stat %s: %v", file.FileName, err)
				}
				if info.Size() == 0 {
					t.Fatalf("%s is empty", file.FileName)
				}
			}
			if profile.Kind == RenditionHLS {
				playlist, err := os.ReadFile(artifact.Primary.Path)
				if err != nil {
					t.Fatalf("read playlist: %v", err)
				}
				if !bytes.Contains(playlist, []byte("#EXTM3U")) {
					t.Fatal("playlist header is missing")
				}
				if strings.Contains(string(playlist), filepath.Dir(artifact.Primary.Path)) {
					t.Fatal("playlist leaks the temporary directory")
				}
				if len(artifact.Files) < 2 {
					t.Fatal("expected playlist and at least one segment")
				}
				for _, file := range artifact.Files {
					if file.Role == "segment" &&
						(file.DurationUS == nil || *file.DurationUS <= 0) {
						t.Fatalf("segment %s has no duration", file.FileName)
					}
				}
			}
		})
	}
}

func TestPosterCommandUsesFastSeekBeforeInput(t *testing.T) {
	duration := int64(30_000_000)
	args, primary := videoCommand(
		VideoProfile{
			Kind: RenditionPoster, Key: "webp_poster_1280",
			Version: 1, MaxWidth: 1280, MaxHeight: 1280,
		},
		Metadata{DurationUS: &duration},
		t.TempDir(),
		"source.mp4",
		NewFFmpegVideoProcessor("ffmpeg", t.TempDir()).softwareVideoEncodingPlan(),
	)
	if primary != "poster.webp" {
		t.Fatalf("primary = %q, want poster.webp", primary)
	}
	seekIndex := indexOfArg(args, "-ss")
	inputIndex := indexOfArg(args, "-i")
	if seekIndex < 0 || inputIndex < 0 {
		t.Fatalf("poster command missing seek or input args: %#v", args)
	}
	if seekIndex > inputIndex {
		t.Fatalf("poster command should seek before input: %#v", args)
	}
}

func TestVideoAccelerationSettingsNormalizeToSafeDefault(t *testing.T) {
	settings := VideoAccelerationSettingsForMode("unexpected")
	if settings.Mode != VideoAccelerationSoftware {
		t.Fatalf("mode = %q, want software", settings.Mode)
	}
	if settings.Encoder != defaultSoftwareVideoEncoder {
		t.Fatalf("encoder = %q, want %q", settings.Encoder, defaultSoftwareVideoEncoder)
	}
	if settings.Hardware {
		t.Fatal("unexpected hardware acceleration for invalid mode")
	}
}

func TestVideoProcessorUsesConfiguredOpenH264SoftwareEncoder(t *testing.T) {
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.SetSoftwareEncoder("libopenh264")
	settings := processor.AccelerationSettings()
	if settings.Encoder != "libopenh264" {
		t.Fatalf("encoder = %q, want libopenh264", settings.Encoder)
	}
	plans := processor.encodingPlans(VideoProfile{Kind: RenditionProxy})
	if len(plans) != 1 || plans[0].encoder != "libopenh264" {
		t.Fatalf("plans = %#v, want one libopenh264 software plan", plans)
	}
}

func TestVideoProcessorUsesManagedVideoToolboxWithoutDuplicateFallback(t *testing.T) {
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.SetSoftwareEncoder("h264_videotoolbox")
	settings := processor.AccelerationSettings()
	if settings.Mode != VideoAccelerationVideoToolbox || settings.Encoder != "h264_videotoolbox" || !settings.Hardware {
		t.Fatalf("settings = %#v, want VideoToolbox hardware encoding", settings)
	}
	if settings.FallbackEncoder != "" {
		t.Fatalf("fallback encoder = %q, want none when VideoToolbox is the managed runtime encoder", settings.FallbackEncoder)
	}
	plans := processor.encodingPlans(VideoProfile{Kind: RenditionProxy})
	if len(plans) != 1 || plans[0].encoder != "h264_videotoolbox" || plans[0].fallback {
		t.Fatalf("plans = %#v, want one VideoToolbox plan with no software fallback", plans)
	}
}

func TestVideoCommandUsesConfiguredVideoEncoder(t *testing.T) {
	plan := videoEncodingPlan{
		mode:             VideoAccelerationNVENC,
		encoder:          "h264_nvenc",
		requestedMode:    VideoAccelerationNVENC,
		requestedEncoder: "h264_nvenc",
	}
	args, primary := videoCommand(
		VideoProfile{
			Kind: RenditionHLS, Key: "hls_h264_aac_720p_4s",
			Version: 1, MaxWidth: 1280, MaxHeight: 720,
			VideoRate: "2500k", AudioRate: "128k", SegmentSec: 4,
		},
		Metadata{},
		t.TempDir(),
		"source.mp4",
		plan,
	)
	if primary != "index.m3u8" {
		t.Fatalf("primary = %q, want index.m3u8", primary)
	}
	if value := argValue(args, "-c:v"); value != "h264_nvenc" {
		t.Fatalf("video encoder = %q, want h264_nvenc: %#v", value, args)
	}
	if value := argValue(args, "-pix_fmt"); value != "yuv420p" {
		t.Fatalf("pixel format = %q, want yuv420p: %#v", value, args)
	}
	if value := argValue(args, "-c:a"); value != "aac" {
		t.Fatalf("audio encoder = %q, want aac: %#v", value, args)
	}
}

func TestVideoProcessorPlansHardwareWithSoftwareFallback(t *testing.T) {
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.SetAccelerationMode("qsv")
	plans := processor.encodingPlans(VideoProfile{Kind: RenditionProxy})
	if len(plans) != 2 {
		t.Fatalf("plan count = %d, want 2", len(plans))
	}
	if plans[0].mode != VideoAccelerationQSV || plans[0].encoder != "h264_qsv" {
		t.Fatalf("first plan = %#v, want qsv", plans[0])
	}
	if !plans[0].canFallback() {
		t.Fatal("hardware plan should allow fallback")
	}
	if plans[1].mode != VideoAccelerationSoftware ||
		plans[1].encoder != defaultSoftwareVideoEncoder ||
		!plans[1].fallback {
		t.Fatalf("fallback plan = %#v, want software fallback", plans[1])
	}
}

func TestVideoMetadataIncludesEncodingDiagnostics(t *testing.T) {
	value := videoMetadataJSON(
		VideoProfile{
			Kind: RenditionHLS, Key: "hls_h264_aac_720p_4s",
			Version: 1, SegmentSec: 4,
		},
		VideoEncodingReport{
			Mode:             VideoAccelerationSoftware,
			Encoder:          defaultSoftwareVideoEncoder,
			RequestedMode:    VideoAccelerationNVENC,
			RequestedEncoder: "h264_nvenc",
			Fallback:         true,
		},
	)
	for _, expected := range []string{
		`"segmentSeconds":4`,
		`"videoEncoder":"libx264"`,
		`"videoAccelerationMode":"software"`,
		`"videoAccelerationRequested":"nvenc"`,
		`"videoAccelerationFallback":true`,
		`"videoAccelerationFallbackFrom":"h264_nvenc"`,
	} {
		if !strings.Contains(value, expected) {
			t.Fatalf("metadata %s missing %s", value, expected)
		}
	}
}

func TestSanitizeVideoDiagnosticsRemovesSensitivePaths(t *testing.T) {
	message := sanitizeVideoDiagnostics(
		`C:\secret\source.mp4: Invalid data found in input`,
		`C:\secret\source.mp4`,
	)
	if strings.Contains(message, `C:\secret\source.mp4`) {
		t.Fatalf("diagnostics leaked sensitive path: %q", message)
	}
	if !strings.Contains(message, "<path>") {
		t.Fatalf("diagnostics did not keep a placeholder: %q", message)
	}
}

func createVideoFixture(t *testing.T) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "source.mp4")
	command := exec.Command(
		"ffmpeg",
		"-v", "error",
		"-f", "lavfi",
		"-i", "testsrc2=size=640x360:rate=30:duration=3",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:sample_rate=48000:duration=3",
		"-c:v", "mpeg4",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-shortest",
		"-y", output,
	)
	if value, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create video fixture: %v: %s", err, value)
	}
	return output
}

func indexOfArg(args []string, target string) int {
	for index, arg := range args {
		if arg == target {
			return index
		}
	}
	return -1
}

func argValue(args []string, target string) string {
	index := indexOfArg(args, target)
	if index < 0 || index+1 >= len(args) {
		return ""
	}
	return args[index+1]
}
