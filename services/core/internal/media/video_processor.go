package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrVideoProcessorUnavailable = errors.New("ffmpeg is unavailable")
	ErrVideoProcessingFailed     = errors.New("video processing failed")
)

const (
	defaultVideoTimeout = 30 * time.Minute
	maxVideoErrorBytes  = 64 << 10
)

const (
	VideoAccelerationSoftware     = "software"
	VideoAccelerationNVENC        = "nvenc"
	VideoAccelerationQSV          = "qsv"
	VideoAccelerationAMF          = "amf"
	VideoAccelerationVideoToolbox = "videotoolbox"
)

const defaultSoftwareVideoEncoder = "libx264"

type VideoAccelerationSettings struct {
	Mode            string
	Encoder         string
	FallbackEncoder string
	Hardware        bool
}

type videoEncodingPlan struct {
	mode             string
	encoder          string
	requestedMode    string
	requestedEncoder string
	fallback         bool
}

type VideoArtifactFile struct {
	Path       string
	FileName   string
	Role       string
	Sequence   int
	MIMEType   string
	DurationUS *int64
}

type VideoArtifact struct {
	Primary  VideoArtifactFile
	Files    []VideoArtifactFile
	Encoding VideoEncodingReport
	Cleanup  func()
}

type VideoEncodingReport struct {
	Mode             string
	Encoder          string
	RequestedMode    string
	RequestedEncoder string
	Fallback         bool
}

func NormalizeVideoAccelerationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case VideoAccelerationNVENC:
		return VideoAccelerationNVENC
	case VideoAccelerationQSV:
		return VideoAccelerationQSV
	case VideoAccelerationAMF:
		return VideoAccelerationAMF
	case VideoAccelerationVideoToolbox:
		return VideoAccelerationVideoToolbox
	default:
		return VideoAccelerationSoftware
	}
}

func VideoAccelerationSettingsForMode(value string) VideoAccelerationSettings {
	return videoAccelerationSettingsForMode(value, defaultSoftwareVideoEncoder)
}

func videoAccelerationSettingsForMode(value string, softwareEncoder string) VideoAccelerationSettings {
	mode := NormalizeVideoAccelerationMode(value)
	settings := VideoAccelerationSettings{
		Mode:    mode,
		Encoder: normalizeSoftwareVideoEncoder(softwareEncoder),
	}
	switch mode {
	case VideoAccelerationNVENC:
		settings.Encoder = "h264_nvenc"
	case VideoAccelerationQSV:
		settings.Encoder = "h264_qsv"
	case VideoAccelerationAMF:
		settings.Encoder = "h264_amf"
	case VideoAccelerationVideoToolbox:
		settings.Encoder = "h264_videotoolbox"
	}
	if mode != VideoAccelerationSoftware {
		settings.Hardware = true
		fallbackEncoder := normalizeSoftwareVideoEncoder(softwareEncoder)
		if fallbackEncoder != settings.Encoder {
			settings.FallbackEncoder = fallbackEncoder
		}
	}
	return settings
}

func (processor *FFmpegVideoProcessor) encodingPlans(
	profile VideoProfile,
) []videoEncodingPlan {
	if profile.Kind != RenditionProxy && profile.Kind != RenditionHLS {
		return []videoEncodingPlan{processor.softwareVideoEncodingPlan()}
	}
	settings := processor.AccelerationSettings()
	if !settings.Hardware {
		return []videoEncodingPlan{processor.softwareVideoEncodingPlan()}
	}
	primary := videoEncodingPlan{
		mode:             settings.Mode,
		encoder:          settings.Encoder,
		requestedMode:    settings.Mode,
		requestedEncoder: settings.Encoder,
	}
	if settings.Encoder == processor.softwareEncoder {
		return []videoEncodingPlan{primary}
	}
	return []videoEncodingPlan{
		primary,
		{
			mode:             VideoAccelerationSoftware,
			encoder:          processor.softwareEncoder,
			requestedMode:    settings.Mode,
			requestedEncoder: settings.Encoder,
			fallback:         true,
		},
	}
}

func (processor *FFmpegVideoProcessor) softwareVideoEncodingPlan() videoEncodingPlan {
	return videoEncodingPlan{
		mode:             VideoAccelerationSoftware,
		encoder:          processor.softwareEncoder,
		requestedMode:    VideoAccelerationSoftware,
		requestedEncoder: processor.softwareEncoder,
	}
}

