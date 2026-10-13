package encoderselection

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"review-studio.local/core/internal/mediaruntime"
	"review-studio.local/core/internal/systemsettings"
)

type fakeSettings struct {
	item      systemsettings.MediaEncodingSettings
	recorded  []systemsettings.RecordMediaEncodingProbeInput
	readErr   error
	recordErr error
	// onProbe runs inside the probe write, which is how a test models a job that
	// trips the breaker while a sweep is in flight.
	onProbe func()
}

func (settings *fakeSettings) GetMediaEncoding(
	context.Context,
) (systemsettings.MediaEncodingSettings, error) {
	if settings.readErr != nil {
		return systemsettings.MediaEncodingSettings{}, settings.readErr
	}
	return settings.item, nil
}

func (settings *fakeSettings) RecordMediaEncodingProbe(
	_ context.Context,
	input systemsettings.RecordMediaEncodingProbeInput,
) (systemsettings.MediaEncodingUpdate, error) {
	if settings.recordErr != nil {
		return systemsettings.MediaEncodingUpdate{}, settings.recordErr
	}
	previous := settings.item
	settings.recorded = append(settings.recorded, input)
	settings.item.DetectedEncoders = input.DetectedEncoders
	detectedAt := time.Now().UTC()
	settings.item.DetectedAt = &detectedAt
	if settings.onProbe != nil {
		settings.onProbe()
	}
	return systemsettings.MediaEncodingUpdate{Previous: previous, Current: settings.item}, nil
}

type fakeApplier struct {
	applied  []string
	rejected map[string]bool
}

func (applier *fakeApplier) apply(_ context.Context, encoder string) bool {
	applier.applied = append(applier.applied, encoder)
	return !applier.rejected[encoder]
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newService(t *testing.T, settings *fakeSettings, applier *fakeApplier, probe Probe) *Service {
	t.Helper()
	return New(Config{
		Settings: settings,
		Apply:    applier.apply,
		Restore:  applier.apply,
		Probe:    probe,
		Logger:   quietLogger(),
	})
}

func probedAt() time.Time { return time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC) }

func TestRestoreKeepsTheConfiguredEncoderWhenNothingWasDecided(t *testing.T) {
	settings := &fakeSettings{}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, nil)

	if err := service.Restore(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want nothing", applier.applied)
	}
}

// The reported effective encoder and the encoder the processor runs must survive
// a restart together, so the runtime choice is what gets re-applied.
func TestRestorePrefersTheRuntimeChoice(t *testing.T) {
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		PreferredEncoder: "libx264",
		ActiveEncoder:    "libopenh264",
	}}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, nil)

	if err := service.Restore(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "libopenh264" {
		t.Fatalf("applied = %v, want [libopenh264]", applier.applied)
	}
}

// An instance that stored a choice before the runtime choice existed still has to
// come back on the encoder the Owner asked for.
func TestRestoreFallsBackToTheOwnerChoice(t *testing.T) {
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		PreferredEncoder: "libx264",
	}}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, nil)

	if err := service.Restore(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "libx264" {
		t.Fatalf("applied = %v, want [libx264]", applier.applied)
	}
}

func TestRestoreReportsAnUnreadableRow(t *testing.T) {
	settings := &fakeSettings{readErr: errors.New("database is down")}
	service := newService(t, settings, &fakeApplier{}, nil)

	if err := service.Restore(context.Background()); err == nil {
		t.Fatal("an unreadable settings row must be reported so the caller can warn")
	}
}

func TestSweepStoresTheSweepAndAppliesTheRecommendation(t *testing.T) {
	settings := &fakeSettings{}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Platform:    "linux",
			Available:   []string{"h264_nvenc", "libx264"},
			Recommended: "h264_nvenc",
			ProbedAt:    probedAt(),
		}, nil
	})

	result, err := service.Sweep(context.Background(), SystemActor)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Recommended != "h264_nvenc" {
		t.Fatalf("result = %#v, want the sweep passed back to the caller", result)
	}
	if len(settings.recorded) != 1 {
		t.Fatalf("recorded %d runtime writes, want the sweep stored once", len(settings.recorded))
	}
	stored := settings.recorded[0]
	if len(stored.DetectedEncoders) != 2 {
		t.Fatalf("stored detected = %v, want the sweep's list", stored.DetectedEncoders)
	}
	if stored.UpdatedBy != SystemActor {
		t.Fatalf("updated by %q, want the system actor", stored.UpdatedBy)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "h264_nvenc" {
		t.Fatalf("applied = %v, want the recommendation", applier.applied)
	}
}

// The Owner's explicit choice outranks the sweep: changing it must stay a
// deliberate act (3.2).
func TestSweepDoesNotOverrideTheOwnerChoice(t *testing.T) {
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		PreferredEncoder: "libx264",
		ActiveEncoder:    "libx264",
	}}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"h264_nvenc", "libx264"},
			Recommended: "h264_nvenc",
		}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want the Owner's choice untouched", applier.applied)
	}
}

