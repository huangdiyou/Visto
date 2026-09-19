package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/catalog"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/invitation"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectmember"
	"review-studio.local/core/internal/projectstorage"
	"review-studio.local/core/internal/ratelimit"
	reviewdomain "review-studio.local/core/internal/review"
	"review-studio.local/core/internal/reviewtemplate"
	"review-studio.local/core/internal/serverupdate"
	sharedomain "review-studio.local/core/internal/share"
	"review-studio.local/core/internal/storage"
	"review-studio.local/core/internal/systemsettings"
	"review-studio.local/core/internal/workspace"
	"review-studio.local/core/internal/workspacesettings"
)

const (
	sessionCookieName      = "rs_session"
	mutationHeaderName     = "X-Review-Studio-Request"
	mutationHeaderValue    = "1"
	maxJSONBodyBytes       = 64 * 1024
	maxVersionBodyBytes    = int64(20<<30) + int64(1<<20)
	shareSessionCookieName = "rs_share_session"
	hostSessionCookieName  = "visto_host_session"
)

type Config struct {
	Version             string
	Logger              *slog.Logger
	Identity            *identity.Service
	Catalog             *catalog.Service
	Storage             *storage.Service
	Media               *media.Service
	Library             *media.LibraryService
	Renditions          *media.RenditionService
	Jobs                *job.Service
	Reviews             *reviewdomain.Service
	ReviewTemplates     *reviewtemplate.Service
	Shares              *sharedomain.Service
	Audit               *audit.Service
	Notifications       *notification.Service
	Authorization       *authorization.Service
	ProjectAccess       *projectaccess.Service
	ProjectMembers      *projectmember.Service
	ProjectStorage      *projectstorage.Service
	Members             *workspace.Service
	Invitations         *invitation.Service
	WorkspaceSettings   *workspacesettings.Service
	SystemSettings      *systemsettings.Service
	RequireRemoteHTTPS  bool
	VideoAcceleration   media.VideoAccelerationSettings
	HostManagementToken string
	TrustedProxyCIDRs   []string
	RateLimits          *ratelimit.Service
	// UpdateSources, UpdatePublicKey, and UpdateRootPublicKey come from the
	// deployment environment. Without sources the Owner update page never
	// contacts a public platform and only offers offline guidance.
	UpdateSources       []string
	UpdatePublicKey     string
	UpdateRootPublicKey string
	// DeploymentKind selects which update commands the Owner page shows.
	// Empty means auto-detect (docker, windows-server, or source).
	DeploymentKind string
	// Commands are the same resolved executables used by Core's media workers.
	// They stay internal; HTTP responses expose availability only.
	FFmpegCommand  string
	FFprobeCommand string
}

