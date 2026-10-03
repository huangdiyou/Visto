package httpapi

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectmember"
	sharedomain "review-studio.local/core/internal/share"
	"review-studio.local/core/internal/storage"
)

type shareRequest struct {
	ReviewSessionID string     `json:"reviewSessionId"`
	Name            string     `json:"name"`
	AllowComment    bool       `json:"allowComment"`
	AllowDownload   bool       `json:"allowDownload"`
	RequireNickname bool       `json:"requireNickname"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	Password        *string    `json:"password"`
	NotifyUserIDs   []string   `json:"notifyUserIds"`
	Revision        int        `json:"revision,omitempty"`
}

type shareLinkResponse struct {
	ID          string     `json:"id"`
	ShareID     string     `json:"shareId"`
	TokenPrefix string     `json:"tokenPrefix"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
}

type visitorCodeRequest struct {
	DisplayName string     `json:"displayName"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

type visitorCodeResponse struct {
	ID          string     `json:"id"`
	ShareID     string     `json:"shareId"`
	CodePrefix  string     `json:"codePrefix"`
	DisplayName string     `json:"displayName"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
	Code        string     `json:"code,omitempty"`
}

type shareResponse struct {
	ID                string              `json:"id"`
	ReviewSessionID   *string             `json:"reviewSessionId"`
	ReviewSessionName *string             `json:"reviewSessionName"`
	Name              string              `json:"name"`
	Status            string              `json:"status"`
	AllowComment      bool                `json:"allowComment"`
	AllowDownload     bool                `json:"allowDownload"`
	RequireNickname   bool                `json:"requireNickname"`
	ExpiresAt         *time.Time          `json:"expiresAt"`
	PasswordProtected bool                `json:"passwordProtected"`
	Revision          int                 `json:"revision"`
	CreatedAt         time.Time           `json:"createdAt"`
	UpdatedAt         time.Time           `json:"updatedAt"`
	RevokedAt         *time.Time          `json:"revokedAt"`
	Links             []shareLinkResponse `json:"links"`
}

type shareListResponse struct {
	Items []shareResponse `json:"items"`
}

type shareSecretResponse struct {
	Share    *shareResponse    `json:"share,omitempty"`
	Link     shareLinkResponse `json:"link"`
	URL      string            `json:"url"`
	Password *string           `json:"password,omitempty"`
}

type shareCredentialLinkResponse struct {
	LinkID string `json:"linkId"`
	URL    string `json:"url"`
}

type shareCredentialsResponse struct {
	ShareID  string                        `json:"shareId"`
	Password *string                       `json:"password"`
	Links    []shareCredentialLinkResponse `json:"links"`
}

type publicShareResponse struct {
	Name            string                `json:"name"`
	ReviewName      string                `json:"reviewName"`
	TeamName        string                `json:"teamName"`
	ReviewStatus    string                `json:"reviewStatus"`
	AllowComment    bool                  `json:"allowComment"`
	AllowDownload   bool                  `json:"allowDownload"`
	RequireNickname bool                  `json:"requireNickname"`
	ExpiresAt       *time.Time            `json:"expiresAt"`
	Visitor         publicVisitorResponse `json:"visitor"`
	Items           []publicItemResponse  `json:"items"`
}

type publicVisitorResponse struct {
	DisplayName    *string `json:"displayName"`
	IdentityMethod string  `json:"identityMethod"`
	Identified     bool    `json:"identified"`
	Verified       bool    `json:"verified"`
}

type publicItemResponse struct {
	ID            string  `json:"id"`
	AssetName     string  `json:"assetName"`
	VersionNumber int     `json:"versionNumber"`
	MediaType     string  `json:"mediaType"`
	DurationUs    *int64  `json:"durationUs"`
	Width         *int    `json:"width"`
	Height        *int    `json:"height"`
	SizeBytes     int64   `json:"sizeBytes"`
	PreviewKind   *string `json:"previewKind"`
	PreviewURL    *string `json:"previewUrl"`
	ThumbnailURL  *string `json:"thumbnailUrl"`
	DownloadURL   *string `json:"downloadUrl"`
}

