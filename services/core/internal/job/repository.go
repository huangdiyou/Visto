package job

import (
	"context"
	"time"
)

type Repository interface {
	Create(ctx context.Context, record createRecord) (Job, bool, error)
	Get(ctx context.Context, workspaceID, jobID string) (Job, error)
	List(ctx context.Context, input ListInput) ([]Job, error)
	EnsureNode(ctx context.Context, node Node) error
	Claim(
		ctx context.Context,
		nodeID string,
		leaseDigest string,
		now time.Time,
		leaseDuration time.Duration,
	) (Job, Attempt, bool, error)
	Start(
		ctx context.Context,
		jobID, attemptID, leaseDigest string,
		now time.Time,
	) error
	Heartbeat(
		ctx context.Context,
		jobID, attemptID, leaseDigest string,
		now time.Time,
		leaseDuration time.Duration,
	) (bool, error)
	SetProgress(
		ctx context.Context,
		jobID, attemptID, leaseDigest string,
		progress Progress,
		now time.Time,
	) error
	Succeed(
		ctx context.Context,
		jobID, attemptID, leaseDigest string,
		now time.Time,
	) error
	Fail(
		ctx context.Context,
		jobID, attemptID, leaseDigest string,
		jobError Error,
		retryAt time.Time,
		now time.Time,
	) error
	Cancel(ctx context.Context, workspaceID, jobID string, now time.Time) (Job, error)
	Retry(ctx context.Context, workspaceID, jobID string, now time.Time) (Job, error)
}

type createRecord struct {
	ID             string
	WorkspaceID    string
	Type           string
	Priority       int
	IdempotencyKey *string
	SubjectType    string
	SubjectID      string
	Payload        string
	MaxAttempts    int
	AvailableAt    time.Time
	Now            time.Time
}
