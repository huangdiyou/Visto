package authorization

import (
	"errors"
	"testing"
)

func TestDefaultRolePermissions(t *testing.T) {
	service := NewService()

	tests := []struct {
		role       string
		permission Permission
		allowed    bool
	}{
		{"owner", PermissionWorkspaceManage, true},
		{"admin", PermissionMembersManage, true},
		{"admin", PermissionNotificationsManage, true},
		{"admin", PermissionWorkspaceManage, false},
		{"member", PermissionAssetsUpload, true},
		{"member", PermissionNotificationsManage, false},
		{"member", PermissionSharesManage, true},
		{"guest", PermissionReviewsComment, true},
		{"guest", PermissionSharesManage, true},
		{"guest", PermissionMembersRead, false},
		{"guest", PermissionReviewsWrite, false},
		{"guest", PermissionAssetsDownload, false},
		{"unknown", PermissionProjectsRead, false},
	}
	for _, test := range tests {
		t.Run(test.role+"/"+string(test.permission), func(t *testing.T) {
			if got := service.Allowed(test.role, test.permission); got != test.allowed {
				t.Fatalf("allowed = %t, want %t", got, test.allowed)
			}
		})
	}
}

func TestRequireReturnsStableDeniedError(t *testing.T) {
	err := NewService().Require("guest", PermissionMembersManage)
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
}
