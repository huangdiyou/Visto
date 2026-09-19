package httpapi

import (
	"testing"
	"time"

	"review-studio.local/core/internal/media"
)

func TestAssetVersionResponseIncludesProcessingStage(t *testing.T) {
	version := media.AssetVersion{
		ID:                  "version-1",
		AssetID:             "asset-1",
		VersionNumber:       1,
		ProcessingStatus:    "processing",
		SourceFilename:      "source.mp4",
		SourceSizeBytes:     64 << 20,
		SourceFingerprint:   "fingerprint",
		StorageObjectID:     "object-1",
		AuthorizedRootID:    "root-1",
		StorageObjectKey:    "media/source.mp4",
		StorageObjectStatus: "available",
		CreatedAt:           time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC),
		Probe: &media.Metadata{
			Status:    "succeeded",
			MediaType: "video",
		},
		Renditions: []media.Rendition{
			{
				ID: "poster-1", Kind: media.RenditionPoster,
				ProfileKey: "webp_poster_1280", Status: "ready",
			},
			{
				ID: "proxy-1", Kind: media.RenditionProxy,
				ProfileKey: "h264_aac_720p", Status: "ready",
			},
		},
	}

	response := toAssetVersionResponse(version)
	if response.ProcessingStage != media.ProcessingStageOptimizing {
		t.Fatalf("processing stage = %q, want %q",
			response.ProcessingStage,
			media.ProcessingStageOptimizing)
	}
}
