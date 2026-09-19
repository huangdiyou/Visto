package share

import "time"

type Share struct {
	ID                string
	WorkspaceID       string
	ReviewSessionID   *string
	ReviewSessionName *string
	Name              string
	Status            string
	AllowComment      bool
	AllowDownload     bool
	RequireNickname   bool
	ExpiresAt         *time.Time
	MaxVisits         *int
	PasswordProtected bool
	PasswordSecretRef *string
	CreatedBy         string
	Revision          int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	RevokedAt         *time.Time
	Links             []Link
}

type Link struct {
	ID             string
	ShareID        string
	TokenPrefix    string
	TokenSecretRef *string
	Status         string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
}

type LinkSecret struct {
	Link
	Token string
}

type VisitorCode struct {
	ID          string
	ShareID     string
	CodePrefix  string
	DisplayName string
	Status      string
	ExpiresAt   *time.Time
	CreatedBy   string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

type VisitorCodeSecret struct {
	VisitorCode
	Code string
}

type CreateInput struct {
	WorkspaceID     string
	UserID          string
	ReviewSessionID string
	Name            string
	AllowComment    bool
	AllowDownload   bool
	RequireNickname bool
	ExpiresAt       *time.Time
	Password        string
}

type CreateResult struct {
	Share Share
	Link  LinkSecret
}

type LinkCredential struct {
	LinkID string
	Token  string
}

type Credentials struct {
	ShareID  string
	Password *string
	Links    []LinkCredential
}

type CreateVisitorCodeInput struct {
	WorkspaceID string
	ShareID     string
	UserID      string
	DisplayName string
	ExpiresAt   *time.Time
}

type UpdateInput struct {
	WorkspaceID     string
	ID              string
	Name            string
	AllowComment    bool
	AllowDownload   bool
	RequireNickname bool
	ExpiresAt       *time.Time
	Password        *string
	Revision        int
}

type StateInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}

type PublicItem struct {
	ID                    string
	AssetVersionID        string
	AssetName             string
	VersionNumber         int
	MediaType             string
	DurationUs            *int64
	Width                 *int
	Height                *int
	PreviewRenditionID    *string
	PreviewRenditionKind  *string
	ThumbnailRenditionID  *string
	SourceStorageObjectID string
	SourceFilename        string
	SourceMIME            *string
	SourceSizeBytes       int64
}

type PublicVisitor struct {
	ID             string
	DisplayName    *string
	IdentityMethod string
	Identified     bool
	Verified       bool
}

type PublicShare struct {
	ID               string
	WorkspaceID      string
	ReviewSessionID  string
	ShareLinkID      string
	VisitorSessionID string
	Name             string
	ReviewName       string
	TeamName         string
	ReviewStatus     string
	AllowComment     bool
	AllowDownload    bool
	RequireNickname  bool
	ExpiresAt        *time.Time
	Visitor          PublicVisitor
	Items            []PublicItem
}

type IdentifyInput struct {
	SessionToken   string
	Method         string
	DisplayName    string
	Code           string
	ClientIdentity string
}

type OpenResult struct {
	SessionToken     string
	SessionExpiresAt time.Time
	PasswordRequired bool
	Share            PublicShare
}
