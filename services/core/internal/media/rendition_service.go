package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"review-studio.local/core/internal/storage"
)

const (
	maxImageSourceBytes = 512 << 20
	maxImagePixels      = 160_000_000
)

type RenditionService struct {
	repository    RenditionRepository
	metadata      Repository
	storage       Storage
	store         *ManagedStore
	processor     ImageProcessor
	video         VideoProcessor
	prober        Prober
	encoderEvents EncoderEventRecorder
	clock         func() time.Time
}

// SetEncoderEventRecorder lets the service tell the Owner when an encoder failed
// and the job fell back, or when the breaker moved the instance off it. Without
// one, encoding behaves exactly as before.
func (service *RenditionService) SetEncoderEventRecorder(recorder EncoderEventRecorder) {
	service.encoderEvents = recorder
}

// reportEncoderEvent attaches the workspace and asset identity, which only the
// caller knows, and delivers the event. It is best effort: the encode already
// produced output, so a notification that cannot be delivered must never fail it.
func (service *RenditionService) reportEncoderEvent(
	ctx context.Context,
	event *EncoderEvent,
	workspaceID string,
	assetID string,
	renditionID string,
) {
	if service.encoderEvents == nil || event == nil {
		return
	}
	event.WorkspaceID = workspaceID
	event.AssetID = assetID
	event.RenditionID = renditionID
	_ = service.encoderEvents.RecordEncoderEvent(context.WithoutCancel(ctx), *event)
}

func NewRenditionService(
	repository RenditionRepository,
	metadata Repository,
	storageService Storage,
	store *ManagedStore,
	processor ImageProcessor,
	videoProcessor VideoProcessor,
	prober Prober,
) *RenditionService {
	return &RenditionService{
		repository: repository,
		metadata:   metadata,
		storage:    storageService,
		store:      store,
		processor:  processor,
		video:      videoProcessor,
		prober:     prober,
		clock:      time.Now,
	}
}

func (service *RenditionService) VideoCandidates(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]storage.StoredObject, error) {
	return service.candidates(ctx, workspaceID, rootID, "video")
}

func (service *RenditionService) Candidates(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]storage.StoredObject, error) {
	return service.candidates(ctx, workspaceID, rootID, "image")
}

func (service *RenditionService) candidates(
	ctx context.Context,
	workspaceID string,
	rootID string,
	mediaType string,
) ([]storage.StoredObject, error) {
	objects, err := service.storage.ListObjects(ctx, workspaceID, rootID)
	if err != nil {
		return nil, err
	}
	metadata, err := service.metadata.ListRoot(ctx, workspaceID, rootID)
	if err != nil {
		return nil, err
	}
	byObject := make(map[string]Metadata, len(metadata))
	for _, item := range metadata {
		byObject[item.StorageObjectID] = item
	}
	candidates := make([]storage.StoredObject, 0)
	for _, object := range objects {
		item, ok := byObject[object.ID]
		if object.Status == "available" && ok &&
			item.Status == "succeeded" && item.MediaType == mediaType &&
			item.SourceFingerprint == object.QuickFingerprint {
			candidates = append(candidates, object)
		}
	}
	return candidates, nil
}

func (service *RenditionService) OpenSegment(
	ctx context.Context,
	workspaceID string,
	renditionID string,
	fileName string,
) (RenditionSegment, io.ReadSeekCloser, int64, error) {
	segment, err := service.repository.Segment(
		ctx,
		workspaceID,
		renditionID,
		fileName,
	)
	if err != nil {
		return RenditionSegment{}, nil, 0, err
	}
	file, size, err := service.openStoredRendition(
		ctx,
		workspaceID,
		segment.AuthorizedRootID,
		segment.ObjectKey,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RenditionSegment{}, nil, 0, ErrRenditionNotFound
		}
		return RenditionSegment{}, nil, 0, err
	}
	return segment, file, size, nil
}

func (service *RenditionService) ListRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) ([]Rendition, error) {
	if _, err := service.storage.ListObjects(ctx, workspaceID, rootID); err != nil {
		return nil, err
	}
	return service.repository.ListRoot(ctx, workspaceID, rootID)
}