type publicEntryResponse struct {
	Status string               `json:"status"`
	Share  *publicShareResponse `json:"share,omitempty"`
}

type verifyShareRequest struct {
	Password string `json:"password"`
}

type identifyShareRequest struct {
	Method      string `json:"method"`
	DisplayName string `json:"displayName"`
	Code        string `json:"code"`
}

func (h *handler) handleListShares(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	items, err := h.shares.List(request.Context(), session.Workspace.ID)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]shareResponse, 0, len(items))
	for _, item := range items {
		allowed, err := h.shareProjectAllowed(
			request,
			session,
			item,
			projectaccess.PermissionProjectRead,
		)
		if err != nil {
			h.internalError(response, request, err)
			return
		}
		if !allowed {
			continue
		}
		result = append(result, toShareResponse(item))
	}
	writeJSON(response, http.StatusOK, shareListResponse{Items: result})
}

func (h *handler) handleGetShare(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	item, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		item,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	writeJSON(response, http.StatusOK, toShareResponse(item))
}

func (h *handler) handleCreateShare(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body shareRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	password := ""
	if body.Password != nil {
		password = *body.Password
	}
	review, err := h.reviews.Get(
		request.Context(),
		session.Workspace.ID,
		body.ReviewSessionID,
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		review.ProjectID,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	notifyUserIDs, err := h.validShareNotificationRecipients(
		request,
		response,
		session.Workspace.ID,
		review.ProjectID,
		body.NotifyUserIDs,
	)
	if err != nil {
		return
	}
	result, err := h.shares.Create(request.Context(), sharedomain.CreateInput{
		WorkspaceID:     session.Workspace.ID,
		UserID:          session.User.ID,
		ReviewSessionID: body.ReviewSessionID,
		Name:            body.Name,
		AllowComment:    body.AllowComment,
		AllowDownload:   body.AllowDownload,
		RequireNickname: body.RequireNickname,
		ExpiresAt:       body.ExpiresAt,
		Password:        password,
	})
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "share.created",
		ResourceType: "share",
		ResourceID:   result.Share.ID,
		After: map[string]any{
			"name":            result.Share.Name,
			"allowComment":    result.Share.AllowComment,
			"allowDownload":   result.Share.AllowDownload,
			"requireNickname": result.Share.RequireNickname,
		},
	})
	if len(notifyUserIDs) > 0 {
		if err := h.notifications.CreateForUsers(
			request.Context(),
			notification.CreateForUsersInput{
				WorkspaceID:      session.Workspace.ID,
				RecipientUserIDs: notifyUserIDs,
				Type:             "share.created",
				ResourceType:     "share",
				ResourceID:       result.Share.ID,
				Title:            "新的审阅分享",
				Body: fmt.Sprintf(
					"%s 创建了审阅分享：%s",
					session.User.DisplayName,
					review.Name,
				),
			},
		); err != nil {
			h.logger.Error(
				"share notification creation failed",
				"error", err,
				"share_id", result.Share.ID,
			)
		}
	}
	item := toShareResponse(result.Share)
	writeJSON(response, http.StatusCreated, shareSecretResponse{
		Share:    &item,
		Link:     toShareLinkResponse(result.Link.Link),
		URL:      publicShareURL(result.Link.Token),
		Password: optionalResponseString(password),
	})
}

