package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/catalog"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/storage"
)

type projectRequest struct {
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	StorageGrantID string  `json:"storageGrantId,omitempty"`
	Revision       int     `json:"revision,omitempty"`
}

type revisionRequest struct {
	Revision int `json:"revision"`
}

type collectionRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Revision    int     `json:"revision,omitempty"`
}

type collectionItemRequest struct {
	AssetID         string  `json:"assetId"`
	PinnedVersionID *string `json:"pinnedVersionId"`
	Caption         *string `json:"caption"`
}

type collectionOrderRequest struct {
	ItemIDs []string `json:"itemIds"`
}

type projectResponse struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspaceId"`
	Name            string     `json:"name"`
	Description     *string    `json:"description"`
	Status          string     `json:"status"`
	Cover           any        `json:"cover"`
	AssetCount      int        `json:"assetCount"`
	CollectionCount int        `json:"collectionCount"`
	Revision        int        `json:"revision"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ArchivedAt      *time.Time `json:"archivedAt"`
	PrimaryOwnerID  *string    `json:"primaryOwnerUserId"`
}

type projectOverviewResponse struct {
	ProjectID      string                                `json:"projectId"`
	GeneratedAt    time.Time                             `json:"generatedAt"`
	Reviews        projectOverviewReviewsResponse        `json:"reviews"`
	OpenFeedback   projectOverviewFeedbackListResponse   `json:"openFeedback"`
	RecentVersions projectOverviewVersionListResponse    `json:"recentVersions"`
	FailedTasks    projectOverviewFailedTaskListResponse `json:"failedTasks"`
}

type projectOverviewReviewsResponse struct {
	Total int                             `json:"total"`
	Items []projectOverviewReviewResponse `json:"items"`
}

type projectOverviewReviewResponse struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Status            string     `json:"status"`
	DueAt             *time.Time `json:"dueAt"`
	ResponsibleName   string     `json:"responsibleName"`
	AssetName         string     `json:"assetName"`
	VersionNumber     int        `json:"versionNumber"`
	ItemCount         int        `json:"itemCount"`
	OpenFeedbackCount int        `json:"openFeedbackCount"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type projectOverviewFeedbackListResponse struct {
	Total int                               `json:"total"`
	Items []projectOverviewFeedbackResponse `json:"items"`
}

type projectOverviewFeedbackResponse struct {
	ID             string    `json:"id"`
	ReviewID       string    `json:"reviewId"`
	ReviewName     string    `json:"reviewName"`
	AssetID        string    `json:"assetId"`
	AssetName      string    `json:"assetName"`
	AssetVersionID string    `json:"assetVersionId"`
	VersionNumber  int       `json:"versionNumber"`
	AuthorName     string    `json:"authorName"`
	Body           string    `json:"body"`
	AnnotationKind string    `json:"annotationKind"`
	TimeStartUS    *int64    `json:"timeStartUs"`
	TimeEndUS      *int64    `json:"timeEndUs"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type projectOverviewVersionListResponse struct {
	Total int                              `json:"total"`
	Items []projectOverviewVersionResponse `json:"items"`
}

type projectOverviewVersionResponse struct {
	ID               string    `json:"id"`
	AssetID          string    `json:"assetId"`
	AssetName        string    `json:"assetName"`
	VersionNumber    int       `json:"versionNumber"`
	SourceFilename   string    `json:"sourceFilename"`
	ProcessingStatus string    `json:"processingStatus"`
	ActorName        string    `json:"actorName"`
	CreatedAt        time.Time `json:"createdAt"`
}

type projectOverviewFailedTaskListResponse struct {
	Total int                                 `json:"total"`
	Items []projectOverviewFailedTaskResponse `json:"items"`
}

type projectOverviewFailedTaskResponse struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	AssetID      string    `json:"assetId"`
	AssetName    string    `json:"assetName"`
	ErrorCode    string    `json:"errorCode"`
	ErrorMessage string    `json:"errorMessage"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type collectionResponse struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	ProjectID   string    `json:"projectId"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Kind        string    `json:"kind"`
	Position    int       `json:"position"`
	ItemCount   int       `json:"itemCount"`
	Revision    int       `json:"revision"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type collectionItemResponse struct {
	ID              string    `json:"id"`
	CollectionID    string    `json:"collectionId"`
	AssetID         string    `json:"assetId"`
	AssetName       string    `json:"assetName"`
	AssetType       string    `json:"assetType"`
	PinnedVersionID *string   `json:"pinnedVersionId"`
	Position        int       `json:"position"`
	Caption         *string   `json:"caption"`
	CreatedAt       time.Time `json:"createdAt"`
}

type projectListResponse struct {
	Items []projectResponse `json:"items"`
}

type collectionListResponse struct {
	Items []collectionResponse `json:"items"`
}

type collectionItemListResponse struct {
	Items []collectionItemResponse `json:"items"`
}

func (h *handler) handleListProjects(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}

	projects, err := h.catalog.ListProjects(
		request.Context(),
		catalog.ListProjectsInput{
			WorkspaceID:   session.Workspace.ID,
			UserID:        session.User.ID,
			WorkspaceRole: session.Role,
		},
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}

	items := make([]projectResponse, 0, len(projects))
	for _, project := range projects {
		items = append(items, toProjectResponse(project))
	}
	writeJSON(response, http.StatusOK, projectListResponse{Items: items})
}

