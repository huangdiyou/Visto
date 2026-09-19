package authorization

import (
	"errors"
	"strings"
)

type Permission string

const (
	PermissionWorkspaceManage     Permission = "workspace.manage"
	PermissionMembersRead         Permission = "members.read"
	PermissionMembersManage       Permission = "members.manage"
	PermissionProjectsRead        Permission = "projects.read"
	PermissionProjectsWrite       Permission = "projects.write"
	PermissionAssetsRead          Permission = "assets.read"
	PermissionAssetsUpload        Permission = "assets.upload"
	PermissionAssetsDownload      Permission = "assets.download"
	PermissionReviewsRead         Permission = "reviews.read"
	PermissionReviewsWrite        Permission = "reviews.write"
	PermissionReviewsComment      Permission = "reviews.comment"
	PermissionReviewsDecide       Permission = "reviews.decide"
	PermissionSharesManage        Permission = "shares.manage"
	PermissionAuditRead           Permission = "audit.read"
	PermissionNotifications       Permission = "notifications.read"
	PermissionNotificationsManage Permission = "notifications.manage"
	PermissionStorageManage       Permission = "storage.manage"
)

var ErrDenied = errors.New("permission denied")

type Service struct {
	rolePermissions map[string]map[Permission]struct{}
}

func NewService() *Service {
	return &Service{rolePermissions: defaultRolePermissions()}
}

func (service *Service) Allowed(role string, permission Permission) bool {
	permissions, ok := service.rolePermissions[strings.ToLower(strings.TrimSpace(role))]
	if !ok {
		return false
	}
	_, ok = permissions[permission]
	return ok
}

func (service *Service) Require(role string, permission Permission) error {
	if !service.Allowed(role, permission) {
		return ErrDenied
	}
	return nil
}

func defaultRolePermissions() map[string]map[Permission]struct{} {
	owner := permissionSet(
		PermissionWorkspaceManage,
		PermissionMembersRead,
		PermissionMembersManage,
		PermissionProjectsRead,
		PermissionProjectsWrite,
		PermissionAssetsRead,
		PermissionAssetsUpload,
		PermissionAssetsDownload,
		PermissionReviewsRead,
		PermissionReviewsWrite,
		PermissionReviewsComment,
		PermissionReviewsDecide,
		PermissionSharesManage,
		PermissionAuditRead,
		PermissionNotifications,
		PermissionNotificationsManage,
		PermissionStorageManage,
	)
	admin := permissionSet(
		PermissionMembersRead,
		PermissionMembersManage,
		PermissionProjectsRead,
		PermissionProjectsWrite,
		PermissionAssetsRead,
		PermissionAssetsUpload,
		PermissionAssetsDownload,
		PermissionReviewsRead,
		PermissionReviewsWrite,
		PermissionReviewsComment,
		PermissionReviewsDecide,
		PermissionSharesManage,
		PermissionAuditRead,
		PermissionNotifications,
		PermissionNotificationsManage,
		PermissionStorageManage,
	)
	member := permissionSet(
		PermissionMembersRead,
		PermissionProjectsRead,
		PermissionProjectsWrite,
		PermissionAssetsRead,
		PermissionAssetsUpload,
		PermissionAssetsDownload,
		PermissionReviewsRead,
		PermissionReviewsWrite,
		PermissionReviewsComment,
		PermissionReviewsDecide,
		PermissionSharesManage,
		PermissionNotifications,
	)
	guest := permissionSet(
		PermissionProjectsRead,
		PermissionAssetsRead,
		PermissionAssetsUpload,
		PermissionReviewsRead,
		PermissionReviewsComment,
		PermissionReviewsDecide,
		PermissionSharesManage,
		PermissionNotifications,
	)
	return map[string]map[Permission]struct{}{
		"owner":  owner,
		"admin":  admin,
		"member": member,
		"guest":  guest,
	}
}

func permissionSet(items ...Permission) map[Permission]struct{} {
	result := make(map[Permission]struct{}, len(items))
	for _, item := range items {
		result[item] = struct{}{}
	}
	return result
}
