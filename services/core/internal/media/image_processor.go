package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	ErrImageProcessorUnavailable = errors.New("ffmpeg is unavailable")
	ErrImageProcessingFailed     = errors.New("image processing failed")
	ErrImageOutputLimit          = errors.New("image rendition output limit exceeded")
)

const (
	defaultImageTimeout = 60 * time.Second
	maxImageOutputBytes = 64 << 20
	maxImageErrorBytes  = 32 << 10
)

type ImageProcessor interface {
	Process(
		ctx context.Context,
		reader io.Reader,
		profile ImageProfile,
	) ([]byte, error)
}

type FFmpegImageProcessor struct {
	command string
	timeout time.Duration
}

func NewFFmpegImageProcessor(command string) *FFmpegImageProcessor {
	if strings.TrimSpace(command) == "" {
		command = "ffmpeg"
	}
	return &FFmpegImageProcessor{
		command: command,
		timeout: defaultImageTimeout,
	}
}

func (processor *FFmpegImageProcessor) Process(
	ctx context.Context,
	reader io.Reader,
	profile ImageProfile,
) ([]byte, error) {
	if _, err := exec.LookPath(processor.command); err != nil {
		return nil, ErrImageProcessorUnavailable
	}
	processContext, cancel := context.WithTimeout(ctx, processor.timeout)
	defer cancel()

	filter := fmt.Sprintf(
		"scale=w='min(%d,iw)':h='min(%d,ih)':"+
			"force_original_aspect_ratio=decrease,setsar=1",
		profile.MaxWidth,
		profile.MaxHeight,
	)
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	command := exec.CommandContext(
		processContext,
		processor.command,
		"-v", "error",
		"-max_alloc", strconv.Itoa(256<<20),
		"-autorotate",
		"-i", "pipe:0",
		"-map_metadata", "-1",
		"-frames:v", "1",
		"-threads", "1",
		"-vf", filter,
		"-c:v", "libwebp",
		"-quality", strconv.Itoa(profile.Quality),
		"-compression_level", "4",
		"-f", "webp",
		"pipe:1",
	)
	command.Stdin = reader
	command.Stdout = &limitedImageWriter{
		writer: &output, remaining: maxImageOutputBytes,
	}
	command.Stderr = &limitedImageWriter{
		writer: &diagnostics, remaining: maxImageErrorBytes,
	}

	err := command.Run()
	if errors.Is(processContext.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: timeout", ErrImageProcessingFailed)
	}
	if errors.Is(err, ErrImageOutputLimit) {
		return nil, err
	}
	if err != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%w: %s", ErrImageProcessingFailed, message)
	}
	if output.Len() == 0 {
		return nil, fmt.Errorf("%w: empty output", ErrImageProcessingFailed)
	}
	return output.Bytes(), nil
}

type limitedImageWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *limitedImageWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > writer.remaining {
		return 0, ErrImageOutputLimit
	}
	count, err := writer.writer.Write(value)
	writer.remaining -= int64(count)
	return count, err
}
