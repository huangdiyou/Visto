package projectstorage

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) ListGrants(
	ctx context.Context,
	workspaceID string,
) ([]Grant, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListGrants(ctx, workspaceID)
}

// ListAvailableGrants returns the grants a project is allowed to select. The
// unfiltered workspace list remains Owner-only (see ListGrants).
func (service *Service) ListAvailableGrants(
	ctx context.Context,
	workspaceID string,
) ([]Grant, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListAvailableGrants(ctx, workspaceID)
}

func (service *Service) SetGrant(
	ctx context.Context,
	input SetGrantInput,
) (Grant, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.StorageProviderID = strings.TrimSpace(input.StorageProviderID)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.GrantedBy = strings.TrimSpace(input.GrantedBy)
	if input.AuthorizedRootID != nil {
		trimmed := strings.TrimSpace(*input.AuthorizedRootID)
		if trimmed == "" {
			input.AuthorizedRootID = nil
		} else {
			input.AuthorizedRootID = &trimmed
		}
	}
	if input.WorkspaceID == "" || input.StorageProviderID == "" ||
		input.GrantedBy == "" ||
		(input.Status != "active" && input.Status != "disabled") {
		return Grant{}, ErrInvalidInput
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Grant{}, err
	}
	return service.repository.SetGrant(ctx, grantRecord{
		ID:                id.String(),
		WorkspaceID:       input.WorkspaceID,
		StorageProviderID: input.StorageProviderID,
		AuthorizedRootID:  input.AuthorizedRootID,
		Status:            input.Status,
		GrantedBy:         input.GrantedBy,
		Now:               service.clock().UTC(),
	})
}

func (service *Service) ListSelections(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]Selection, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	if workspaceID == "" || projectID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListSelections(ctx, workspaceID, projectID)
}

func (service *Service) Select(
	ctx context.Context,
	input SelectInput,
) (Selection, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.GrantID = strings.TrimSpace(input.GrantID)
	input.Purpose = strings.ToLower(strings.TrimSpace(input.Purpose))
	input.SelectedBy = strings.TrimSpace(input.SelectedBy)
	if input.WorkspaceID == "" || input.ProjectID == "" || input.GrantID == "" ||
		input.SelectedBy == "" || !validPurpose(input.Purpose) {
		return Selection{}, ErrInvalidInput
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Selection{}, err
	}
	return service.repository.Select(ctx, selectionRecord{
		ID:          id.String(),
		WorkspaceID: input.WorkspaceID,
		ProjectID:   input.ProjectID,
		GrantID:     input.GrantID,
		Purpose:     input.Purpose,
		SelectedBy:  input.SelectedBy,
		Now:         service.clock().UTC(),
	})
}

func validPurpose(value string) bool {
	return value == "upload" ||
		value == "default_rendition" ||
		value == "archive"
}
