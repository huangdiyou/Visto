package job

import "errors"

var (
	ErrNotFound          = errors.New("job not found")
	ErrInvalidInput      = errors.New("job input is invalid")
	ErrInvalidTransition = errors.New("job state transition is invalid")
	ErrLeaseLost         = errors.New("job lease was lost")
	ErrNotRetryable      = errors.New("job is not retryable")
)
