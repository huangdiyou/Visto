package media

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/storage"
)

func TestProbeRootPersistsAndInvalidatesByFingerprint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, workspaceID := mediaTestDatabase(t, ctx)
	localPath := t.TempDir()
	objectPath := filepath.Join(localPath, "poster.png")
	if err := os.WriteFile(objectPath, []byte("version-one"), 0o600); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}

	storageService := storage.NewService(storage.NewSQLiteRepository(db))
	root, err := storageService.RegisterLocalRoot(
		ctx,
		storage.RegisterLocalRootInput{
			WorkspaceID: workspaceID,
			DisplayName: "素材",
			LocalPath:   localPath,
			Mode:        "referenced",
			ScanEnabled: true,
		},
	)
	if err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := storageService.ScanRoot(ctx, workspaceID, root.ID); err != nil {
		t.Fatalf("scan root: %v", err)
	}

	prober := &countingProber{}
	service := NewService(NewSQLiteRepository(db), storageService, prober)
	first, err := service.ProbeRoot(ctx, workspaceID, root.ID)
	if err != nil {
		t.Fatalf("first probe: %v", err)
	}
	if first.Attempted != 1 || first.Succeeded != 1 || prober.calls != 1 {
		t.Fatalf("unexpected first batch: %#v calls=%d", first, prober.calls)
	}

	second, err := service.ProbeRoot(ctx, workspaceID, root.ID)
	if err != nil {
		t.Fatalf("cached probe: %v", err)
	}
	if second.Attempted != 0 || second.Skipped != 1 || prober.calls != 1 {
		t.Fatalf("probe was not cached: %#v calls=%d", second, prober.calls)
	}

	if err := os.WriteFile(objectPath, []byte("version-two"), 0o600); err != nil {
		t.Fatalf("modify media fixture: %v", err)
	}
	if _, err := storageService.ScanRoot(ctx, workspaceID, root.ID); err != nil {
		t.Fatalf("rescan root: %v", err)
	}
	third, err := service.ProbeRoot(ctx, workspaceID, root.ID)
	if err != nil {
		t.Fatalf("reprobe modified file: %v", err)
	}
	if third.Attempted != 1 || third.Succeeded != 1 || prober.calls != 2 {
		t.Fatalf("modified file was not reprobed: %#v calls=%d", third, prober.calls)
	}

	items, err := service.ListRoot(ctx, workspaceID, root.ID)
	if err != nil {
		t.Fatalf("list probes: %v", err)
	}
	if len(items) != 1 || items[0].MediaType != "image" ||
		value(items[0].Width) != 640 || value(items[0].Height) != 360 {
		t.Fatalf("unexpected persisted metadata: %#v", items)
	}
}

func TestFFProberRecognizesCommonFormats(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}

	tests := []struct {
		name      string
		extension string
		mimeType  string
		mediaType string
		video     bool
	}{
		{name: "JPEG", extension: ".jpg", mimeType: "image/jpeg", mediaType: "image"},
		{name: "PNG", extension: ".png", mimeType: "image/png", mediaType: "image"},
		{name: "WebP", extension: ".webp", mimeType: "image/webp", mediaType: "image"},
		{name: "MP4", extension: ".mp4", mimeType: "video/mp4", mediaType: "video", video: true},
		{name: "MOV", extension: ".mov", mimeType: "video/quicktime", mediaType: "video", video: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture"+test.extension)
			arguments := []string{
				"-v", "error",
				"-f", "lavfi",
				"-i", "color=c=red:s=64x48:d=0.5",
			}
			if test.video {
				arguments = append(
					arguments,
					"-c:v", "mpeg4",
					"-movflags", "+faststart",
					"-an",
				)
			} else {
				arguments = append(arguments, "-frames:v", "1")
			}
			arguments = append(arguments, "-y", path)
			output, err := exec.Command("ffmpeg", arguments...).CombinedOutput()
			if err != nil {
				t.Fatalf("generate %s fixture: %v: %s", test.name, err, output)
			}

			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s fixture: %v", test.name, err)
			}
			result, err := NewFFProber("ffprobe").Probe(
				context.Background(),
				bytes.NewReader(content),
				ProbeHint{
					ObjectKey: filepath.Base(path),
					MIMEType:  test.mimeType,
				},
			)
			if err != nil {
				t.Fatalf("probe %s: %v", test.name, err)
			}
			if result.MediaType != test.mediaType ||
				value(result.Width) != 64 ||
				value(result.Height) != 48 {
				t.Fatalf("unexpected %s result: %#v", test.name, result)
			}
			if test.video && (result.DurationUS == nil || *result.DurationUS <= 0) {
				t.Fatalf("%s duration was not detected: %#v", test.name, result)
			}
		})
	}
}