func (h *handler) handleUpdateShare(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body shareRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	existing, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		existing,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	item, err := h.shares.Update(request.Context(), sharedomain.UpdateInput{
		WorkspaceID:     session.Workspace.ID,
		ID:              request.PathValue("shareId"),
		Name:            body.Name,
		AllowComment:    body.AllowComment,
		AllowDownload:   body.AllowDownload,
		RequireNickname: body.RequireNickname,
		ExpiresAt:       body.ExpiresAt,
		Password:        body.Password,
		Revision:        body.Revision,
	})
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "share.updated",
		ResourceType: "share",
		ResourceID:   item.ID,
		After: map[string]any{
			"name":            item.Name,
			"allowComment":    item.AllowComment,
			"allowDownload":   item.AllowDownload,
			"requireNickname": item.RequireNickname,
		},
	})
	writeJSON(response, http.StatusOK, toShareResponse(item))
}

func (h *handler) handleCreateShareLink(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	existing, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		existing,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	link, err := h.shares.CreateLink(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "share.link_created",
		ResourceType: "share",
		ResourceID:   request.PathValue("shareId"),
		After:        map[string]any{"linkId": link.Link.ID},
	})
	writeJSON(response, http.StatusCreated, shareSecretResponse{
		Link: toShareLinkResponse(link.Link),
		URL:  publicShareURL(link.Token),
	})
}

func (h *handler) handleGetShareCredentials(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	existing, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		existing,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	credentials, err := h.shares.Credentials(
		request.Context(),
		session.Workspace.ID,
		existing.ID,
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	result := shareCredentialsResponse{
		ShareID:  credentials.ShareID,
		Password: credentials.Password,
		Links:    make([]shareCredentialLinkResponse, 0, len(credentials.Links)),
	}
	for _, link := range credentials.Links {
		result.Links = append(result.Links, shareCredentialLinkResponse{
			LinkID: link.LinkID,
			URL:    publicShareURL(link.Token),
		})
	}
	writeJSON(response, http.StatusOK, result)
}

func (h *handler) handleCreateShareVisitorCode(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body visitorCodeRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	existing, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		existing,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	item, err := h.shares.CreateVisitorCode(
		request.Context(),
		sharedomain.CreateVisitorCodeInput{
			WorkspaceID: session.Workspace.ID,
			ShareID:     request.PathValue("shareId"),
			UserID:      session.User.ID,
			DisplayName: body.DisplayName,
			ExpiresAt:   body.ExpiresAt,
		},
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "share.visitor_code_created",
		ResourceType: "share",
		ResourceID:   request.PathValue("shareId"),
		After: map[string]any{
			"visitorCodeId": item.ID,
			"displayName":   item.DisplayName,
		},
	})
	writeJSON(response, http.StatusCreated, toVisitorCodeResponse(item))
}

func (h *handler) handleRevokeShare(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	existing, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("shareId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		existing,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	item, err := h.shares.Revoke(request.Context(), sharedomain.StateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          request.PathValue("shareId"),
		Revision:    body.Revision,
	})
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "share.revoked",
		ResourceType: "share",
		ResourceID:   item.ID,
	})
	writeJSON(response, http.StatusOK, toShareResponse(item))
}

func (h *handler) handleRevokeShareLink(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	existing, err := h.shares.GetByLink(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("linkId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	if !h.requireShareProjectPermission(
		response,
		request,
		session,
		existing,
		projectaccess.PermissionSharesCreate,
	) {
		return
	}
	item, err := h.shares.RevokeLink(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("linkId"),
	)
	if err != nil {
		h.handleShareError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "share.link_revoked",
		ResourceType: "share_link",
		ResourceID:   item.ID,
	})
	writeJSON(response, http.StatusOK, toShareLinkResponse(item))
}

func (h *handler) requireShareProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	item sharedomain.Share,
	permission projectaccess.Permission,
) bool {
	allowed, err := h.shareProjectAllowed(request, session, item, permission)
	if err != nil {
		h.internalError(response, request, err)
		return false
	}
	if allowed {
		return true
	}
	writeError(
		response,
		http.StatusForbidden,
		requestID(response),
		"permission.project_denied",
		"当前账号不能操作这个项目",
	)
	return false
}

