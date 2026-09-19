package workspacesettings

import (
	"context"
	"strings"
	"time"
)

type Service struct {
	repository Repository
	clock      func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) GetRegistration(
	ctx context.Context,
	workspaceID string,
) (RegistrationSettings, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return RegistrationSettings{}, ErrInvalidInput
	}
	return service.repository.GetRegistration(ctx, workspaceID, service.clock().UTC())
}

func (service *Service) UpdateRegistration(
	ctx context.Context,
	input UpdateRegistrationInput,
) (RegistrationSettings, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.TeamName = strings.TrimSpace(input.TeamName)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.WorkspaceID == "" ||
		input.UpdatedBy == "" ||
		len([]rune(input.TeamName)) < 1 ||
		len([]rune(input.TeamName)) > 80 ||
		input.Revision < 1 {
		return RegistrationSettings{}, ErrInvalidInput
	}
	return service.repository.UpdateRegistration(ctx, updateRegistrationRecord{
		UpdateRegistrationInput: input,
		Now:                     service.clock().UTC(),
	})
}

func (service *Service) PublicRegistration(
	ctx context.Context,
) (PublicRegistration, error) {
	return service.repository.PublicRegistration(ctx)
}
