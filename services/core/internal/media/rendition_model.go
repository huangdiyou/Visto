package media

import (
	"strings"
	"time"
)

const (
	RenditionThumbnail     = "thumbnail"
	RenditionScreenPreview = "screen_preview"
	RenditionPoster        = "poster"
	RenditionProxy         = "proxy"
	RenditionHLS           = "hls"
	RenditionStoryboard    = "storyboard"
)

const (
	ProcessingStageIngested          = "ingested"
	ProcessingStageProcessing        = "processing"
	ProcessingStagePreviewReady      = "preview_ready"
	ProcessingStageOptimizing        = "optimizing"
	ProcessingStageComplete          = "complete"
	ProcessingStageEnhancementFailed = "enhancement_failed"
	ProcessingStageFailed            = "failed"
)

type Rendition struct {
	ID                    string
	WorkspaceID           string
	SourceStorageObjectID string
	AuthorizedRootID      *string
	AssetVersionID        *string
	Kind                  string
	ProfileKey            string
	ProfileVersion        int
	SourceFingerprint     string
	Status                string
	ObjectKey             *string
	MIMEType              *string
	Width                 *int
	Height                *int
	DurationUS            *int64
	SizeBytes             *int64
	MetadataJSON          string
	ErrorCode             *string
	ErrorMessage          *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CompletedAt           *time.Time
}

type RenditionSegment struct {
	ID               string
	RenditionID      string
	AuthorizedRootID *string
	Role             string
	Sequence         int
	FileName         string
	ObjectKey        string
	MIMEType         string
	SizeBytes        int64
	DurationUS       *int64
	CreatedAt        time.Time
}

type RenditionResult struct {
	ObjectKey    string
	MIMEType     string
	Width        *int
	Height       *int
	DurationUS   *int64
	SizeBytes    int64
	MetadataJSON string
	Segments     []RenditionSegment
}

type VideoProfile struct {
	Kind       string
	Key        string
	Version    int
	MaxWidth   int
	MaxHeight  int
	VideoRate  string
	AudioRate  string
	SegmentSec int
}

var defaultVideoProfiles = []VideoProfile{
	{
		Kind: RenditionPoster, Key: "webp_poster_1280",
		Version: 1, MaxWidth: 1280, MaxHeight: 1280,
	},
	{
		Kind: RenditionProxy, Key: "h264_aac_720p",
		Version: 1, MaxWidth: 1280, MaxHeight: 720,
		VideoRate: "2500k", AudioRate: "128k",
	},
	{
		Kind: RenditionHLS, Key: "hls_h264_aac_720p_4s",
		Version: 1, MaxWidth: 1280, MaxHeight: 720,
		VideoRate: "2500k", AudioRate: "128k", SegmentSec: 4,
	},
	{
		Kind: RenditionStoryboard, Key: "webp_storyboard_20",
		Version: 1, MaxWidth: 240, MaxHeight: 180,
	},
}

const largeVideoPreviewThresholdBytes int64 = 512 << 20

func videoProfilesForSource(sourceSizeBytes int64, metadata Metadata) []VideoProfile {
	coreProfiles := videoCoreProfilesForSource(sourceSizeBytes, metadata)
	enhancementProfiles := videoEnhancementProfilesForSource(sourceSizeBytes, metadata)
	result := make([]VideoProfile, 0, len(coreProfiles)+len(enhancementProfiles))
	result = append(result, coreProfiles...)
	result = append(result, enhancementProfiles...)
	return result
}

func videoCoreProfilesForSource(sourceSizeBytes int64, metadata Metadata) []VideoProfile {
	playbackKind := RenditionProxy
	if preferHLSPreview(sourceSizeBytes, metadata) {
		playbackKind = RenditionHLS
	}
	result := make([]VideoProfile, 0, 2)
	for _, profile := range defaultVideoProfiles {
		switch profile.Kind {
		case RenditionPoster:
			result = append(result, profile)
		case playbackKind:
			result = append(result, profile)
		}
	}
	return result
}

func videoEnhancementProfilesForSource(
	sourceSizeBytes int64,
	metadata Metadata,
) []VideoProfile {
	result := make([]VideoProfile, 0, 1)
	for _, profile := range defaultVideoProfiles {
		if profile.Kind == RenditionStoryboard {
			result = append(result, profile)
		}
	}
	return result
}

func AssetVersionProcessingStage(version AssetVersion) string {
	statusByProfile := make(map[string]string, len(version.Renditions))
	for _, rendition := range version.Renditions {
		statusByProfile[rendition.Kind+":"+rendition.ProfileKey] = rendition.Status
	}
	if version.Probe == nil {
		if version.ProcessingStatus == "failed" {
			return ProcessingStageFailed
		}
		if version.ProcessingStatus == "processing" {
			return ProcessingStageProcessing
		}
		return ProcessingStageIngested
	}
	if version.Probe.Status == "failed" {
		return ProcessingStageFailed
	}
	if version.Probe.Status != "succeeded" {
		if version.ProcessingStatus == "failed" {
			return ProcessingStageFailed
		}
		if version.ProcessingStatus == "processing" {
			return ProcessingStageProcessing
		}
		return ProcessingStageIngested
	}
	switch version.Probe.MediaType {
	case "image":
		return imageProcessingStage(version, statusByProfile)
	case "video":
		return videoProcessingStage(version, statusByProfile)
	default:
		return ProcessingStageComplete
	}
}

