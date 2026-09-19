package httpapi

import (
	"net/http"
	"strings"

	"review-studio.local/core/internal/authorization"
)

func (h *handler) permissionGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		permission, protected := managementPermission(request)
		if !protected {
			next.ServeHTTP(response, request)
			return
		}
		if _, ok := h.requirePermission(response, request, permission); !ok {
			return
		}
		next.ServeHTTP(response, request)
	})
}

func managementPermission(
	request *http.Request,
) (authorization.Permission, bool) {
	path := request.URL.Path
	if !strings.HasPrefix(path, "/api/v1/") {
		return "", false
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/invitations"):
		return authorization.PermissionMembersManage, true
	case strings.HasPrefix(path, "/api/v1/members"):
		return authorization.PermissionMembersManage, true
	case strings.HasPrefix(path, "/api/v1/audit-logs"),
		strings.HasSuffix(path, "/access-events"):
		return authorization.PermissionAuditRead, true
	case strings.HasPrefix(path, "/api/v1/notification-channels"),
		strings.HasPrefix(path, "/api/v1/notification-deliveries"):
		return authorization.PermissionNotificationsManage, true
	case strings.HasPrefix(path, "/api/v1/notification-preferences"):
		return authorization.PermissionNotifications, true
	case strings.HasPrefix(path, "/api/v1/notifications"):
		return authorization.PermissionNotifications, true
	case strings.HasPrefix(path, "/api/v1/jobs"):
		return authorization.PermissionStorageManage, true
	case strings.HasPrefix(path, "/api/v1/storage-providers"):
		return authorization.PermissionStorageManage, true
	case strings.HasPrefix(path, "/api/v1/storage-copy-tasks"):
		return authorization.PermissionStorageManage, true
	case strings.HasPrefix(path, "/api/v1/review-templates"):
		if request.Method == http.MethodGet {
			return authorization.PermissionReviewsRead, true
		}
		return authorization.PermissionReviewsWrite, true
	case strings.HasPrefix(path, "/api/v1/review-sessions"):
		if strings.Contains(path, "/attachments") {
			if request.Method == http.MethodGet {
				return authorization.PermissionReviewsRead, true
			}
			return authorization.PermissionReviewsComment, true
		}
		if strings.Contains(path, "/threads") {
			if request.Method == http.MethodGet {
				return authorization.PermissionReviewsRead, true
			}
			return authorization.PermissionReviewsComment, true
		}
		if strings.HasSuffix(path, "/decisions") {
			if request.Method == http.MethodGet {
				return authorization.PermissionReviewsRead, true
			}
			return authorization.PermissionReviewsDecide, true
		}
		if request.Method == http.MethodGet {
			return authorization.PermissionReviewsRead, true
		}
		return authorization.PermissionReviewsWrite, true
	case strings.HasPrefix(path, "/api/v1/shares"),
		strings.HasPrefix(path, "/api/v1/share-links"):
		return authorization.PermissionSharesManage, true
	case strings.HasPrefix(path, "/api/v1/projects/"),
		strings.HasPrefix(path, "/api/v1/collections/"):
		return "", false
	case strings.HasPrefix(path, "/api/v1/projects"):
		if request.Method == http.MethodGet {
			return authorization.PermissionProjectsRead, true
		}
		return authorization.PermissionProjectsWrite, true
	case strings.HasPrefix(path, "/api/v1/storage-objects"):
		return authorization.PermissionProjectsWrite, true
	case strings.HasPrefix(path, "/api/v1/assets"):
		if request.Method == http.MethodGet {
			return authorization.PermissionAssetsRead, true
		}
		return authorization.PermissionAssetsUpload, true
	case strings.HasPrefix(path, "/api/v1/authorized-roots"):
		// Raw object endpoints (list, metadata, content) enumerate storage
		// objects without project context. They are host-managed storage
		// administration and must not be reachable with member-level asset
		// permissions; require storage administration instead.
		if strings.Contains(path, "/objects") {
			return authorization.PermissionStorageManage, true
		}
		// The bare list and detail routes disclose host display paths, so they
		// are storage administration rather than member-level asset reads.
		// Media sub-resources (library, probes, renditions) stay member-readable.
		if path == "/api/v1/authorized-roots" ||
			!strings.Contains(strings.TrimPrefix(path, "/api/v1/authorized-roots/"), "/") {
			return authorization.PermissionStorageManage, true
		}
		if request.Method == http.MethodGet {
			return authorization.PermissionAssetsRead, true
		}
		return authorization.PermissionStorageManage, true
	case strings.HasPrefix(path, "/api/v1/renditions"):
		return authorization.PermissionAssetsRead, true
	default:
		return "", false
	}
}
