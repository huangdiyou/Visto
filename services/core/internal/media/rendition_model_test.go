package media

import "testing"

func TestVideoProfilesPreferProxyForSmallCompatibleVideo(t *testing.T) {
	codec := "h264"
	width := 1920
	height := 1080
	profiles := videoProfilesForSource(64<<20, Metadata{
		MediaType:  "video",
		Width:      &width,
		Height:     &height,
		VideoCodec: &codec,
	})

	assertVideoProfileKinds(t, profiles, []string{
		RenditionPoster,
		RenditionProxy,
		RenditionStoryboard,
	})
}

func TestVideoProfilesPreferHLSForLargeOrRiskyVideo(t *testing.T) {
	hevc := "hevc"
	width := 1920
	height := 1080
	profiles := videoProfilesForSource(64<<20, Metadata{
		MediaType:  "video",
		Width:      &width,
		Height:     &height,
		VideoCodec: &hevc,
	})
	assertVideoProfileKinds(t, profiles, []string{
		RenditionPoster,
		RenditionHLS,
		RenditionStoryboard,
	})

	h264 := "h264"
	profiles = videoProfilesForSource(2<<30, Metadata{
		MediaType:  "video",
		Width:      &width,
		Height:     &height,
		VideoCodec: &h264,
	})
	assertVideoProfileKinds(t, profiles, []string{
		RenditionPoster,
		RenditionHLS,
		RenditionStoryboard,
	})

	uhdWidth := 3840
	uhdHeight := 2160
	profiles = videoProfilesForSource(64<<20, Metadata{
		MediaType:  "video",
		Width:      &uhdWidth,
		Height:     &uhdHeight,
		VideoCodec: &h264,
	})
	assertVideoProfileKinds(t, profiles, []string{
		RenditionPoster,
		RenditionHLS,
		RenditionStoryboard,
	})
}

func TestAssetVersionProcessingStageSplitsPreviewAndEnhancement(t *testing.T) {
	videoProbe := &Metadata{Status: "succeeded", MediaType: "video"}
	version := AssetVersion{
		ProcessingStatus: "processing",
		Probe:            videoProbe,
		SourceSizeBytes:  64 << 20,
		Renditions: []Rendition{
			{Kind: RenditionPoster, ProfileKey: "webp_poster_1280", Status: "ready"},
			{Kind: RenditionProxy, ProfileKey: "h264_aac_720p", Status: "ready"},
		},
	}
	if stage := AssetVersionProcessingStage(version); stage != ProcessingStageOptimizing {
		t.Fatalf("stage = %q, want %q", stage, ProcessingStageOptimizing)
	}

	version.ProcessingStatus = "failed"
	version.Renditions = append(version.Renditions, Rendition{
		Kind: RenditionStoryboard, ProfileKey: "webp_storyboard_20", Status: "failed",
	})
	if stage := AssetVersionProcessingStage(version); stage != ProcessingStageEnhancementFailed {
		t.Fatalf("stage = %q, want %q", stage, ProcessingStageEnhancementFailed)
	}

	version.ProcessingStatus = "ready"
	version.Renditions[len(version.Renditions)-1].Status = "ready"
	if stage := AssetVersionProcessingStage(version); stage != ProcessingStageComplete {
		t.Fatalf("stage = %q, want %q", stage, ProcessingStageComplete)
	}
}

func TestVideoProfileLayersSplitCoreAndEnhancementWork(t *testing.T) {
	codec := "h264"
	width := 1920
	height := 1080
	metadata := Metadata{
		MediaType: "video", Width: &width, Height: &height, VideoCodec: &codec,
	}
	assertVideoProfileKinds(t, videoCoreProfilesForSource(64<<20, metadata), []string{
		RenditionPoster,
		RenditionProxy,
	})
	assertVideoProfileKinds(t, videoEnhancementProfilesForSource(64<<20, metadata), []string{
		RenditionStoryboard,
	})
	assertVideoProfileKinds(t, videoProfilesForSource(64<<20, metadata), []string{
		RenditionPoster,
		RenditionProxy,
		RenditionStoryboard,
	})
}

func assertVideoProfileKinds(
	t *testing.T,
	profiles []VideoProfile,
	expected []string,
) {
	t.Helper()
	if len(profiles) != len(expected) {
		t.Fatalf("profile count = %d, want %d: %#v", len(profiles), len(expected), profiles)
	}
	for index, kind := range expected {
		if profiles[index].Kind != kind {
			t.Fatalf("profile %d kind = %q, want %q", index, profiles[index].Kind, kind)
		}
	}
}
