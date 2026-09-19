package review

import "time"

type Session struct {
	ID                string
	WorkspaceID       string
	ProjectID         string
	ProjectName       string
	CollectionID      *string
	Name              string
	Status            string
	DueAt             *time.Time
	TemplateID        *string
	TemplateRevision  *int
	ResponsibleUserID *string
	ResponsibleName   string
	AllowDownload     bool
	DecisionRule      string
	CreatedBy         string
	Revision          int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ClosedAt          *time.Time
	Items             []Item
	Participants      []Participant
}

type Item struct {
	ID             string
	AssetID        string
	AssetVersionID string
	AssetName      string
	VersionNumber  int
	Position       int
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Participant struct {
	ID          string
	UserID      *string
	DisplayName string
	Role        string
	CreatedAt   time.Time
}

type ParticipantInput struct {
	UserID      *string
	DisplayName string
	Role        string
}

type ItemInput struct {
	AssetID        string
	AssetVersionID string
}

type CreateSessionInput struct {
	WorkspaceID       string
	UserID            string
	ProjectID         string
	CollectionID      *string
	Name              string
	DueAt             *time.Time
	TemplateID        *string
	TemplateRevision  *int
	ResponsibleUserID *string
	AllowDownload     bool
	DecisionRule      string
	Participants      []ParticipantInput
	Items             []ItemInput
}

type UpdateSessionInput struct {
	WorkspaceID       string
	ID                string
	Name              string
	DueAt             *time.Time
	ResponsibleUserID *string
	Participants      []ParticipantInput
	Revision          int
}

type SessionStateInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}

type ListFilter struct {
	AssetVersionID string
	ProjectID      string
	UserID         string
	WorkspaceOwner bool
	At             time.Time
	Limit          int
	Offset         int
}

type ListPage struct {
	Items      []Session
	NextOffset *int
}

type CommentAuthor struct {
	Kind        string
	ID          string
	DisplayName string
}

type Annotation struct {
	ID              string
	Kind            string
	TimeStartUs     *int64
	TimeEndUs       *int64
	Geometry        *AnnotationGeometry
	GeometryVersion int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type AnnotationGeometry struct {
	Shape    string                     `json:"shape"`
	X        float64                    `json:"x"`
	Y        float64                    `json:"y"`
	Width    *float64                   `json:"width,omitempty"`
	Height   *float64                   `json:"height,omitempty"`
	Elements []AnnotationDrawingElement `json:"elements,omitempty"`
}

type AnnotationDrawingElement struct {
	Tool        string            `json:"tool"`
	Color       string            `json:"color"`
	StrokeWidth float64           `json:"strokeWidth"`
	Points      []AnnotationPoint `json:"points,omitempty"`
	X           *float64          `json:"x,omitempty"`
	Y           *float64          `json:"y,omitempty"`
	Width       *float64          `json:"width,omitempty"`
	Height      *float64          `json:"height,omitempty"`
}

type AnnotationPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Comment struct {
	ID          string
	Author      CommentAuthor
	Body        string
	Attachments []CommentAttachment
	EditedAt    *time.Time
	CreatedAt   time.Time
}

type CommentAttachment struct {
	ID                   string
	WorkspaceID          string
	ReviewSessionID      string
	ReviewItemID         string
	ThreadID             *string
	CommentID            *string
	SourceType           string
	UploadedByUserID     *string
	ShareVisitorID       *string
	AuthorizedRootID     string
	ObjectKey            string
	OriginalFilename     string
	MIMEType             string
	SizeBytes            int64
	Width                *int
	Height               *int
	UploadSecurityPolicy string
	Status               string
	ResultCode           string
	Message              *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	AttachedAt           *time.Time
}

type CommentThread struct {
	ID              string
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	AssetVersionID  string
	Status          string
	Author          CommentAuthor
	ResolvedBy      *CommentAuthor
	ResolvedAt      *time.Time
	Annotation      Annotation
	Comments        []Comment
	Revision        int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type AnnotationInput struct {
	Kind            string
	TimeStartUs     *int64
	TimeEndUs       *int64
	Geometry        *AnnotationGeometry
	GeometryVersion int
}

type CreateThreadInput struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	AssetVersionID  string
	AuthorKind      string
	AuthorUserID    string
	AuthorVisitorID string
	Body            string
	Annotation      AnnotationInput
	AttachmentIDs   []string
}

type ThreadCommentInput struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	ThreadID        string
	AuthorKind      string
	AuthorUserID    string
	AuthorVisitorID string
	Body            string
	AttachmentIDs   []string
}

type CreatePendingAttachmentInput struct {
	WorkspaceID          string
	ReviewSessionID      string
	ReviewItemID         string
	SourceType           string
	UploadedByUserID     string
	ShareVisitorID       string
	AuthorizedRootID     string
	ObjectKey            string
	OriginalFilename     string
	MIMEType             string
	SizeBytes            int64
	Width                *int
	Height               *int
	UploadSecurityPolicy string
	Status               string
	ResultCode           string
	Message              *string
}

type CommentAttachmentLookup struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	ID              string
	ActorKind       string
	ActorUserID     string
	ActorVisitorID  string
}

type UpdateCommentInput struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	ThreadID        string
	CommentID       string
	AuthorKind      string
	AuthorUserID    string
	AuthorVisitorID string
	Body            string
}

type DeleteCommentInput struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	ThreadID        string
	CommentID       string
	AuthorKind      string
	AuthorUserID    string
	AuthorVisitorID string
}

type ThreadStateInput struct {
	WorkspaceID     string
	ReviewSessionID string
	ThreadID        string
	UserID          string
	Revision        int
}

type ThreadListFilter struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	Limit           int
	Offset          int
}

type Decision struct {
	ID              string
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    *string
	Actor           CommentAuthor
	Decision        string
	Note            *string
	CreatedAt       time.Time
}

type DecisionListFilter struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	Limit           int
	Offset          int
}

// Comment threads, decisions and nested comments are all bounded so a busy
// review cannot stream an unbounded result set into memory or a response body.
const (
	defaultThreadListLimit   = 200
	maxThreadListLimit       = 200
	defaultDecisionListLimit = 200
	maxDecisionListLimit     = 200
	maxCommentsPerThread     = 200
)

type CreateDecisionInput struct {
	WorkspaceID     string
	ReviewSessionID string
	ReviewItemID    string
	ActorKind       string
	ActorUserID     string
	ActorVisitorID  string
	Decision        string
	Note            string
}
