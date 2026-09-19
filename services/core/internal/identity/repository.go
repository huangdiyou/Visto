package identity

import (
	"context"
	"errors"
	"time"
)

var (
	ErrSetupAlreadyCompleted = errors.New("setup already completed")
	ErrSetupRequired         = errors.New("setup is required")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrSessionNotFound       = errors.New("session not found")
	ErrUserNotFound          = errors.New("user not found")
	ErrEmailInUse            = errors.New("email is already in use")
	ErrWorkspaceNotFound     = errors.New("workspace not found")
)

type setupRecord struct {
	UserID         string
	WorkspaceID    string
	MembershipID   string
	SessionID      string
	WorkspaceName  string
	OwnerName      string
	OwnerEmail     *string
	PasswordHash   string
	SessionDigest  string
	Locale         string
	Timezone       string
	Now            time.Time
	SessionExpires time.Time
}

type credentialRecord struct {
	UserID            string
	WorkspaceID       string
	Email             *string
	DisplayName       string
	WorkspaceName     string
	WorkspaceTeamName string
	Locale            string
	Timezone          string
	Role              string
	PasswordHash      string
}

type sessionRecord struct {
	SessionID         string
	UserID            string
	WorkspaceID       string
	Email             *string
	DisplayName       string
	WorkspaceName     string
	WorkspaceTeamName string
	Locale            string
	Timezone          string
	Role              string
	ExpiresAt         time.Time
}

type accountRecord struct {
	UserID       string
	MembershipID string
	WorkspaceID  string
	Email        string
	DisplayName  string
	PasswordHash string
	Locale       string
	Role         string
	Now          time.Time
}

type Repository interface {
	IsSetupComplete(ctx context.Context) (bool, error)
	Setup(ctx context.Context, record setupRecord) error
	LegacyOwnerCredential(ctx context.Context) (credentialRecord, error)
	CredentialByEmail(ctx context.Context, email string) (credentialRecord, error)
	CreateAccount(ctx context.Context, record accountRecord) error
	UpdateProfile(
		ctx context.Context,
		userID string,
		displayName string,
		locale string,
		now time.Time,
	) (User, error)
	CreateSession(
		ctx context.Context,
		sessionID string,
		userID string,
		workspaceID string,
		tokenDigest string,
		now time.Time,
		expiresAt time.Time,
	) error
	SessionByDigest(
		ctx context.Context,
		tokenDigest string,
		now time.Time,
		idleCutoff time.Time,
	) (sessionRecord, error)
	TouchSession(ctx context.Context, sessionID string, now time.Time) error
	RevokeSession(ctx context.Context, tokenDigest string, now time.Time) error
}
