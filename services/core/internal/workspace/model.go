package workspace

import "time"

type Membership struct {
	ID          string
	WorkspaceID string
	UserID      string
	Email       *string
	DisplayName string
	Role        string
	Status      string
	Revision    int
	JoinedAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DisabledAt  *time.Time
}

type UpdateMembershipInput struct {
	WorkspaceID string
	ID          string
	Role        string
	Status      string
	Revision    int
}
