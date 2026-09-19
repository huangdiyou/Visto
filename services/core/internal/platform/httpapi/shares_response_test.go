package httpapi

import (
	"testing"

	"review-studio.local/core/internal/media"
	sharedomain "review-studio.local/core/internal/share"
)

func TestPublicShareResponseUsesHLSPreviewURL(t *testing.T) {
	previewID := "rendition-hls-1"
	previewKind := media.RenditionHLS
	response := toPublicShareResponse(sharedomain.PublicShare{
		Name:          "Share",
		ReviewName:    "Review",
		ReviewStatus:  "open",
		AllowComment:  true,
		AllowDownload: false,
		Visitor: sharedomain.PublicVisitor{
			IdentityMethod: "anonymous",
		},
		Items: []sharedomain.PublicItem{
			{
				ID:                   "item-1",
				AssetName:            "cut.mp4",
				VersionNumber:        1,
				MediaType:            "video",
				PreviewRenditionID:   &previewID,
				PreviewRenditionKind: &previewKind,
			},
		},
	})

	if len(response.Items) != 1 {
		t.Fatalf("item count = %d, want 1", len(response.Items))
	}
	item := response.Items[0]
	if item.PreviewKind == nil || *item.PreviewKind != media.RenditionHLS {
		t.Fatalf("preview kind = %#v, want hls", item.PreviewKind)
	}
	if item.PreviewURL == nil ||
		*item.PreviewURL != "/share-api/v1/items/item-1/hls/index.m3u8" {
		t.Fatalf("preview URL = %#v", item.PreviewURL)
	}
}
