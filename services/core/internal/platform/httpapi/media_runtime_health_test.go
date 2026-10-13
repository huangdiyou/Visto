package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"review-studio.local/core/internal/job"
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

func TestMediaAvailabilityRechecksResolvedExecutable(t *testing.T) {
	config := testConfig(t)
	path := filepath.Join(t.TempDir(), "component.exe")
	config.FFmpegCommand, config.FFprobeCommand = path, path
	handler := NewHandler(config)
	check := func(want bool) {
		t.Helper()
		response := performJSONRequest(handler, http.MethodGet, "/health/ready", "", nil)
		var body struct {
			Dependencies map[string]bool `json:"dependencies"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Dependencies["ffmpeg"] != want || body.Dependencies["ffprobe"] != want {
			t.Fatalf("stale availability: %s", response.Body.String())
		}
	}
	check(false)
	if err := os.WriteFile(path, []byte("availability fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestRuntimeRepairGuardsRetryWithoutChangingJob(t *testing.T) {
	config := testConfig(t)
	config.FFmpegCommand, config.FFprobeCommand = filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "missing")
	handler := NewHandler(config)
	cookie, workspaceID := setupInstance(t, handler)
	ctx := context.Background()
	if err := config.Jobs.EnsureNode(ctx, job.Node{ID: "node-runtime", Name: "fixture", Kind: "embedded", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	item, _, err := config.Jobs.Create(ctx, job.CreateInput{WorkspaceID: workspaceID, Type: "media.process_asset_version", SubjectType: "storageObject", SubjectID: "fixture", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, claimed, err := config.Jobs.Claim(ctx, "node-runtime", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := config.Jobs.Start(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := config.Jobs.Fail(ctx, lease, job.Error{Code: "probe.unavailable", Message: "ffprobe is unavailable"}); err != nil {
		t.Fatal(err)
	}
	target := "/api/v1/jobs/" + item.ID + "/retry"
	blocked := performJSONRequest(handler, http.MethodPost, target, "", cookie)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "media.runtime_unavailable") {
		t.Fatalf("retry not blocked: %d %s", blocked.Code, blocked.Body.String())
	}
	unchanged, err := config.Jobs.Get(ctx, workspaceID, item.ID)
	if err != nil || unchanged.Status != job.StatusFailed || unchanged.AttemptCount != 1 {
		t.Fatalf("job changed: %#v %v", unchanged, err)
	}
	// A nonexistent or foreign job must not disclose any runtime information.
	missing := performJSONRequest(handler, http.MethodPost, "/api/v1/jobs/foreign-job/retry", "", cookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("foreign job: %d", missing.Code)
	}
	if denied := performJSONRequest(handler, http.MethodPost, target, "", nil); denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous retry: %d", denied.Code)
	}
	// Other media failures may use processors unrelated to FFmpeg (for example PDF).
	other, _, err := config.Jobs.Create(ctx, job.CreateInput{WorkspaceID: workspaceID, Type: "media.process_asset_version", SubjectType: "storageObject", SubjectID: "other-fixture", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	otherLease, claimed, err := config.Jobs.Claim(ctx, "node-runtime", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim unrelated failure: %v %v", claimed, err)
	}
	if err := config.Jobs.Start(ctx, otherLease); err != nil {
		t.Fatal(err)
	}
	if err := config.Jobs.Fail(ctx, otherLease, job.Error{Code: "job.execution_failed", Message: "temporary PDF processor error"}); err != nil {
		t.Fatal(err)
	}
	if response := performJSONRequest(handler, http.MethodPost, "/api/v1/jobs/"+other.ID+"/retry", "", cookie); response.Code != http.StatusOK {
		t.Fatalf("unrelated media retry blocked: %d %s", response.Code, response.Body.String())
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config.FFmpegCommand, config.FFprobeCommand = executable, executable
	repaired := NewHandler(config)
	response := performJSONRequest(repaired, http.MethodPost, target, "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("repaired retry: %d %s", response.Code, response.Body.String())
	}
}