func (plan videoEncodingPlan) canFallback() bool {
	return plan.requestedMode != "" && plan.mode != VideoAccelerationSoftware
}

func (plan videoEncodingPlan) report() VideoEncodingReport {
	return VideoEncodingReport{
		Mode:             stringOrDefault(plan.mode, VideoAccelerationSoftware),
		Encoder:          stringOrDefault(plan.encoder, defaultSoftwareVideoEncoder),
		RequestedMode:    stringOrDefault(plan.requestedMode, plan.mode),
		RequestedEncoder: stringOrDefault(plan.requestedEncoder, plan.encoder),
		Fallback:         plan.fallback,
	}
}

func stringOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type VideoProcessor interface {
	Process(
		ctx context.Context,
		reader io.Reader,
		metadata Metadata,
		profile VideoProfile,
	) (VideoArtifact, error)
}

type FFmpegVideoProcessor struct {
	command          string
	timeout          time.Duration
	tempDir          string
	accelerationMode string
	softwareEncoder  string
}

func NewFFmpegVideoProcessor(command string, tempDir string) *FFmpegVideoProcessor {
	if strings.TrimSpace(command) == "" {
		command = "ffmpeg"
	}
	return &FFmpegVideoProcessor{
		command:          command,
		timeout:          defaultVideoTimeout,
		tempDir:          tempDir,
		accelerationMode: VideoAccelerationSoftware,
		softwareEncoder:  defaultSoftwareVideoEncoder,
	}
}

func (processor *FFmpegVideoProcessor) SetAccelerationMode(value string) {
	processor.accelerationMode = NormalizeVideoAccelerationMode(value)
}

func (processor *FFmpegVideoProcessor) SetSoftwareEncoder(value string) {
	processor.softwareEncoder = normalizeSoftwareVideoEncoder(value)
	if processor.softwareEncoder == "h264_videotoolbox" {
		processor.accelerationMode = VideoAccelerationVideoToolbox
	}
}

func normalizeSoftwareVideoEncoder(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "libopenh264":
		return "libopenh264"
	case "h264_videotoolbox":
		return "h264_videotoolbox"
	default:
		return defaultSoftwareVideoEncoder
	}
}

func (processor *FFmpegVideoProcessor) AccelerationSettings() VideoAccelerationSettings {
	return videoAccelerationSettingsForMode(processor.accelerationMode, processor.softwareEncoder)
}