func (h *handler) shareProjectAllowed(
	request *http.Request,
	session identity.Session,
	item sharedomain.Share,
	permission projectaccess.Permission,
) (bool, error) {
	if item.ReviewSessionID == nil {
		return session.Role == "owner", nil
	}
	review, err := h.reviews.Get(
		request.Context(),
		session.Workspace.ID,
		*item.ReviewSessionID,
	)
	if err != nil {
		return false, err
	}
	return h.projectAccess.Allowed(
		request.Context(),
		projectaccess.CheckInput{
			WorkspaceID:   session.Workspace.ID,
			ProjectID:     review.ProjectID,
			UserID:        session.User.ID,
			WorkspaceRole: session.Role,
			Permission:    permission,
		},
	)
}

func (h *handler) validShareNotificationRecipients(
	request *http.Request,
	response http.ResponseWriter,
	workspaceID string,
	projectID string,
	rawUserIDs []string,
) ([]string, error) {
	seen := make(map[string]struct{}, len(rawUserIDs))
	ids := make([]string, 0, len(rawUserIDs))
	for _, id := range rawUserIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	members, err := h.projectMembers.List(
		request.Context(),
		workspaceID,
		projectID,
	)
	if err != nil {
		h.handleProjectMemberError(response, request, err)
		return nil, err
	}
	active := make(map[string]struct{}, len(members))
	now := time.Now().UTC()
	for _, member := range members {
		if member.Status != "active" {
			continue
		}
		if member.ExpiresAt != nil && !member.ExpiresAt.After(now) {
			continue
		}
		active[member.UserID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := active[id]; !ok {
			writeError(
				response,
				http.StatusBadRequest,
				requestID(response),
				"share.notify_user_invalid",
				"通知对象必须是当前项目的有效成员",
			)
			return nil, projectmember.ErrInvalidInput
		}
	}
	return ids, nil
}

func (h *handler) handleOpenShareEntry(
	response http.ResponseWriter,
	request *http.Request,
) {
	entryToken := request.PathValue("token")
	entryRateKey := shareEntryRateLimitKey(
		entryToken,
		h.requestRateLimitClientIdentity(request),
	)
	if !h.consumeRateLimit(
		response,
		request,
		"share-entry",
		entryRateKey,
		20,
		5*time.Minute,
	) {
		return
	}
	result, err := h.shares.Open(
		request.Context(),
		entryToken,
	)
	if err != nil {
		h.handlePublicShareError(response, request, err)
		return
	}
	h.recordAccess(request, result.Share, "opened", "share", result.Share.ID)
	h.setShareSessionCookie(
		response,
		request,
		result.SessionToken,
		result.SessionExpiresAt,
	)
	if result.PasswordRequired {
		writeJSON(response, http.StatusOK, publicEntryResponse{
			Status: "password_required",
		})
		return
	}
	item := toPublicShareResponse(result.Share)
	writeJSON(response, http.StatusOK, publicEntryResponse{
		Status: "ready",
		Share:  &item,
	})
}

func shareEntryRateLimitKey(entryToken string, clientIdentity string) string {
	entryDigest := sha256.Sum256([]byte(entryToken))
	return fmt.Sprintf("share:%x|client:%s", entryDigest, clientIdentity)
}

func (h *handler) handleVerifySharePassword(
	response http.ResponseWriter,
	request *http.Request,
) {
	var body verifyShareRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.shares.Verify(
		request.Context(),
		shareSessionToken(request),
		body.Password,
		h.requestRateLimitClientIdentity(request),
	)
	if err != nil {
		h.handlePublicShareError(response, request, err)
		return
	}
	h.recordAccess(request, item, "verified", "share", item.ID)
	result := toPublicShareResponse(item)
	writeJSON(response, http.StatusOK, publicEntryResponse{
		Status: "ready",
		Share:  &result,
	})
}

func (h *handler) handleIdentifyPublicShare(
	response http.ResponseWriter,
	request *http.Request,
) {
	var body identifyShareRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.shares.Identify(request.Context(), sharedomain.IdentifyInput{
		SessionToken:   shareSessionToken(request),
		Method:         body.Method,
		DisplayName:    body.DisplayName,
		Code:           body.Code,
		ClientIdentity: h.requestRateLimitClientIdentity(request),
	})
	if err != nil {
		h.handlePublicShareError(response, request, err)
		return
	}
	h.recordAccess(request, item, "identified", "share", item.ID)
	writeJSON(response, http.StatusOK, toPublicShareResponse(item))
}

func (h *handler) handlePublicShare(
	response http.ResponseWriter,
	request *http.Request,
) {
	item, ok := h.publicShareSession(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, toPublicShareResponse(item))
}

func (h *handler) handlePublicShareItems(
	response http.ResponseWriter,
	request *http.Request,
) {
	item, ok := h.publicShareSession(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"items": toPublicShareResponse(item).Items,
	})
}

