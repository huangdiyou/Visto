package reviewtemplate

import "time"

type Template struct {
	ID               string
	WorkspaceID      string
	Name             string
	Description      *string
	ParticipantRoles []string
	AllowDownload    bool
	DueDays          *int
	DecisionRule     string
	CreatedBy        string
	Revision         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type CreateInput struct {
	WorkspaceID      string
	UserID           string
	Name             string
	Description      *string
	ParticipantRoles []string
	AllowDownload    bool
	DueDays          *int
	DecisionRule     string
}

type UpdateInput struct {
	WorkspaceID      string
	ID               string
	Name             string
	Description      *string
	ParticipantRoles []string
	AllowDownload    bool
	DueDays          *int
	DecisionRule     string
	Revision         int
}

type DeleteInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}
