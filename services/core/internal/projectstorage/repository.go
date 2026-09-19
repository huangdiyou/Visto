package projectstorage

import (
	"context"
	"time"
)

type grantRecord struct {
	ID                string
	WorkspaceID       string
	StorageProviderID string
	AuthorizedRootID  *string
	Status            string
	GrantedBy         string
	Now               time.Time
}

type selectionRecord struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	GrantID     string
	Purpose     string
	SelectedBy  string
	Now         time.Time
}

type Repository interface {
	ListGrants(context.Context, string) ([]Grant, error)
	ListAvailableGrants(context.Context, string) ([]Grant, error)
	SetGrant(context.Context, grantRecord) (Grant, error)
	ListSelections(context.Context, string, string) ([]Selection, error)
	Select(context.Context, selectionRecord) (Selection, error)
}
