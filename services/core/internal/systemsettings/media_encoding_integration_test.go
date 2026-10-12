package systemsettings

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
)

func mediaEncodingService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, time.September, 24, 8, 0, 0, 0, time.UTC)
	}
	return service, ctx
}

// An instance upgraded from before this setting existed must keep encoding the
// way it already did. Every default is therefore "nothing chosen, nothing
// probed" and no encoder is forced.
func TestMediaEncodingDefaultsToNothingChosen(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	item, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}
	if item.PreferredEncoder != "" || item.ActiveEncoder != "" {
		t.Fatalf("expected no encoder to be forced, got %#v", item)
	}
	if len(item.DetectedEncoders) != 0 || item.DetectedAt != nil {
		t.Fatalf("expected no probe result, got %#v", item)
	}
	if item.FailureCount != 0 || item.TrippedEncoder != nil || item.Revision != 1 {
		t.Fatalf("unexpected default media encoding settings: %#v", item)
	}
}

func TestMediaEncodingStoresOwnerChoiceAndKeepsBothSides(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	initial, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}

	updated, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "libopenh264",
		Revision:         initial.Revision,
		UpdatedBy:        "owner-1",
	})
	if err != nil {
		t.Fatalf("update media encoding settings: %v", err)
	}
	if updated.Current.PreferredEncoder != "libopenh264" ||
		updated.Current.Revision != initial.Revision+1 {
		t.Fatalf("unexpected current settings: %#v", updated.Current)
	}
	// Both sides are returned so the caller can record the change.
	if updated.Previous.PreferredEncoder != "" {
		t.Fatalf("unexpected previous settings: %#v", updated.Previous)
	}
	if updated.Current.UpdatedBy == nil || *updated.Current.UpdatedBy != "owner-1" {
		t.Fatalf("actor was not recorded: %#v", updated.Current.UpdatedBy)
	}
}

// Clearing the choice is how an Owner returns to "follow the probe", so the empty
// value must be accepted rather than treated as a missing field.
func TestMediaEncodingAcceptsClearingTheChoice(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	initial, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}
	if _, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "h264_videotoolbox",
		Revision:         initial.Revision,
		UpdatedBy:        "owner-1",
	}); err != nil {
		t.Fatalf("set encoder: %v", err)
	}
	current, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("re-read media encoding settings: %v", err)
	}
	cleared, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "",
		Revision:         current.Revision,
		UpdatedBy:        "owner-1",
	})
	if err != nil {
		t.Fatalf("clear encoder: %v", err)
	}
	if cleared.Current.PreferredEncoder != "" {
		t.Fatalf("choice was not cleared: %#v", cleared.Current)
	}
}

func TestMediaEncodingRefusesAnUnknownEncoder(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	initial, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}
	_, err = service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "h264_not_a_real_encoder",
		Revision:         initial.Revision,
		UpdatedBy:        "owner-1",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown encoder error = %v, want ErrInvalidInput", err)
	}
}

func TestMediaEncodingRequiresAnActor(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	initial, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}
	if _, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "libx264",
		Revision:         initial.Revision,
		UpdatedBy:        "   ",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing actor error = %v, want ErrInvalidInput", err)
	}
}

func TestMediaEncodingRejectsStaleRevision(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	initial, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}
	if _, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "libx264",
		Revision:         initial.Revision,
		UpdatedBy:        "owner-1",
	}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	_, err = service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "libopenh264",
		Revision:         initial.Revision,
		UpdatedBy:        "owner-2",
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v, want ErrRevisionConflict", err)
	}
}

// A stored list that is not a JSON array is a storage fault. Flattening it into
// "no encoders detected" would silently look like a clean probe result.
func TestMediaEncodingReportsAMalformedStoredList(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	db := service.repository.(*SQLiteRepository).db
	if _, err := db.ExecContext(ctx,
		"UPDATE system_media_encoding_settings SET detected_encoders = ? WHERE id = 1",
		"not json"); err != nil {
		t.Fatalf("seed malformed list: %v", err)
	}
	if _, err := service.GetMediaEncoding(ctx); err == nil {
		t.Fatal("a malformed stored list must be reported")
	}
}

func TestMediaEncodingRuntimeRecordsTheBreakerState(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	trippedAt := time.Date(2026, time.September, 24, 9, 30, 0, 0, time.UTC)
	updated, err := service.RecordMediaEncodingRuntime(ctx, RecordMediaEncodingRuntimeInput{
		ActiveEncoder:  "libopenh264",
		FailureCount:   0,
		TrippedEncoder: "h264_videotoolbox",
		TrippedReason:  "h264_videotoolbox failed 3 times in a row: no device",
		TrippedAt:      &trippedAt,
		UpdatedBy:      "encoder-job",
	})
	if err != nil {
		t.Fatalf("record runtime state: %v", err)
	}
	current := updated.Current
	if current.ActiveEncoder != "libopenh264" {
		t.Fatalf("active encoder = %q", current.ActiveEncoder)
	}
	if current.TrippedEncoder == nil || *current.TrippedEncoder != "h264_videotoolbox" {
		t.Fatalf("tripped encoder = %#v", current.TrippedEncoder)
	}
	if current.TrippedAt == nil || !current.TrippedAt.Equal(trippedAt) {
		t.Fatalf("tripped at = %#v", current.TrippedAt)
	}
	// Re-reading must return the same state, otherwise a restart would forget the
	// trip and repeat the failure cycle.
	reread, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.ActiveEncoder != "libopenh264" || reread.TrippedReason == nil {
		t.Fatalf("runtime state was not persisted: %#v", reread)
	}
}