func TestProbeRootJobExecutorPersistsResultsAndProgress(t *testing.T) {
	ctx := context.Background()
	db, workspaceID := mediaTestDatabase(t, ctx)
	localPath := t.TempDir()
	for name, content := range map[string]string{
		"poster.png": "poster",
		"cover.jpg":  "cover",
	} {
		if err := os.WriteFile(
			filepath.Join(localPath, name),
			[]byte(content),
			0o600,
		); err != nil {
			t.Fatalf("write %s fixture: %v", name, err)
		}
	}

	storageService := storage.NewService(storage.NewSQLiteRepository(db))
	root, err := storageService.RegisterLocalRoot(
		ctx,
		storage.RegisterLocalRootInput{
			WorkspaceID: workspaceID,
			DisplayName: "任务素材",
			LocalPath:   localPath,
			Mode:        "referenced",
			ScanEnabled: true,
		},
	)
	if err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := storageService.ScanRoot(ctx, workspaceID, root.ID); err != nil {
		t.Fatalf("scan root: %v", err)
	}

	mediaService := NewService(
		NewSQLiteRepository(db),
		storageService,
		&countingProber{},
	)
	jobService := job.NewService(job.NewSQLiteRepository(db))
	worker := job.NewWorker(jobService, job.WorkerConfig{
		NodeID:          "media-test-worker",
		NodeName:        "Media Test Worker",
		SoftwareVersion: "test",
		PollInterval:    10 * time.Millisecond,
		LeaseDuration:   300 * time.Millisecond,
		Executors: map[string]job.Executor{
			ProbeRootJobType: ProbeRootJobExecutor{Service: mediaService},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	workerContext, cancelWorker := context.WithCancel(ctx)
	if err := worker.Start(workerContext); err != nil {
		t.Fatalf("start media worker: %v", err)
	}
	defer func() {
		cancelWorker()
		stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := worker.Stop(stopContext); err != nil {
			t.Errorf("stop media worker: %v", err)
		}
	}()

	payload, err := json.Marshal(ProbeRootJobPayload{RootID: root.ID})
	if err != nil {
		t.Fatalf("encode probe payload: %v", err)
	}
	item, _, err := jobService.Create(ctx, job.CreateInput{
		WorkspaceID: workspaceID,
		Type:        ProbeRootJobType,
		SubjectType: "authorizedRoot",
		SubjectID:   root.ID,
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("create probe job: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := jobService.Get(ctx, workspaceID, item.ID)
		if getErr != nil {
			t.Fatalf("get probe job: %v", getErr)
		}
		if current.Status == job.StatusSucceeded {
			if current.Progress == nil ||
				current.Progress.Current != 2 ||
				current.Progress.Total != 2 {
				t.Fatalf("unexpected probe progress: %#v", current.Progress)
			}
			probes, listErr := mediaService.ListRoot(ctx, workspaceID, root.ID)
			if listErr != nil {
				t.Fatalf("list job probes: %v", listErr)
			}
			if len(probes) != 2 {
				t.Fatalf("expected two probe results, got %#v", probes)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("media probe job did not complete")
}

type countingProber struct {
	calls int
}

func (prober *countingProber) Probe(
	_ context.Context,
	reader io.Reader,
	_ ProbeHint,
) (ProbeResult, error) {
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return ProbeResult{}, err
	}
	prober.calls++
	return ProbeResult{
		MediaType:       "image",
		Width:           pointer(640),
		Height:          pointer(360),
		VideoCodec:      pointer("png"),
		RawMetadataJSON: `{"fixture":true}`,
	}, nil
}

func mediaTestDatabase(
	t *testing.T,
	ctx context.Context,
) (*sql.DB, string) {
	t.Helper()

	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	})

	result, err := identity.NewService(identity.NewSQLiteRepository(db)).Setup(
		ctx,
		identity.SetupInput{
			WorkspaceName: "Studio",
			OwnerName:     "Owner",
			Password:      "local-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	return db, result.Session.Workspace.ID
}
