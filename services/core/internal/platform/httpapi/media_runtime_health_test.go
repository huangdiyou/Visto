package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaAvailabilityUsesSelectedExecutables(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A known executable is enough for availability; actual codec capability is
	// validated by mediaruntime. No media tool is discoverable on this PATH.
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		name, ffmpeg, ffprobe   string
		wantFFmpeg, wantFFprobe bool
	}{
		{"selected outside PATH", executable, executable, true, true},
		{"selected probe missing", executable, filepath.Join(t.TempDir(), "missing"), true, false},
		{"unselected without PATH tools", "", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := testConfig(t)
			config.FFmpegCommand, config.FFprobeCommand = tc.ffmpeg, tc.ffprobe
			handler := NewHandler(config)
			for _, endpoint := range []string{"/health/ready", "/api/v1/system/info"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, newRequest(http.MethodGet, endpoint, nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s: %d", endpoint, response.Code)
				}
				if strings.Contains(response.Body.String(), executable) {
					t.Fatal("executable path leaked")
				}
				var body struct {
					Dependencies map[string]bool `json:"dependencies"`
					Media        mediaInfo       `json:"media"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				gotFFmpeg, gotFFprobe := body.Media.FFmpegAvailable, body.Media.FFprobeAvailable
				if endpoint == "/health/ready" {
					gotFFmpeg, gotFFprobe = body.Dependencies["ffmpeg"], body.Dependencies["ffprobe"]
				}
				if gotFFmpeg != tc.wantFFmpeg || gotFFprobe != tc.wantFFprobe {
					t.Fatalf("%s: ffmpeg=%v ffprobe=%v", endpoint, gotFFmpeg, gotFFprobe)
				}
			}
		})
	}
}