// 3.3: a re-probe that finds the tripped encoder usable again is one of the two
// documented ways out of a trip.
func TestSweepUntripsAnEncoderThatWorksAgain(t *testing.T) {
	tripped := "h264_nvenc"
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		ActiveEncoder:  "libx264",
		TrippedEncoder: &tripped,
	}}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"h264_nvenc", "libx264"},
			Recommended: "h264_nvenc",
		}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "h264_nvenc" {
		t.Fatalf("applied = %v, want the recovered encoder taken back", applier.applied)
	}
}

// A sweep that still cannot see the failed encoder must not silently end the trip
// and send the next job back into the same failure.
func TestSweepKeepsTheTripWhileTheEncoderIsStillMissing(t *testing.T) {
	tripped := "h264_nvenc"
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		ActiveEncoder:  "libx264",
		TrippedEncoder: &tripped,
	}}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"libx264"},
			Recommended: "libx264",
		}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want the fallback left in place", applier.applied)
	}
}

// A sweep that finds nothing still has to be recorded: "probed, nothing usable"
// is what makes the pane fall back to software encoding.
func TestSweepRecordsAnEmptyResult(t *testing.T) {
	settings := &fakeSettings{}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{ProbedAt: probedAt()}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(settings.recorded) != 1 || settings.recorded[0].DetectedEncoders == nil {
		t.Fatalf("recorded = %#v, want an empty list stored", settings.recorded)
	}
	if len(settings.recorded[0].DetectedEncoders) != 0 {
		t.Fatalf("stored detected = %v, want empty", settings.recorded[0].DetectedEncoders)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want nothing when the sweep found nothing", applier.applied)
	}
}

// A sweep that could not run is not evidence that the previous answer was wrong.
func TestSweepWritesNothingWhenTheProbeFails(t *testing.T) {
	settings := &fakeSettings{}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{}, errors.New("ffmpeg is not installed")
	})

	if _, err := service.Sweep(context.Background(), "owner"); err == nil {
		t.Fatal("a failed sweep must be reported")
	}
	if len(settings.recorded) != 0 {
		t.Fatalf("recorded = %#v, want nothing written", settings.recorded)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want nothing applied", applier.applied)
	}
}

// The sweep already updated the row with its result; re-selecting the encoder
// that is already in use would be a second write for no change.
func TestSweepDoesNotRewriteAnUnchangedChoice(t *testing.T) {
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		ActiveEncoder: "h264_nvenc",
	}}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"h264_nvenc", "libx264"},
			Recommended: "h264_nvenc",
		}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want no second write", applier.applied)
	}
}

// Recommending a name this build cannot run must leave the previous choice in
// place rather than record something that cannot work.
func TestSweepIgnoresAnUnrecognisedRecommendation(t *testing.T) {
	settings := &fakeSettings{item: systemsettings.MediaEncodingSettings{
		ActiveEncoder: "libx264",
	}}
	applier := &fakeApplier{rejected: map[string]bool{"h264_mystery": true}}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"h264_mystery"},
			Recommended: "h264_mystery",
		}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err == nil {
		t.Fatal("a refused application must be reported")
	}
	if len(applier.applied) != 1 || applier.applied[0] != "h264_mystery" {
		t.Fatalf("applied = %v, want one refused attempt", applier.applied)
	}
	if len(settings.recorded) != 1 {
		t.Fatalf("recorded %d writes, want the sweep stored and no selection", len(settings.recorded))
	}
}

// A job can fail its way to a trip while a sweep is in flight. That decision is
// newer than the sweep's, so the sweep must stand down instead of clearing the
// trip and putting the next job back on the encoder that just failed.
func TestSweepStandsDownWhenAJobTripsDuringTheSweep(t *testing.T) {
	tripped := "h264_nvenc"
	settings := &fakeSettings{}
	settings.onProbe = func() {
		settings.item.TrippedEncoder = &tripped
		settings.item.ActiveEncoder = "libx264"
	}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"h264_nvenc", "libx264"},
			Recommended: "h264_nvenc",
		}, nil
	})

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want the trip to win", applier.applied)
	}
}

// The check fails closed: if the state cannot be re-read, the sweep must not
// override a decision it cannot see.
func TestSweepStandsDownWhenTheStateCannotBeReRead(t *testing.T) {
	settings := &fakeSettings{}
	applier := &fakeApplier{}
	service := newService(t, settings, applier, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{
			Available:   []string{"h264_nvenc"},
			Recommended: "h264_nvenc",
		}, nil
	})
	settings.readErr = errors.New("database is busy")

	if _, err := service.Sweep(context.Background(), "owner"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(applier.applied) != 0 {
		t.Fatalf("applied = %v, want the sweep to leave the choice alone", applier.applied)
	}
}

func TestProbeEnabledFollowsTheWiring(t *testing.T) {
	withoutProbe := newService(t, &fakeSettings{}, &fakeApplier{}, nil)
	if withoutProbe.ProbeEnabled() {
		t.Fatal("a build without a probe must report that it cannot sweep")
	}
	withProbe := newService(t, &fakeSettings{}, &fakeApplier{}, func(context.Context) (mediaruntime.EncoderProbeResult, error) {
		return mediaruntime.EncoderProbeResult{}, nil
	})
	if !withProbe.ProbeEnabled() {
		t.Fatal("a wired probe must report that it can sweep")
	}
}
