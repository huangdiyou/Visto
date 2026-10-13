package share

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound           = errors.New("share not found")
	ErrRevisionConflict   = errors.New("share revision conflict")
	ErrInvalidReview      = errors.New("share review session is invalid")
	ErrInvalidState       = errors.New("share state is invalid")
	ErrEntryUnavailable   = errors.New("share entry is unavailable")
	ErrSessionNotFound    = errors.New("share session not found")
	ErrPasswordRequired   = errors.New("share password is required")
	ErrPasswordInvalid    = errors.New("share password is invalid")
	ErrRateLimited        = errors.New("share password verification rate limited")
	ErrDownloadDenied     = errors.New("share download is not allowed")
	ErrCommentDenied      = errors.New("share commenting is not allowed")
	ErrItemNotFound       = errors.New("share item not found")
	ErrIdentityRequired   = errors.New("share visitor identity is required")
	ErrVisitorCodeInvalid = errors.New("share visitor code is invalid")
)

type createRecord struct {
	Share
	PasswordHash   string
	LinkID         string
	TokenDigest    string
	TokenSecretRef string
	TokenPrefix    string
	Now            time.Time
}

type updateRecord struct {
	UpdateInput
	PasswordHash         *string
	SetPasswordHash      bool
	PasswordSecretRef    *string
	SetPasswordSecretRef bool
	Now                  time.Time
}

type sessionRecord struct {
	ID                  string
	SessionDigest       string
	VisitorID           string
	ExpiresAt           time.Time
	UnverifiedExpiresAt time.Time
	Now                 time.Time
}

type sessionAccess struct {
	PublicShare
	PasswordHash     string
	Verified         bool
	SessionID        string
	ShareLinkID      string
	SessionExpiresAt time.Time
}

type visitorCodeRecord struct {
	ID          string
	WorkspaceID string
	ShareID     string
	CodeDigest  string
	CodePrefix  string
	DisplayName string
	ExpiresAt   *time.Time
	CreatedBy   string
	Now         time.Time
}

type visitorIdentityRecord struct {
	VisitorID      string
	DisplayName    *string
	IdentityMethod string
	VerifiedAt     *time.Time
	Now            time.Time
}

type visitorCodeUseRecord struct {
	VisitorID     string
	SessionDigest string
	CodeDigest    string
	Now           time.Time
}

type Repository interface {
	List(context.Context, string, time.Time) ([]Share, error)
	Get(context.Context, string, string, time.Time) (Share, error)
	GetByLink(context.Context, string, string, time.Time) (Share, error)
	Create(context.Context, createRecord) (Share, Link, error)
	CreateVisitorCode(context.Context, visitorCodeRecord) (VisitorCode, error)
	Update(context.Context, updateRecord) (Share, error)
	Revoke(context.Context, StateInput, time.Time) (Share, error)
	CreateLink(
		context.Context,
		string,
		string,
		string,
		string,
		string,
		string,
		time.Time,
	) (Link, error)
	RevokeLink(context.Context, string, string, time.Time) (Link, error)
	OpenEntry(context.Context, string, sessionRecord) (sessionAccess, error)
	Session(context.Context, string, time.Time) (sessionAccess, error)
	MarkSessionVerified(context.Context, string, time.Time, time.Time) error
	IdentifySession(context.Context, string, visitorIdentityRecord) (sessionAccess, error)
	IdentifySessionWithCode(context.Context, visitorCodeUseRecord) (sessionAccess, error)
}