type handler struct {
	version             string
	logger              *slog.Logger
	identity            *identity.Service
	catalog             *catalog.Service
	storage             *storage.Service
	media               *media.Service
	library             *media.LibraryService
	renditions          *media.RenditionService
	jobs                *job.Service
	reviews             *reviewdomain.Service
	reviewTemplates     *reviewtemplate.Service
	shares              *sharedomain.Service
	audit               *audit.Service
	notifications       *notification.Service
	authorization       *authorization.Service
	projectAccess       *projectaccess.Service
	projectMembers      *projectmember.Service
	projectStorage      *projectstorage.Service
	members             *workspace.Service
	invitations         *invitation.Service
	workspaceSettings   *workspacesettings.Service
	systemSettings      *systemsettings.Service
	ffmpegAvailable     bool
	ffprobeAvailable    bool
	videoAcceleration   media.VideoAccelerationSettings
	hostManagementToken string
	trustedProxyCIDRs   []*net.IPNet
	rateLimits          *ratelimit.Service
	requireRemoteHTTPS  atomic.Bool
	updateSources       []string
	updatePublicKey     string
	updateRootPublicKey string
	deploymentKind      string
	updateCheckCache    updateCheckCache
	// attachmentUploadSlots bounds concurrent comment-attachment uploads, which
	// buffer and decode entire images in memory before storage.
	attachmentUploadSlots chan struct{}
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

type systemInfo struct {
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	APIVersion string     `json:"apiVersion"`
	Mode       string     `json:"mode"`
	Database   string     `json:"database"`
	Media      mediaInfo  `json:"media"`
	Access     accessInfo `json:"access"`
}

type mediaInfo struct {
	FFmpegAvailable           bool   `json:"ffmpegAvailable"`
	FFprobeAvailable          bool   `json:"ffprobeAvailable"`
	VideoAccelerationMode     string `json:"videoAccelerationMode"`
	VideoEncoder              string `json:"videoEncoder"`
	VideoFallbackEncoder      string `json:"videoFallbackEncoder"`
	VideoHardwareAcceleration bool   `json:"videoHardwareAcceleration"`
}

type setupStatusResponse struct {
	SetupRequired     bool `json:"setupRequired"`
	HostClaimRequired bool `json:"hostClaimRequired"`
}

type hostClaimRequest struct {
	Token string `json:"token"`
}

type setupRequest struct {
	WorkspaceName string `json:"workspaceName"`
	OwnerName     string `json:"ownerName"`
	OwnerEmail    string `json:"ownerEmail"`
	Password      string `json:"password"`
	Locale        string `json:"locale"`
	Timezone      string `json:"timezone"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type profileRequest struct {
	DisplayName string `json:"displayName"`
	Locale      string `json:"locale"`
}

type sessionResponse struct {
	User      userResponse      `json:"user"`
	Workspace workspaceResponse `json:"workspace"`
	Role      string            `json:"role"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type userResponse struct {
	ID          string  `json:"id"`
	Email       *string `json:"email"`
	DisplayName string  `json:"displayName"`
	Locale      string  `json:"locale"`
}

type workspaceResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TeamName string `json:"teamName"`
	Timezone string `json:"timezone"`
}

func NewHandler(config Config) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if config.Identity == nil {
		panic("httpapi: identity service is required")
	}
	if config.Catalog == nil {
		panic("httpapi: catalog service is required")
	}
	if config.Storage == nil {
		panic("httpapi: storage service is required")
	}
	if config.Media == nil {
		panic("httpapi: media service is required")
	}
	if config.Library == nil {
		panic("httpapi: media library service is required")
	}
	if config.Renditions == nil {
		panic("httpapi: rendition service is required")
	}
	if config.Jobs == nil {
		panic("httpapi: job service is required")
	}
	if config.Reviews == nil {
		panic("httpapi: review service is required")
	}
	if config.Shares == nil {
		panic("httpapi: share service is required")
	}
	if config.Audit == nil {
		panic("httpapi: audit service is required")
	}
	if config.Notifications == nil {
		panic("httpapi: notification service is required")
	}
	if config.Authorization == nil {
		panic("httpapi: authorization service is required")
	}
	if config.ProjectAccess == nil {
		panic("httpapi: project access service is required")
	}
	if config.ProjectMembers == nil {
		panic("httpapi: project member service is required")
	}
	if config.ProjectStorage == nil {
		panic("httpapi: project storage service is required")
	}
	if config.Members == nil {
		panic("httpapi: workspace member service is required")
	}
	if config.Invitations == nil {
		panic("httpapi: invitation service is required")
	}
	if config.WorkspaceSettings == nil {
		panic("httpapi: workspace settings service is required")
	}
	if config.SystemSettings == nil {
		panic("httpapi: system settings service is required")
	}
	if config.ReviewTemplates == nil {
		panic("httpapi: review template service is required")
	}

	videoAcceleration := config.VideoAcceleration
	if videoAcceleration.Mode == "" {
		videoAcceleration = media.VideoAccelerationSettingsForMode("")
	}

	ffmpegCommand, ffprobeCommand := config.FFmpegCommand, config.FFprobeCommand
	if ffmpegCommand == "" {
		ffmpegCommand = "ffmpeg"
	}
	if ffprobeCommand == "" {
		ffprobeCommand = "ffprobe"
	}
	h := &handler{
		version:             config.Version,
		logger:              logger,
		identity:            config.Identity,
		catalog:             config.Catalog,
		storage:             config.Storage,
		media:               config.Media,
		library:             config.Library,
		renditions:          config.Renditions,
		jobs:                config.Jobs,
		reviews:             config.Reviews,
		reviewTemplates:     config.ReviewTemplates,
		shares:              config.Shares,
		audit:               config.Audit,
		notifications:       config.Notifications,
		authorization:       config.Authorization,
		projectAccess:       config.ProjectAccess,
		projectMembers:      config.ProjectMembers,
		projectStorage:      config.ProjectStorage,
		members:             config.Members,
		invitations:         config.Invitations,
		workspaceSettings:   config.WorkspaceSettings,
		systemSettings:      config.SystemSettings,
		ffmpegAvailable:     executableAvailable(ffmpegCommand),
		ffprobeAvailable:    executableAvailable(ffprobeCommand),
		videoAcceleration:   videoAcceleration,
		hostManagementToken: strings.TrimSpace(config.HostManagementToken),
		trustedProxyCIDRs:   parseTrustedProxyCIDRs(config.TrustedProxyCIDRs),
		rateLimits:          config.RateLimits,
		attachmentUploadSlots: make(
			chan struct{},
			maxConcurrentCommentAttachmentUploads,
		),
	}
	h.requireRemoteHTTPS.Store(config.RequireRemoteHTTPS)
	h.updateSources = config.UpdateSources
	h.updatePublicKey = strings.TrimSpace(config.UpdatePublicKey)
	h.updateRootPublicKey = strings.TrimSpace(config.UpdateRootPublicKey)
	h.deploymentKind = config.DeploymentKind
	if h.deploymentKind == "" {
		h.deploymentKind = serverupdate.DetectDeploymentKind("")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", h.handleLive)
	mux.HandleFunc("GET /health/ready", h.handleReady)
	mux.HandleFunc("GET /api/v1/system/info", h.handleSystemInfo)
	mux.HandleFunc("GET /api/v1/setup/status", h.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup/claim", h.handleHostClaim)
	mux.HandleFunc("POST /api/v1/setup", h.handleSetup)
	mux.HandleFunc("GET /api/v1/session", h.handleCurrentSession)
	mux.HandleFunc("POST /api/v1/session", h.handleLogin)
	mux.HandleFunc("DELETE /api/v1/session", h.handleLogout)
	mux.HandleFunc("PATCH /api/v1/profile", h.handleUpdateProfile)
	mux.HandleFunc(
		"GET /api/v1/workspace/registration-settings",
		h.handleGetRegistrationSettings,
	)
	mux.HandleFunc(
		"PUT /api/v1/workspace/registration-settings",
		h.handleUpdateRegistrationSettings,
	)
	mux.HandleFunc(
		"GET /api/v1/system/network-settings",
		h.handleGetSystemNetworkSettings,
	)
	mux.HandleFunc(
		"PUT /api/v1/system/network-settings",
		h.handleUpdateSystemNetworkSettings,
	)
	mux.HandleFunc(
		"GET /api/v1/system/update-status",
		h.handleSystemUpdateStatus,
	)
	mux.HandleFunc(
		"GET /join-api/v1/registration",
		h.handlePublicRegistrationSettings,
	)
	mux.HandleFunc(
		"POST /join-api/v1/registration",
		h.handlePublicRegistration,
	)
	mux.HandleFunc("GET /api/v1/members", h.handleListMemberships)
	mux.HandleFunc(
		"PATCH /api/v1/members/{membershipId}",
		h.handleUpdateMembership,
	)
	mux.HandleFunc("GET /api/v1/invitations", h.handleListInvitations)
	mux.HandleFunc("POST /api/v1/invitations", h.handleCreateInvitation)
	mux.HandleFunc(
		"POST /api/v1/invitations/{invitationId}/resend",
		h.handleResendInvitation,
	)
	mux.HandleFunc(
		"POST /api/v1/invitations/{invitationId}/revoke",
		h.handleRevokeInvitation,
	)
	mux.HandleFunc(
		"POST /join-api/v1/invitations/preview",
		h.handlePreviewInvitation,
	)
	mux.HandleFunc(
		"POST /join-api/v1/invitations/accept",
		h.handleAcceptInvitation,
	)
	mux.HandleFunc("GET /api/v1/review-templates", h.handleListReviewTemplates)
	mux.HandleFunc("POST /api/v1/review-templates", h.handleCreateReviewTemplate)
	mux.HandleFunc(
		"PATCH /api/v1/review-templates/{templateId}",
		h.handleUpdateReviewTemplate,
	)
	mux.HandleFunc(
		"DELETE /api/v1/review-templates/{templateId}",
		h.handleDeleteReviewTemplate,
	)
	mux.HandleFunc("GET /api/v1/audit-logs", h.handleListAuditLogs)
	mux.HandleFunc("GET /api/v1/notifications", h.handleListNotifications)
	mux.HandleFunc(
		"POST /api/v1/notifications/{notificationId}/read",
		h.handleMarkNotificationRead,
	)
	mux.HandleFunc(
		"POST /api/v1/notifications/read-all",
		h.handleMarkAllNotificationsRead,
	)
	mux.HandleFunc(
		"GET /api/v1/notification-channels",
		h.handleListNotificationChannels,
	)
	mux.HandleFunc(
		"POST /api/v1/notification-channels",
		h.handleCreateNotificationChannel,
	)
	mux.HandleFunc(
		"PATCH /api/v1/notification-channels/{channelId}",
		h.handleUpdateNotificationChannel,
	)
	mux.HandleFunc(
		"DELETE /api/v1/notification-channels/{channelId}",
		h.handleDeleteNotificationChannel,
	)
	mux.HandleFunc(
		"POST /api/v1/notification-channels/{channelId}/test",
		h.handleTestNotificationChannel,
	)
	mux.HandleFunc(
		"GET /api/v1/notification-preferences/me",
		h.handleGetNotificationPreferences,
	)
	mux.HandleFunc(
		"PUT /api/v1/notification-preferences/me",
		h.handleUpdateNotificationPreferences,
	)
	mux.HandleFunc(
		"GET /api/v1/notification-deliveries",
		h.handleListNotificationDeliveries,
	)
	mux.HandleFunc(
		"POST /api/v1/notification-deliveries/{deliveryId}/retry",
		h.handleRetryNotificationDelivery,
	)
	mux.HandleFunc("GET /api/v1/jobs", h.handleListJobs)
	mux.HandleFunc("GET /api/v1/jobs/{jobId}", h.handleGetJob)
	mux.HandleFunc("POST /api/v1/jobs/{jobId}/retry", h.handleRetryJob)
	mux.HandleFunc("POST /api/v1/jobs/{jobId}/cancel", h.handleCancelJob)
	mux.HandleFunc("GET /api/v1/upload-checks", h.handleListUploadChecks)
	mux.HandleFunc(
		"POST /api/v1/upload-checks/{checkId}/release",
		h.handleReleaseUploadCheck,
	)
	mux.HandleFunc(
		"POST /api/v1/upload-checks/{checkId}/reject",
		h.handleRejectUploadCheck,
	)
	mux.HandleFunc("GET /api/v1/review-sessions", h.handleListReviewSessions)
	mux.HandleFunc("POST /api/v1/review-sessions", h.handleCreateReviewSession)
	mux.HandleFunc(
		"GET /api/v1/review-sessions/{reviewId}",
		h.handleGetReviewSession,
	)
	mux.HandleFunc(
		"PATCH /api/v1/review-sessions/{reviewId}",
		h.handleUpdateReviewSession,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/open",
		h.handleOpenReviewSession,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/close",
		h.handleCloseReviewSession,
	)
	mux.HandleFunc(
		"GET /api/v1/review-sessions/{reviewId}/threads",
		h.handleListReviewThreads,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/threads",
		h.handleCreateReviewThread,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/attachments",
		h.handleUploadReviewCommentAttachment,
	)
	mux.HandleFunc(
		"GET /api/v1/review-sessions/{reviewId}/items/{itemId}/attachments/{attachmentId}/content",
		h.handleReviewCommentAttachmentContent,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/threads/{threadId}/comments",
		h.handleAddReviewThreadComment,
	)
	mux.HandleFunc(
		"PATCH /api/v1/review-sessions/{reviewId}/threads/{threadId}/comments/{commentId}",
		h.handleUpdateReviewComment,
	)
	mux.HandleFunc(
		"DELETE /api/v1/review-sessions/{reviewId}/threads/{threadId}/comments/{commentId}",
		h.handleDeleteReviewComment,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/threads/{threadId}/resolve",
		h.handleResolveReviewThread,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/threads/{threadId}/reopen",
		h.handleReopenReviewThread,
	)
	mux.HandleFunc(
		"GET /api/v1/review-sessions/{reviewId}/decisions",
		h.handleListReviewDecisions,
	)
	mux.HandleFunc(
		"POST /api/v1/review-sessions/{reviewId}/decisions",
		h.handleCreateReviewDecision,
	)
	mux.HandleFunc("GET /api/v1/shares", h.handleListShares)
	mux.HandleFunc("POST /api/v1/shares", h.handleCreateShare)
	mux.HandleFunc("GET /api/v1/shares/{shareId}", h.handleGetShare)
	mux.HandleFunc("PATCH /api/v1/shares/{shareId}", h.handleUpdateShare)
	mux.HandleFunc(
		"GET /api/v1/shares/{shareId}/credentials",
		h.handleGetShareCredentials,
	)
	mux.HandleFunc("POST /api/v1/shares/{shareId}/links", h.handleCreateShareLink)
	mux.HandleFunc(
		"POST /api/v1/shares/{shareId}/visitor-codes",
		h.handleCreateShareVisitorCode,
	)
	mux.HandleFunc("POST /api/v1/shares/{shareId}/revoke", h.handleRevokeShare)
	mux.HandleFunc(
		"GET /api/v1/shares/{shareId}/access-events",
		h.handleListShareAccessEvents,
	)
	mux.HandleFunc(
		"POST /api/v1/share-links/{linkId}/revoke",
		h.handleRevokeShareLink,
	)
	mux.HandleFunc(
		"POST /share-api/v1/entries/{token}/open",
		h.handleOpenShareEntry,
	)
	mux.HandleFunc(
		"POST /share-api/v1/entries/{token}/verify",
		h.handleVerifySharePassword,
	)
	mux.HandleFunc(
		"POST /share-api/v1/session/verify",
		h.handleVerifySharePassword,
	)
	mux.HandleFunc(
		"POST /share-api/v1/session/identity",
		h.handleIdentifyPublicShare,
	)
	mux.HandleFunc("GET /share-api/v1/share", h.handlePublicShare)
	mux.HandleFunc("GET /share-api/v1/items", h.handlePublicShareItems)
	mux.HandleFunc(
		"GET /share-api/v1/items/{itemId}/content",
		h.handlePublicShareContent,
	)
	mux.HandleFunc(
		"GET /share-api/v1/items/{itemId}/hls/{fileName}",
		h.handlePublicShareHLS,
	)
	mux.HandleFunc(
		"GET /share-api/v1/items/{itemId}/download",
		h.handlePublicShareDownload,
	)
	mux.HandleFunc(
		"GET /share-api/v1/items/{itemId}/threads",
		h.handleListPublicThreads,
	)
	mux.HandleFunc(
		"POST /share-api/v1/items/{itemId}/threads",
		h.handleCreatePublicThread,
	)
	mux.HandleFunc(
		"POST /share-api/v1/items/{itemId}/attachments",
		h.handleUploadPublicCommentAttachment,
	)
	mux.HandleFunc(
		"GET /share-api/v1/items/{itemId}/attachments/{attachmentId}/content",
		h.handlePublicCommentAttachmentContent,
	)
	mux.HandleFunc(
		"POST /share-api/v1/items/{itemId}/threads/{threadId}/comments",
		h.handleAddPublicThreadComment,
	)
	mux.HandleFunc(
		"PATCH /share-api/v1/items/{itemId}/threads/{threadId}/comments/{commentId}",
		h.handleUpdatePublicComment,
	)
	mux.HandleFunc(
		"DELETE /share-api/v1/items/{itemId}/threads/{threadId}/comments/{commentId}",
		h.handleDeletePublicComment,
	)
	mux.HandleFunc(
		"POST /share-api/v1/decisions",
		h.handleCreatePublicDecision,
	)
	mux.HandleFunc("GET /api/v1/projects", h.handleListProjects)
	mux.HandleFunc("POST /api/v1/projects", h.handleCreateProject)
	mux.HandleFunc("GET /api/v1/projects/{projectId}", h.handleGetProject)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/overview",
		h.handleGetProjectOverview,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/activity",
		h.handleListProjectActivity,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/library",
		h.handleQueryProjectMediaLibrary,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/asset-candidates",
		h.handleQueryProjectMediaCandidates,
	)
	mux.HandleFunc("PATCH /api/v1/projects/{projectId}", h.handleUpdateProject)
	mux.HandleFunc("POST /api/v1/projects/{projectId}/archive", h.handleArchiveProject)
	mux.HandleFunc("POST /api/v1/projects/{projectId}/restore", h.handleRestoreProject)
	mux.HandleFunc("DELETE /api/v1/projects/{projectId}", h.handleDeleteProject)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/members",
		h.handleListProjectMembers,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/members",
		h.handleAddProjectMember,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/guests",
		h.handleCreateProjectGuest,
	)
	mux.HandleFunc(
		"PATCH /api/v1/projects/{projectId}/members/{membershipId}",
		h.handleUpdateProjectMember,
	)
	mux.HandleFunc(
		"DELETE /api/v1/projects/{projectId}/members/{membershipId}",
		h.handleRemoveProjectMember,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/transfer",
		h.handleTransferProjectPrimaryOwner,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/asset-trash",
		h.handleListProjectAssetTrash,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/assets",
		h.handleAddProjectAsset,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/assets/{assetId}/move",
		h.handleMoveProjectAsset,
	)
	mux.HandleFunc(
		"DELETE /api/v1/projects/{projectId}/assets/{assetId}",
		h.handleTrashProjectAsset,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/asset-trash/{assetId}/restore",
		h.handleRestoreProjectAsset,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/storage-selections",
		h.handleListProjectStorageSelections,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/upload-target",
		h.handleGetProjectUploadTarget,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/storage-grants",
		h.handleListAvailableProjectStorageGrants,
	)
	mux.HandleFunc(
		"PUT /api/v1/projects/{projectId}/storage-selections/{purpose}",
		h.handleSelectProjectStorage,
	)
	mux.HandleFunc(
		"GET /api/v1/projects/{projectId}/collections",
		h.handleListCollections,
	)
	mux.HandleFunc(
		"POST /api/v1/projects/{projectId}/collections",
		h.handleCreateCollection,
	)
	mux.HandleFunc("GET /api/v1/collections/{collectionId}", h.handleGetCollection)
	mux.HandleFunc(
		"PATCH /api/v1/collections/{collectionId}",
		h.handleUpdateCollection,
	)
	mux.HandleFunc(
		"DELETE /api/v1/collections/{collectionId}",
		h.handleDeleteCollection,
	)
	mux.HandleFunc(
		"GET /api/v1/collections/{collectionId}/items",
		h.handleListCollectionItems,
	)
	mux.HandleFunc(
		"POST /api/v1/collections/{collectionId}/items",
		h.handleAddCollectionItem,
	)
	mux.HandleFunc(
		"PATCH /api/v1/collections/{collectionId}/items/order",
		h.handleReorderCollectionItems,
	)
	mux.HandleFunc(
		"DELETE /api/v1/collections/{collectionId}/items/{itemId}",
		h.handleDeleteCollectionItem,
	)
	mux.HandleFunc(
		"GET /api/v1/storage-providers",
		h.handleListStorageProviders,
	)
	mux.HandleFunc(
		"POST /api/v1/storage-providers",
		h.handleCreateStorageProvider,
	)
	mux.HandleFunc(
		"PATCH /api/v1/storage-providers/{providerId}",
		h.handleUpdateStorageProvider,
	)
	mux.HandleFunc(
		"GET /api/v1/storage-providers/{providerId}/delete-impact",
		h.handleGetStorageProviderDeleteImpact,
	)
	mux.HandleFunc(
		"DELETE /api/v1/storage-providers/{providerId}",
		h.handleDeleteStorageProvider,
	)
	mux.HandleFunc(
		"POST /api/v1/storage-providers/{providerId}/test",
		h.handleTestStorageProvider,
	)
	mux.HandleFunc(
		"POST /api/v1/storage-providers/{providerId}/roots",
		h.handleCreateStorageProviderRoot,
	)
	mux.HandleFunc(
		"POST /api/v1/storage-copy-tasks",
		h.handleCreateStorageCopyTask,
	)
	mux.HandleFunc(
		"GET /api/v1/storage-copy-tasks/{taskId}",
		h.handleGetStorageCopyTask,
	)
	mux.HandleFunc(
		"GET /api/v1/project-storage-grants/available",
		h.handleListProjectCreationStorageGrants,
	)
	mux.HandleFunc(
		"GET /api/v1/project-storage-grants",
		h.handleListProjectStorageGrants,
	)
	mux.HandleFunc(
		"PUT /api/v1/project-storage-grants",
		h.handleSetProjectStorageGrant,
	)
	mux.HandleFunc(
		"GET /api/v1/local-managed-buckets",
		h.handleListLocalManagedBuckets,
	)
	mux.HandleFunc(
		"POST /api/v1/local-managed-buckets",
		h.handleCreateLocalManagedBucket,
	)
	mux.HandleFunc(
		"PATCH /api/v1/local-managed-buckets/{bucketId}",
		h.handleUpdateLocalManagedBucket,
	)
	mux.HandleFunc(
		"GET /api/v1/local-managed-buckets/{bucketId}/delete-impact",
		h.handleGetLocalManagedBucketDeleteImpact,
	)
	mux.HandleFunc(
		"DELETE /api/v1/local-managed-buckets/{bucketId}",
		h.handleDeleteLocalManagedBucket,
	)
	mux.HandleFunc("GET /api/v1/authorized-roots", h.handleListAuthorizedRoots)
	mux.HandleFunc("POST /api/v1/authorized-roots", h.handleCreateAuthorizedRoot)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}",
		h.handleGetAuthorizedRoot,
	)
	mux.HandleFunc(
		"PATCH /api/v1/authorized-roots/{rootId}",
		h.handleUpdateAuthorizedRoot,
	)
	mux.HandleFunc(
		"DELETE /api/v1/authorized-roots/{rootId}",
		h.handleDeleteAuthorizedRoot,
	)
	mux.HandleFunc(
		"POST /api/v1/authorized-roots/{rootId}/scan",
		h.handleScanAuthorizedRoot,
	)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}/objects",
		h.handleListStorageObjects,
	)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}/library",
		h.handleQueryMediaLibrary,
	)
	mux.HandleFunc(
		"PATCH /api/v1/storage-objects/{objectId}/project",
		h.handleAssignMediaAssetProject,
	)
	mux.HandleFunc("POST /api/v1/assets", h.handleUploadAsset)
	mux.HandleFunc(
		"PATCH /api/v1/assets/{assetId}",
		h.handleUpdateMediaAsset,
	)
	mux.HandleFunc(
		"GET /api/v1/assets/{assetId}/versions",
		h.handleListAssetVersions,
	)
	mux.HandleFunc(
		"POST /api/v1/assets/{assetId}/versions",
		h.handleUploadAssetVersion,
	)
	mux.HandleFunc(
		"POST /api/v1/assets/{assetId}/current-version",
		h.handleSetCurrentAssetVersion,
	)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}/media-probes",
		h.handleListMediaProbes,
	)
	mux.HandleFunc(
		"POST /api/v1/authorized-roots/{rootId}/media-probes",
		h.handleProbeAuthorizedRoot,
	)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}/renditions",
		h.handleListRenditions,
	)
	mux.HandleFunc(
		"POST /api/v1/authorized-roots/{rootId}/renditions",
		h.handleGenerateRenditions,
	)
	mux.HandleFunc(
		"POST /api/v1/authorized-roots/{rootId}/video-renditions",
		h.handleGenerateVideoRenditions,
	)
	mux.HandleFunc(
		"GET /api/v1/renditions/{renditionId}/content",
		h.handleRenditionContent,
	)
	mux.HandleFunc(
		"GET /api/v1/renditions/{renditionId}/hls/{fileName}",
		h.handleRenditionSegment,
	)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}/objects/metadata",
		h.handleLocalObjectMetadata,
	)
	mux.HandleFunc(
		"GET /api/v1/authorized-roots/{rootId}/objects/content",
		h.handleLocalObjectContent,
	)

	return h.securityHeaders(
		h.requestLog(
			h.requestID(
				h.transportSecurity(
					h.mutationGuard(
						h.permissionGuard(mux),
					),
				),
			),
		),
	)
}