func (h *handler) handleCreateProject(
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

	var body projectRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if strings.TrimSpace(body.StorageGrantID) == "" {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"project.storage_required",
			"请选择项目存储位置",
		)
		return
	}

	project, err := h.catalog.CreateProject(request.Context(), catalog.CreateProjectInput{
		WorkspaceID:    session.Workspace.ID,
		UserID:         session.User.ID,
		Name:           body.Name,
		Description:    body.Description,
		StorageGrantID: body.StorageGrantID,
	})
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.created",
		ResourceType: "project",
		ResourceID:   project.ID,
		After: map[string]any{
			"name":           project.Name,
			"status":         project.Status,
			"storageGrantId": strings.TrimSpace(body.StorageGrantID),
		},
	})
	writeJSON(response, http.StatusCreated, toProjectResponse(project))
}

func (h *handler) handleGetProject(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}

	project, err := h.catalog.Project(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("projectId"),
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		project.ID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	writeJSON(response, http.StatusOK, toProjectResponse(project))
}

func (h *handler) handleGetProjectOverview(
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
	overview, err := h.catalog.ProjectOverview(
		request.Context(),
		session.Workspace.ID,
		projectID,
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toProjectOverviewResponse(overview))
}

func (h *handler) handleUpdateProject(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}

	var body projectRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		request.PathValue("projectId"),
		projectaccess.PermissionProjectManage,
	) {
		return
	}
	project, err := h.catalog.UpdateProject(request.Context(), catalog.UpdateProjectInput{
		WorkspaceID: session.Workspace.ID,
		ID:          request.PathValue("projectId"),
		Name:        body.Name,
		Description: body.Description,
		Revision:    body.Revision,
	})
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.updated",
		ResourceType: "project",
		ResourceID:   project.ID,
		After: map[string]any{
			"name":   project.Name,
			"status": project.Status,
		},
	})
	writeJSON(response, http.StatusOK, toProjectResponse(project))
}

func (h *handler) handleArchiveProject(
	response http.ResponseWriter,
	request *http.Request,
) {
	h.handleProjectState(response, request, "archive")
}

func (h *handler) handleRestoreProject(
	response http.ResponseWriter,
	request *http.Request,
) {
	h.handleProjectState(response, request, "restore")
}

func (h *handler) handleProjectState(
	response http.ResponseWriter,
	request *http.Request,
	action string,
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
	permission := projectaccess.PermissionProjectManage
	if !h.requireProjectPermission(
		response,
		request,
		session,
		request.PathValue("projectId"),
		permission,
	) {
		return
	}

	input := catalog.ProjectStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          request.PathValue("projectId"),
		Revision:    body.Revision,
	}
	var project catalog.Project
	var err error
	if action == "archive" {
		if err := h.prepareProjectArchiveTasks(
			request.Context(),
			session.Workspace.ID,
			input.ID,
		); err != nil {
			h.internalError(response, request, err)
			return
		}
		project, err = h.catalog.ArchiveProject(request.Context(), input)
	} else {
		project, err = h.catalog.RestoreProject(request.Context(), input)
	}
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	auditAction := "project.archived"
	if action != "archive" {
		auditAction = "project.restored"
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       auditAction,
		ResourceType: "project",
		ResourceID:   project.ID,
		After: map[string]any{
			"status": project.Status,
		},
	})
	writeJSON(response, http.StatusOK, toProjectResponse(project))
}

