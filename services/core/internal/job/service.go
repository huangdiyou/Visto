package job

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultMaxAttempts = 3
	defaultListLimit   = 50
	maxListLimit       = 200
	maxPayloadBytes    = 64 * 1024
)

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) Create(
	ctx context.Context,
	input CreateInput,
) (Job, bool, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Type = strings.TrimSpace(input.Type)
	input.SubjectType = strings.TrimSpace(input.SubjectType)
	input.SubjectID = strings.TrimSpace(input.SubjectID)
	if input.IdempotencyKey != nil {
		value := strings.TrimSpace(*input.IdempotencyKey)
		input.IdempotencyKey = &value
	}
	if input.WorkspaceID == "" || input.Type == "" ||
		input.SubjectType == "" || input.SubjectID == "" {
		return Job{}, false, fmt.Errorf("%w: required field is empty", ErrInvalidInput)
	}
	if len(input.Type) > 120 || len(input.SubjectType) > 120 ||
		len(input.SubjectID) > 200 ||
		(input.IdempotencyKey != nil && len(*input.IdempotencyKey) > 240) {
		return Job{}, false, fmt.Errorf("%w: field is too long", ErrInvalidInput)
	}
	if len(input.Payload) == 0 {
		input.Payload = json.RawMessage(`{}`)
	}
	if len(input.Payload) > maxPayloadBytes || !json.Valid(input.Payload) {
		return Job{}, false, fmt.Errorf("%w: payload is invalid", ErrInvalidInput)
	}
	if input.MaxAttempts == 0 {
		input.MaxAttempts = defaultMaxAttempts
	}
	if input.MaxAttempts < 1 || input.MaxAttempts > 20 {
		return Job{}, false, fmt.Errorf("%w: max attempts is invalid", ErrInvalidInput)
	}
	now := service.clock().UTC()
	if input.AvailableAt.IsZero() {
		input.AvailableAt = now
	}
	id, err := newID()
	if err != nil {
		return Job{}, false, err
	}
	return service.repository.Create(ctx, createRecord{
		ID:             id,
		WorkspaceID:    input.WorkspaceID,
		Type:           input.Type,
		Priority:       input.Priority,
		IdempotencyKey: input.IdempotencyKey,
		SubjectType:    input.SubjectType,
		SubjectID:      input.SubjectID,
		Payload:        string(input.Payload),
		MaxAttempts:    input.MaxAttempts,
		AvailableAt:    input.AvailableAt.UTC(),
		Now:            now,
	})
}

func (service *Service) Get(
	ctx context.Context,
	workspaceID string,
	jobID string,
) (Job, error) {
	return service.repository.Get(ctx, workspaceID, jobID)
}

func (service *Service) List(
	ctx context.Context,
	input ListInput,
) ([]Job, error) {
	if input.Limit == 0 {
		input.Limit = defaultListLimit
	}
	if input.Limit < 1 || input.Limit > maxListLimit {
		return nil, fmt.Errorf("%w: list limit is invalid", ErrInvalidInput)
	}
	switch input.Status {
	case "", StatusQueued, StatusLeased, StatusRunning, StatusSucceeded,
		StatusFailed, StatusCancelRequested, StatusCancelled:
	default:
		return nil, fmt.Errorf("%w: status is invalid", ErrInvalidInput)
	}
	return service.repository.List(ctx, input)
}

func (service *Service) EnsureNode(
	ctx context.Context,
	node Node,
) error {
	now := service.clock().UTC()
	if node.CreatedAt.IsZero() {
		node.CreatedAt = now
	}
	node.UpdatedAt = now
	node.LastSeenAt = &now
	if len(node.Capabilities) == 0 {
		node.Capabilities = json.RawMessage(`{}`)
	}
	return service.repository.EnsureNode(ctx, node)
}

func (service *Service) Claim(
	ctx context.Context,
	nodeID string,
	leaseDuration time.Duration,
) (Lease, bool, error) {
	if leaseDuration <= 0 {
		return Lease{}, false, fmt.Errorf("%w: lease duration is invalid", ErrInvalidInput)
	}
	token, err := randomToken()
	if err != nil {
		return Lease{}, false, err
	}
	item, attempt, claimed, err := service.repository.Claim(
		ctx,
		nodeID,
		tokenDigest(token),
		service.clock().UTC(),
		leaseDuration,
	)
	if err != nil || !claimed {
		return Lease{}, claimed, err
	}
	return Lease{
		Job:      item,
		Attempt:  attempt,
		Token:    token,
		Duration: leaseDuration,
	}, true, nil
}

func (service *Service) Start(ctx context.Context, lease Lease) error {
	return service.repository.Start(
		ctx,
		lease.Job.ID,
		lease.Attempt.ID,
		tokenDigest(lease.Token),
		service.clock().UTC(),
	)
}

func (service *Service) Heartbeat(
	ctx context.Context,
	lease Lease,
) (bool, error) {
	return service.repository.Heartbeat(
		ctx,
		lease.Job.ID,
		lease.Attempt.ID,
		tokenDigest(lease.Token),
		service.clock().UTC(),
		lease.Duration,
	)
}

func (service *Service) SetProgress(
	ctx context.Context,
	lease Lease,
	progress Progress,
) error {
	progress.Unit = strings.TrimSpace(progress.Unit)
	if progress.Current < 0 || progress.Total <= 0 ||
		progress.Current > progress.Total || progress.Unit == "" ||
		len(progress.Unit) > 40 {
		return fmt.Errorf("%w: progress is invalid", ErrInvalidInput)
	}
	return service.repository.SetProgress(
		ctx,
		lease.Job.ID,
		lease.Attempt.ID,
		tokenDigest(lease.Token),
		progress,
		service.clock().UTC(),
	)
}

func (service *Service) Succeed(ctx context.Context, lease Lease) error {
	return service.repository.Succeed(
		ctx,
		lease.Job.ID,
		lease.Attempt.ID,
		tokenDigest(lease.Token),
		service.clock().UTC(),
	)
}

func (service *Service) Fail(
	ctx context.Context,
	lease Lease,
	jobError Error,
) error {
	jobError.Code = strings.TrimSpace(jobError.Code)
	jobError.Message = sanitizeError(jobError.Message)
	if jobError.Code == "" {
		jobError.Code = "job.execution_failed"
	}
	now := service.clock().UTC()
	retryDelay := time.Second << min(lease.Attempt.AttemptNumber-1, 6)
	return service.repository.Fail(
		ctx,
		lease.Job.ID,
		lease.Attempt.ID,
		tokenDigest(lease.Token),
		jobError,
		now.Add(retryDelay),
		now,
	)
}

func (service *Service) Cancel(
	ctx context.Context,
	workspaceID string,
	jobID string,
) (Job, error) {
	return service.repository.Cancel(ctx, workspaceID, jobID, service.clock().UTC())
}

func (service *Service) Retry(
	ctx context.Context,
	workspaceID string,
	jobID string,
) (Job, error) {
	return service.repository.Retry(ctx, workspaceID, jobID, service.clock().UTC())
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return id.String(), nil
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate lease token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func tokenDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sanitizeError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2000 {
		value = value[:2000]
	}
	return value
}