func (h *handler) handleLive(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) handleReady(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{
		"status": "ready",
		"dependencies": map[string]bool{
			"ffmpeg":  h.ffmpegAvailable,
			"ffprobe": h.ffprobeAvailable,
		},
	})
}

func (h *handler) handleSystemInfo(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, systemInfo{
		Name:       "Review Studio Core",
		Version:    h.version,
		APIVersion: "v1",
		Mode:       "local",
		Database:   "sqlite",
		Media: mediaInfo{
			FFmpegAvailable:           h.ffmpegAvailable,
			FFprobeAvailable:          h.ffprobeAvailable,
			VideoAccelerationMode:     h.videoAcceleration.Mode,
			VideoEncoder:              h.videoAcceleration.Encoder,
			VideoFallbackEncoder:      h.videoAcceleration.FallbackEncoder,
			VideoHardwareAcceleration: h.videoAcceleration.Hardware,
		},
		Access: h.requestAccessInfo(request),
	})
}

func (h *handler) handleSetupStatus(response http.ResponseWriter, request *http.Request) {
	complete, err := h.identity.IsSetupComplete(request.Context())
	if err != nil {
		h.internalError(response, request, err)
		return
	}

	writeJSON(response, http.StatusOK, setupStatusResponse{
		SetupRequired:     !complete,
		HostClaimRequired: !complete && h.hostManagementToken != "" && !h.requestHasHostManagement(request),
	})
}

