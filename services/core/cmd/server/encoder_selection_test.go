package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"review-studio.local/core/internal/encoderselection"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/mediaruntime"
	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/systemsettings"
)

// Exercise the actual main assembly: Load -> Restore used to immediately Reset.
func TestStartupAssemblyRetainsFailuresAndTrips(t *testing.T) {
	for _, tripped := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending-failures", true: "tripped"}[tripped], func(t *testing.T) {
			ctx := context.Background()
			db, err := database.Open(ctx, database.Config{Path: filepath.Join(t.TempDir(), "state.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			settings := systemsettings.NewService(systemsettings.NewSQLiteRepository(db))
			input := systemsettings.RecordMediaEncodingRuntimeInput{ActiveEncoder: "libopenh264", FailureCount: 2, UpdatedBy: "test"}
			if tripped {
				at := time.Now().UTC().Truncate(time.Second)
				input.FailureCount = 0
				input.TrippedEncoder = "h264_qsv"
				input.TrippedReason = "device failed"
				input.TrippedAt = &at
			}
			if _, err := settings.RecordMediaEncodingRuntime(ctx, input); err != nil {
				t.Fatal(err)
			}
			before, err := settings.GetMediaEncoding(ctx)
			if err != nil {
				t.Fatal(err)
			}
			processor := media.NewFFmpegVideoProcessor("ffmpeg", "software")
			selection, apply := setupMediaEncoderSelection(settings, processor, "", func(context.Context) (mediaruntime.EncoderProbeResult, error) {
				return mediaruntime.EncoderProbeResult{Available: []string{"h264_qsv", "libopenh264"}, Recommended: "h264_qsv"}, nil
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			after, err := settings.GetMediaEncoding(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("startup rewrote stored state: before=%+v after=%+v", before, after)
			}
			if _, err := selection.Sweep(ctx, encoderselection.SystemActor); err != nil {
				t.Fatal(err)
			}
			swept, err := settings.GetMediaEncoding(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if swept.FailureCount != before.FailureCount || !reflect.DeepEqual(swept.TrippedEncoder, before.TrippedEncoder) || swept.ActiveEncoder != before.ActiveEncoder {
				t.Fatalf("startup probe forgave failures: %+v", swept)
			}
			if !apply(ctx, "libx264") {
				t.Fatal("explicit choice failed")
			}
			reset, err := settings.GetMediaEncoding(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if reset.FailureCount != 0 || reset.TrippedEncoder != nil || reset.ActiveEncoder != "libx264" {
				t.Fatalf("explicit choice did not reset: %+v", reset)
			}
		})
	}
}
