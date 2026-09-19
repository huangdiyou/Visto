package catalog

import "time"

type Project struct {
	ID                 string
	WorkspaceID        string
	PrimaryOwnerUserID *string
	Name               string
	Description        *string
	Status             string
	AssetCount         int
	CollectionCount    int
	Revision           int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ArchivedAt         *time.Time
}

type ProjectOverview struct {
	ProjectID      string
	GeneratedAt    time.Time
	Reviews        ProjectOverviewReviews
	OpenFeedback   ProjectOverviewFeedbackList
	RecentVersions ProjectOverviewVersionList
	FailedTasks    ProjectOverviewFailedTaskList
}

type ProjectOverviewReviews struct {
	Total int
	Items []ProjectOverviewReview
}

type ProjectOverviewReview struct {
	ID                string
	Name              string
	Status            string
	DueAt             *time.Time
	ResponsibleName   string
	AssetName         string
	VersionNumber     int
	ItemCount         int
	OpenFeedbackCount int
	UpdatedAt         time.Time
}

type ProjectOverviewFeedbackList struct {
	Total int
	Items []ProjectOverviewFeedback
}

type ProjectOverviewFeedback struct {
	ID             string
	ReviewID       string
	ReviewName     string
	AssetID        string
	AssetName      string
	AssetVersionID string
	VersionNumber  int
	AuthorName     string
	Body           string
	AnnotationKind string
	TimeStartUS    *int64
	TimeEndUS      *int64
	UpdatedAt      time.Time
}

type ProjectOverviewVersionList struct {
	Total int
	Items []ProjectOverviewVersion
}

type ProjectOverviewVersion struct {
	ID               string
	AssetID          string
	AssetName        string
	VersionNumber    int
	SourceFilename   string
	ProcessingStatus string
	ActorName        string
	CreatedAt        time.Time
}

type ProjectOverviewFailedTaskList struct {
	Total int
	Items []ProjectOverviewFailedTask
}

type ProjectOverviewFailedTask struct {
	ID           string
	Type         string
	AssetID      string
	AssetName    string
	ErrorCode    string
	ErrorMessage string
	UpdatedAt    time.Time
}

type Collection struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	Name        string
	Description *string
	Kind        string
	Position    int
	ItemCount   int
	Revision    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CollectionItem struct {
	ID              string
	CollectionID    string
	AssetID         string
	AssetName       string
	AssetType       string
	PinnedVersionID *string
	Position        int
	Caption         *string
	CreatedAt       time.Time
}

type CreateProjectInput struct {
	WorkspaceID    string
	UserID         string
	Name           string
	Description    *string
	StorageGrantID string
}

type ListProjectsInput struct {
	WorkspaceID   string
	UserID        string
	WorkspaceRole string
}

type UpdateProjectInput struct {
	WorkspaceID string
	ID          string
	Name        string
	Description *string
	Revision    int
}

type ProjectStateInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}

type CreateCollectionInput struct {
	WorkspaceID string
	UserID      string
	ProjectID   string
	Name        string
	Description *string
}

type UpdateCollectionInput struct {
	WorkspaceID string
	ID          string
	Name        string
	Description *string
	Revision    int
}

type AddCollectionItemInput struct {
	WorkspaceID     string
	CollectionID    string
	AssetID         string
	PinnedVersionID *string
	Caption         *string
}