func (h *handler) handleHostClaim(response http.ResponseWriter, request *http.Request) {
	complete, err := h.identity.IsSetupComplete(request.Context())
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	if complete {
		writeError(response, http.StatusConflict, requestID(response), "setup.already_complete", "首次设置已经完成")
		return
	}
	if h.hostManagementToken == "" {
		writeError(response, http.StatusNotFound, requestID(response), "setup.host_claim_unavailable", "当前部署不需要初始化令牌")
		return
	}
	var body hostClaimRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !secureStringEqual(h.hostManagementToken, strings.TrimSpace(body.Token)) {
		writeError(response, http.StatusForbidden, requestID(response), "setup.host_claim_invalid", "初始化令牌无效")
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     hostSessionCookieName,
		Value:    hostSessionValue(h.hostManagementToken),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.secureCookie(request),
		MaxAge:   int((8 * time.Hour).Seconds()),
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleSetup(response http.ResponseWriter, request *http.Request) {
	if !h.requireHostManagement(response, request) {
		return
	}
	var body setupRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}

	result, err := h.identity.Setup(request.Context(), identity.SetupInput{
		WorkspaceName: body.WorkspaceName,
		OwnerName:     body.OwnerName,
		OwnerEmail:    body.OwnerEmail,
		Password:      body.Password,
		Locale:        defaultString(body.Locale, "zh-CN"),
		Timezone:      defaultString(body.Timezone, "Asia/Shanghai"),
	})
	if errors.Is(err, identity.ErrSetupAlreadyCompleted) {
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"setup.already_completed",
			"首次设置已经完成",
		)
		return
	}
	if err != nil {
		if isValidationError(err) {
			h.badRequest(response, request, err)
			return
		}
		h.internalError(response, request, err)
		return
	}

	h.setSessionCookie(response, request, result.Token, result.Session.ExpiresAt)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  result.Session.Workspace.ID,
		ActorType:    "user",
		ActorID:      result.Session.User.ID,
		Action:       "identity.workspace_setup",
		ResourceType: "workspace",
		ResourceID:   result.Session.Workspace.ID,
	})
	writeJSON(response, http.StatusCreated, toSessionResponse(result.Session))
}