func (h *handler) prepareProjectArchiveTasks(
	ctx context.Context,
	workspaceID string,
	projectID string,
) error {
	tasks, err := h.storage.CreateProjectArchiveTasks(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return fmt.Errorf("prepare project archive copies: %w", err)
	}
	for _, task := range tasks {
		payload, err := json.Marshal(storage.CopyTaskPayload{TaskID: task.ID})
		if err != nil {
			return err
		}
		idempotencyKey := fmt.Sprintf(
			"project-archive:%s:%s:%s",
			projectID,
			task.SourceStorageObjectID,
			task.TargetRootID,
		)
		item, _, err := h.jobs.Create(ctx, job.CreateInput{
			WorkspaceID:    workspaceID,
			Type:           storage.CopyObjectJobType,
			IdempotencyKey: &idempotencyKey,
			SubjectType:    "storageCopyTask",
			SubjectID:      task.ID,
			Payload:        payload,
			MaxAttempts:    5,
			AvailableAt:    time.Now().UTC(),
		})
		if err != nil {
			return fmt.Errorf("queue project archive copy: %w", err)
		}
		if _, err := h.storage.AttachCopyTaskJob(
			ctx,
			workspaceID,
			task.ID,
			item.ID,
		); err != nil {
			return fmt.Errorf("attach project archive copy job: %w", err)
		}
	}
	return nil
}

func (h *handler) handleDeleteProject(
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
	if !h.requireProjectPermission(
		response,
		request,
		session,
		request.PathValue("projectId"),
		projectaccess.PermissionProjectManage,
	) {
		return
	}
	err := h.catalog.DeleteProject(request.Context(), catalog.ProjectStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          request.PathValue("projectId"),
		Revision:    body.Revision,
	})
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project.deleted",
		ResourceType: "project",
		ResourceID:   request.PathValue("projectId"),
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleListCollections(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		request.PathValue("projectId"),
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	collections, err := h.catalog.ListCollections(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("projectId"),
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	items := make([]collectionResponse, 0, len(collections))
	for _, collection := range collections {
		items = append(items, toCollectionResponse(collection))
	}
	writeJSON(response, http.StatusOK, collectionListResponse{Items: items})
}

func (h *handler) handleCreateCollection(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body collectionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		request.PathValue("projectId"),
		projectaccess.PermissionProjectManage,
	) {
		return
	}
	collection, err := h.catalog.CreateCollection(
		request.Context(),
		catalog.CreateCollectionInput{
			WorkspaceID: session.Workspace.ID,
			UserID:      session.User.ID,
			ProjectID:   request.PathValue("projectId"),
			Name:        body.Name,
			Description: body.Description,
		},
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, toCollectionResponse(collection))
}

