package reviewtemplate

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

func (service *Service) List(
	ctx context.Context,
	workspaceID string,
) ([]Template, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.List(ctx, workspaceID)
}

func (service *Service) Get(
	ctx context.Context,
	workspaceID string,
	id string,
) (Template, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	id = strings.TrimSpace(id)
	if workspaceID == "" || id == "" {
		return Template{}, ErrInvalidInput
	}
	return service.repository.Get(ctx, workspaceID, id)
}

func (service *Service) Create(
	ctx context.Context,
	input CreateInput,
) (Template, error) {
	if err := normalizeAndValidate(
		&input.Name,
		&input.Description,
		&input.ParticipantRoles,
		input.DueDays,
		&input.DecisionRule,
	); err != nil {
		return Template{}, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.UserID == "" {
		return Template{}, ErrInvalidInput
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Template{}, err
	}
	now := service.clock().UTC()
	return service.repository.Create(ctx, Template{
		ID:               id.String(),
		WorkspaceID:      input.WorkspaceID,
		Name:             input.Name,
		Description:      input.Description,
		ParticipantRoles: input.ParticipantRoles,
		AllowDownload:    input.AllowDownload,
		DueDays:          input.DueDays,
		DecisionRule:     input.DecisionRule,
		CreatedBy:        input.UserID,
		Revision:         1,
	}, now)
}

func (service *Service) Update(
	ctx context.Context,
	input UpdateInput,
) (Template, error) {
	if err := normalizeAndValidate(
		&input.Name,
		&input.Description,
		&input.ParticipantRoles,
		input.DueDays,
		&input.DecisionRule,
	); err != nil {
		return Template{}, err
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	if input.WorkspaceID == "" || input.ID == "" || input.Revision < 1 {
		return Template{}, ErrInvalidInput
	}
	return service.repository.Update(ctx, input, service.clock().UTC())
}

func (service *Service) Delete(
	ctx context.Context,
	input DeleteInput,
) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	if input.WorkspaceID == "" || input.ID == "" || input.Revision < 1 {
		return ErrInvalidInput
	}
	return service.repository.Delete(ctx, input, service.clock().UTC())
}

func normalizeAndValidate(
	name *string,
	description **string,
	roles *[]string,
	dueDays *int,
	decisionRule *string,
) error {
	*name = strings.TrimSpace(*name)
	if length := len([]rune(*name)); length < 1 || length > 120 {
		return ErrInvalidInput
	}
	if *description != nil {
		value := strings.TrimSpace(**description)
		if len([]rune(value)) > 500 {
			return ErrInvalidInput
		}
		if value == "" {
			*description = nil
		} else {
			*description = &value
		}
	}
	if len(*roles) < 1 || len(*roles) > 20 {
		return ErrInvalidInput
	}
	normalized := make([]string, 0, len(*roles))
	reviewerCount := 0
	for _, role := range *roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role != "reviewer" && role != "observer" {
			return ErrInvalidInput
		}
		if role == "reviewer" {
			reviewerCount++
		}
		normalized = append(normalized, role)
	}
	*roles = normalized
	if dueDays != nil && (*dueDays < 1 || *dueDays > 365) {
		return ErrInvalidInput
	}
	*decisionRule = strings.ToLower(strings.TrimSpace(*decisionRule))
	switch *decisionRule {
	case "any_reviewer", "all_reviewers", "responsible_only":
	default:
		return ErrInvalidInput
	}
	if *decisionRule == "all_reviewers" && reviewerCount == 0 {
		return ErrInvalidInput
	}
	return nil
}