func (h *handler) handleCurrentSession(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, err := h.authenticateRequest(request)
	if errors.Is(err, identity.ErrSessionNotFound) {
		writeError(
			response,
			http.StatusUnauthorized,
			requestID(response),
			"auth.required",
			"请先登录",
		)
		return
	}
	if err != nil {
		h.internalError(response, request, err)
		return
	}

	writeJSON(response, http.StatusOK, toSessionResponse(session))
}

func (h *handler) handleUpdateProfile(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body profileRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	user, err := h.identity.UpdateProfile(
		request.Context(),
		identity.UpdateProfileInput{
			UserID:      session.User.ID,
			DisplayName: body.DisplayName,
			Locale:      firstNonEmpty(body.Locale, session.User.Locale),
		},
	)
	if errors.Is(err, identity.ErrUserNotFound) {
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"identity.user_not_found",
			"当前账号不存在",
		)
		return
	}
	if err != nil {
		h.badRequest(response, request, err)
		return
	}
	session.User = user
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "identity.profile_updated",
		ResourceType: "user",
		ResourceID:   session.User.ID,
	})
	writeJSON(response, http.StatusOK, toSessionResponse(session))
}

func (h *handler) handleLogin(response http.ResponseWriter, request *http.Request) {
	var body loginRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	accountKey := loginAccountKey(body.Email)
	sourceKey := requestRateLimitSource(request)
	if !h.consumeRateLimit(response, request, "login-source", sourceKey, 30, 5*time.Minute) ||
		!h.consumeRateLimit(response, request, "login-account", accountKey, 10, 5*time.Minute) {
		return
	}

	result, err := h.identity.LoginAccount(request.Context(), identity.LoginInput{
		Email:    body.Email,
		Password: body.Password,
	})
	if errors.Is(err, identity.ErrSetupRequired) {
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"setup.required",
			"请先完成首次设置",
		)
		return
	}
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeError(
			response,
			http.StatusUnauthorized,
			requestID(response),
			"auth.invalid",
			"邮箱或密码不正确",
		)
		return
	}
	if err != nil {
		h.internalError(response, request, err)
		return
	}

	h.clearRateLimit(request, "login-account", accountKey)
	h.clearRateLimit(request, "login-source", sourceKey)
	h.setSessionCookie(response, request, result.Token, result.Session.ExpiresAt)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  result.Session.Workspace.ID,
		ActorType:    "user",
		ActorID:      result.Session.User.ID,
		Action:       "identity.session_created",
		ResourceType: "session",
		ResourceID:   result.Session.ID,
	})
	writeJSON(response, http.StatusOK, toSessionResponse(result.Session))
}

