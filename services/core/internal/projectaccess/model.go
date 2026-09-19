package projectaccess

import (
	"errors"
	"time"
)

type Permission string

const (
	PermissionProjectRead          Permission = "project.read"
	PermissionProjectManage        Permission = "project.manage"
	PermissionProjectMembersManage Permission = "project.members.manage"
	PermissionAssetsAdd            Permission = "assets.add"
	PermissionAssetsUpload         Permission = "assets.upload"
	PermissionAssetsRemove         Permission = "assets.remove"
	PermissionReviewsCreate        Permission = "reviews.create"
	PermissionReviewsComment       Permission = "reviews.comment"
	PermissionReviewsDecide        Permission = "reviews.decide"
	PermissionReviewsDelete        Permission = "reviews.delete"
	PermissionSharesCreate         Permission = "shares.create"
)

var ErrDenied = errors.New("project permission denied")

type CheckInput struct {
	WorkspaceID   string
	ProjectID     string
	UserID        string
	WorkspaceRole string
	Permission    Permission
}

type PermissionInput struct {
	WorkspaceID   string
	ProjectID     string
	UserID        string
	WorkspaceRole string
}

type Membership struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	UserID      string
	RoleKey     string
	Permissions map[Permission]bool
	Status      string
	ExpiresAt   *time.Time
}
