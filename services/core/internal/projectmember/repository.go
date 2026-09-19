package projectmember

import (
	"context"
	"time"
)

type addRecord struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	UserID      string
	RoleKey     string
	Permissions string
	ExpiresAt   *time.Time
	CreatedBy   string
	Now         time.Time
}

type createGuestRecord struct {
	UserID          string
	MembershipID    string
	ProjectMemberID string
	WorkspaceID     string
	ProjectID       string
	Email           *string
	DisplayName     string
	Locale          string
	Permissions     string
	ExpiresAt       time.Time
	CreatedBy       string
	Now             time.Time
}

type updateRecord struct {
	UpdateInput
	Permissions string
	Now         time.Time
}

type transferRecord struct {
	TransferInput
	NewMembershipID string
	Permissions     string
	Now             time.Time
}

type Repository interface {
	List(context.Context, string, string, time.Time) ([]Member, error)
	Add(context.Context, addRecord) (Member, error)
	CreateGuest(context.Context, createGuestRecord) (Member, error)
	Update(context.Context, updateRecord) (Member, error)
	Remove(context.Context, RemoveInput, time.Time) error
	Transfer(context.Context, transferRecord) (Member, error)
}