func (h *handler) handlePublicShareContent(
	response http.ResponseWriter,
	request *http.Request,
) {
	item, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	_ = item
	if publicItem.PreviewRenditionID == nil {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrItemNotFound,
		)
		return
	}
	rendition, reader, _, err := h.renditions.Open(
		request.Context(),
		item.WorkspaceID,
		*publicItem.PreviewRenditionID,
	)
	if err != nil {
		if errors.Is(err, media.ErrRenditionNotFound) {
			h.handlePublicShareError(
				response,
				request,
				sharedomain.ErrItemNotFound,
			)
			return
		}
		h.internalError(response, request, err)
		return
	}
	defer reader.Close()
	h.recordAccess(request, item, "viewed", "review_item", publicItem.ID)
	mimeType := "application/octet-stream"
	if rendition.MIMEType != nil {
		mimeType = *rendition.MIMEType
	}
	response.Header().Set("Content-Type", mimeType)
	response.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(
		response,
		request,
		publicItem.AssetName,
		rendition.UpdatedAt,
		reader,
	)
}

func (h *handler) handlePublicShareHLS(
	response http.ResponseWriter,
	request *http.Request,
) {
	item, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	if publicItem.PreviewRenditionID == nil ||
		publicItem.PreviewRenditionKind == nil ||
		*publicItem.PreviewRenditionKind != media.RenditionHLS {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrItemNotFound,
		)
		return
	}
	fileName := request.PathValue("fileName")
	if fileName == "index.m3u8" {
		rendition, reader, _, err := h.renditions.Open(
			request.Context(),
			item.WorkspaceID,
			*publicItem.PreviewRenditionID,
		)
		if err != nil {
			h.handlePublicShareRenditionError(response, request, err)
			return
		}
		defer reader.Close()
		h.recordAccess(request, item, "viewed", "review_item", publicItem.ID)
		mimeType := "application/vnd.apple.mpegurl"
		if rendition.MIMEType != nil {
			mimeType = *rendition.MIMEType
		}
		response.Header().Set("Content-Type", mimeType)
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(
			response,
			request,
			"index.m3u8",
			rendition.UpdatedAt,
			reader,
		)
		return
	}
	segment, reader, _, err := h.renditions.OpenSegment(
		request.Context(),
		item.WorkspaceID,
		*publicItem.PreviewRenditionID,
		fileName,
	)
	if err != nil {
		h.handlePublicShareRenditionError(response, request, err)
		return
	}
	defer reader.Close()
	response.Header().Set("Content-Type", segment.MIMEType)
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(
		response,
		request,
		segment.FileName,
		segment.CreatedAt,
		reader,
	)
}