func (service *RenditionService) Get(
	ctx context.Context,
	workspaceID string,
	renditionID string,
) (Rendition, error) {
	return service.repository.Get(ctx, workspaceID, renditionID)
}

func (service *RenditionService) Open(
	ctx context.Context,
	workspaceID string,
	renditionID string,
) (Rendition, io.ReadSeekCloser, int64, error) {
	item, err := service.repository.Get(ctx, workspaceID, renditionID)
	if err != nil {
		return Rendition{}, nil, 0, err
	}
	if item.Status != "ready" || item.ObjectKey == nil {
		return Rendition{}, nil, 0, ErrRenditionNotFound
	}
	file, size, err := service.openStoredRendition(
		ctx,
		workspaceID,
		item.AuthorizedRootID,
		*item.ObjectKey,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Rendition{}, nil, 0, ErrRenditionNotFound
		}
		return Rendition{}, nil, 0, err
	}
	return item, file, size, nil
}

func (service *RenditionService) Generate(
	ctx context.Context,
	workspaceID string,
	objectID string,
	expectedFingerprint string,
	progress func(current, total int64) error,
) error {
	object, err := service.storage.Object(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}
	if object.Status != "available" ||
		object.QuickFingerprint != expectedFingerprint {
		return codedRenditionError{
			code: "rendition.source_changed",
			err:  errors.New("source object changed before rendition generation"),
		}
	}
	metadata, err := service.metadata.Get(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}
	if err := validateImageSource(object, metadata); err != nil {
		return err
	}
	adapter, err := service.storage.Adapter(
		ctx,
		workspaceID,
		object.AuthorizedRootID,
	)
	if err != nil {
		return err
	}
	targetRootID, err := service.repository.TargetRoot(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}

	for index, profile := range defaultImageProfiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := service.generateProfile(
			ctx,
			adapter,
			object,
			targetRootID,
			profile,
		); err != nil {
			return err
		}
		if progress != nil {
			if err := progress(int64(index+1), int64(len(defaultImageProfiles))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *RenditionService) GenerateVideo(
	ctx context.Context,
	workspaceID string,
	objectID string,
	expectedFingerprint string,
	progress func(current, total int64) error,
) error {
	return service.generateVideoProfiles(
		ctx,
		workspaceID,
		objectID,
		expectedFingerprint,
		videoProfilesForSource,
		progress,
	)
}

func (service *RenditionService) GenerateVideoCore(
	ctx context.Context,
	workspaceID string,
	objectID string,
	expectedFingerprint string,
	progress func(current, total int64) error,
) error {
	return service.generateVideoProfiles(
		ctx,
		workspaceID,
		objectID,
		expectedFingerprint,
		videoCoreProfilesForSource,
		progress,
	)
}

func (service *RenditionService) GenerateVideoEnhancements(
	ctx context.Context,
	workspaceID string,
	objectID string,
	expectedFingerprint string,
	progress func(current, total int64) error,
) error {
	return service.generateVideoProfiles(
		ctx,
		workspaceID,
		objectID,
		expectedFingerprint,
		videoEnhancementProfilesForSource,
		progress,
	)
}

func (service *RenditionService) generateVideoProfiles(
	ctx context.Context,
	workspaceID string,
	objectID string,
	expectedFingerprint string,
	profileSelector func(int64, Metadata) []VideoProfile,
	progress func(current, total int64) error,
) error {
	if service.video == nil {
		return codedRenditionError{
			code: "rendition.processor_unavailable",
			err:  ErrVideoProcessorUnavailable,
		}
	}
	object, err := service.storage.Object(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}
	if object.Status != "available" ||
		object.QuickFingerprint != expectedFingerprint {
		return codedRenditionError{
			code: "rendition.source_changed",
			err:  errors.New("source object changed before rendition generation"),
		}
	}
	metadata, err := service.metadata.Get(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}
	if err := validateVideoSource(object, metadata); err != nil {
		return err
	}
	adapter, err := service.storage.Adapter(
		ctx,
		workspaceID,
		object.AuthorizedRootID,
	)
	if err != nil {
		return err
	}
	targetRootID, err := service.repository.TargetRoot(ctx, workspaceID, objectID)
	if err != nil {
		return err
	}
	profiles := profileSelector(object.SizeBytes, metadata)
	for index, profile := range profiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := service.generateVideoProfile(
			ctx,
			adapter,
			object,
			metadata,
			targetRootID,
			profile,
		); err != nil {
			return err
		}
		if progress != nil {
			if err := progress(int64(index+1), int64(len(profiles))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *RenditionService) generateVideoProfile(
	ctx context.Context,
	adapter storage.Adapter,
	object storage.StoredObject,
	metadata Metadata,
	targetRootID *string,
	profile VideoProfile,
) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate video rendition id: %w", err)
	}
	item, err := service.repository.Begin(ctx, Rendition{
		ID:                    id.String(),
		WorkspaceID:           object.WorkspaceID,
		SourceStorageObjectID: object.ID,
		AuthorizedRootID:      targetRootID,
		Kind:                  profile.Kind,
		ProfileKey:            profile.Key,
		ProfileVersion:        profile.Version,
		SourceFingerprint:     object.QuickFingerprint,
	}, service.clock().UTC())
	if err != nil {
		return err
	}

	reader, _, err := adapter.OpenRange(
		ctx,
		object.ObjectKey,
		storage.ByteRange{},
	)
	if err != nil {
		service.fail(ctx, item.ID, "rendition.open_failed", err)
		return err
	}
	artifact, processErr := service.video.Process(ctx, reader, metadata, profile)
	closeErr := reader.Close()
	if processErr != nil {
		service.fail(ctx, item.ID, videoRenditionErrorCode(processErr), processErr)
		return processErr
	}
	defer artifact.Cleanup()
	// The workspace and the asset are only known here, so this is where an encoder
	// failure becomes something the Owner can be told about.
	service.reportEncoderEvent(
		ctx, artifact.EncoderEvent, object.WorkspaceID, object.ID, item.ID)
	if closeErr != nil {
		service.fail(ctx, item.ID, "rendition.close_failed", closeErr)
		return closeErr
	}

	result, err := service.probeVideoArtifact(
		ctx,
		artifact,
		profile,
		metadata,
	)
	if err != nil {
		service.fail(ctx, item.ID, "rendition.verify_failed", err)
		return err
	}
	completion, err := service.importVideoArtifact(
		ctx,
		item,
		object,
		targetRootID,
		profile,
		artifact,
		result,
	)
	if err != nil {
		service.fail(ctx, item.ID, "rendition.store_failed", err)
		return err
	}
	return service.repository.Ready(
		context.WithoutCancel(ctx),
		item.ID,
		completion,
		service.clock().UTC(),
	)
}

func (service *RenditionService) probeVideoArtifact(
	ctx context.Context,
	artifact VideoArtifact,
	profile VideoProfile,
	metadata Metadata,
) (ProbeResult, error) {
	file := artifact.Primary
	if profile.Kind == RenditionHLS {
		for _, candidate := range artifact.Files {
			if candidate.Role == "segment" {
				file = candidate
				break
			}
		}
	}
	reader, err := os.Open(file.Path)
	if err != nil {
		return ProbeResult{}, err
	}
	defer reader.Close()
	result, err := service.prober.Probe(ctx, reader, ProbeHint{
		ObjectKey: file.FileName,
		MIMEType:  file.MIMEType,
	})
	if err == nil && profile.Kind == RenditionHLS {
		result.DurationUS = metadata.DurationUS
	}
	return result, err
}

func (service *RenditionService) importVideoArtifact(
	ctx context.Context,
	item Rendition,
	object storage.StoredObject,
	targetRootID *string,
	profile VideoProfile,
	artifact VideoArtifact,
	probe ProbeResult,
) (RenditionResult, error) {
	completion := RenditionResult{
		MIMEType: artifact.Primary.MIMEType,
		Width:    probe.Width, Height: probe.Height,
		DurationUS:   probe.DurationUS,
		MetadataJSON: videoMetadataJSON(profile, artifact.Encoding),
	}
	for _, file := range artifact.Files {
		if err := ctx.Err(); err != nil {
			return RenditionResult{}, err
		}
		key := videoRenditionObjectKey(object, profile, file.FileName)
		if err := service.importStoredRendition(
			ctx,
			object.WorkspaceID,
			targetRootID,
			key,
			file.Path,
			file.MIMEType,
		); err != nil {
			return RenditionResult{}, err
		}
		info, err := os.Stat(file.Path)
		if err != nil {
			return RenditionResult{}, err
		}
		completion.SizeBytes += info.Size()
		if file.FileName == artifact.Primary.FileName {
			completion.ObjectKey = key
		}
		if profile.Kind == RenditionHLS {
			segmentID, err := uuid.NewV7()
			if err != nil {
				return RenditionResult{}, err
			}
			completion.Segments = append(completion.Segments, RenditionSegment{
				ID: segmentID.String(), RenditionID: item.ID,
				Role: file.Role, Sequence: file.Sequence,
				FileName: file.FileName, ObjectKey: key,
				MIMEType: file.MIMEType, SizeBytes: info.Size(),
				DurationUS: file.DurationUS,
			})
		}
	}
	return completion, nil
}

func (service *RenditionService) generateProfile(
	ctx context.Context,
	adapter storage.Adapter,
	object storage.StoredObject,
	targetRootID *string,
	profile ImageProfile,
) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate rendition id: %w", err)
	}
	item, err := service.repository.Begin(ctx, Rendition{
		ID:                    id.String(),
		WorkspaceID:           object.WorkspaceID,
		SourceStorageObjectID: object.ID,
		AuthorizedRootID:      targetRootID,
		Kind:                  profile.Kind,
		ProfileKey:            profile.Key,
		ProfileVersion:        profile.Version,
		SourceFingerprint:     object.QuickFingerprint,
	}, service.clock().UTC())
	if err != nil {
		return err
	}

	reader, _, err := adapter.OpenRange(
		ctx,
		object.ObjectKey,
		storage.ByteRange{},
	)
	if err != nil {
		service.fail(ctx, item.ID, "rendition.open_failed", err)
		return err
	}
	output, processErr := service.processor.Process(ctx, reader, profile)
	closeErr := reader.Close()
	if processErr != nil {
		service.fail(ctx, item.ID, renditionErrorCode(processErr), processErr)
		return processErr
	}
	if closeErr != nil {
		service.fail(ctx, item.ID, "rendition.close_failed", closeErr)
		return closeErr
	}

	result, err := service.prober.Probe(
		ctx,
		bytes.NewReader(output),
		ProbeHint{ObjectKey: "rendition.webp", MIMEType: "image/webp"},
	)
	if err != nil || result.Width == nil || result.Height == nil {
		if err == nil {
			err = errors.New("rendition dimensions are missing")
		}
		service.fail(ctx, item.ID, "rendition.verify_failed", err)
		return err
	}
	objectKey := renditionObjectKey(object, profile)
	if err := service.writeStoredRendition(
		ctx,
		object.WorkspaceID,
		targetRootID,
		objectKey,
		output,
		"image/webp",
	); err != nil {
		service.fail(ctx, item.ID, "rendition.store_failed", err)
		return err
	}
	return service.repository.Ready(
		context.WithoutCancel(ctx),
		item.ID,
		RenditionResult{
			ObjectKey: objectKey,
			MIMEType:  "image/webp",
			Width:     result.Width, Height: result.Height,
			SizeBytes:    int64(len(output)),
			MetadataJSON: "{}",
		},
		service.clock().UTC(),
	)
}

