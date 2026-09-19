package workspace

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

func (service *Service) ListMemberships(
	ctx context.Context,
	workspaceID string,
) ([]Membership, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListMemberships(ctx, workspaceID)
}

func (service *Service) UpdateMembership(
	ctx context.Context,
	input UpdateMembershipInput,
) (Membership, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.WorkspaceID == "" || input.ID == "" || input.Revision < 1 ||
		!validRole(input.Role) || !validStatus(input.Status) {
		return Membership{}, ErrInvalidInput
	}
	return service.repository.UpdateMembership(ctx, input, service.clock().UTC())
}

func validRole(value string) bool {
	switch value {
	case "owner", "admin", "member", "guest":
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	return value == "active" || value == "disabled"
}