func (h *handler) handleGetCollection(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	collection, err := h.catalog.Collection(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("collectionId"),
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		collection.ProjectID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	writeJSON(response, http.StatusOK, toCollectionResponse(collection))
}

func (h *handler) handleUpdateCollection(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body collectionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	collection, ok := h.requireCollectionProjectPermission(
		response,
		request,
		session,
		request.PathValue("collectionId"),
		projectaccess.PermissionProjectManage,
	)
	if !ok {
		return
	}
	collection, err := h.catalog.UpdateCollection(
		request.Context(),
		catalog.UpdateCollectionInput{
			WorkspaceID: session.Workspace.ID,
			ID:          request.PathValue("collectionId"),
			Name:        body.Name,
			Description: body.Description,
			Revision:    body.Revision,
		},
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toCollectionResponse(collection))
}

func (h *handler) handleDeleteCollection(
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
	if _, ok := h.requireCollectionProjectPermission(
		response,
		request,
		session,
		request.PathValue("collectionId"),
		projectaccess.PermissionProjectManage,
	); !ok {
		return
	}
	err := h.catalog.DeleteCollection(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("collectionId"),
		body.Revision,
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleListCollectionItems(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if _, ok := h.requireCollectionProjectPermission(
		response,
		request,
		session,
		request.PathValue("collectionId"),
		projectaccess.PermissionProjectRead,
	); !ok {
		return
	}
	items, err := h.catalog.ListCollectionItems(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("collectionId"),
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	result := make([]collectionItemResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toCollectionItemResponse(item))
	}
	writeJSON(response, http.StatusOK, collectionItemListResponse{Items: result})
}

func (h *handler) handleAddCollectionItem(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body collectionItemRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if strings.TrimSpace(body.AssetID) == "" {
		h.badRequest(response, request, errors.New("asset id is required"))
		return
	}
	if _, ok := h.requireCollectionProjectPermission(
		response,
		request,
		session,
		request.PathValue("collectionId"),
		projectaccess.PermissionAssetsAdd,
	); !ok {
		return
	}
	if !h.requireAssetProjectPermission(
		response,
		request,
		session,
		strings.TrimSpace(body.AssetID),
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	item, err := h.catalog.AddCollectionItem(
		request.Context(),
		catalog.AddCollectionItemInput{
			WorkspaceID:     session.Workspace.ID,
			CollectionID:    request.PathValue("collectionId"),
			AssetID:         body.AssetID,
			PinnedVersionID: body.PinnedVersionID,
			Caption:         body.Caption,
		},
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, toCollectionItemResponse(item))
}

func (h *handler) handleReorderCollectionItems(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body collectionOrderRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireCollectionProjectPermission(
		response,
		request,
		session,
		request.PathValue("collectionId"),
		projectaccess.PermissionProjectManage,
	); !ok {
		return
	}
	err := h.catalog.ReorderCollectionItems(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("collectionId"),
		body.ItemIDs,
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleDeleteCollectionItem(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if _, ok := h.requireCollectionProjectPermission(
		response,
		request,
		session,
		request.PathValue("collectionId"),
		projectaccess.PermissionAssetsRemove,
	); !ok {
		return
	}
	err := h.catalog.DeleteCollectionItem(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("collectionId"),
		request.PathValue("itemId"),
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) requireCollectionProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	collectionID string,
	permission projectaccess.Permission,
) (catalog.Collection, bool) {
	collection, err := h.catalog.Collection(
		request.Context(),
		session.Workspace.ID,
		collectionID,
	)
	if err != nil {
		h.handleCatalogError(response, request, err)
		return catalog.Collection{}, false
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		collection.ProjectID,
		permission,
	) {
		return catalog.Collection{}, false
	}
	return collection, true
}

func (h *handler) requireSession(
	response http.ResponseWriter,
	request *http.Request,
) (identity.Session, bool) {
	session, err := h.authenticateRequest(request)
	if errors.Is(err, identity.ErrSessionNotFound) {
		writeError(
			response,
			http.StatusUnauthorized,
			requestID(response),
			"auth.required",
			"请先登录",
		)
		return identity.Session{}, false
	}
	if err != nil {
		h.internalError(response, request, err)
		return identity.Session{}, false
	}
	return session, true
}

func (h *handler) handleCatalogError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"resource.not_found",
			"资源不存在",
		)
	case errors.Is(err, catalog.ErrRevisionConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"resource.revision_conflict",
			"内容已被更新，请刷新后重试",
		)
	case errors.Is(err, catalog.ErrProjectArchived):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"project.archived",
			"归档项目不能继续修改",
		)
	case errors.Is(err, catalog.ErrProjectStorageUnavailable):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"project.storage_unavailable",
			"所选项目存储位置当前不可用，请重新选择",
		)
	case errors.Is(err, catalog.ErrDuplicateItem):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"collection.item_exists",
			"该资产已经在集合中",
		)
	case errors.Is(err, catalog.ErrAssetProjectConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"asset.project_conflict",
			"该资产已经属于另一个项目",
		)
	case errors.Is(err, catalog.ErrPinnedVersionInvalid):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"collection.pinned_version_invalid",
			"固定版本必须属于所选媒体",
		)
	case errors.Is(err, catalog.ErrInvalidOrder):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"collection.order_invalid",
			"集合顺序无效",
		)
	case isCatalogValidationError(err):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func isCatalogValidationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "must contain") ||
		strings.Contains(message, "must be positive") ||
		strings.Contains(message, "is required")
}