func (h *handler) handleLogout(response http.ResponseWriter, request *http.Request) {
	token := sessionToken(request)
	session, sessionErr := h.identity.Authenticate(request.Context(), token)
	if err := h.identity.Logout(request.Context(), token); err != nil {
		h.internalError(response, request, err)
		return
	}

	h.clearSessionCookie(response, request)
	if sessionErr == nil {
		h.recordAudit(request, audit.RecordLogInput{
			WorkspaceID:  session.Workspace.ID,
			ActorType:    "user",
			ActorID:      session.User.ID,
			Action:       "identity.session_revoked",
			ResourceType: "session",
			ResourceID:   session.ID,
		})
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) authenticateRequest(request *http.Request) (identity.Session, error) {
	return h.identity.Authenticate(request.Context(), sessionToken(request))
}

func (h *handler) badRequest(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	writeError(
		response,
		http.StatusBadRequest,
		requestID(response),
		"request.invalid",
		err.Error(),
	)
}

func (h *handler) internalError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	h.logger.Error(
		"request failed",
		"request_id", requestID(response),
		"method", request.Method,
		"path", safeRequestPath(request.URL.Path),
		"error", err,
	)
	writeError(
		response,
		http.StatusInternalServerError,
		requestID(response),
		"internal.unexpected",
		"服务暂时无法完成这个操作",
	)
}

