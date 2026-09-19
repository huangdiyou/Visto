package httpapi

import (
	"errors"
	"net/http"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/storage"
)

type storageDeleteImpactResponse struct {
	TargetType       string                            `json:"targetType"`
	ProviderID       string                            `json:"providerId"`
	ProviderName     string                            `json:"providerName"`
	ProviderKind     string                            `json:"providerKind"`
	AuthorizedRootID *string                           `json:"authorizedRootId"`
	BucketID         *string                           `json:"bucketId"`
	BucketName       *string                           `json:"bucketName"`
	CanDelete        bool                              `json:"canDelete"`
	CanDisable       bool                              `json:"canDisable"`
	Counts           storageDeleteImpactCountsResponse `json:"counts"`
	BlockingReasons  []string                          `json:"blockingReasons"`
}

type storageDeleteImpactCountsResponse struct {
	AuthorizedRoots   int `json:"authorizedRoots"`
	ProjectGrants     int `json:"projectGrants"`
	ProjectSelections int `json:"projectSelections"`
	StorageObjects    int `json:"storageObjects"`
	AssetVersions     int `json:"assetVersions"`
	Renditions        int `json:"renditions"`
	ReviewSessions    int `json:"reviewSessions"`
	PendingJobs       int `json:"pendingJobs"`
	StorageCopyTasks  int `json:"storageCopyTasks"`
	UploadChecks      int `json:"uploadChecks"`
}

func (h *handler) handleGetStorageProviderDeleteImpact(
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
	impact, err := h.storage.ProviderDeleteImpact(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("providerId"),
	)
	if err != nil {
		h.handleStorageProviderError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toStorageDeleteImpactResponse(impact))
}

func (h *handler) handleGetLocalManagedBucketDeleteImpact(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	impact, err := h.storage.LocalManagedBucketDeleteImpact(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("bucketId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toStorageDeleteImpactResponse(impact))
}

func (h *handler) handleDeleteLocalManagedBucket(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	id := request.PathValue("bucketId")
	if err := h.storage.DeleteLocalManagedBucket(
		request.Context(),
		storage.LocalManagedBucketStateInput{
			WorkspaceID: session.Workspace.ID,
			ID:          id,
			Revision:    body.Revision,
		},
	); err != nil {
		h.handleStorageLocationDeletionError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "storage.local_bucket_deleted",
		ResourceType: "local_managed_bucket",
		ResourceID:   id,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleStorageLocationDeletionError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, storage.ErrProviderInUse):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"storage.location_in_use",
			"这个存储位置仍被项目、媒体、审阅或任务引用，请先停用、迁移或清理引用后再删除。",
		)
	case errors.Is(err, storage.ErrBucketRevisionConflict),
		errors.Is(err, storage.ErrRevisionConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"resource.revision_conflict",
			"存储设置已更新，请刷新后重试",
		)
	default:
		h.handleStorageError(response, request, err)
	}
}

func toStorageDeleteImpactResponse(
	impact storage.StorageLocationDeleteImpact,
) storageDeleteImpactResponse {
	return storageDeleteImpactResponse{
		TargetType:       impact.TargetType,
		ProviderID:       impact.ProviderID,
		ProviderName:     impact.ProviderName,
		ProviderKind:     impact.ProviderKind,
		AuthorizedRootID: impact.AuthorizedRootID,
		BucketID:         impact.BucketID,
		BucketName:       impact.BucketName,
		CanDelete:        impact.CanDelete,
		CanDisable:       impact.CanDisable,
		Counts: storageDeleteImpactCountsResponse{
			AuthorizedRoots:   impact.Counts.AuthorizedRoots,
			ProjectGrants:     impact.Counts.ProjectGrants,
			ProjectSelections: impact.Counts.ProjectSelections,
			StorageObjects:    impact.Counts.StorageObjects,
			AssetVersions:     impact.Counts.AssetVersions,
			Renditions:        impact.Counts.Renditions,
			ReviewSessions:    impact.Counts.ReviewSessions,
			PendingJobs:       impact.Counts.PendingJobs,
			StorageCopyTasks:  impact.Counts.StorageCopyTasks,
			UploadChecks:      impact.Counts.UploadChecks,
		},
		BlockingReasons: impact.BlockingReasons,
	}
}
