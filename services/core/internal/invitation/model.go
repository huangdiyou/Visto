package invitation

import (
	"time"

	"review-studio.local/core/internal/identity"
)

type Invitation struct {
	ID               string
	WorkspaceID      string
	WorkspaceName    string
	Email            string
	Role             string
	TokenPrefix      string
	Status           string
	ExpiresAt        time.Time
	InvitedBy        string
	AcceptedByUserID *string
	SendCount        int
	LastSentAt       time.Time
	AcceptedAt       *time.Time
	RevokedAt        *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Secret struct {
	Invitation Invitation
	URL        string
}

type CreateInput struct {
	WorkspaceID string
	InvitedBy   string
	Email       string
	Role        string
	ExpiresAt   time.Time
}

type AcceptInput struct {
	Token       string
	DisplayName string
	Password    string
	Locale      string
}

type AcceptResult struct {
	Invitation Invitation
	Session    identity.Session
	Token      string
}
