package projectmember

import (
	"errors"
	"time"
)

var (
	ErrNotFound              = errors.New("project member not found")
	ErrInvalidInput          = errors.New("project member input is invalid")
	ErrRevisionConflict      = errors.New("project member revision conflict")
	ErrAlreadyMember         = errors.New("user already belongs to project")
	ErrWorkspaceMemberNeeded = errors.New("user must be an active workspace member")
	ErrPrimaryOwnerProtected = errors.New("project primary owner is protected")
	ErrTransferForbidden     = errors.New("project transfer is forbidden")
	ErrEmailInUse            = errors.New("project guest email is already in use")
	ErrExpiryInvalid         = errors.New("project guest expiry is invalid")
)

type Member struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	UserID      string
	Email       *string
	DisplayName string
	RoleKey     string
	Status      string
	Permissions map[string]bool
	ExpiresAt   *time.Time
	JoinedAt    *time.Time
	RemovedAt   *time.Time
	Revision    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AddInput struct {
	WorkspaceID string
	ProjectID   string
	UserID      string
	RoleKey     string
	Permissions map[string]bool
	ExpiresAt   *time.Time
	CreatedBy   string
}

type CreateGuestInput struct {
	WorkspaceID string
	ProjectID   string
	Email       *string
	DisplayName string
	Locale      string
	Permissions map[string]bool
	ExpiresAt   *time.Time
	CreatedBy   string
}

type UpdateInput struct {
	WorkspaceID string
	ProjectID   string
	ID          string
	RoleKey     string
	Status      string
	Permissions map[string]bool
	ExpiresAt   *time.Time
	Revision    int
}

type RemoveInput struct {
	WorkspaceID string
	ProjectID   string
	ID          string
	Revision    int
}

type TransferInput struct {
	WorkspaceID        string
	ProjectID          string
	NewPrimaryUserID   string
	FormerOwnerAction  string
	ActorUserID        string
	ActorWorkspaceRole string
}
