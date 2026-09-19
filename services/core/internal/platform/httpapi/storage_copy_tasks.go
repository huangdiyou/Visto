package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/storage"
)

type storageCopyTaskRequest struct {
	SourceStorageObjectID string `json:"sourceStorageObjectId"`
	SourceAssetID         string `json:"sourceAssetId"`
	SourceAssetVersionID  string `json:"sourceAssetVersionId"`
	TargetRootID          string `json:"targetRootId"`
	TargetObjectKey       string `json:"targetObjectKey"`
}

type storageCopyTaskResponse struct {
	ID                    string     `json:"id"`
	WorkspaceID           string     `json:"workspaceId"`
	SourceStorageObjectID string     `json:"sourceStorageObjectId"`
	SourceRootID          string     `json:"sourceRootId"`
	SourceObjectKey       string     `json:"sourceObjectKey"`
	TargetRootID          string     `json:"targetRootId"`
	TargetObjectKey       string     `json:"targetObjectKey"`
	TargetStorageObjectID *string    `json:"targetStorageObjectId"`
	JobID                 *string    `json:"jobId"`
	Status                string     `json:"status"`
	TotalBytes            int64      `json:"totalBytes"`
	CopiedBytes           int64      `json:"copiedBytes"`
	ContentHash           *string    `json:"contentHash"`
	ContentHashAlgorithm  *string    `json:"contentHashAlgorithm"`
	ErrorCode             *string    `json:"errorCode"`
	ErrorMessage          *string    `json:"errorMessage"`
	Revision              int        `json:"revision"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	CompletedAt           *time.Time `json:"completedAt"`
}

type storageCopyTaskEnvelope struct {
	Task storageCopyTaskResponse `json:"task"`
	Job  jobResponse             `json:"job"`
}

func (h *handler) handleCreateStorageCopyTask(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	var body storageCopyTaskRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireCopySourceReadPermission(response, request, session, body) {
		return
	}
	task, err := h.storage.CreateCopyTask(
		request.Context(),
		storage.CreateCopyTaskInput{
			WorkspaceID:           session.Workspace.ID,
			SourceStorageObjectID: body.SourceStorageObjectID,
			SourceAssetID:         body.SourceAssetID,
			SourceAssetVersionID:  body.SourceAssetVersionID,
			TargetRootID:          body.TargetRootID,
			TargetObjectKey:       body.TargetObjectKey,
		},
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	payload, err := json.Marshal(storage.CopyTaskPayload{TaskID: task.ID})
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	item, _, err := h.jobs.Create(
		request.Context(),
		job.CreateInput{
			WorkspaceID: session.Workspace.ID,
			Type:        storage.CopyObjectJobType,
			SubjectType: "storageCopyTask",
			SubjectID:   task.ID,
			Payload:     payload,
			MaxAttempts: 5,
			AvailableAt: time.Now().UTC(),
		},
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	task, err = h.storage.AttachCopyTaskJob(
		request.Context(),
		session.Workspace.ID,
		task.ID,
		item.ID,
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, storageCopyTaskEnvelope{
		Task: toStorageCopyTaskResponse(task),
		Job:  toJobResponse(item),
	})
}

// requireCopySourceReadPermission resolves a copy source selector to a library
// asset and enforces source-project read authority. Storage administration
// alone must not authorize copying media out of a project the caller cannot
// read. A raw object that cannot be resolved to an asset is restricted to the
// workspace owner.
func (h *handler) requireCopySourceReadPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	body storageCopyTaskRequest,
) bool {
	assetID := strings.TrimSpace(body.SourceAssetID)
	switch {
	case strings.TrimSpace(body.SourceStorageObjectID) != "":
		asset, err := h.library.AssetForStorageObject(
			request.Context(),
			session.Workspace.ID,
			strings.TrimSpace(body.SourceStorageObjectID),
		)
		if errors.Is(err, media.ErrLibraryAssetNotFound) {
			return h.requireCopyOwnerOnly(response, session)
		}
		if err != nil {
			h.internalError(response, request, err)
			return false
		}
		assetID = asset.ID
	case strings.TrimSpace(body.SourceAssetVersionID) != "":
		resolved, err := h.library.AssetIDForVersion(
			request.Context(),
			session.Workspace.ID,
			strings.TrimSpace(body.SourceAssetVersionID),
		)
		if errors.Is(err, media.ErrLibraryAssetNotFound) {
			return h.requireCopyOwnerOnly(response, session)
		}
		if err != nil {
			h.internalError(response, request, err)
			return false
		}
		assetID = resolved
	}
	if assetID == "" {
		return true
	}
	return h.requireAssetMutationPermission(
		response,
		request,
		session,
		assetID,
		projectaccess.PermissionProjectRead,
	)
}

func (h *handler) requireCopyOwnerOnly(
	response http.ResponseWriter,
	session identity.Session,
) bool {
	if session.Role == "owner" {
		return true
	}
	writeError(
		response,
		http.StatusForbidden,
		requestID(response),
		"permission.project_denied",
		"原始对象复制仅限 Owner",
	)
	return false
}

func (h *handler) handleGetStorageCopyTask(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionStorageManage,
	)
	if !ok {
		return
	}
	task, err := h.storage.CopyTask(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("taskId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toStorageCopyTaskResponse(task))
}

func toStorageCopyTaskResponse(task storage.CopyTask) storageCopyTaskResponse {
	return storageCopyTaskResponse{
		ID:                    task.ID,
		WorkspaceID:           task.WorkspaceID,
		SourceStorageObjectID: task.SourceStorageObjectID,
		SourceRootID:          task.SourceRootID,
		SourceObjectKey:       task.SourceObjectKey,
		TargetRootID:          task.TargetRootID,
		TargetObjectKey:       task.TargetObjectKey,
		TargetStorageObjectID: task.TargetStorageObjectID,
		JobID:                 task.JobID,
		Status:                task.Status,
		TotalBytes:            task.TotalBytes,
		CopiedBytes:           task.CopiedBytes,
		ContentHash:           task.ContentHash,
		ContentHashAlgorithm:  task.ContentHashAlgorithm,
		ErrorCode:             task.ErrorCode,
		ErrorMessage:          task.ErrorMessage,
		Revision:              task.Revision,
		CreatedAt:             task.CreatedAt,
		UpdatedAt:             task.UpdatedAt,
		CompletedAt:           task.CompletedAt,
	}
}