func (processor *FFmpegVideoProcessor) Process(
	ctx context.Context,
	reader io.Reader,
	metadata Metadata,
	profile VideoProfile,
) (VideoArtifact, error) {
	if _, err := exec.LookPath(processor.command); err != nil {
		return VideoArtifact{}, ErrVideoProcessorUnavailable
	}
	workDir, err := os.MkdirTemp(processor.tempDir, "video-rendition-*")
	if err != nil {
		return VideoArtifact{}, fmt.Errorf("create video work directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	artifact, err := processor.process(ctx, reader, metadata, profile, workDir)
	if err != nil {
		cleanup()
		return VideoArtifact{}, err
	}
	artifact.Cleanup = cleanup
	return artifact, nil
}

func (processor *FFmpegVideoProcessor) process(
	ctx context.Context,
	reader io.Reader,
	metadata Metadata,
	profile VideoProfile,
	workDir string,
) (VideoArtifact, error) {
	processContext, cancel := context.WithTimeout(ctx, processor.timeout)
	defer cancel()

	var input processInput
	var err error
	if profile.Kind == RenditionPoster {
		input, err = inputForFastSeekProcess(reader, workDir)
	} else {
		input, err = inputForSeekableProcess(reader, workDir)
	}
	if err != nil {
		return VideoArtifact{}, err
	}
	if input.cleanup != nil {
		defer input.cleanup()
	}
	plans := processor.encodingPlans(profile)
	var lastErr error
	for _, plan := range plans {
		if err := cleanupVideoOutputs(workDir); err != nil {
			return VideoArtifact{}, err
		}
		args, primaryName := videoCommand(profile, metadata, workDir, input.name, plan)
		if err := processor.runVideoCommand(processContext, workDir, input, args); err != nil {
			if errors.Is(processContext.Err(), context.DeadlineExceeded) {
				return VideoArtifact{}, fmt.Errorf("%w: timeout", ErrVideoProcessingFailed)
			}
			lastErr = err
			if plan.canFallback() {
				continue
			}
			return VideoArtifact{}, err
		}

		files, err := collectVideoArtifacts(workDir, primaryName, profile)
		if err != nil {
			return VideoArtifact{}, err
		}
		return VideoArtifact{
			Primary:  files[0],
			Files:    files,
			Encoding: plan.report(),
		}, nil
	}
	if lastErr != nil {
		return VideoArtifact{}, lastErr
	}
	return VideoArtifact{}, fmt.Errorf("%w: no video encoding plan", ErrVideoProcessingFailed)
}

func (processor *FFmpegVideoProcessor) runVideoCommand(
	ctx context.Context,
	workDir string,
	input processInput,
	args []string,
) error {
	var diagnostics bytes.Buffer
	command := exec.CommandContext(ctx, processor.command, args...)
	command.Dir = workDir
	command.Stdin = input.stdin
	command.ExtraFiles = input.extraFiles
	command.Stdout = io.Discard
	command.Stderr = &limitedImageWriter{
		writer: &diagnostics, remaining: maxVideoErrorBytes,
	}
	if err := command.Run(); err != nil {
		message := sanitizeVideoDiagnostics(
			diagnostics.String(),
			append([]string{workDir}, input.sensitivePaths...)...,
		)
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%w: %s", ErrVideoProcessingFailed, message)
	}
	return nil
}

func videoCommand(
	profile VideoProfile,
	metadata Metadata,
	workDir string,
	inputName string,
	plan videoEncodingPlan,
) ([]string, string) {
	inputArgs := []string{
		"-v", "error",
		"-max_alloc", strconv.Itoa(512 << 20),
		"-autorotate",
	}
	outputArgs := []string{
		"-map_metadata", "-1",
		"-threads", "1",
	}
	scale := fmt.Sprintf(
		"scale=w='min(%d,iw)':h='min(%d,ih)':"+
			"force_original_aspect_ratio=decrease:"+
			"force_divisible_by=2,setsar=1",
		profile.MaxWidth,
		profile.MaxHeight,
	)
	switch profile.Kind {
	case RenditionPoster:
		name := "poster.webp"
		seek := posterSeek(metadata.DurationUS)
		args := append([]string{}, inputArgs...)
		args = append(args, "-ss", seek, "-i", inputName)
		args = append(args, outputArgs...)
		args = append(args, "-frames:v", "1", "-vf", scale, "-an")
		args = append(args, "-c:v", "libwebp", "-quality", "84")
		args = append(args, "-compression_level", "4", "-y", name)
		return args, name
	case RenditionStoryboard:
		name := "storyboard.webp"
		interval := storyboardInterval(metadata.DurationUS)
		filter := fmt.Sprintf(
			"fps=1/%s,%s,tile=5x4:padding=2:margin=2",
			interval,
			scale,
		)
		common := commonVideoArgs(inputArgs, outputArgs, inputName)
		args := append(common,
			"-frames:v", "1",
			"-vf", filter,
			"-an",
			"-c:v", "libwebp",
			"-quality", "76",
			"-compression_level", "4",
			"-y", name,
		)
		return args, name
	case RenditionHLS:
		name := "index.m3u8"
		common := commonVideoArgs(inputArgs, outputArgs, inputName)
		args := append(common, transcodeArgs(profile, scale, plan)...)
		args = append(args,
			"-f", "hls",
			"-hls_time", strconv.Itoa(profile.SegmentSec),
			"-hls_playlist_type", "vod",
			"-hls_flags", "independent_segments",
			"-hls_segment_filename", "segment_%05d.ts",
			"-y", name,
		)
		return args, name
	default:
		name := "proxy.mp4"
		common := commonVideoArgs(inputArgs, outputArgs, inputName)
		args := append(common, transcodeArgs(profile, scale, plan)...)
		args = append(args,
			"-movflags", "+faststart",
			"-f", "mp4",
			"-y", name,
		)
		return args, name
	}
}

func commonVideoArgs(inputArgs []string, outputArgs []string, inputName string) []string {
	args := append([]string{}, inputArgs...)
	args = append(args, "-i", inputName)
	return append(args, outputArgs...)
}

func transcodeArgs(profile VideoProfile, scale string, plan videoEncodingPlan) []string {
	return []string{
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-vf", scale,
		"-c:v", plan.encoder,
		"-preset", "veryfast",
		"-profile:v", "main",
		"-pix_fmt", "yuv420p",
		"-b:v", profile.VideoRate,
		"-maxrate", profile.VideoRate,
		"-bufsize", "5000k",
		"-g", "120",
		"-keyint_min", "120",
		"-sc_threshold", "0",
		"-c:a", "aac",
		"-b:a", profile.AudioRate,
		"-ac", "2",
		"-ar", "48000",
	}
}

func cleanupVideoOutputs(workDir string) error {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return fmt.Errorf("list video work directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "source-") {
			continue
		}
		if err := os.Remove(filepath.Join(workDir, entry.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale video artifact: %w", err)
		}
	}
	return nil
}

func collectVideoArtifacts(
	workDir string,
	primaryName string,
	profile VideoProfile,
) ([]VideoArtifactFile, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil, fmt.Errorf("list video artifacts: %w", err)
	}
	files := make([]VideoArtifactFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "source-") {
			continue
		}
		role := "segment"
		sequence := 0
		if name == primaryName {
			role = "playlist"
			if profile.Kind != RenditionHLS {
				role = "master"
			}
		} else if strings.HasPrefix(name, "segment_") {
			number := strings.TrimSuffix(strings.TrimPrefix(name, "segment_"), filepath.Ext(name))
			sequence, _ = strconv.Atoi(number)
		}
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if name == "index.m3u8" {
			mimeType = "application/vnd.apple.mpegurl"
		} else if strings.HasSuffix(name, ".ts") {
			mimeType = "video/mp2t"
		} else if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		files = append(files, VideoArtifactFile{
			Path: filepath.Join(workDir, name), FileName: name,
			Role: role, Sequence: sequence, MIMEType: mimeType,
		})
	}
	for index, file := range files {
		if file.FileName == primaryName {
			files[0], files[index] = files[index], files[0]
			break
		}
	}
	if len(files) == 0 || files[0].FileName != primaryName {
		return nil, fmt.Errorf("%w: primary artifact missing", ErrVideoProcessingFailed)
	}
	if profile.Kind == RenditionHLS {
		if err := assignHLSDurations(files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func assignHLSDurations(files []VideoArtifactFile) error {
	byName := make(map[string]int, len(files))
	for index, file := range files {
		byName[file.FileName] = index
	}
	playlist, err := os.Open(files[0].Path)
	if err != nil {
		return fmt.Errorf("open HLS playlist: %w", err)
	}
	defer playlist.Close()
	var durationUS *int64
	scanner := bufio.NewScanner(playlist)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#EXTINF:") {
			value := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			seconds, parseErr := strconv.ParseFloat(value, 64)
			if parseErr == nil && seconds >= 0 {
				microseconds := int64(math.Round(seconds * 1_000_000))
				durationUS = &microseconds
			}
			continue
		}
		if durationUS != nil && !strings.HasPrefix(line, "#") {
			if index, ok := byName[line]; ok {
				files[index].DurationUS = durationUS
			}
			durationUS = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read HLS playlist: %w", err)
	}
	return nil
}

func posterSeek(durationUS *int64) string {
	if durationUS == nil || *durationUS <= 0 {
		return "0"
	}
	seconds := math.Min(float64(*durationUS)/10_000_000, 5)
	return strconv.FormatFloat(seconds, 'f', 3, 64)
}

func storyboardInterval(durationUS *int64) string {
	if durationUS == nil || *durationUS <= 0 {
		return "1"
	}
	seconds := math.Max(float64(*durationUS)/1_000_000/20, 0.1)
	return strconv.FormatFloat(seconds, 'f', 3, 64)
}

func sanitizeVideoDiagnostics(value string, sensitivePaths ...string) string {
	for _, path := range sensitivePaths {
		if path == "" {
			continue
		}
		value = strings.ReplaceAll(value, path, "<path>")
		value = strings.ReplaceAll(value, filepath.ToSlash(path), "<path>")
	}
	value = strings.TrimSpace(value)
	if len(value) > 4000 {
		value = value[:4000]
	}
	return value
}
