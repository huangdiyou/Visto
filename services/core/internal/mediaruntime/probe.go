package mediaruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Probe struct {
	FFmpeg         string    `json:"ffmpeg"`
	FFprobe        string    `json:"ffprobe"`
	FFmpegSHA256   string    `json:"ffmpegSha256"`
	FFprobeSHA256  string    `json:"ffprobeSha256"`
	FFmpegVersion  string    `json:"ffmpegVersion"`
	FFprobeVersion string    `json:"ffprobeVersion"`
	Encoder        string    `json:"encoder"`
	VerifiedAt     time.Time `json:"verifiedAt"`
}
type boundedOutput struct{ bytes.Buffer }

// FFmpeg and FFprobe wrap their license text at a fixed column, so a legal
// LGPL notice can be split across lines. Compare on collapsed whitespace so the
// managed LGPL check does not depend on where upstream wraps its text.
func collapseSpace(value string) string { return strings.Join(strings.Fields(value), " ") }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256<<10 {
		return 0, fmt.Errorf("runtime output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func runTool(ctx context.Context, file string, args ...string) (string, error) {
	child, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, file, args...)
	cmd.WaitDelay = 2 * time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if child.Err() != nil {
		return "", child.Err()
	}
	if err != nil {
		return "", runtimeError("media capability command failed: " + filepath.Base(file))
	}
	return out.String(), nil
}
func hashFile(name string) (string, error) {
	f, e := os.Open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func regularExecutable(name string) (string, error) {
	real, e := filepath.EvalSymlinks(name)
	if e != nil {
		return "", runtimeError("media executable was not found")
	}
	real, e = filepath.Abs(real)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(real)
	if e != nil || !info.Mode().IsRegular() {
		return "", runtimeError("media executable is not a regular file")
	}
	return real, nil
}

// Inspect validates actual media output. It never installs or changes PATH.
func Inspect(ctx context.Context, source, directory string) (Probe, error) {
	var p Probe
	var err error
	if source == "system" {
		p.FFmpeg, err = exec.LookPath("ffmpeg")
		if err == nil {
			p.FFprobe, err = exec.LookPath("ffprobe")
		}
	} else {
		if !filepath.IsAbs(directory) {
			return p, runtimeError("custom runtime requires an absolute directory")
		}
		suffix := ""
		if runtime.GOOS == "windows" {
			suffix = ".exe"
		}
		p.FFmpeg = filepath.Join(directory, "ffmpeg"+suffix)
		p.FFprobe = filepath.Join(directory, "ffprobe"+suffix)
	}
	if err != nil {
		return p, runtimeError("system FFmpeg/FFprobe is missing")
	}
	if source == "managed" {
		for _, name := range []string{p.FFmpeg, p.FFprobe} {
			info, e := os.Lstat(name)
			if e != nil || !info.Mode().IsRegular() {
				return p, runtimeError("managed executable must be a regular file")
			}
		}
	}
	p.FFmpeg, err = regularExecutable(p.FFmpeg)
	if err != nil {
		return p, err
	}
	p.FFprobe, err = regularExecutable(p.FFprobe)
	if err != nil {
		return p, err
	}
	v, err := runTool(ctx, p.FFmpeg, "-version")
	if err != nil {
		return p, err
	}
	p.FFmpegVersion = strings.TrimSpace(strings.SplitN(v, "\n", 2)[0])
	pv, err := runTool(ctx, p.FFprobe, "-version")
	if err != nil {
		return p, err
	}
	p.FFprobeVersion = strings.TrimSpace(strings.SplitN(pv, "\n", 2)[0])
	if len(p.FFmpegVersion) > 1024 || len(p.FFprobeVersion) > 1024 || !strings.HasPrefix(p.FFmpegVersion, "ffmpeg version ") || !strings.HasPrefix(p.FFprobeVersion, "ffprobe version ") {
		return p, runtimeError("unexpected media executable version output")
	}
	if source == "managed" {
		for _, tool := range []struct{ name, version string }{{p.FFmpeg, v}, {p.FFprobe, pv}} {
			if strings.Contains(tool.version, "--enable-gpl") || strings.Contains(tool.version, "--enable-nonfree") {
				return p, runtimeError("managed runtime contains GPL/nonfree components")
			}
			license, e := runTool(ctx, tool.name, "-L")
			if e != nil || !strings.Contains(collapseSpace(license), "GNU Lesser General Public License") {
				return p, runtimeError("managed runtime does not report LGPL")
			}
		}
	}
	encoders, err := runTool(ctx, p.FFmpeg, "-hide_banner", "-encoders")
	if err != nil {
		return p, err
	}
	if strings.Contains(encoders, "libopenh264") {
		p.Encoder = "libopenh264"
	} else if source != "managed" && strings.Contains(encoders, "libx264") {
		p.Encoder = "libx264"
	} else if runtime.GOOS == "darwin" && strings.Contains(encoders, "h264_videotoolbox") {
		p.Encoder = "h264_videotoolbox"
	} else {
		return p, runtimeError("runtime lacks a supported H.264 encoder")
	}
	temp, err := os.MkdirTemp("", "visto-runtime-probe-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(temp)
	mp4 := filepath.Join(temp, "sample.mp4")
	_, err = runTool(ctx, p.FFmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=25", "-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000", "-t", "0.4", "-threads", "1", "-c:v", p.Encoder, "-pix_fmt", "yuv420p", "-c:a", "aac", "-y", mp4)
	if err != nil {
		return p, err
	}
	data, err := runTool(ctx, p.FFprobe, "-v", "error", "-show_streams", "-of", "json", mp4)
	if err != nil {
		return p, err
	}
	var info struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Pix      string `json:"pix_fmt"`
			Rate     string `json:"sample_rate"`
			TimeBase string `json:"time_base"`
		} `json:"streams"`
	}
	if json.Unmarshal([]byte(data), &info) != nil {
		return p, runtimeError("invalid probe report")
	}
	video, audio := false, false
	for _, s := range info.Streams {
		video = video || (s.Codec == "h264" && s.Pix == "yuv420p" && s.TimeBase != "")
		audio = audio || (s.Codec == "aac" && s.Rate == "48000")
	}
	if !video || !audio {
		return p, runtimeError("media output format does not meet runtime contract")
	}
	for _, ext := range []string{"jpg", "webp"} {
		image := filepath.Join(temp, "cover."+ext)
		if _, err = runTool(ctx, p.FFmpeg, "-nostdin", "-v", "error", "-i", mp4, "-frames:v", "1", "-threads", "1", "-y", image); err != nil {
			return p, err
		}
		if _, err = runTool(ctx, p.FFprobe, "-v", "error", "-show_streams", image); err != nil {
			return p, err
		}
	}
	if _, err = runTool(ctx, p.FFmpeg, "-nostdin", "-v", "error", "-i", mp4, "-c", "copy", "-f", "hls", "-hls_time", "0.2", "-hls_segment_filename", filepath.Join(temp, "segment-%d.ts"), "-y", filepath.Join(temp, "index.m3u8")); err != nil {
		return p, err
	}
	p.FFmpegSHA256, err = hashFile(p.FFmpeg)
	if err != nil {
		return p, err
	}
	p.FFprobeSHA256, err = hashFile(p.FFprobe)
	p.VerifiedAt = time.Now().UTC()
	return p, err
}
