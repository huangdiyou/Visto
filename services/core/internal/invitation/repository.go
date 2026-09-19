package invitation

import (
	"context"
	"errors"
	"time"

	"review-studio.local/core/internal/identity"
)

var (
	ErrInvalidInput  = errors.New("invitation input is invalid")
	ErrNotFound      = errors.New("invitation not found")
	ErrEmailInUse    = errors.New("email already belongs to a workspace member")
	ErrPendingExists = errors.New("pending invitation already exists")
	ErrUnavailable   = errors.New("invitation is unavailable")
	ErrExpired       = errors.New("invitation has expired")
)

type createRecord struct {
	ID          string
	WorkspaceID string
	InvitedBy   string
	Email       string
	Role        string
	TokenDigest string
	TokenPrefix string
	ExpiresAt   time.Time
	Now         time.Time
}

type rotateRecord struct {
	WorkspaceID string
	ID          string
	TokenDigest string
	TokenPrefix string
	ExpiresAt   time.Time
	Now         time.Time
}

type acceptRecord struct {
	TokenDigest string
	DisplayName string
	Locale      string
	Material    identity.ProvisioningMaterial
	Now         time.Time
}

type acceptedRecord struct {
	Invitation    Invitation
	Workspace     identity.Workspace
	User          identity.User
	Role          string
	SessionID     string
	SessionExpiry time.Time
}

type Repository interface {
	Create(context.Context, createRecord) (Invitation, error)
	List(context.Context, string, time.Time) ([]Invitation, error)
	Rotate(context.Context, rotateRecord) (Invitation, error)
	Revoke(context.Context, string, string, time.Time) (Invitation, error)
	Preview(context.Context, string, time.Time) (Invitation, error)
	Accept(context.Context, acceptRecord) (acceptedRecord, error)
}
