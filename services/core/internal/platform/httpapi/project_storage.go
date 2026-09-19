package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectstorage"
)

type projectStorageGrantRequest struct {
	StorageProviderID string  `json:"storageProviderId"`
	AuthorizedRootID  *string `json:"authorizedRootId"`
	Status            string  `json:"status"`
}

type projectStorageSelectionRequest struct {
	GrantID string `json:"grantId"`
}

type projectStorageGrantResponse struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspaceId"`
	StorageProviderID    string    `json:"storageProviderId"`
	AuthorizedRootID     *string   `json:"authorizedRootId"`
	ProviderName         string    `json:"providerName"`
	ProviderKind         string    `json:"providerKind"`
	ProviderStatus       string    `json:"providerStatus"`
	RootName             *string   `json:"rootName"`
	RootStatus           *string   `json:"rootStatus"`
	LocalManagedBucketID *string   `json:"localManagedBucketId"`
	BucketPurpose        *string   `json:"bucketPurpose"`
	Status               string    `json:"status"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type projectStorageGrantListResponse struct {
	Items []projectStorageGrantResponse `json:"items"`
}

type projectStorageSelectionResponse struct {
	ID          string                      `json:"id"`
	WorkspaceID string                      `json:"workspaceId"`
	ProjectID   string                      `json:"projectId"`
	GrantID     string                      `json:"grantId"`
	Purpose     string                      `json:"purpose"`
	SelectedBy  string                      `json:"selectedBy"`
	CreatedAt   time.Time                   `json:"createdAt"`
	UpdatedAt   time.Time                   `json:"updatedAt"`
	Grant       projectStorageGrantResponse `json:"grant"`
}

type projectStorageSelectionListResponse struct {
	Items []projectStorageSelectionResponse `json:"items"`
}

type projectUploadTargetResponse struct {
	ProjectID            string  `json:"projectId"`
	UploadTarget         string  `json:"uploadTarget"`
	UploadSecurityPolicy string  `json:"uploadSecurityPolicy"`
	StorageProviderID    string  `json:"storageProviderId"`
	AuthorizedRootID     string  `json:"authorizedRootId"`
	LocalManagedBucketID *string `json:"localManagedBucketId"`
	QuotaBytes           *int64  `json:"quotaBytes"`
	UsedBytesEstimate    int64   `json:"usedBytesEstimate"`
	QuotaAvailableBytes  *int64  `json:"quotaAvailableBytes"`
	MaxSingleFileBytes   int64   `json:"maxSingleFileBytes"`
}

func (h *handler) handleListProjectStorageGrants(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	items, err := h.projectStorage.ListGrants(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	result := make([]projectStorageGrantResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toProjectStorageGrantResponse(item))
	}
	writeJSON(response, http.StatusOK, projectStorageGrantListResponse{Items: result})
}

func (h *handler) handleListAvailableProjectStorageGrants(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectManage,
	) {
		return
	}
	// Available storage is configuration metadata, so only project managers may
	// enumerate it. Read-only members can still see the project's selected
	// storage through the selections endpoint.
	items, err := h.projectStorage.ListAvailableGrants(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	result := make([]projectStorageGrantResponse, 0, len(items))
	for _, item := range items {
		if item.Status == "active" {
			result = append(result, toProjectStorageGrantResponse(item))
		}
	}
	writeJSON(response, http.StatusOK, projectStorageGrantListResponse{Items: result})
}

func (h *handler) handleListProjectCreationStorageGrants(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !canCreateWorkspaceProject(session) {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_create_denied",
			"当前账号不能创建项目",
		)
		return
	}
	items, err := h.projectStorage.ListGrants(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	result := make([]projectStorageGrantResponse, 0, len(items))
	for _, item := range items {
		if projectStorageGrantSupportsUpload(item) {
			result = append(result, toProjectStorageGrantResponse(item))
		}
	}
	writeJSON(response, http.StatusOK, projectStorageGrantListResponse{Items: result})
}

func projectStorageGrantSupportsUpload(grant projectstorage.Grant) bool {
	if grant.Status != "active" || grant.ProviderStatus != "active" ||
		grant.AuthorizedRootID == nil || grant.RootStatus == nil ||
		*grant.RootStatus != "available" {
		return false
	}
	if grant.ProviderKind == "local" {
		return grant.LocalManagedBucketID != nil &&
			grant.BucketPurpose != nil && *grant.BucketPurpose == "upload"
	}
	return remoteProjectUploadGrantAllowed(grant)
}

func (h *handler) handleSetProjectStorageGrant(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body projectStorageGrantRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.projectStorage.SetGrant(
		request.Context(),
		projectstorage.SetGrantInput{
			WorkspaceID:       session.Workspace.ID,
			StorageProviderID: body.StorageProviderID,
			AuthorizedRootID:  body.AuthorizedRootID,
			Status:            defaultString(body.Status, "active"),
			GrantedBy:         session.User.ID,
		},
	)
	if err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project_storage.grant_set",
		ResourceType: "project_storage_grant",
		ResourceID:   item.ID,
		After: map[string]any{
			"providerId": item.StorageProviderID,
			"rootId":     item.AuthorizedRootID,
			"status":     item.Status,
		},
	})
	writeJSON(response, http.StatusOK, toProjectStorageGrantResponse(item))
}

func (h *handler) handleListProjectStorageSelections(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	items, err := h.projectStorage.ListSelections(
		request.Context(),
		session.Workspace.ID,
		projectID,
	)
	if err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	result := make([]projectStorageSelectionResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toProjectStorageSelectionResponse(item))
	}
	writeJSON(response, http.StatusOK, projectStorageSelectionListResponse{Items: result})
}

func (h *handler) handleGetProjectUploadTarget(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	projectIDPtr := &projectID
	if !h.requireUploadProjectPermission(
		response,
		request,
		session,
		&projectIDPtr,
	) {
		return
	}
	target, err := h.projectUploadTarget(
		request.Context(),
		session.Workspace.ID,
		projectIDPtr,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	if target == nil {
		h.handleLibraryError(response, request, media.ErrLibraryUploadTargetNeeded)
		return
	}
	if sizeBytesValue := request.URL.Query().Get("sizeBytes"); sizeBytesValue != "" {
		sizeBytes, parseErr := strconv.ParseInt(sizeBytesValue, 10, 64)
		if parseErr != nil || sizeBytes < 0 {
			writeError(
				response,
				http.StatusBadRequest,
				requestID(response),
				"project_storage.invalid_upload_size",
				"upload size is invalid",
			)
			return
		}
		if validateErr := target.ValidateUploadSize(sizeBytes); validateErr != nil {
			h.handleLibraryError(response, request, validateErr)
			return
		}
	}
	writeJSON(response, http.StatusOK, projectUploadTargetResponse{
		ProjectID:            target.ProjectID,
		UploadTarget:         uploadTargetKind(target),
		UploadSecurityPolicy: target.UploadSecurityPolicy,
		StorageProviderID:    target.StorageProviderID,
		AuthorizedRootID:     target.AuthorizedRootID,
		LocalManagedBucketID: nullableString(target.LocalManagedBucketID),
		QuotaBytes:           target.QuotaBytes,
		UsedBytesEstimate:    target.UsedBytesEstimate,
		QuotaAvailableBytes:  target.QuotaAvailableBytes,
		MaxSingleFileBytes:   target.MaxSingleFileBytes,
	})
}

func (h *handler) handleSelectProjectStorage(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectManage,
	) {
		return
	}
	var body projectStorageSelectionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.projectStorage.Select(
		request.Context(),
		projectstorage.SelectInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			GrantID:     body.GrantID,
			Purpose:     request.PathValue("purpose"),
			SelectedBy:  session.User.ID,
		},
	)
	if err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project_storage.selected",
		ResourceType: "project",
		ResourceID:   projectID,
		After: map[string]any{
			"grantId": item.GrantID,
			"purpose": item.Purpose,
		},
	})
	writeJSON(response, http.StatusOK, toProjectStorageSelectionResponse(item))
}

func toProjectStorageGrantResponse(
	item projectstorage.Grant,
) projectStorageGrantResponse {
	return projectStorageGrantResponse{
		ID:                   item.ID,
		WorkspaceID:          item.WorkspaceID,
		StorageProviderID:    item.StorageProviderID,
		AuthorizedRootID:     item.AuthorizedRootID,
		ProviderName:         item.ProviderName,
		ProviderKind:         item.ProviderKind,
		ProviderStatus:       item.ProviderStatus,
		RootName:             item.RootName,
		RootStatus:           item.RootStatus,
		LocalManagedBucketID: item.LocalManagedBucketID,
		BucketPurpose:        item.BucketPurpose,
		Status:               item.Status,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
	}
}

func toProjectStorageSelectionResponse(
	item projectstorage.Selection,
) projectStorageSelectionResponse {
	return projectStorageSelectionResponse{
		ID:          item.ID,
		WorkspaceID: item.WorkspaceID,
		ProjectID:   item.ProjectID,
		GrantID:     item.GrantID,
		Purpose:     item.Purpose,
		SelectedBy:  item.SelectedBy,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
		Grant:       toProjectStorageGrantResponse(item.Grant),
	}
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (h *handler) handleProjectStorageError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, projectstorage.ErrInvalidInput):
		h.badRequest(response, request, err)
	case errors.Is(err, projectstorage.ErrNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"project_storage.not_found",
			"project storage resource was not found",
		)
	case errors.Is(err, projectstorage.ErrGrantUnavailable):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"project_storage.grant_unavailable",
			"project storage grant is not active",
		)
	default:
		h.internalError(response, request, err)
	}
}
