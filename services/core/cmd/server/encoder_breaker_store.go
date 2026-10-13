package main

import (
	"context"

	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/systemsettings"
)

// encoderBreakerActor names the encoder job in the activity record. The Owner
// reads it, so it says what wrote the value rather than naming a person.
const encoderBreakerActor = "media-encoder"

// encoderBreakerStore adapts the system settings service to the breaker's
// persistence port. The adapter lives at the composition root so that neither
// internal/media nor internal/systemsettings has to import the other.
type encoderBreakerStore struct {
	settings *systemsettings.Service
}

func (store encoderBreakerStore) LoadEncoderBreaker(
	ctx context.Context,
) (media.EncoderBreakerState, error) {
	item, err := store.settings.GetMediaEncoding(ctx)
	if err != nil {
		return media.EncoderBreakerState{}, err
	}
	state := media.EncoderBreakerState{
		ActiveEncoder: item.ActiveEncoder,
		FailureCount:  item.FailureCount,
	}
	if item.TrippedEncoder != nil {
		state.TrippedEncoder = *item.TrippedEncoder
	}
	if item.TrippedReason != nil {
		state.TrippedReason = *item.TrippedReason
	}
	if item.TrippedAt != nil {
		state.TrippedAt = *item.TrippedAt
	}
	return state, nil
}

func (store encoderBreakerStore) SaveEncoderBreaker(
	ctx context.Context,
	state media.EncoderBreakerState,
) error {
	input := systemsettings.RecordMediaEncodingRuntimeInput{
		ActiveEncoder:  state.ActiveEncoder,
		FailureCount:   state.FailureCount,
		TrippedEncoder: state.TrippedEncoder,
		TrippedReason:  state.TrippedReason,
		UpdatedBy:      encoderBreakerActor,
	}
	if !state.TrippedAt.IsZero() {
		trippedAt := state.TrippedAt
		input.TrippedAt = &trippedAt
	}
	_, err := store.settings.RecordMediaEncodingRuntime(ctx, input)
	return err
}