func (h *handler) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := randomID()
		response.Header().Set("X-Request-Id", requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (h *handler) mutationGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if isSafeMethod(request.Method) ||
			request.Header.Get(mutationHeaderName) == mutationHeaderValue {
			next.ServeHTTP(response, request)
			return
		}

		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"request.cross_site_rejected",
			"写操作缺少有效的本地请求标识",
		)
	})
}

func (h *handler) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}

		next.ServeHTTP(recorder, request)

		h.logger.Info(
			"http request",
			"method", request.Method,
			"path", safeRequestPath(request.URL.Path),
			"status", recorder.status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"request_id", response.Header().Get("X-Request-Id"),
		)
	})
}

func safeRequestPath(value string) string {
	const prefix = "/share-api/v1/entries/"
	if !strings.HasPrefix(value, prefix) {
		return value
	}
	remainder := strings.TrimPrefix(value, prefix)
	if separator := strings.IndexByte(remainder, '/'); separator >= 0 {
		return prefix + "<redacted>" + remainder[separator:]
	}
	return prefix + "<redacted>"
}

func (h *handler) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		// SAR-F61: share responses are authorized per visitor through cookie or
		// bearer sessions. Default them to private, no-store so a shared or
		// misconfigured cache cannot reuse one visitor's share data for another.
		// Handlers that serve immutable media may still override this header.
		if strings.HasPrefix(request.URL.Path, "/share-api/") {
			response.Header().Set("Cache-Control", "private, no-store")
		}
		next.ServeHTTP(response, request)
	})
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)

	if err := json.NewEncoder(response).Encode(body); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

