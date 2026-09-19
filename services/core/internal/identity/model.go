package identity

import "time"

type User struct {
	ID          string
	Email       *string
	DisplayName string
	Locale      string
}

type Workspace struct {
	ID       string
	Name     string
	TeamName string
	Timezone string
}

type Session struct {
	ID        string
	User      User
	Workspace Workspace
	Role      string
	ExpiresAt time.Time
}

type SetupInput struct {
	WorkspaceName string
	OwnerName     string
	OwnerEmail    string
	Password      string
	Locale        string
	Timezone      string
}

type LoginInput struct {
	Email    string
	Password string
}

type CreateAccountInput struct {
	WorkspaceID string
	Email       string
	DisplayName string
	Password    string
	Locale      string
	Role        string
}

type UpdateProfileInput struct {
	UserID      string
	DisplayName string
	Locale      string
}

type ProvisioningMaterial struct {
	UserID         string
	MembershipID   string
	SessionID      string
	PasswordHash   string
	SessionToken   string
	SessionDigest  string
	SessionExpires time.Time
}

type SetupResult struct {
	Session Session
	Token   string
}

type LoginResult struct {
	Session Session
	Token   string
}