func imageProcessingStage(
	version AssetVersion,
	statusByProfile map[string]string,
) string {
	if profilesReady(defaultImageProfiles, statusByProfile) {
		return ProcessingStageComplete
	}
	previewReady := profileStatus(
		statusByProfile,
		RenditionScreenPreview,
		"webp_2048_fit",
	) == "ready"
	if previewReady {
		if profilesFailed(defaultImageProfiles, statusByProfile) {
			return ProcessingStageEnhancementFailed
		}
		if version.ProcessingStatus == "processing" {
			return ProcessingStageOptimizing
		}
		return ProcessingStagePreviewReady
	}
	if profilesFailed(defaultImageProfiles, statusByProfile) ||
		version.ProcessingStatus == "failed" {
		return ProcessingStageFailed
	}
	if version.ProcessingStatus == "processing" {
		return ProcessingStageProcessing
	}
	return ProcessingStageIngested
}

func videoProcessingStage(
	version AssetVersion,
	statusByProfile map[string]string,
) string {
	profiles := videoProfilesForSource(version.SourceSizeBytes, *version.Probe)
	if videoProfilesReady(profiles, readyProfileSet(statusByProfile)) {
		return ProcessingStageComplete
	}
	playbackReady := false
	playbackFailed := false
	for _, profile := range profiles {
		if profile.Kind != RenditionProxy && profile.Kind != RenditionHLS {
			continue
		}
		switch profileStatus(statusByProfile, profile.Kind, profile.Key) {
		case "ready":
			playbackReady = true
		case "failed":
			playbackFailed = true
		}
	}
	if playbackReady {
		if profilesFailed(profiles, statusByProfile) ||
			version.ProcessingStatus == "failed" {
			return ProcessingStageEnhancementFailed
		}
		if version.ProcessingStatus == "processing" {
			return ProcessingStageOptimizing
		}
		return ProcessingStagePreviewReady
	}
	if playbackFailed || version.ProcessingStatus == "failed" {
		return ProcessingStageFailed
	}
	if version.ProcessingStatus == "processing" {
		return ProcessingStageProcessing
	}
	return ProcessingStageIngested
}

func profileStatus(
	statusByProfile map[string]string,
	kind string,
	key string,
) string {
	return statusByProfile[kind+":"+key]
}

func readyProfileSet(statusByProfile map[string]string) map[string]struct{} {
	readyProfiles := make(map[string]struct{}, len(statusByProfile))
	for key, status := range statusByProfile {
		if status == "ready" {
			readyProfiles[key] = struct{}{}
		}
	}
	return readyProfiles
}

func profilesReady[T interface{ profileID() string }](
	profiles []T,
	statusByProfile map[string]string,
) bool {
	for _, profile := range profiles {
		if statusByProfile[profile.profileID()] != "ready" {
			return false
		}
	}
	return true
}

func profilesFailed[T interface{ profileID() string }](
	profiles []T,
	statusByProfile map[string]string,
) bool {
	for _, profile := range profiles {
		if statusByProfile[profile.profileID()] == "failed" {
			return true
		}
	}
	return false
}

func (profile VideoProfile) profileID() string {
	return profile.Kind + ":" + profile.Key
}

func (profile ImageProfile) profileID() string {
	return profile.Kind + ":" + profile.Key
}

func preferHLSPreview(sourceSizeBytes int64, metadata Metadata) bool {
	if sourceSizeBytes >= largeVideoPreviewThresholdBytes {
		return true
	}
	if isUltraHighDefinition(metadata) {
		return true
	}
	return isBrowserCompatibilityRiskCodec(metadata.VideoCodec)
}

func isUltraHighDefinition(metadata Metadata) bool {
	if metadata.Width == nil || metadata.Height == nil {
		return false
	}
	width := *metadata.Width
	height := *metadata.Height
	return width >= 3840 || height >= 2160 || width*height >= 3840*2160
}

func isBrowserCompatibilityRiskCodec(codec *string) bool {
	if codec == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(*codec))
	switch value {
	case "hevc", "h265", "h.265", "hev1", "hvc1", "av1", "av01", "vp9", "vp09":
		return true
	default:
		return false
	}
}

type ImageProfile struct {
	Kind      string
	Key       string
	Version   int
	MaxWidth  int
	MaxHeight int
	Quality   int
}

var defaultImageProfiles = []ImageProfile{
	{
		Kind: RenditionThumbnail, Key: "webp_320_fit",
		Version: 1, MaxWidth: 320, MaxHeight: 320, Quality: 78,
	},
	{
		Kind: RenditionScreenPreview, Key: "webp_2048_fit",
		Version: 1, MaxWidth: 2048, MaxHeight: 2048, Quality: 85,
	},
}