func (service *RenditionService) writeStoredRendition(
	ctx context.Context,
	workspaceID string,
	rootID *string,
	objectKey string,
	data []byte,
	mimeType string,
) error {
	if rootID == nil {
		return service.store.Write(objectKey, data)
	}
	adapter, err := service.storage.Adapter(ctx, workspaceID, *rootID)
	if err != nil {
		return err
	}
	_, err = adapter.Put(
		ctx,
		objectKey,
		bytes.NewReader(data),
		int64(len(data)),
		mimeType,
	)
	return err
}

func (service *RenditionService) importStoredRendition(
	ctx context.Context,
	workspaceID string,
	rootID *string,
	objectKey string,
	filePath string,
	mimeType string,
) error {
	if rootID == nil {
		return service.store.Import(objectKey, filePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	adapter, err := service.storage.Adapter(ctx, workspaceID, *rootID)
	if err != nil {
		return err
	}
	_, err = adapter.Put(ctx, objectKey, file, info.Size(), mimeType)
	return err
}

func (service *RenditionService) openStoredRendition(
	ctx context.Context,
	workspaceID string,
	rootID *string,
	objectKey string,
) (io.ReadSeekCloser, int64, error) {
	if rootID == nil {
		file, info, err := service.store.Open(objectKey)
		if err != nil {
			return nil, 0, err
		}
		return file, info.Size(), nil
	}
	adapter, err := service.storage.Adapter(ctx, workspaceID, *rootID)
	if err != nil {
		return nil, 0, err
	}
	reader, info, err := adapter.OpenRange(ctx, objectKey, storage.ByteRange{})
	if err != nil {
		return nil, 0, err
	}
	if seeker, ok := reader.(io.ReadSeekCloser); ok {
		return seeker, info.SizeBytes, nil
	}
	defer reader.Close()

	temporary, err := os.CreateTemp("", ".visto-rendition-*")
	if err != nil {
		return nil, 0, err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return nil, 0, err
	}
	written, err := io.Copy(
		temporary,
		io.LimitReader(reader, info.SizeBytes+1),
	)
	if err != nil || written != info.SizeBytes {
		cleanup()
		if err != nil {
			return nil, 0, err
		}
		return nil, 0, errors.New("rendition size changed while reading")
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, 0, err
	}
	return &temporaryRenditionFile{
		File: temporary,
		path: temporaryPath,
	}, written, nil
}

type temporaryRenditionFile struct {
	*os.File
	path string
}

func (file *temporaryRenditionFile) Close() error {
	err := file.File.Close()
	removeErr := os.Remove(file.path)
	if err != nil {
		return err
	}
	return removeErr
}

func (service *RenditionService) fail(
	ctx context.Context,
	id string,
	code string,
	err error,
) {
	message := strings.TrimSpace(err.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	_ = service.repository.Fail(
		context.WithoutCancel(ctx),
		id,
		code,
		message,
		service.clock().UTC(),
	)
}

func validateImageSource(
	object storage.StoredObject,
	metadata Metadata,
) error {
	if metadata.Status != "succeeded" || metadata.MediaType != "image" ||
		metadata.SourceFingerprint != object.QuickFingerprint {
		return codedRenditionError{
			code: "rendition.source_not_ready",
			err:  errors.New("source image metadata is not ready"),
		}
	}
	if object.SizeBytes <= 0 || object.SizeBytes > maxImageSourceBytes {
		return codedRenditionError{
			code: "rendition.source_too_large",
			err:  errors.New("source image exceeds the size limit"),
		}
	}
	if metadata.Width == nil || metadata.Height == nil ||
		*metadata.Width <= 0 || *metadata.Height <= 0 ||
		int64(*metadata.Width)*int64(*metadata.Height) > maxImagePixels {
		return codedRenditionError{
			code: "rendition.pixel_limit",
			err:  errors.New("source image exceeds the pixel limit"),
		}
	}
	return nil
}

func validateVideoSource(
	object storage.StoredObject,
	metadata Metadata,
) error {
	if metadata.Status != "succeeded" || metadata.MediaType != "video" ||
		metadata.SourceFingerprint != object.QuickFingerprint {
		return codedRenditionError{
			code: "rendition.source_not_ready",
			err:  errors.New("source video metadata is not ready"),
		}
	}
	if object.SizeBytes <= 0 || object.SizeBytes > 500<<30 {
		return codedRenditionError{
			code: "rendition.source_too_large",
			err:  errors.New("source video exceeds the size limit"),
		}
	}
	if metadata.Width == nil || metadata.Height == nil ||
		*metadata.Width <= 0 || *metadata.Height <= 0 ||
		int64(*metadata.Width)*int64(*metadata.Height) > maxImagePixels {
		return codedRenditionError{
			code: "rendition.pixel_limit",
			err:  errors.New("source video exceeds the pixel limit"),
		}
	}
	if metadata.DurationUS == nil || *metadata.DurationUS <= 0 ||
		*metadata.DurationUS > int64(6*time.Hour/time.Microsecond) {
		return codedRenditionError{
			code: "rendition.duration_limit",
			err:  errors.New("source video duration is invalid or exceeds the limit"),
		}
	}
	return nil
}

func renditionObjectKey(
	object storage.StoredObject,
	profile ImageProfile,
) string {
	fingerprint := object.QuickFingerprint
	if len(fingerprint) > 20 {
		fingerprint = fingerprint[:20]
	}
	name := fmt.Sprintf(
		"%s-v%d-%s.webp",
		profile.Key,
		profile.Version,
		fingerprint,
	)
	return path.Join(object.WorkspaceID, object.ID, profile.Kind, name)
}

func videoRenditionObjectKey(
	object storage.StoredObject,
	profile VideoProfile,
	fileName string,
) string {
	fingerprint := object.QuickFingerprint
	if len(fingerprint) > 20 {
		fingerprint = fingerprint[:20]
	}
	return path.Join(
		object.WorkspaceID,
		object.ID,
		profile.Kind,
		fmt.Sprintf("%s-v%d-%s", profile.Key, profile.Version, fingerprint),
		fileName,
	)
}

func videoMetadataJSON(profile VideoProfile, encoding VideoEncodingReport) string {
	type videoMetadata struct {
		SegmentSeconds                *int    `json:"segmentSeconds,omitempty"`
		Columns                       *int    `json:"columns,omitempty"`
		Rows                          *int    `json:"rows,omitempty"`
		MaxFrames                     *int    `json:"maxFrames,omitempty"`
		ImageEncoder                  string  `json:"imageEncoder,omitempty"`
		VideoEncoder                  string  `json:"videoEncoder,omitempty"`
		VideoAccelerationMode         string  `json:"videoAccelerationMode,omitempty"`
		VideoAccelerationRequested    string  `json:"videoAccelerationRequested,omitempty"`
		VideoAccelerationFallback     *bool   `json:"videoAccelerationFallback,omitempty"`
		VideoAccelerationFallbackFrom *string `json:"videoAccelerationFallbackFrom,omitempty"`
	}
	item := videoMetadata{}
	switch profile.Kind {
	case RenditionHLS:
		item.SegmentSeconds = &profile.SegmentSec
	case RenditionStoryboard:
		columns := 5
		rows := 4
		maxFrames := 20
		item.Columns = &columns
		item.Rows = &rows
		item.MaxFrames = &maxFrames
		item.ImageEncoder = "libwebp"
	}
	if profile.Kind == RenditionProxy || profile.Kind == RenditionHLS {
		fallback := encoding.Fallback
		item.VideoEncoder = stringOrDefault(encoding.Encoder, defaultSoftwareVideoEncoder)
		item.VideoAccelerationMode = stringOrDefault(
			encoding.Mode,
			VideoAccelerationSoftware,
		)
		item.VideoAccelerationRequested = stringOrDefault(
			encoding.RequestedMode,
			item.VideoAccelerationMode,
		)
		item.VideoAccelerationFallback = &fallback
		if fallback {
			fallbackFrom := stringOrDefault(
				encoding.RequestedEncoder,
				item.VideoEncoder,
			)
			item.VideoAccelerationFallbackFrom = &fallbackFrom
		}
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func renditionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrImageProcessorUnavailable):
		return "rendition.processor_unavailable"
	case errors.Is(err, ErrImageOutputLimit):
		return "rendition.output_limit"
	default:
		return "rendition.processing_failed"
	}
}

func videoRenditionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrVideoProcessorUnavailable):
		return "rendition.processor_unavailable"
	default:
		return "rendition.processing_failed"
	}
}

type codedRenditionError struct {
	code string
	err  error
}

func (value codedRenditionError) Error() string { return value.err.Error() }
func (value codedRenditionError) Unwrap() error { return value.err }
func (value codedRenditionError) Code() string  { return value.code }