// The Owner's revision guard must survive a background write, otherwise the next
// Owner save fails with a conflict the Owner cannot explain.
func TestMediaEncodingRuntimeLeavesTheOwnerRevisionAlone(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	initial, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("get media encoding settings: %v", err)
	}
	afterOwner, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "libopenh264",
		Revision:         initial.Revision,
		UpdatedBy:        "owner-1",
	})
	if err != nil {
		t.Fatalf("owner update: %v", err)
	}

	if _, err := service.RecordMediaEncodingRuntime(ctx, RecordMediaEncodingRuntimeInput{
		ActiveEncoder: "libopenh264",
		FailureCount:  2,
		UpdatedBy:     "encoder-job",
	}); err != nil {
		t.Fatalf("record runtime state: %v", err)
	}
	reread, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.Revision != afterOwner.Current.Revision {
		t.Fatalf("runtime write bumped the owner revision to %d, want %d",
			reread.Revision, afterOwner.Current.Revision)
	}
	if reread.FailureCount != 2 {
		t.Fatalf("failure count = %d, want 2", reread.FailureCount)
	}
	// The Owner can still save against the revision they last read.
	if _, err := service.UpdateMediaEncoding(ctx, UpdateMediaEncodingInput{
		PreferredEncoder: "libx264",
		Revision:         reread.Revision,
		UpdatedBy:        "owner-1",
	}); err != nil {
		t.Fatalf("owner update after a runtime write: %v", err)
	}
}

func TestMediaEncodingRuntimeRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	cases := map[string]RecordMediaEncodingRuntimeInput{
		"unknown active encoder":  {ActiveEncoder: "h264_bogus", UpdatedBy: "encoder-job"},
		"unknown tripped encoder": {TrippedEncoder: "h264_bogus", UpdatedBy: "encoder-job"},
		"negative failure count":  {ActiveEncoder: "libx264", FailureCount: -1, UpdatedBy: "encoder-job"},
		"missing actor":           {ActiveEncoder: "libx264", UpdatedBy: "  "},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := service.RecordMediaEncodingRuntime(ctx, input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestMediaEncodingRuntimeRecordsAProbeResult(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	if _, err := service.RecordMediaEncodingProbe(ctx, RecordMediaEncodingProbeInput{
		DetectedEncoders: []string{"h264_videotoolbox", "libopenh264"},
		UpdatedBy:        "encoder-job",
	}); err != nil {
		t.Fatalf("record probe: %v", err)
	}
	item, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(item.DetectedEncoders) != 2 || item.DetectedEncoders[0] != "h264_videotoolbox" {
		t.Fatalf("detected encoders = %v", item.DetectedEncoders)
	}
	if item.DetectedAt == nil {
		t.Fatal("a probe result must record when it ran")
	}
}

// A breaker write must not erase what the probe found: the probe and the breaker
// update the same row but own different columns.
func TestMediaEncodingRuntimePreservesTheProbeResult(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	if _, err := service.RecordMediaEncodingProbe(ctx, RecordMediaEncodingProbeInput{
		DetectedEncoders: []string{"h264_qsv", "libopenh264"},
		UpdatedBy:        "encoder-job",
	}); err != nil {
		t.Fatalf("record probe: %v", err)
	}
	if _, err := service.RecordMediaEncodingRuntime(ctx, RecordMediaEncodingRuntimeInput{
		ActiveEncoder: "libopenh264",
		FailureCount:  1,
		UpdatedBy:     "encoder-job",
	}); err != nil {
		t.Fatalf("record breaker update: %v", err)
	}
	item, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(item.DetectedEncoders) != 2 {
		t.Fatalf("breaker write erased the probe result: %v", item.DetectedEncoders)
	}
	if item.FailureCount != 1 || item.ActiveEncoder != "libopenh264" {
		t.Fatalf("breaker state = %#v", item)
	}
}

// The reverse must hold too: a sweep reports which encoders exist and knows
// nothing about which one is in use, so it must not clear the runtime choice or a
// trip. This is the bug the separate probe write exists to prevent.
func TestMediaEncodingProbePreservesTheRuntimeChoice(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	if _, err := service.RecordMediaEncodingRuntime(ctx, RecordMediaEncodingRuntimeInput{
		ActiveEncoder:  "libopenh264",
		TrippedEncoder: "h264_qsv",
		TrippedReason:  "h264_qsv failed 3 times in a row",
		UpdatedBy:      "encoder-job",
	}); err != nil {
		t.Fatalf("record breaker state: %v", err)
	}
	if _, err := service.RecordMediaEncodingProbe(ctx, RecordMediaEncodingProbeInput{
		DetectedEncoders: []string{"h264_videotoolbox", "libopenh264"},
		UpdatedBy:        "encoder-job",
	}); err != nil {
		t.Fatalf("record probe: %v", err)
	}
	item, err := service.GetMediaEncoding(ctx)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if item.ActiveEncoder != "libopenh264" {
		t.Fatalf("active encoder = %q, want the runtime choice untouched", item.ActiveEncoder)
	}
	if item.TrippedEncoder == nil || *item.TrippedEncoder != "h264_qsv" {
		t.Fatalf("tripped encoder = %v, want the trip untouched", item.TrippedEncoder)
	}
	if len(item.DetectedEncoders) != 2 {
		t.Fatalf("detected encoders = %v, want the sweep stored", item.DetectedEncoders)
	}
}

func TestMediaEncodingProbeRequiresAnActor(t *testing.T) {
	t.Parallel()

	service, ctx := mediaEncodingService(t)
	if _, err := service.RecordMediaEncodingProbe(ctx, RecordMediaEncodingProbeInput{
		DetectedEncoders: []string{"libopenh264"},
		UpdatedBy:        "  ",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}
