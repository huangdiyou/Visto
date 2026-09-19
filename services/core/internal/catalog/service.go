package catalog

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Clock func() time.Time

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) ListProjects(
	ctx context.Context,
	input ListProjectsInput,
) ([]Project, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.WorkspaceRole = strings.ToLower(strings.TrimSpace(input.WorkspaceRole))
	return service.repository.ListProjects(ctx, input, service.clock().UTC())
}

func (service *Service) Project(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (Project, error) {
	return service.repository.Project(ctx, workspaceID, projectID)
}

func (service *Service) ProjectOverview(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (ProjectOverview, error) {
	if _, err := service.repository.Project(ctx, workspaceID, projectID); err != nil {
		return ProjectOverview{}, err
	}
	overview, err := service.repository.ProjectOverview(ctx, workspaceID, projectID)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.ProjectID = projectID
	overview.GeneratedAt = service.clock().UTC()
	return overview, nil
}

func (service *Service) CreateProject(
	ctx context.Context,
	input CreateProjectInput,
) (Project, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = normalizeDescription(input.Description)
	input.StorageGrantID = strings.TrimSpace(input.StorageGrantID)
	if err := validateName(input.Name); err != nil {
		return Project{}, err
	}
	if err := validateDescription(input.Description); err != nil {
		return Project{}, err
	}

	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	membershipID, err := newID()
	if err != nil {
		return Project{}, err
	}
	var storageSelectionIDs [3]string
	if input.StorageGrantID != "" {
		for index := range storageSelectionIDs {
			storageSelectionIDs[index], err = newID()
			if err != nil {
				return Project{}, err
			}
		}
	}

	return service.repository.CreateProject(ctx, projectRecord{
		ID:                  id,
		MembershipID:        membershipID,
		WorkspaceID:         input.WorkspaceID,
		UserID:              input.UserID,
		Name:                input.Name,
		Description:         input.Description,
		StorageGrantID:      input.StorageGrantID,
		StorageSelectionIDs: storageSelectionIDs,
		PrimaryPermissions: `{"project.manage":true,"project.members.manage":true,` +
			`"assets.add":true,"assets.upload":true,"assets.remove":true,` +
			`"reviews.create":true,"reviews.comment":true,"reviews.decide":true,` +
			`"reviews.delete":true,"shares.create":true}`,
		Now: service.clock().UTC(),
	})
}

func (service *Service) UpdateProject(
	ctx context.Context,
	input UpdateProjectInput,
) (Project, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = normalizeDescription(input.Description)
	if err := validateName(input.Name); err != nil {
		return Project{}, err
	}
	if err := validateDescription(input.Description); err != nil {
		return Project{}, err
	}
	if input.Revision < 1 {
		return Project{}, errors.New("revision must be positive")
	}

	return service.repository.UpdateProject(ctx, input, service.clock().UTC())
}

func (service *Service) ArchiveProject(
	ctx context.Context,
	input ProjectStateInput,
) (Project, error) {
	return service.setProjectStatus(ctx, input, "archived")
}

func (service *Service) RestoreProject(
	ctx context.Context,
	input ProjectStateInput,
) (Project, error) {
	return service.setProjectStatus(ctx, input, "active")
}

func (service *Service) DeleteProject(
	ctx context.Context,
	input ProjectStateInput,
) error {
	if input.Revision < 1 {
		return errors.New("revision must be positive")
	}
	return service.repository.DeleteProject(ctx, input, service.clock().UTC())
}

func (service *Service) setProjectStatus(
	ctx context.Context,
	input ProjectStateInput,
	status string,
) (Project, error) {
	if input.Revision < 1 {
		return Project{}, errors.New("revision must be positive")
	}
	return service.repository.SetProjectStatus(
		ctx,
		input,
		status,
		service.clock().UTC(),
	)
}

func (service *Service) ListCollections(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]Collection, error) {
	if _, err := service.repository.Project(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return service.repository.ListCollections(ctx, workspaceID, projectID)
}

func (service *Service) Collection(
	ctx context.Context,
	workspaceID string,
	collectionID string,
) (Collection, error) {
	return service.repository.Collection(ctx, workspaceID, collectionID)
}

func (service *Service) CreateCollection(
	ctx context.Context,
	input CreateCollectionInput,
) (Collection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = normalizeDescription(input.Description)
	if err := validateName(input.Name); err != nil {
		return Collection{}, err
	}
	if err := validateDescription(input.Description); err != nil {
		return Collection{}, err
	}

	id, err := newID()
	if err != nil {
		return Collection{}, err
	}

	return service.repository.CreateCollection(ctx, collectionRecord{
		ID:          id,
		WorkspaceID: input.WorkspaceID,
		ProjectID:   input.ProjectID,
		UserID:      input.UserID,
		Name:        input.Name,
		Description: input.Description,
		Now:         service.clock().UTC(),
	})
}

func (service *Service) UpdateCollection(
	ctx context.Context,
	input UpdateCollectionInput,
) (Collection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = normalizeDescription(input.Description)
	if err := validateName(input.Name); err != nil {
		return Collection{}, err
	}
	if err := validateDescription(input.Description); err != nil {
		return Collection{}, err
	}
	if input.Revision < 1 {
		return Collection{}, errors.New("revision must be positive")
	}

	return service.repository.UpdateCollection(ctx, input, service.clock().UTC())
}

func (service *Service) DeleteCollection(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	revision int,
) error {
	if revision < 1 {
		return errors.New("revision must be positive")
	}
	return service.repository.DeleteCollection(
		ctx,
		workspaceID,
		collectionID,
		revision,
		service.clock().UTC(),
	)
}

func (service *Service) ListCollectionItems(
	ctx context.Context,
	workspaceID string,
	collectionID string,
) ([]CollectionItem, error) {
	return service.repository.ListCollectionItems(ctx, workspaceID, collectionID)
}

func (service *Service) AddCollectionItem(
	ctx context.Context,
	input AddCollectionItemInput,
) (CollectionItem, error) {
	id, err := newID()
	if err != nil {
		return CollectionItem{}, err
	}

	return service.repository.AddCollectionItem(ctx, collectionItemRecord{
		ID:              id,
		WorkspaceID:     input.WorkspaceID,
		CollectionID:    input.CollectionID,
		AssetID:         input.AssetID,
		PinnedVersionID: normalizeDescription(input.PinnedVersionID),
		Caption:         normalizeDescription(input.Caption),
		Now:             service.clock().UTC(),
	})
}

func (service *Service) ReorderCollectionItems(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	itemIDs []string,
) error {
	if len(itemIDs) == 0 {
		return ErrInvalidOrder
	}
	return service.repository.ReorderCollectionItems(
		ctx,
		workspaceID,
		collectionID,
		itemIDs,
	)
}

func (service *Service) DeleteCollectionItem(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	itemID string,
) error {
	return service.repository.DeleteCollectionItem(
		ctx,
		workspaceID,
		collectionID,
		itemID,
	)
}

func validateName(name string) error {
	length := len([]rune(name))
	if length < 1 || length > 120 {
		return errors.New("name must contain 1 to 120 characters")
	}
	return nil
}

func validateDescription(description *string) error {
	if description != nil && len([]rune(*description)) > 2000 {
		return errors.New("description must contain at most 2000 characters")
	}
	return nil
}

func normalizeDescription(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
