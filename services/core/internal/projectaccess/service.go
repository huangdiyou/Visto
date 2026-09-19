package projectaccess

import (
	"context"
	"strings"
	"time"
)

type Clock func() time.Time

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

func (service *Service) Allowed(
	ctx context.Context,
	input CheckInput,
) (bool, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.WorkspaceRole = strings.ToLower(strings.TrimSpace(input.WorkspaceRole))
	if input.WorkspaceID == "" ||
		input.ProjectID == "" ||
		input.UserID == "" ||
		input.Permission == "" {
		return false, nil
	}
	if input.WorkspaceRole == "owner" {
		return true, nil
	}

	membership, err := service.repository.ActiveMembership(
		ctx,
		input.WorkspaceID,
		input.ProjectID,
		input.UserID,
		service.clock().UTC(),
	)
	if err != nil {
		return false, err
	}
	return membershipAllows(membership, input.Permission), nil
}

func (service *Service) Require(
	ctx context.Context,
	input CheckInput,
) error {
	allowed, err := service.Allowed(ctx, input)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrDenied
	}
	return nil
}

func (service *Service) Permissions(
	ctx context.Context,
	input PermissionInput,
) (map[Permission]bool, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.WorkspaceRole = strings.ToLower(strings.TrimSpace(input.WorkspaceRole))
	if input.WorkspaceID == "" || input.ProjectID == "" || input.UserID == "" {
		return map[Permission]bool{}, nil
	}
	if input.WorkspaceRole == "owner" {
		return allPermissions(), nil
	}
	membership, err := service.repository.ActiveMembership(
		ctx,
		input.WorkspaceID,
		input.ProjectID,
		input.UserID,
		service.clock().UTC(),
	)
	if err != nil {
		return nil, err
	}
	return EffectivePermissions(membership.RoleKey, membership.Permissions), nil
}

// EffectivePermissions expands role defaults before applying explicit
// overrides. Authorization code that delegates permissions must compare this
// complete set rather than only the submitted override map.
func EffectivePermissions(
	role string,
	overrides map[Permission]bool,
) map[Permission]bool {
	result := make(map[Permission]bool, len(knownPermissions()))
	for _, permission := range knownPermissions() {
		result[permission] = roleAllows(role, permission)
		if override, ok := overrides[permission]; ok {
			result[permission] = override
		}
	}
	return result
}

func membershipAllows(membership Membership, permission Permission) bool {
	if membership.Status != "active" {
		return false
	}
	base := roleAllows(membership.RoleKey, permission)
	if override, ok := membership.Permissions[permission]; ok {
		return override
	}
	return base
}

func roleAllows(role string, permission Permission) bool {
	switch role {
	case "primary_owner", "supervisor":
		return true
	case "member":
		switch permission {
		case PermissionProjectRead,
			PermissionAssetsAdd,
			PermissionAssetsUpload,
			PermissionAssetsRemove,
			PermissionReviewsCreate,
			PermissionReviewsComment,
			PermissionReviewsDecide,
			PermissionReviewsDelete,
			PermissionSharesCreate:
			return true
		default:
			return false
		}
	case "guest":
		return permission == PermissionProjectRead
	default:
		return false
	}
}

func allPermissions() map[Permission]bool {
	result := make(map[Permission]bool, len(knownPermissions()))
	for _, permission := range knownPermissions() {
		result[permission] = true
	}
	return result
}

func knownPermissions() []Permission {
	return []Permission{
		PermissionProjectRead,
		PermissionProjectManage,
		PermissionProjectMembersManage,
		PermissionAssetsAdd,
		PermissionAssetsUpload,
		PermissionAssetsRemove,
		PermissionReviewsCreate,
		PermissionReviewsComment,
		PermissionReviewsDecide,
		PermissionReviewsDelete,
		PermissionSharesCreate,
	}
}
