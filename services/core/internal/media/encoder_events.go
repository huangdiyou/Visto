package media

import "context"

// The two encoder events the Owner is told about
// (docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.4).
const (
	// EncoderEventFallback fires once per failure cycle, on the first failure:
	// the encoder in use failed and the job fell back automatically.
	EncoderEventFallback = "media.encoding_fallback"
	// EncoderEventTripped fires when the same encoder reached the threshold and
	// the breaker moved the instance to another encoder.
	EncoderEventTripped = "media.encoding_tripped"
)

// encoderFirstFailure is the failure count that opens a cycle. Notifying only on
// it is the design's debounce: later failures in the same cycle only raise the
// count, so the Owner is not flooded while an encoder is on its way to tripping.
const encoderFirstFailure = 1

// EncoderEvent is what the Owner reads. The processor fills the encoding facts;
// the caller, which knows the workspace and the asset, fills the identity. It
// carries no filesystem paths.
type EncoderEvent struct {
	Kind            string
	PreviousEncoder string
	ActiveEncoder   string
	FailureCount    int
	Reason          string
	WorkspaceID     string
	AssetID         string
	RenditionID     string
}

// EncoderEventRecorder delivers encoder events. Implementations must be
// best-effort: a notification that cannot be delivered is a reporting problem,
// never a reason to fail an encode that otherwise succeeded.
type EncoderEventRecorder interface {
	RecordEncoderEvent(ctx context.Context, event EncoderEvent) error
}

// encoderEventFor turns one recorded failure into the event to report, or nil
// when the failure is not worth telling the Owner about. A tripped breaker always
// reports; otherwise only the failure that opened the cycle does, and only when
// the job actually fell back to another encoder.
func encoderEventFor(outcome EncoderBreakerOutcome, willFallBack bool) *EncoderEvent {
	if outcome.Tripped {
		return &EncoderEvent{
			Kind:            EncoderEventTripped,
			PreviousEncoder: outcome.PreviousEncoder,
			ActiveEncoder:   outcome.ActiveEncoder,
			FailureCount:    outcome.FailureCount,
			Reason:          outcome.Reason,
		}
	}
	if !willFallBack || outcome.FailureCount != encoderFirstFailure {
		return nil
	}
	return &EncoderEvent{
		Kind:            EncoderEventFallback,
		PreviousEncoder: outcome.PreviousEncoder,
		ActiveEncoder:   outcome.ActiveEncoder,
		FailureCount:    outcome.FailureCount,
	}
}