func (h *handler) handlePublicShareDownload(
	response http.ResponseWriter,
	request *http.Request,
) {
	item, publicItem, ok := h.publicShareItem(response, request)
	if !ok {
		return
	}
	if !item.AllowDownload {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrDownloadDenied,
		)
		return
	}
	object, err := h.storage.Object(
		request.Context(),
		item.WorkspaceID,
		publicItem.SourceStorageObjectID,
	)
	if err != nil {
		h.handlePublicShareStorageError(response, request, err)
		return
	}
	adapter, err := h.storage.Adapter(
		request.Context(),
		item.WorkspaceID,
		object.AuthorizedRootID,
	)
	if err != nil {
		h.handlePublicShareStorageError(response, request, err)
		return
	}
	byteRange, partial, err := parseRangeHeader(
		request.Header.Get("Range"),
		object.SizeBytes,
	)
	if err != nil {
		response.Header().Set(
			"Content-Range",
			fmt.Sprintf("bytes */%d", object.SizeBytes),
		)
		h.handlePublicShareStorageError(response, request, err)
		return
	}
	reader, info, err := adapter.OpenRange(
		request.Context(),
		object.ObjectKey,
		byteRange,
	)
	if err != nil {
		h.handlePublicShareStorageError(response, request, err)
		return
	}
	defer reader.Close()
	h.recordAccess(request, item, "downloaded", "review_item", publicItem.ID)

	length := byteRange.Length
	if length == 0 {
		length = info.SizeBytes - byteRange.Offset
	}
	filename := filepath.Base(publicItem.SourceFilename)
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Type", defaultString(info.MIMEType, "application/octet-stream"))
	response.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	response.Header().Set("Content-Disposition", mime.FormatMediaType(
		"attachment",
		map[string]string{"filename": filename},
	))
	response.Header().Set("Cache-Control", "private, no-store")
	if partial {
		response.Header().Set(
			"Content-Range",
			fmt.Sprintf(
				"bytes %d-%d/%d",
				byteRange.Offset,
				byteRange.Offset+length-1,
				info.SizeBytes,
			),
		)
		response.WriteHeader(http.StatusPartialContent)
	} else {
		response.WriteHeader(http.StatusOK)
	}
	if _, err := io.Copy(response, reader); err != nil &&
		!errors.Is(err, request.Context().Err()) {
		h.logger.Warn(
			"public share download interrupted",
			"request_id", requestID(response),
			"error", err,
		)
	}
}

func (h *handler) handlePublicShareRenditionError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	if errors.Is(err, media.ErrRenditionNotFound) {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrItemNotFound,
		)
		return
	}
	h.internalError(response, request, err)
}

func (h *handler) publicShareSession(
	response http.ResponseWriter,
	request *http.Request,
) (sharedomain.PublicShare, bool) {
	item, err := h.shares.PublicSession(
		request.Context(),
		shareSessionToken(request),
	)
	if err != nil {
		h.handlePublicShareError(response, request, err)
		return sharedomain.PublicShare{}, false
	}
	return item, true
}

func (h *handler) publicShareItem(
	response http.ResponseWriter,
	request *http.Request,
) (sharedomain.PublicShare, sharedomain.PublicItem, bool) {
	item, ok := h.publicShareSession(response, request)
	if !ok {
		return sharedomain.PublicShare{}, sharedomain.PublicItem{}, false
	}
	itemID := request.PathValue("itemId")
	for _, candidate := range item.Items {
		if candidate.ID == itemID {
			return item, candidate, true
		}
	}
	h.handlePublicShareError(response, request, sharedomain.ErrItemNotFound)
	return sharedomain.PublicShare{}, sharedomain.PublicItem{}, false
}

func (h *handler) handleShareError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, sharedomain.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"share.not_found", "分享不存在")
	case errors.Is(err, sharedomain.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"share.revision_conflict", "分享已被更新，请刷新后重试")
	case errors.Is(err, sharedomain.ErrInvalidReview):
		writeError(response, http.StatusBadRequest, requestID(response),
			"share.invalid_review", "这个审阅会话当前无法分享")
	case errors.Is(err, sharedomain.ErrInvalidState):
		writeError(response, http.StatusConflict, requestID(response),
			"share.invalid_state", "分享当前无法执行这个操作")
	default:
		if isValidationError(err) {
			h.badRequest(response, request, err)
			return
		}
		h.internalError(response, request, err)
	}
}