func canCreateWorkspaceProject(session identity.Session) bool {
	switch strings.ToLower(strings.TrimSpace(session.Role)) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func toProjectResponse(project catalog.Project) projectResponse {
	return projectResponse{
		ID:              project.ID,
		WorkspaceID:     project.WorkspaceID,
		PrimaryOwnerID:  project.PrimaryOwnerUserID,
		Name:            project.Name,
		Description:     project.Description,
		Status:          project.Status,
		Cover:           nil,
		AssetCount:      project.AssetCount,
		CollectionCount: project.CollectionCount,
		Revision:        project.Revision,
		CreatedAt:       project.CreatedAt,
		UpdatedAt:       project.UpdatedAt,
		ArchivedAt:      project.ArchivedAt,
	}
}

func toProjectOverviewResponse(
	overview catalog.ProjectOverview,
) projectOverviewResponse {
	reviews := make([]projectOverviewReviewResponse, 0, len(overview.Reviews.Items))
	for _, item := range overview.Reviews.Items {
		reviews = append(reviews, projectOverviewReviewResponse{
			ID:                item.ID,
			Name:              item.Name,
			Status:            item.Status,
			DueAt:             item.DueAt,
			ResponsibleName:   item.ResponsibleName,
			AssetName:         item.AssetName,
			VersionNumber:     item.VersionNumber,
			ItemCount:         item.ItemCount,
			OpenFeedbackCount: item.OpenFeedbackCount,
			UpdatedAt:         item.UpdatedAt,
		})
	}
	feedback := make(
		[]projectOverviewFeedbackResponse,
		0,
		len(overview.OpenFeedback.Items),
	)
	for _, item := range overview.OpenFeedback.Items {
		feedback = append(feedback, projectOverviewFeedbackResponse{
			ID:             item.ID,
			ReviewID:       item.ReviewID,
			ReviewName:     item.ReviewName,
			AssetID:        item.AssetID,
			AssetName:      item.AssetName,
			AssetVersionID: item.AssetVersionID,
			VersionNumber:  item.VersionNumber,
			AuthorName:     item.AuthorName,
			Body:           item.Body,
			AnnotationKind: item.AnnotationKind,
			TimeStartUS:    item.TimeStartUS,
			TimeEndUS:      item.TimeEndUS,
			UpdatedAt:      item.UpdatedAt,
		})
	}
	versions := make(
		[]projectOverviewVersionResponse,
		0,
		len(overview.RecentVersions.Items),
	)
	for _, item := range overview.RecentVersions.Items {
		versions = append(versions, projectOverviewVersionResponse{
			ID:               item.ID,
			AssetID:          item.AssetID,
			AssetName:        item.AssetName,
			VersionNumber:    item.VersionNumber,
			SourceFilename:   item.SourceFilename,
			ProcessingStatus: item.ProcessingStatus,
			ActorName:        item.ActorName,
			CreatedAt:        item.CreatedAt,
		})
	}
	failedTasks := make(
		[]projectOverviewFailedTaskResponse,
		0,
		len(overview.FailedTasks.Items),
	)
	for _, item := range overview.FailedTasks.Items {
		failedTasks = append(failedTasks, projectOverviewFailedTaskResponse{
			ID:           item.ID,
			Type:         item.Type,
			AssetID:      item.AssetID,
			AssetName:    item.AssetName,
			ErrorCode:    item.ErrorCode,
			ErrorMessage: item.ErrorMessage,
			UpdatedAt:    item.UpdatedAt,
		})
	}
	return projectOverviewResponse{
		ProjectID:   overview.ProjectID,
		GeneratedAt: overview.GeneratedAt,
		Reviews: projectOverviewReviewsResponse{
			Total: overview.Reviews.Total,
			Items: reviews,
		},
		OpenFeedback: projectOverviewFeedbackListResponse{
			Total: overview.OpenFeedback.Total,
			Items: feedback,
		},
		RecentVersions: projectOverviewVersionListResponse{
			Total: overview.RecentVersions.Total,
			Items: versions,
		},
		FailedTasks: projectOverviewFailedTaskListResponse{
			Total: overview.FailedTasks.Total,
			Items: failedTasks,
		},
	}
}

func toCollectionResponse(collection catalog.Collection) collectionResponse {
	return collectionResponse{
		ID:          collection.ID,
		WorkspaceID: collection.WorkspaceID,
		ProjectID:   collection.ProjectID,
		Name:        collection.Name,
		Description: collection.Description,
		Kind:        collection.Kind,
		Position:    collection.Position,
		ItemCount:   collection.ItemCount,
		Revision:    collection.Revision,
		CreatedAt:   collection.CreatedAt,
		UpdatedAt:   collection.UpdatedAt,
	}
}

func toCollectionItemResponse(item catalog.CollectionItem) collectionItemResponse {
	return collectionItemResponse{
		ID:              item.ID,
		CollectionID:    item.CollectionID,
		AssetID:         item.AssetID,
		AssetName:       item.AssetName,
		AssetType:       item.AssetType,
		PinnedVersionID: item.PinnedVersionID,
		Position:        item.Position,
		Caption:         item.Caption,
		CreatedAt:       item.CreatedAt,
	}
}
