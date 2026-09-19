package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"review-studio.local/core/internal/authorization"
)

func TestManagementPermissionRoutes(t *testing.T) {
	tests := []struct {
		method     string
		path       string
		permission authorization.Permission
		protected  bool
	}{
		{http.MethodPost, "/api/v1/session", "", false},
		{http.MethodGet, "/api/v1/projects", authorization.PermissionProjectsRead, true},
		{http.MethodPost, "/api/v1/projects", authorization.PermissionProjectsWrite, true},
		{http.MethodPost, "/api/v1/assets", authorization.PermissionAssetsUpload, true},
		{http.MethodPatch, "/api/v1/assets/a", authorization.PermissionAssetsUpload, true},
		{http.MethodGet, "/api/v1/assets/a/versions", authorization.PermissionAssetsRead, true},
		{http.MethodGet, "/api/v1/authorized-roots", authorization.PermissionStorageManage, true},
		{http.MethodGet, "/api/v1/authorized-roots/r", authorization.PermissionStorageManage, true},
		{http.MethodGet, "/api/v1/authorized-roots/r/objects", authorization.PermissionStorageManage, true},
		{http.MethodGet, "/api/v1/authorized-roots/r/objects/metadata", authorization.PermissionStorageManage, true},
		{http.MethodGet, "/api/v1/authorized-roots/r/objects/content", authorization.PermissionStorageManage, true},
		{http.MethodGet, "/api/v1/authorized-roots/r/library", authorization.PermissionAssetsRead, true},
		{http.MethodPost, "/api/v1/authorized-roots/r/scan", authorization.PermissionStorageManage, true},
		{http.MethodPost, "/api/v1/review-sessions/r/threads", authorization.PermissionReviewsComment, true},
		{http.MethodPost, "/api/v1/review-sessions/r/decisions", authorization.PermissionReviewsDecide, true},
		{http.MethodGet, "/api/v1/review-templates", authorization.PermissionReviewsRead, true},
		{http.MethodPost, "/api/v1/review-templates", authorization.PermissionReviewsWrite, true},
		{http.MethodPost, "/api/v1/shares", authorization.PermissionSharesManage, true},
		{http.MethodGet, "/api/v1/shares/s/access-events", authorization.PermissionAuditRead, true},
		{http.MethodPost, "/api/v1/notification-channels", authorization.PermissionNotificationsManage, true},
		{http.MethodPost, "/api/v1/notification-deliveries/d/retry", authorization.PermissionNotificationsManage, true},
		{http.MethodGet, "/api/v1/notification-preferences/me", authorization.PermissionNotifications, true},
		{http.MethodGet, "/api/v1/members", authorization.PermissionMembersManage, true},
		{http.MethodPatch, "/api/v1/members/m", authorization.PermissionMembersManage, true},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		permission, protected := managementPermission(request)
		if protected != test.protected || permission != test.permission {
			t.Fatalf(
				"%s %s => (%q, %t), want (%q, %t)",
				test.method,
				test.path,
				permission,
				protected,
				test.permission,
				test.protected,
			)
		}
	}
}
