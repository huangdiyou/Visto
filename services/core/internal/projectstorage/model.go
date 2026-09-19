package projectstorage

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput     = errors.New("project storage input is invalid")
	ErrNotFound         = errors.New("project storage resource not found")
	ErrGrantUnavailable = errors.New("project storage grant is unavailable")
)

type Grant struct {
	ID                   string
	WorkspaceID          string
	StorageProviderID    string
	AuthorizedRootID     *string
	ProviderName         string
	ProviderKind         string
	ProviderStatus       string
	RootName             *string
	RootStatus           *string
	LocalManagedBucketID *string
	BucketPurpose        *string
	Status               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type Selection struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	GrantID     string
	Purpose     string
	SelectedBy  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Grant       Grant
}

type SetGrantInput struct {
	WorkspaceID       string
	StorageProviderID string
	AuthorizedRootID  *string
	Status            string
	GrantedBy         string
}

type SelectInput struct {
	WorkspaceID string
	ProjectID   string
	GrantID     string
	Purpose     string
	SelectedBy  string
}
