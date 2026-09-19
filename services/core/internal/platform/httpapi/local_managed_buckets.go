package httpapi

import (
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/projectstorage"
	"review-studio.local/core/internal/storage"
)

type localManagedBucketResponse struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspaceId"`
	AuthorizedRootID     string    `json:"authorizedRootId"`
	StorageProviderID    string    `json:"storageProviderId"`
	DisplayName          string    `json:"displayName"`
	DisplayPath          string    `json:"displayPath"`
	Purpose              string    `json:"purpose"`
	QuotaBytes           *int64    `json:"quotaBytes"`
	UsedBytesEstimate    *int64    `json:"usedBytesEstimate"`
	UploadSecurityPolicy string    `json:"uploadSecurityPolicy"`
	ProjectAvailable     bool      `json:"projectAvailable"`
	Status               string    `json:"status"`
	CreatedBy            *string   `json:"createdBy"`
	Revision             int       `json:"revision"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type localManagedBucketListResponse struct {
	Items []localManagedBucketResponse `json:"items"`
}

type createLocalManagedBucketRequest struct {
	DisplayName          string `json:"displayName"`
	LocalPath            string `json:"localPath"`
	Purpose              string `json:"purpose"`
	QuotaBytes           *int64 `json:"quotaBytes"`
	UploadSecurityPolicy string `json:"uploadSecurityPolicy"`
	ProjectAvailable     *bool  `json:"projectAvailable"`
}

type updateLocalManagedBucketRequest struct {
	DisplayName          string `json:"displayName"`
	Purpose              string `json:"purpose"`
	QuotaBytes           *int64 `json:"quotaBytes"`
	UploadSecurityPolicy string `json:"uploadSecurityPolicy"`
	ProjectAvailable     bool   `json:"projectAvailable"`
	Status               string `json:"status"`
	Revision             int    `json:"revision"`
}

func (h *handler) handleListLocalManagedBuckets(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	buckets, err := h.storage.ListLocalManagedBuckets(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	items := make([]localManagedBucketResponse, 0, len(buckets))
	for _, bucket := range buckets {
		items = append(items, toLocalManagedBucketResponse(bucket))
	}
	writeJSON(response, http.StatusOK, localManagedBucketListResponse{Items: items})
}

func (h *handler) handleCreateLocalManagedBucket(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	if !h.requireHostManagement(response, request) {
		return
	}

	var body createLocalManagedBucketRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	projectAvailable := true
	if body.ProjectAvailable != nil {
		projectAvailable = *body.ProjectAvailable
	}
	bucket, err := h.storage.CreateLocalManagedBucket(
		request.Context(),
		storage.CreateLocalManagedBucketInput{
			WorkspaceID:          session.Workspace.ID,
			DisplayName:          body.DisplayName,
			LocalPath:            body.LocalPath,
			Purpose:              body.Purpose,
			QuotaBytes:           body.QuotaBytes,
			UploadSecurityPolicy: body.UploadSecurityPolicy,
			ProjectAvailable:     projectAvailable,
			CreatedBy:            session.User.ID,
		},
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	if err := h.syncLocalManagedBucketProjectGrant(
		request,
		session.User.ID,
		bucket,
	); err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "storage.local_bucket_created",
		ResourceType: "local_managed_bucket",
		ResourceID:   bucket.ID,
		After: map[string]any{
			"purpose":              bucket.Purpose,
			"uploadSecurityPolicy": bucket.UploadSecurityPolicy,
			"projectAvailable":     bucket.ProjectAvailable,
			"status":               bucket.Status,
		},
	})
	writeJSON(response, http.StatusCreated, toLocalManagedBucketResponse(bucket))
}

func (h *handler) handleUpdateLocalManagedBucket(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}

	var body updateLocalManagedBucketRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	bucket, err := h.storage.UpdateLocalManagedBucket(
		request.Context(),
		storage.UpdateLocalManagedBucketInput{
			WorkspaceID:          session.Workspace.ID,
			ID:                   request.PathValue("bucketId"),
			DisplayName:          body.DisplayName,
			Purpose:              body.Purpose,
			QuotaBytes:           body.QuotaBytes,
			UploadSecurityPolicy: body.UploadSecurityPolicy,
			ProjectAvailable:     body.ProjectAvailable,
			Status:               body.Status,
			Revision:             body.Revision,
		},
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	if err := h.syncLocalManagedBucketProjectGrant(
		request,
		session.User.ID,
		bucket,
	); err != nil {
		h.handleProjectStorageError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "storage.local_bucket_updated",
		ResourceType: "local_managed_bucket",
		ResourceID:   bucket.ID,
		After: map[string]any{
			"purpose":              bucket.Purpose,
			"uploadSecurityPolicy": bucket.UploadSecurityPolicy,
			"projectAvailable":     bucket.ProjectAvailable,
			"status":               bucket.Status,
		},
	})
	writeJSON(response, http.StatusOK, toLocalManagedBucketResponse(bucket))
}

func (h *handler) syncLocalManagedBucketProjectGrant(
	request *http.Request,
	userID string,
	bucket storage.LocalManagedBucket,
) error {
	status := "disabled"
	if bucket.ProjectAvailable && bucket.Status == "active" {
		status = "active"
	}
	rootID := bucket.AuthorizedRootID
	_, err := h.projectStorage.SetGrant(
		request.Context(),
		projectstorage.SetGrantInput{
			WorkspaceID:       bucket.WorkspaceID,
			StorageProviderID: bucket.StorageProviderID,
			AuthorizedRootID:  &rootID,
			Status:            status,
			GrantedBy:         userID,
		},
	)
	return err
}

func toLocalManagedBucketResponse(
	item storage.LocalManagedBucket,
) localManagedBucketResponse {
	return localManagedBucketResponse{
		ID:                   item.ID,
		WorkspaceID:          item.WorkspaceID,
		AuthorizedRootID:     item.AuthorizedRootID,
		StorageProviderID:    item.StorageProviderID,
		DisplayName:          item.DisplayName,
		DisplayPath:          item.DisplayPath,
		Purpose:              item.Purpose,
		QuotaBytes:           item.QuotaBytes,
		UsedBytesEstimate:    item.UsedBytesEstimate,
		UploadSecurityPolicy: item.UploadSecurityPolicy,
		ProjectAvailable:     item.ProjectAvailable,
		Status:               item.Status,
		CreatedBy:            item.CreatedBy,
		Revision:             item.Revision,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
	}
}
