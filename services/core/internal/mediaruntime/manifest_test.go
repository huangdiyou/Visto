package mediaruntime

import "testing"

// The runtime manifest may point at either the versioned GitHub Release shape or
// the Visto Cloudflare Pages shape. Both must reject the same unsafe forms:
// rolling versions, HTTPS downgrades, credentials, queries, and traversal.
func TestPayloadURLAcceptsOnlyVersionedTrustedShapes(t *testing.T) {
	accepted := []string{
		"https://github.com/huangdiyou/Visto/releases/download/media-runtime-v1/ffmpeg.tar.xz",
		"https://visto-server-updates.pages.dev/media-runtime/ffmpeg-8.1.2-linux-amd64-lgpl/Visto-Media-Runtime_ffmpeg-8.1.2-linux-amd64-lgpl_linux-amd64.tar.xz",
		"https://visto-server-updates.pages.dev/media-runtime/ffmpeg-8.1.2-windows-amd64-lgpl/Visto-Media-Runtime_ffmpeg-8.1.2-windows-amd64-lgpl_windows-amd64.zip",
		"https://dl.819101.xyz/media-runtime/ffmpeg-8.1.2-macos-lgpl.1/Visto-Media-Runtime_ffmpeg-8.1.2-macos-lgpl.1_macos-arm64.tar.xz",
		"https://dl.819101.xyz/media-runtime/ffmpeg-8.1.2-windows-amd64-lgpl/Visto-Media-Runtime_ffmpeg-8.1.2-windows-amd64-lgpl_windows-amd64.zip",
	}
	for _, value := range accepted {
		if err := payloadURL(value, false); err != nil {
			t.Errorf("payloadURL(%q) = %v, want nil", value, err)
		}
	}

	rejected := []struct {
		name  string
		value string
	}{
		{"github latest tag", "https://github.com/a/b/releases/download/latest/runtime.zip"},
		{"github latest substring", "https://github.com/a/b/releases/download/latest-build/runtime.zip"},
		{"github short path", "https://github.com/a/b/releases/download/runtime.zip"},
		{"pages rolling version", "https://visto-server-updates.pages.dev/media-runtime/latest/runtime.tar.xz"},
		{"pages rolling substring", "https://visto-server-updates.pages.dev/media-runtime/latest-linux/runtime.tar.xz"},
		{"pages traversal", "https://visto-server-updates.pages.dev/media-runtime/../secret/runtime.tar.xz"},
		{"pages missing archive", "https://visto-server-updates.pages.dev/media-runtime/ffmpeg-8.1.2-linux-amd64-lgpl"},
		{"r2 rolling version", "https://dl.819101.xyz/media-runtime/latest/runtime.tar.xz"},
		{"r2 traversal", "https://dl.819101.xyz/media-runtime/../secret/runtime.tar.xz"},
		{"r2 extra segment", "https://dl.819101.xyz/media-runtime/v1/extra/runtime.tar.xz"},
		{"r2 with query", "https://dl.819101.xyz/media-runtime/v1/runtime.tar.xz?token=x"},
		{"r2 lookalike host", "https://dl.819101.xyz.evil.example/media-runtime/v1/runtime.tar.xz"},
		{"r2 plain http", "http://dl.819101.xyz/media-runtime/v1/runtime.tar.xz"},
		{"pages extra segment", "https://visto-server-updates.pages.dev/media-runtime/v1/extra/runtime.tar.xz"},
		{"pages wrong root", "https://visto-server-updates.pages.dev/other/v1/runtime.tar.xz"},
		{"pages with query", "https://visto-server-updates.pages.dev/media-runtime/v1/runtime.tar.xz?token=x"},
		{"pages http downgrade", "http://visto-server-updates.pages.dev/media-runtime/v1/runtime.tar.xz"},
		{"pages credentials", "https://user:pass@visto-server-updates.pages.dev/media-runtime/v1/runtime.tar.xz"},
		{"pages with port", "https://visto-server-updates.pages.dev:8443/media-runtime/v1/runtime.tar.xz"},
		{"pages with fragment", "https://visto-server-updates.pages.dev/media-runtime/v1/runtime.tar.xz#x"},
		{"untrusted host", "https://example.com/media-runtime/v1/runtime.tar.xz"},
		{"pages lookalike host", "https://visto-server-updates.pages.dev.evil.example/media-runtime/v1/runtime.tar.xz"},
		{"private address", "https://127.0.0.1/media-runtime/v1/runtime.tar.xz"},
	}
	for _, tc := range rejected {
		if err := payloadURL(tc.value, false); err == nil {
			t.Errorf("payloadURL(%q) for %s = nil, want an error", tc.value, tc.name)
		}
	}
}

// A redirect away from the Pages host to the GitHub asset CDN is the only
// redirect shape that is allowed, and only when the caller says it is a redirect.
func TestPayloadURLRedirectHandling(t *testing.T) {
	asset := "https://release-assets.githubusercontent.com/github-production-release-asset/1/2"
	if err := payloadURL(asset, true); err != nil {
		t.Errorf("redirect to the release asset CDN = %v, want nil", err)
	}
	if err := payloadURL(asset, false); err == nil {
		t.Error("the release asset CDN must not be a valid initial payload URL")
	}
}
