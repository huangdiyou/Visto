package job

import (
	"encoding/json"
	"time"
)

const (
	StatusQueued          = "queued"
	StatusLeased          = "leased"
	StatusRunning         = "running"
	StatusSucceeded       = "succeeded"
	StatusFailed          = "failed"
	StatusCancelRequested = "cancel_requested"
	StatusCancelled       = "cancelled"
)

type Progress struct {
	Current int64
	Total   int64
	Unit    string
}

type Error struct {
	Code    string
	Message string
}

type Job struct {
	ID             string
	WorkspaceID    string
	Type           string
	Status         string
	Priority       int
	IdempotencyKey *string
	SubjectType    string
	SubjectID      string
	Payload        json.RawMessage
	Progress       *Progress
	AvailableAt    time.Time
	MaxAttempts    int
	AttemptCount   int
	LastError      *Error
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

type Attempt struct {
	ID             string
	JobID          string
	MediaNodeID    *string
	AttemptNumber  int
	LeaseExpiresAt time.Time
	StartedAt      time.Time
	HeartbeatAt    time.Time
	FinishedAt     *time.Time
	Status         string
	Error          *Error
}

type Lease struct {
	Job      Job
	Attempt  Attempt
	Token    string
	Duration time.Duration
}

type CreateInput struct {
	WorkspaceID    string
	Type           string
	Priority       int
	IdempotencyKey *string
	SubjectType    string
	SubjectID      string
	Payload        json.RawMessage
	MaxAttempts    int
	AvailableAt    time.Time
}

type ListInput struct {
	WorkspaceID string
	Status      string
	Limit       int
}

type Node struct {
	ID              string
	WorkspaceID     *string
	Name            string
	Kind            string
	Status          string
	Capabilities    json.RawMessage
	SoftwareVersion string
	LastSeenAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
