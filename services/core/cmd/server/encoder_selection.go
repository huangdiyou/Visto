package main

import (
	"context"
	"log/slog"
	"os"

	"review-studio.local/core/internal/encoderselection"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/systemsettings"
)

// setupMediaEncoderSelection is shared by actual startup and the assembly regression.
func setupMediaEncoderSelection(settings *systemsettings.Service, videoProcessor *media.FFmpegVideoProcessor, runtimeEncoder string, probe encoderselection.Probe, logger *slog.Logger) (*encoderselection.Service, encoderselection.ApplyEncoderChoice) {
	// The breaker remembers across restarts which encoder kept failing, so the
	// Owner does not watch the same three failures again after every restart. If
	// the stored state cannot be read the breaker keeps no opinion, which is the
	// previous behaviour rather than a failed start.
	encoderBreaker := media.NewEncoderBreaker(
		encoderBreakerStore{settings: settings},
	)
	if err := encoderBreaker.Load(context.Background()); err != nil {
		logger.Warn(
			"media encoder breaker state is unavailable; encoder selection continues without it",
			"error", err,
		)
	}
	videoProcessor.SetEncoderBreaker(encoderBreaker)
	// Startup restores only the processor. Explicit edits and successful
	// re-probes also reset the breaker; a restart must retain failures and trips.
	restoreMediaEncoder := func(ctx context.Context, encoder string) bool {
		if encoder == "" {
			videoProcessor.SetAccelerationMode(os.Getenv("REVIEW_STUDIO_VIDEO_ACCELERATION"))
			videoProcessor.SetSoftwareEncoder(os.Getenv("REVIEW_STUDIO_SOFTWARE_VIDEO_ENCODER"))
			if runtimeEncoder != "" {
				videoProcessor.SetSoftwareEncoder(runtimeEncoder)
			}
		} else if !videoProcessor.ApplyEncoderChoice(encoder) {
			return false
		}
		return true
	}
	applyMediaEncoder := func(ctx context.Context, encoder string) bool {
		if !restoreMediaEncoder(ctx, encoder) {
			return false
		}
		if err := encoderBreaker.Reset(ctx, encoder); err != nil {
			logger.Warn("media encoder choice was not persisted", "error", err)
			return false
		}
		return true
	}
	encoderSelection := encoderselection.New(encoderselection.Config{
		Settings: settings,
		Apply:    applyMediaEncoder,
		Restore:  restoreMediaEncoder,
		Probe:    probe,
		Logger:   logger,
	})
	// Put the processor back on the encoder the runtime last settled on. Without
	// this a restart would run one encoder while the pane reported another, and
	// an instance that never decided anything keeps its configured encoder, which
	// is the upgraded behaviour the design requires.
	if err := encoderSelection.Restore(context.Background()); err != nil {
		logger.Warn("stored media encoding settings are unavailable; keeping the configured encoder",
			"error", err)
	}
	return encoderSelection, applyMediaEncoder
}
