// Package encoderselection settles which media encoder this instance uses.
//
// Three inputs decide it: the Owner's explicit choice, the last sweep of the
// installed runtime, and what the failure breaker remembers. They live together
// because the API, the startup path and the background probe have to reach the
// same answer — otherwise the encoder the Owner is shown and the encoder a job
// actually runs drift apart, which is what
// docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.2 and 3.3 forbid.
package encoderselection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"review-studio.local/core/internal/mediaruntime"
	"review-studio.local/core/internal/systemsettings"
)

// SystemActor names the automatic sweep in the activity record. It is not a
// person, and the Owner reading the row should be able to tell.
const SystemActor = "media-encoder-probe"

// ProbeTimeout bounds one sweep. The sweep runs a short idle encode per
// candidate, so this is generous rather than tight.
const ProbeTimeout = 5 * time.Minute

// Settings is the persisted half of the selection state.
type Settings interface {
	GetMediaEncoding(context.Context) (systemsettings.MediaEncodingSettings, error)
	RecordMediaEncodingProbe(
		context.Context,
		systemsettings.RecordMediaEncodingProbeInput,
	) (systemsettings.MediaEncodingUpdate, error)
}

// ApplyEncoderChoice pushes an encoder into the running processor and moves the
// persisted runtime choice with it, clearing any trip the breaker remembered. The
// empty string means "nothing decided": the deployment default is restored.
//
// It reports false when this build does not recognise the name, in which case
// nothing is changed and the previous choice stands.
type ApplyEncoderChoice func(ctx context.Context, encoder string) bool

// Probe runs one sweep of the installed runtime.
type Probe func(ctx context.Context) (mediaruntime.EncoderProbeResult, error)

type Config struct {
	Settings Settings
	Apply    ApplyEncoderChoice
	Restore  ApplyEncoderChoice
	Probe    Probe
	Logger   *slog.Logger
}

// Service is the only place that decides which encoder is active. Callers hand
// it a sweep or an Owner edit and it settles the rest.
type Service struct {
	settings Settings
	apply    ApplyEncoderChoice
	restore  ApplyEncoderChoice
	probe    Probe
	logger   *slog.Logger
}

func New(config Config) *Service {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		settings: config.Settings,
		apply:    config.Apply,
		restore:  config.Restore,
		probe:    config.Probe,
		logger:   logger,
	}
}

// ProbeEnabled reports whether this build can run a sweep at all, so the HTTP
// layer can answer 503 instead of accepting a request it will never carry out.
func (service *Service) ProbeEnabled() bool {
	return service != nil && service.probe != nil && service.settings != nil
}

// Restore applies the encoder the runtime last settled on.
//
// Without it a restart would leave the processor on the deployment default while
// the settings row still reported the stored choice as effective, which is
// exactly the drift 3.2 is about. An instance that never decided anything keeps
// its configured encoder, as the upgrade rule in 4 requires.
func (service *Service) Restore(ctx context.Context) error {
	item, err := service.settings.GetMediaEncoding(ctx)
	if err != nil {
		return fmt.Errorf("read media encoding settings: %w", err)
	}
	choice := item.ActiveEncoder
	source := "runtime"
	if choice == "" {
		choice = item.PreferredEncoder
		source = "owner"
	}
	if choice == "" {
		return nil
	}
	if service.restore == nil || !service.restore(ctx, choice) {
		service.logger.Warn(
			"stored media encoder is not recognised; keeping the configured encoder",
			"encoder", choice,
		)
		return nil
	}
	// The restored choice is the one the pane reports, so it is worth a log line
	// an operator can check against the encoder a job actually runs.
	service.logger.Info("media encoder choice restored", "encoder", choice, "source", source)
	return nil
}

// Sweep runs one probe, stores what it found and settles what that means:
//
//   - an encoder the breaker tripped that the sweep finds usable again is taken
//     back into use (3.3: a re-probe is one of the two ways out of a trip);
//   - an Owner's explicit choice is never overridden, the sweep only reports;
//   - otherwise the sweep's recommendation becomes the encoder in use, which is
//     the "the probe decides the default" rule of 3.2.
//
// A sweep that could not run writes nothing: an unavailable runtime is not
// evidence that the last answer was wrong.
func (service *Service) Sweep(
	ctx context.Context,
	actorID string,
) (mediaruntime.EncoderProbeResult, error) {
	result, err := service.probe(ctx)
	if err != nil {
		return result, fmt.Errorf("probe H.264 encoders: %w", err)
	}
	available := result.Available
	if available == nil {
		available = []string{}
	}
	update, err := service.settings.RecordMediaEncodingProbe(
		ctx,
		systemsettings.RecordMediaEncodingProbeInput{
			DetectedEncoders: available,
			UpdatedBy:        actorID,
		},
	)
	if err != nil {
		return result, fmt.Errorf("store the encoder sweep: %w", err)
	}

	previous := update.Previous
	service.logger.Info(
		"media encoder probe completed",
		"detected", available,
		"recommended", result.Recommended,
		"actor", actorID,
	)
	// The automatic startup sweep is a diagnosis, not permission to forgive
	// failures. Only an explicit Owner re-probe can end persisted failures/trips.
	if actorID == SystemActor && (previous.TrippedEncoder != nil || previous.FailureCount > 0) {
		return result, nil
	}
	tripped := ""
	if previous.TrippedEncoder != nil {
		tripped = *previous.TrippedEncoder
	}
	switch {
	case tripped != "" && containsEncoder(available, tripped):
		return result, service.selectEncoder(ctx, previous, tripped)
	case tripped != "":
		// A trip is in force and the sweep still cannot see the encoder that
		// failed: the fallback stands and the trip is not ended. Recommending
		// the fallback is not evidence that the failure was a fluke.
		return result, nil
	case previous.PreferredEncoder != "":
		return result, nil
	case result.Recommended != "":
		if service.trippedWhileSweeping(ctx) {
			// A job failed its way to a trip while the sweep was running. That
			// decision is newer than the sweep's and must win, or the sweep
			// would clear the trip and send the next job back to a bad encoder.
			return result, nil
		}
		return result, service.selectEncoder(ctx, previous, result.Recommended)
	default:
		return result, nil
	}
}

// trippedWhileSweeping reports whether a trip appeared after the sweep read the
// row. It fails closed: if the state cannot be re-read the sweep stands down
// rather than override a decision it cannot see.
func (service *Service) trippedWhileSweeping(ctx context.Context) bool {
	item, err := service.settings.GetMediaEncoding(ctx)
	if err != nil {
		service.logger.Warn(
			"could not re-read the encoder state after the sweep; leaving the choice alone",
			"error", err,
		)
		return true
	}
	return item.TrippedEncoder != nil
}

// selectEncoder moves the instance onto encoder. Nothing is written when the
// answer would not change: a sweep must not churn the row it just updated.
func (service *Service) selectEncoder(
	ctx context.Context,
	previous systemsettings.MediaEncodingSettings,
	encoder string,
) error {
	if previous.ActiveEncoder == encoder &&
		previous.TrippedEncoder == nil &&
		previous.FailureCount == 0 {
		return nil
	}
	if !service.apply(ctx, encoder) {
		// The sweep recommends something this build cannot run. Keeping the
		// previous choice is better than recording one that cannot work: the
		// breaker will move off a choice that really fails.
		return fmt.Errorf("encoder choice could not be applied: %s", encoder)
	}
	return nil
}

func containsEncoder(encoders []string, wanted string) bool {
	for _, encoder := range encoders {
		if encoder == wanted {
			return true
		}
	}
	return false
}