func writeError(response http.ResponseWriter, status int, requestID, code, message string) {
	writeJSON(response, status, errorEnvelope{
		Error: apiError{
			Code:      code,
			Message:   message,
			RequestID: requestID,
		},
	})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(response, request.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("请求内容无效: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("请求只能包含一个 JSON 对象")
	}

	return nil
}

func toSessionResponse(session identity.Session) sessionResponse {
	return sessionResponse{
		User: userResponse{
			ID:          session.User.ID,
			Email:       session.User.Email,
			DisplayName: session.User.DisplayName,
			Locale:      session.User.Locale,
		},
		Workspace: workspaceResponse{
			ID:       session.Workspace.ID,
			Name:     session.Workspace.Name,
			TeamName: session.Workspace.TeamName,
			Timezone: session.Workspace.Timezone,
		},
		Role:      session.Role,
		ExpiresAt: session.ExpiresAt,
	}
}

func (h *handler) setSessionCookie(
	response http.ResponseWriter,
	request *http.Request,
	token string,
	expiresAt time.Time,
) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookie(request),
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func (h *handler) clearSessionCookie(response http.ResponseWriter, request *http.Request) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookie(request),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func sessionToken(request *http.Request) string {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}

	return cookie.Value
}

// secureCookie reports whether the current request actually travelled over
// HTTPS, which is the only thing the Cookie Secure attribute should depend on.
//
// Three sources count, in order of directness: TLS terminated by Core itself, an
// allowlisted proxy asserting the outer connection was HTTPS, and the explicit
// REVIEW_STUDIO_SECURE_COOKIES=1 deployment declaration for gateways that do not
// forward the scheme. A forwarded header from a peer outside the allowlist is
// untrusted input and is ignored.
//
// There is deliberately no Owner-facing toggle for this: a separate cookie
// switch would be hard to reason about and easy to set inconsistently with the
// real transport. It is also independent of the "remote access requires HTTPS"
// policy, which governs whether plaintext requests are served at all.
func (h *handler) secureCookie(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	if os.Getenv("REVIEW_STUDIO_SECURE_COOKIES") == "1" {
		return true
	}
	peer, ok := requestClientIP(request)
	if !ok || !h.trustedProxyContains(peer) {
		return false
	}
	return strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https")
}

func requestID(response http.ResponseWriter) string {
	return response.Header().Get("X-Request-Id")
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isValidationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "must contain") ||
		strings.Contains(message, "is required") ||
		strings.Contains(message, "is invalid") ||
		strings.Contains(message, "must be in the future")
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func executableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func randomID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "req_unavailable"
	}

	return "req_" + hex.EncodeToString(bytes[:])
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

type requestIDContextKey struct{}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}