func (h *handler) handlePublicShareError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, sharedomain.ErrPasswordRequired):
		writeError(response, http.StatusUnauthorized, requestID(response),
			"share.password_required", "请输入分享访问密码")
	case errors.Is(err, sharedomain.ErrPasswordInvalid):
		writeError(response, http.StatusUnauthorized, requestID(response),
			"share.password_invalid", "访问密码不正确")
	case errors.Is(err, sharedomain.ErrRateLimited):
		response.Header().Set("Retry-After", "300")
		writeError(response, http.StatusTooManyRequests, requestID(response),
			"share.rate_limited", "分享访问或密码尝试次数过多，请稍后再试")
	case errors.Is(err, sharedomain.ErrDownloadDenied):
		writeError(response, http.StatusForbidden, requestID(response),
			"share.download_denied", "这个分享不允许下载源文件")
	case errors.Is(err, sharedomain.ErrCommentDenied):
		writeError(response, http.StatusForbidden, requestID(response),
			"share.comment_denied", "这个分享不允许发表评论")
	case errors.Is(err, sharedomain.ErrItemNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"share.item_not_found", "分享中的版本不存在或暂不可用")
	case errors.Is(err, sharedomain.ErrIdentityRequired):
		writeError(response, http.StatusBadRequest, requestID(response),
			"share.identity_required", "请先填写访客身份")
	case errors.Is(err, sharedomain.ErrVisitorCodeInvalid):
		writeError(response, http.StatusUnauthorized, requestID(response),
			"share.visitor_code_invalid", "访客验证码无效或已过期")
	case errors.Is(err, sharedomain.ErrEntryUnavailable),
		errors.Is(err, sharedomain.ErrSessionNotFound):
		h.clearShareSessionCookie(response, request)
		writeError(response, http.StatusNotFound, requestID(response),
			"share.unavailable", "分享已撤销、已过期或不可用")
	default:
		h.internalError(response, request, err)
	}
}

func (h *handler) handlePublicShareStorageError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, storage.ErrObjectNotFound),
		errors.Is(err, storage.ErrRootNotFound),
		errors.Is(err, storage.ErrRootUnavailable):
		h.handlePublicShareError(response, request, sharedomain.ErrItemNotFound)
	case errors.Is(err, storage.ErrRangeInvalid):
		writeError(response, http.StatusRequestedRangeNotSatisfiable,
			requestID(response), "share.range_invalid", "请求的文件范围无效")
	default:
		h.internalError(response, request, err)
	}
}

func toShareResponse(item sharedomain.Share) shareResponse {
	result := shareResponse{
		ID:                item.ID,
		ReviewSessionID:   item.ReviewSessionID,
		ReviewSessionName: item.ReviewSessionName,
		Name:              item.Name,
		Status:            item.Status,
		AllowComment:      item.AllowComment,
		AllowDownload:     item.AllowDownload,
		RequireNickname:   item.RequireNickname,
		ExpiresAt:         item.ExpiresAt,
		PasswordProtected: item.PasswordProtected,
		Revision:          item.Revision,
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
		RevokedAt:         item.RevokedAt,
		Links:             make([]shareLinkResponse, 0, len(item.Links)),
	}
	for _, link := range item.Links {
		result.Links = append(result.Links, toShareLinkResponse(link))
	}
	return result
}

func toShareLinkResponse(item sharedomain.Link) shareLinkResponse {
	return shareLinkResponse{
		ID:          item.ID,
		ShareID:     item.ShareID,
		TokenPrefix: item.TokenPrefix,
		Status:      item.Status,
		CreatedAt:   item.CreatedAt,
		LastUsedAt:  item.LastUsedAt,
		RevokedAt:   item.RevokedAt,
	}
}

