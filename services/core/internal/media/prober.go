package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrProbeUnavailable = errors.New("ffprobe is unavailable")
	ErrProbeFailed      = errors.New("media probe failed")
	ErrProbeOutputLimit = errors.New("media probe output limit exceeded")
)

const (
	defaultProbeTimeout = 30 * time.Second
	maxProbeOutputBytes = 4 << 20
	maxProbeErrorBytes  = 32 << 10
)

type FFProber struct {
	command string
	timeout time.Duration
	tempDir string
}

func NewFFProber(command string) *FFProber {
	return NewFFProberWithTempDir(command, os.TempDir())
}

func NewFFProberWithTempDir(command string, tempDir string) *FFProber {
	if strings.TrimSpace(command) == "" {
		command = "ffprobe"
	}
	if strings.TrimSpace(tempDir) == "" {
		tempDir = os.TempDir()
	}
	return &FFProber{
		command: command,
		timeout: defaultProbeTimeout,
		tempDir: tempDir,
	}
}

func (prober *FFProber) Probe(
	ctx context.Context,
	reader io.Reader,
	hint ProbeHint,
) (ProbeResult, error) {
	if _, err := exec.LookPath(prober.command); err != nil {
		return ProbeResult{}, ErrProbeUnavailable
	}

	probeCtx, cancel := context.WithTimeout(ctx, prober.timeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	input, err := inputForSeekableProcess(reader, prober.tempDir)
	if err != nil {
		return ProbeResult{}, err
	}
	if input.cleanup != nil {
		defer input.cleanup()
	}
	command := exec.CommandContext(
		probeCtx,
		prober.command,
		"-v", "error",
		"-show_format",
		"-show_streams",
		"-of", "json",
		"-i", input.name,
	)
	command.Stdin = input.stdin
	command.ExtraFiles = input.extraFiles
	command.Dir = prober.tempDir
	command.Stdout = &limitedWriter{writer: &stdout, remaining: maxProbeOutputBytes}
	command.Stderr = &limitedWriter{writer: &stderr, remaining: maxProbeErrorBytes}

	err = command.Run()
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return ProbeResult{}, fmt.Errorf("%w: timeout", ErrProbeFailed)
	}
	if errors.Is(err, ErrProbeOutputLimit) {
		return ProbeResult{}, err
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return ProbeResult{}, fmt.Errorf("%w: %s", ErrProbeFailed, message)
	}

	return parseProbeJSON(stdout.Bytes(), hint)
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *limitedWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > writer.remaining {
		return 0, ErrProbeOutputLimit
	}
	count, err := writer.writer.Write(value)
	writer.remaining -= int64(count)
	return count, err
}

type probeDocument struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeFormat struct {
	FormatName     string `json:"format_name"`
	FormatLongName string `json:"format_long_name"`
	Duration       string `json:"duration"`
	BitRate        string `json:"bit_rate"`
}

type probeStream struct {
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	AverageRate  string            `json:"avg_frame_rate"`
	Tags         map[string]string `json:"tags"`
	SideDataList []probeSideData   `json:"side_data_list"`
}

type probeSideData struct {
	Rotation float64 `json:"rotation"`
}

func parseProbeJSON(value []byte, hint ProbeHint) (ProbeResult, error) {
	var document probeDocument
	if err := json.Unmarshal(value, &document); err != nil {
		return ProbeResult{}, fmt.Errorf("%w: decode output: %v", ErrProbeFailed, err)
	}

	result := ProbeResult{
		MediaType:       classifyMedia(hint, document.Streams),
		FormatName:      optionalString(document.Format.FormatName),
		FormatLongName:  optionalString(document.Format.FormatLongName),
		DurationUS:      parseDurationUS(document.Format.Duration),
		BitRate:         parseInt64(document.Format.BitRate),
		RawMetadataJSON: string(value),
	}

	for _, stream := range document.Streams {
		switch stream.CodecType {
		case "video":
			if result.VideoCodec == nil {
				result.VideoCodec = optionalString(stream.CodecName)
			}
			if result.Width == nil && stream.Width > 0 {
				result.Width = pointer(stream.Width)
			}
			if result.Height == nil && stream.Height > 0 {
				result.Height = pointer(stream.Height)
			}
			if result.FrameRate == nil {
				result.FrameRate = parseRate(stream.AverageRate)
			}
			if result.RotationDegrees == nil {
				result.RotationDegrees = streamRotation(stream)
			}
		case "audio":
			if result.AudioCodec == nil {
				result.AudioCodec = optionalString(stream.CodecName)
			}
		}
	}
	return result, nil
}

func classifyMedia(hint ProbeHint, streams []probeStream) string {
	mimeType := strings.ToLower(strings.TrimSpace(hint.MIMEType))
	extension := strings.ToLower(filepath.Ext(hint.ObjectKey))
	switch {
	case strings.HasPrefix(mimeType, "image/"),
		extension == ".jpg", extension == ".jpeg", extension == ".png",
		extension == ".webp", extension == ".gif", extension == ".avif":
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case mimeType == "application/pdf", extension == ".pdf":
		return "pdf"
	}

	for _, stream := range streams {
		if stream.CodecType == "video" {
			return "video"
		}
	}
	for _, stream := range streams {
		if stream.CodecType == "audio" {
			return "audio"
		}
	}
	return "other"
}

func parseDurationUS(value string) *int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return nil
	}
	microseconds := int64(math.Round(seconds * 1_000_000))
	return &microseconds
}

func parseInt64(value string) *int64 {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number < 0 {
		return nil
	}
	return &number
}

func parseRate(value string) *float64 {
	numeratorText, denominatorText, ok := strings.Cut(value, "/")
	if !ok {
		return nil
	}
	numerator, numeratorErr := strconv.ParseFloat(numeratorText, 64)
	denominator, denominatorErr := strconv.ParseFloat(denominatorText, 64)
	if numeratorErr != nil || denominatorErr != nil || denominator <= 0 ||
		numerator < 0 {
		return nil
	}
	rate := numerator / denominator
	return &rate
}

func streamRotation(stream probeStream) *int {
	for _, sideData := range stream.SideDataList {
		if sideData.Rotation != 0 {
			rotation := normalizeRotation(int(math.Round(sideData.Rotation)))
			return &rotation
		}
	}
	if value, ok := stream.Tags["rotate"]; ok {
		rotation, err := strconv.Atoi(value)
		if err == nil {
			normalized := normalizeRotation(rotation)
			return &normalized
		}
	}
	return nil
}

func normalizeRotation(value int) int {
	value %= 360
	if value < 0 {
		value += 360
	}
	return value
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func pointer[T any](value T) *T {
	return &value
}
