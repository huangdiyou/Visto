package catalog

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound                  = errors.New("catalog resource not found")
	ErrRevisionConflict          = errors.New("catalog revision conflict")
	ErrProjectArchived           = errors.New("project is archived")
	ErrDuplicateItem             = errors.New("asset already belongs to collection")
	ErrAssetProjectConflict      = errors.New("asset belongs to another project")
	ErrPinnedVersionInvalid      = errors.New("pinned version does not belong to asset")
	ErrInvalidOrder              = errors.New("collection item order is invalid")
	ErrProjectStorageUnavailable = errors.New(
		"project storage grant is unavailable",
	)
)

type projectRecord struct {
	ID                  string
	MembershipID        string
	WorkspaceID         string
	UserID              string
	Name                string
	Description         *string
	PrimaryPermissions  string
	StorageGrantID      string
	StorageSelectionIDs [3]string
	Now                 time.Time
}

type collectionRecord struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	UserID      string
	Name        string
	Description *string
	Now         time.Time
}

type collectionItemRecord struct {
	ID              string
	WorkspaceID     string
	CollectionID    string
	AssetID         string
	PinnedVersionID *string
	Caption         *string
	Now             time.Time
}

type Repository interface {
	ListProjects(ctx context.Context, input ListProjectsInput, now time.Time) ([]Project, error)
	Project(ctx context.Context, workspaceID, projectID string) (Project, error)
	ProjectOverview(
		ctx context.Context,
		workspaceID string,
		projectID string,
	) (ProjectOverview, error)
	CreateProject(ctx context.Context, record projectRecord) (Project, error)
	UpdateProject(ctx context.Context, input UpdateProjectInput, now time.Time) (Project, error)
	SetProjectStatus(
		ctx context.Context,
		input ProjectStateInput,
		status string,
		now time.Time,
	) (Project, error)
	DeleteProject(ctx context.Context, input ProjectStateInput, now time.Time) error

	ListCollections(
		ctx context.Context,
		workspaceID string,
		projectID string,
	) ([]Collection, error)
	Collection(ctx context.Context, workspaceID, collectionID string) (Collection, error)
	CreateCollection(ctx context.Context, record collectionRecord) (Collection, error)
	UpdateCollection(
		ctx context.Context,
		input UpdateCollectionInput,
		now time.Time,
	) (Collection, error)
	DeleteCollection(
		ctx context.Context,
		workspaceID string,
		collectionID string,
		revision int,
		now time.Time,
	) error

	ListCollectionItems(
		ctx context.Context,
		workspaceID string,
		collectionID string,
	) ([]CollectionItem, error)
	AddCollectionItem(
		ctx context.Context,
		record collectionItemRecord,
	) (CollectionItem, error)
	ReorderCollectionItems(
		ctx context.Context,
		workspaceID string,
		collectionID string,
		itemIDs []string,
	) error
	DeleteCollectionItem(
		ctx context.Context,
		workspaceID string,
		collectionID string,
		itemID string,
	) error
}
