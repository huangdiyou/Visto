package media

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// EncoderBreakerThreshold is how many consecutive failures of the encoder in use
// trip the breaker. docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.3 recommends
// three; a single failure is common (a bad source, a transient device error) and
// must not move the whole instance off hardware encoding.
const EncoderBreakerThreshold = 3

// EncoderBreakerState is the persisted breaker. Persisting it is the point: if a
// restart forgot the count, the Owner would watch the same encoder fail three
// more times after every service restart.
type EncoderBreakerState struct {
	ActiveEncoder  string    `json:"activeEncoder"`
	FailureCount   int       `json:"failureCount"`
	TrippedEncoder string    `json:"trippedEncoder,omitempty"`
	TrippedReason  string    `json:"trippedReason,omitempty"`
	TrippedAt      time.Time `json:"trippedAt,omitempty"`
}

// EncoderBreakerStore persists the breaker. Implementations must be safe for
// concurrent use.
type EncoderBreakerStore interface {
	LoadEncoderBreaker(context.Context) (EncoderBreakerState, error)
	SaveEncoderBreaker(context.Context, EncoderBreakerState) error
}

// EncoderBreakerOutcome reports what recording a failure actually changed, so the
// caller only notifies the Owner when something moved.
type EncoderBreakerOutcome struct {
	Tripped         bool
	PreviousEncoder string
	ActiveEncoder   string
	FailureCount    int
	Reason          string
}

// EncoderBreaker counts consecutive failures of the encoder in use and switches
// away from one that keeps failing. The final candidate on the list is software
// encoding, so a tripped breaker always lands on something that should work.
type EncoderBreaker struct {
	mu    sync.Mutex
	store EncoderBreakerStore
	now   func() time.Time
	state EncoderBreakerState
}

func NewEncoderBreaker(store EncoderBreakerStore) *EncoderBreaker {
	return &EncoderBreaker{store: store, now: time.Now}
}

// Load reads the persisted state. A store with nothing recorded yet yields the
// zero value, which means "no failures, nothing tripped".
func (breaker *EncoderBreaker) Load(ctx context.Context) error {
	state, err := breaker.store.LoadEncoderBreaker(ctx)
	if err != nil {
		return fmt.Errorf("load encoder breaker: %w", err)
	}
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	breaker.state = state
	if breaker.state.FailureCount < 0 {
		breaker.state.FailureCount = 0
	}
	return nil
}

// ActiveEncoder returns the encoder the next job should use: the breaker's
// choice when it has one, otherwise the caller's default. An unloaded breaker
// never overrides the caller.
func (breaker *EncoderBreaker) ActiveEncoder(fallback string) string {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	if breaker.state.ActiveEncoder != "" {
		return breaker.state.ActiveEncoder
	}
	return fallback
}

// RecordSuccess clears the consecutive-failure count. The encoder is passed in
// so a success from the fallback encoder does not clear a count that belongs to
// the encoder that failed.
func (breaker *EncoderBreaker) RecordSuccess(ctx context.Context, encoder string) error {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	if breaker.state.FailureCount == 0 || encoder == "" || encoder != breaker.state.ActiveEncoder {
		return nil
	}
	breaker.state.FailureCount = 0
	return breaker.persist(ctx)
}

// RecordFailure counts one failure of encoder. At the threshold it trips that
// encoder and moves ActiveEncoder to the next entry in candidates, which is the
// probe's preference order ending in software encoding.
func (breaker *EncoderBreaker) RecordFailure(
	ctx context.Context,
	encoder string,
	reason string,
	candidates []string,
) (EncoderBreakerOutcome, error) {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	outcome := EncoderBreakerOutcome{PreviousEncoder: encoder, ActiveEncoder: encoder}
	if encoder == "" {
		return outcome, nil
	}
	// A failure of anything other than the encoder in use belongs to a previous
	// cycle and must not be counted against the current one.
	if breaker.state.ActiveEncoder != "" && encoder != breaker.state.ActiveEncoder {
		outcome.ActiveEncoder = breaker.state.ActiveEncoder
		outcome.FailureCount = breaker.state.FailureCount
		return outcome, nil
	}
	breaker.state.ActiveEncoder = encoder
	breaker.state.FailureCount++
	outcome.FailureCount = breaker.state.FailureCount
	if breaker.state.FailureCount < EncoderBreakerThreshold {
		outcome.ActiveEncoder = encoder
		return outcome, breaker.persist(ctx)
	}

	next := nextEncoderCandidate(candidates, encoder)
	breaker.state.TrippedEncoder = encoder
	breaker.state.TrippedReason = tripReason(encoder, breaker.state.FailureCount, reason)
	breaker.state.TrippedAt = breaker.now().UTC()
	breaker.state.ActiveEncoder = next
	breaker.state.FailureCount = 0

	outcome.Tripped = true
	outcome.ActiveEncoder = next
	outcome.FailureCount = 0
	outcome.Reason = breaker.state.TrippedReason
	return outcome, breaker.persist(ctx)
}

// Trip records an Owner-visible trip caused outside the failure path (for
// example a re-probe that found the encoder gone).
func (breaker *EncoderBreaker) Trip(ctx context.Context, encoder, reason, next string) error {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	breaker.state.TrippedEncoder = encoder
	breaker.state.TrippedReason = tripReason(encoder, EncoderBreakerThreshold, reason)
	breaker.state.TrippedAt = breaker.now().UTC()
	breaker.state.ActiveEncoder = next
	breaker.state.FailureCount = 0
	return breaker.persist(ctx)
}

// Reset clears the breaker, which is what the Owner changing the encoder or a
// successful re-probe does.
func (breaker *EncoderBreaker) Reset(ctx context.Context, encoder string) error {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	breaker.state = EncoderBreakerState{ActiveEncoder: encoder}
	return breaker.persist(ctx)
}

func (breaker *EncoderBreaker) Snapshot() EncoderBreakerState {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	return breaker.state
}

func (breaker *EncoderBreaker) persist(ctx context.Context) error {
	if err := breaker.store.SaveEncoderBreaker(ctx, breaker.state); err != nil {
		return fmt.Errorf("save encoder breaker: %w", err)
	}
	return nil
}

// nextEncoderCandidate returns the entry after current. An unknown current (or a
// current that is already last) yields the last entry, which the candidate list
// always ends with software encoding.
func nextEncoderCandidate(candidates []string, current string) string {
	if len(candidates) == 0 {
		return current
	}
	for index, name := range candidates {
		if name != current {
			continue
		}
		if index+1 < len(candidates) {
			return candidates[index+1]
		}
		return candidates[len(candidates)-1]
	}
	return candidates[len(candidates)-1]
}

// tripReason is what the Owner reads, so it names the encoder, the count and the
// underlying cause rather than only saying "failed".
func tripReason(encoder string, failures int, reason string) string {
	message := fmt.Sprintf("%s failed %d times in a row", encoder, failures)
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return message
	}
	return message + ": " + trimmed
}