func toVisitorCodeResponse(item sharedomain.VisitorCodeSecret) visitorCodeResponse {
	return visitorCodeResponse{
		ID:          item.ID,
		ShareID:     item.ShareID,
		CodePrefix:  item.CodePrefix,
		DisplayName: item.DisplayName,
		Status:      item.Status,
		ExpiresAt:   item.ExpiresAt,
		CreatedAt:   item.CreatedAt,
		LastUsedAt:  item.LastUsedAt,
		RevokedAt:   item.RevokedAt,
		Code:        item.Code,
	}
}

func toPublicShareResponse(item sharedomain.PublicShare) publicShareResponse {
	result := publicShareResponse{
		Name:            item.Name,
		ReviewName:      item.ReviewName,
		TeamName:        item.TeamName,
		ReviewStatus:    item.ReviewStatus,
		AllowComment:    item.AllowComment,
		AllowDownload:   item.AllowDownload,
		RequireNickname: item.RequireNickname,
		ExpiresAt:       item.ExpiresAt,
		Visitor: publicVisitorResponse{
			DisplayName:    item.Visitor.DisplayName,
			IdentityMethod: item.Visitor.IdentityMethod,
			Identified:     item.Visitor.Identified,
			Verified:       item.Visitor.Verified,
		},
		Items: make([]publicItemResponse, 0, len(item.Items)),
	}
	for _, publicItem := range item.Items {
		var previewURL, thumbnailURL, downloadURL *string
		if publicItem.PreviewRenditionID != nil {
			value := "/share-api/v1/items/" + publicItem.ID + "/content"
			if publicItem.PreviewRenditionKind != nil &&
				*publicItem.PreviewRenditionKind == media.RenditionHLS {
				value = "/share-api/v1/items/" + publicItem.ID + "/hls/index.m3u8"
			}
			previewURL = &value
		}
		if publicItem.ThumbnailRenditionID != nil {
			value := "/share-api/v1/items/" + publicItem.ID + "/content"
			thumbnailURL = &value
		}
		if item.AllowDownload {
			value := "/share-api/v1/items/" + publicItem.ID + "/download"
			downloadURL = &value
		}
		result.Items = append(result.Items, publicItemResponse{
			ID:            publicItem.ID,
			AssetName:     publicItem.AssetName,
			VersionNumber: publicItem.VersionNumber,
			MediaType:     publicItem.MediaType,
			DurationUs:    publicItem.DurationUs,
			Width:         publicItem.Width,
			Height:        publicItem.Height,
			SizeBytes:     publicItem.SourceSizeBytes,
			PreviewKind:   publicItem.PreviewRenditionKind,
			PreviewURL:    previewURL,
			ThumbnailURL:  thumbnailURL,
			DownloadURL:   downloadURL,
		})
	}
	return result
}

func publicShareURL(token string) string {
	return "/s/#" + token
}

func optionalResponseString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func shareSessionToken(request *http.Request) string {
	cookie, err := request.Cookie(shareSessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (h *handler) setShareSessionCookie(
	response http.ResponseWriter,
	request *http.Request,
	token string,
	expiresAt time.Time,
) {
	http.SetCookie(response, &http.Cookie{
		Name:     shareSessionCookieName,
		Value:    token,
		Path:     "/share-api",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie(request),
		Expires:  expiresAt,
		MaxAge:   max(1, int(time.Until(expiresAt).Seconds())),
	})
}

func (h *handler) clearShareSessionCookie(
	response http.ResponseWriter,
	request *http.Request,
) {
	http.SetCookie(response, &http.Cookie{
		Name:     shareSessionCookieName,
		Value:    "",
		Path:     "/share-api",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie(request),
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}
