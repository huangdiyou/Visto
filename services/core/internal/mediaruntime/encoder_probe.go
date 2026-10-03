package mediaruntime

import (
	"context"
	"runtime"
	"strings"
	"time"
)

// Encoder availability probing for the managed media runtime.
//
// The `ffmpeg -encoders` listing only says which encoders were compiled in. It
// does not say whether this host can actually run them: a container without a
// passed-through device, a machine without the GPU driver, or a headless Linux
// box with no render node all list h264_nvenc/h264_qsv while every encode fails.
// The probe therefore runs a tiny idle encode per candidate and treats only a
// successful encode as "available" (docs/MEDIA_ENCODING_SELECTION_DESIGN.md §3.1).

// H264EncoderCandidates returns the H.264 encoders this platform may offer, most
// preferred first. The order is also the fallback order: a tripped encoder moves
// to the next available entry, and the final entry is always a software encoder.
func H264EncoderCandidates(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"h264_videotoolbox", "libopenh264", "libx264"}
	case "windows":
		return []string{"h264_nvenc", "h264_qsv", "h264_amf", "libopenh264", "libx264"}
	default:
		return []string{"h264_nvenc", "h264_qsv", "libopenh264", "libx264"}
	}
}

// EncoderAvailability is one candidate's verdict. Reason is empty when the
// encoder is available and otherwise carries a short, path-free explanation the
// Owner settings page can show.
type EncoderAvailability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// EncoderProbeResult is one full sweep. Recommended is the first available
// candidate, or empty when nothing could be confirmed; callers must then fall
// back to software encoding rather than blocking work.
type EncoderProbeResult struct {
	Platform    string                `json:"platform"`
	Available   []string              `json:"available"`
	Recommended string                `json:"recommended"`
	Candidates  []EncoderAvailability `json:"candidates"`
	ProbedAt    time.Time             `json:"probedAt"`
}

// encoderProbeDeps keeps the probe testable without a real FFmpeg or a real
// device node: behavior is injected instead of shelling out to a fake binary, so
// the same test runs on every platform the Server builds for.
type encoderProbeDeps struct {
	run func(ctx context.Context, file string, args ...string) (string, error)
}

func defaultEncoderProbeDeps() encoderProbeDeps {
	return encoderProbeDeps{run: runTool}
}

// ProbeH264Encoders sweeps the candidates for this platform. A failure to read
// the encoder listing is returned as an error; an individual candidate that
// cannot encode is recorded as unavailable, never as an error.
func ProbeH264Encoders(ctx context.Context, ffmpeg string) (EncoderProbeResult, error) {
	return probeH264Encoders(ctx, ffmpeg, runtime.GOOS, defaultEncoderProbeDeps())
}

func probeH264Encoders(
	ctx context.Context,
	ffmpeg string,
	platform string,
	deps encoderProbeDeps,
) (EncoderProbeResult, error) {
	result := EncoderProbeResult{Platform: platform, ProbedAt: time.Now().UTC()}
	listing, err := deps.run(ctx, ffmpeg, "-hide_banner", "-encoders")
	if err != nil {
		return result, err
	}
	for _, name := range H264EncoderCandidates(platform) {
		if !encoderListed(listing, name) {
			result.Candidates = append(result.Candidates, EncoderAvailability{
				Name:   name,
				Reason: "encoder is not built into this runtime",
			})
			continue
		}
		available, reason := probeIdleEncode(ctx, ffmpeg, name, deps)
		result.Candidates = append(result.Candidates, EncoderAvailability{
			Name:      name,
			Available: available,
			Reason:    reason,
		})
		if available {
			result.Available = append(result.Available, name)
		}
	}
	if len(result.Available) > 0 {
		result.Recommended = result.Available[0]
	}
	return result, nil
}

// encoderListed reports whether the `-encoders` table names the encoder exactly.
// It compares the name token rather than a substring so that, for example,
// libx264 does not match libx264rgb.
func encoderListed(listing, name string) bool {
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}

// probeIdleEncode runs the smallest real encode that still exercises the
// encoder: 320x240, at most one second, null output. This is the decisive test;
// the listing above only decides whether it is worth trying.
func probeIdleEncode(
	ctx context.Context,
	ffmpeg string,
	name string,
	deps encoderProbeDeps,
) (bool, string) {
	args := []string{"-nostdin", "-v", "error"}
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25",
		"-t", "1", "-threads", "1", "-pix_fmt", "yuv420p")
	args = append(args, "-c:v", name, "-f", "null", "-")
	if _, err := deps.run(ctx, ffmpeg, args...); err != nil {
		// runTool already reduces the failure to the tool's base name, so this
		// reason stays free of host paths.
		return false, "idle encode failed on this host"
	}
	return true, ""
}
