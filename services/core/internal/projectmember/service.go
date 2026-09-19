package projectmember

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	clock      func() time.Time
}

const defaultGuestDuration = 7 * 24 * time.Hour

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) List(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]Member, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	if workspaceID == "" || projectID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.List(ctx, workspaceID, projectID, service.clock().UTC())
}

func (service *Service) Add(ctx context.Context, input AddInput) (Member, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	input.RoleKey = strings.ToLower(strings.TrimSpace(input.RoleKey))
	if input.WorkspaceID == "" || input.ProjectID == "" || input.UserID == "" ||
		input.CreatedBy == "" || !validAssignableRole(input.RoleKey) {
		return Member{}, ErrInvalidInput
	}
	permissions, err := marshalPermissions(input.Permissions)
	if err != nil {
		return Member{}, err
	}
	id, err := newID()
	if err != nil {
		return Member{}, err
	}
	return service.repository.Add(ctx, addRecord{
		ID:          id,
		WorkspaceID: input.WorkspaceID,
		ProjectID:   input.ProjectID,
		UserID:      input.UserID,
		RoleKey:     input.RoleKey,
		Permissions: permissions,
		ExpiresAt:   input.ExpiresAt,
		CreatedBy:   input.CreatedBy,
		Now:         service.clock().UTC(),
	})
}

func (service *Service) CreateGuest(
	ctx context.Context,
	input CreateGuestInput,
) (Member, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Locale = strings.TrimSpace(input.Locale)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.Locale == "" {
		input.Locale = "zh-CN"
	}
	var email *string
	if input.Email != nil && strings.TrimSpace(*input.Email) != "" {
		normalized, err := normalizeEmail(*input.Email)
		if err != nil {
			return Member{}, ErrInvalidInput
		}
		email = &normalized
	}
	now := service.clock().UTC()
	expiresAt := now.Add(defaultGuestDuration)
	if input.ExpiresAt != nil {
		expiresAt = input.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) {
		return Member{}, ErrExpiryInvalid
	}
	if input.WorkspaceID == "" || input.ProjectID == "" ||
		len([]rune(input.DisplayName)) < 1 ||
		len([]rune(input.DisplayName)) > 80 ||
		input.CreatedBy == "" {
		return Member{}, ErrInvalidInput
	}
	permissions, err := marshalPermissions(input.Permissions)
	if err != nil {
		return Member{}, err
	}
	userID, err := newID()
	if err != nil {
		return Member{}, err
	}
	membershipID, err := newID()
	if err != nil {
		return Member{}, err
	}
	projectMemberID, err := newID()
	if err != nil {
		return Member{}, err
	}
	return service.repository.CreateGuest(ctx, createGuestRecord{
		UserID:          userID,
		MembershipID:    membershipID,
		ProjectMemberID: projectMemberID,
		WorkspaceID:     input.WorkspaceID,
		ProjectID:       input.ProjectID,
		Email:           email,
		DisplayName:     input.DisplayName,
		Locale:          input.Locale,
		Permissions:     permissions,
		ExpiresAt:       expiresAt,
		CreatedBy:       input.CreatedBy,
		Now:             now,
	})
}

func (service *Service) Update(ctx context.Context, input UpdateInput) (Member, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ID = strings.TrimSpace(input.ID)
	input.RoleKey = strings.ToLower(strings.TrimSpace(input.RoleKey))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.WorkspaceID == "" || input.ProjectID == "" || input.ID == "" ||
		input.Revision < 1 || !validAssignableRole(input.RoleKey) ||
		!validStatus(input.Status) {
		return Member{}, ErrInvalidInput
	}
	permissions, err := marshalPermissions(input.Permissions)
	if err != nil {
		return Member{}, err
	}
	return service.repository.Update(ctx, updateRecord{
		UpdateInput: input,
		Permissions: permissions,
		Now:         service.clock().UTC(),
	})
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", errors.New("email is invalid")
	}
	return value, nil
}

func (service *Service) Remove(ctx context.Context, input RemoveInput) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ID = strings.TrimSpace(input.ID)
	if input.WorkspaceID == "" || input.ProjectID == "" || input.ID == "" ||
		input.Revision < 1 {
		return ErrInvalidInput
	}
	return service.repository.Remove(ctx, input, service.clock().UTC())
}

func (service *Service) Transfer(
	ctx context.Context,
	input TransferInput,
) (Member, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.NewPrimaryUserID = strings.TrimSpace(input.NewPrimaryUserID)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	input.ActorWorkspaceRole = strings.ToLower(strings.TrimSpace(input.ActorWorkspaceRole))
	input.FormerOwnerAction = strings.ToLower(strings.TrimSpace(input.FormerOwnerAction))
	if input.WorkspaceID == "" || input.ProjectID == "" ||
		input.NewPrimaryUserID == "" || input.ActorUserID == "" ||
		!validFormerOwnerAction(input.FormerOwnerAction) {
		return Member{}, ErrInvalidInput
	}
	id, err := newID()
	if err != nil {
		return Member{}, err
	}
	return service.repository.Transfer(ctx, transferRecord{
		TransferInput:   input,
		NewMembershipID: id,
		Permissions: `{"project.manage":true,"project.members.manage":true,` +
			`"assets.add":true,"assets.upload":true,"assets.remove":true,` +
			`"reviews.create":true,"reviews.comment":true,"reviews.decide":true,` +
			`"reviews.delete":true,"shares.create":true}`,
		Now: service.clock().UTC(),
	})
}

func validAssignableRole(value string) bool {
	return value == "supervisor" || value == "member" || value == "guest"
}

func validStatus(value string) bool {
	return value == "active" || value == "disabled" || value == "expired"
}

func validFormerOwnerAction(value string) bool {
	return value == "keep_supervisor" || value == "demote_member" || value == "leave"
}

func marshalPermissions(value map[string]bool) (string, error) {
	if value == nil {
		value = map[string]bool{}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
