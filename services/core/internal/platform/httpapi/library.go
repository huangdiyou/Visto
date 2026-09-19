package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectstorage"
	"review-studio.local/core/internal/storage"
)

type mediaLibraryItemResponse struct {
	Object      storageObjectResponse      `json:"object"`
	Asset       *mediaLibraryAssetResponse `json:"asset"`
	Probe       *mediaProbeResponse        `json:"probe"`
	Renditions  []renditionResponse        `json:"renditions"`
	Jobs        []mediaLibraryJobResponse  `json:"jobs"`
	UploadCheck *uploadCheckResponse       `json:"uploadCheck"`
}

type mediaLibraryAssetResponse struct {
	ID            string  `json:"id"`
	ProjectID     *string `json:"projectId"`
	ProjectName   *string `json:"projectName"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Revision      int     `json:"revision"`
	VersionID     string  `json:"versionId"`
	VersionNumber int     `json:"versionNumber"`
}

type mediaAssetProjectRequest struct {
	ProjectID *string `json:"projectId"`
}

type projectAssetRequest struct {
	AssetID string `json:"assetId"`
}

type moveProjectAssetRequest struct {
	TargetProjectID string `json:"targetProjectId"`
}

type projectAssetResponse struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspaceId"`
	ProjectID      string     `json:"projectId"`
	ProjectName    string     `json:"projectName"`
	AssetID        string     `json:"assetId"`
	AssetName      string     `json:"assetName"`
	AssetType      string     `json:"assetType"`
	Status         string     `json:"status"`
	AddedBy        string     `json:"addedBy"`
	TrashedBy      *string    `json:"trashedBy"`
	TrashedAt      *time.Time `json:"trashedAt"`
	TrashExpiresAt *time.Time `json:"trashExpiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type projectAssetListResponse struct {
	Items []projectAssetResponse `json:"items"`
}

type assetVersionResponse struct {
	ID                  string               `json:"id"`
	AssetID             string               `json:"assetId"`
	VersionNumber       int                  `json:"versionNumber"`
	Label               *string              `json:"label"`
	Note                *string              `json:"note"`
	ProcessingStatus    string               `json:"processingStatus"`
	ProcessingStage     string               `json:"processingStage"`
	SourceFilename      string               `json:"sourceFilename"`
	SourceMIME          *string              `json:"sourceMime"`
	SourceSizeBytes     int64                `json:"sourceSizeBytes"`
	SourceFingerprint   string               `json:"sourceFingerprint"`
	StorageObjectID     string               `json:"storageObjectId"`
	AuthorizedRootID    string               `json:"authorizedRootId"`
	StorageObjectKey    string               `json:"storageObjectKey"`
	StorageObjectStatus string               `json:"storageObjectStatus"`
	CreatedAt           time.Time            `json:"createdAt"`
	IsCurrent           bool                 `json:"isCurrent"`
	Probe               *mediaProbeResponse  `json:"probe"`
	Renditions          []renditionResponse  `json:"renditions"`
	UploadCheck         *uploadCheckResponse `json:"uploadCheck"`
}

type uploadCheckResponse struct {
	ID                   string     `json:"id"`
	ProjectID            *string    `json:"projectId"`
	AssetID              string     `json:"assetId"`
	AssetVersionID       string     `json:"assetVersionId"`
	StorageObjectID      string     `json:"storageObjectId"`
	SourceType           string     `json:"sourceType"`
	UploadSecurityPolicy string     `json:"uploadSecurityPolicy"`
	Status               string     `json:"status"`
	ResultCode           *string    `json:"resultCode"`
	Message              *string    `json:"message"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	CompletedAt          *time.Time `json:"completedAt"`
	QuarantinedAt        *time.Time `json:"quarantinedAt"`
	RejectedAt           *time.Time `json:"rejectedAt"`
}

type assetVersionListResponse struct {
	Items []assetVersionResponse `json:"items"`
}

type setCurrentAssetVersionRequest struct {
	VersionID string `json:"versionId"`
	Revision  int    `json:"revision"`
}

type updateMediaAssetRequest struct {
	Name     string `json:"name"`
	Revision int    `json:"revision"`
}

type mediaLibraryJobResponse struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Status       string               `json:"status"`
	Priority     int                  `json:"priority"`
	Subject      jobSubjectResponse   `json:"subject"`
	Progress     *jobProgressResponse `json:"progress"`
	Error        *jobErrorResponse    `json:"error"`
	AttemptCount int                  `json:"attemptCount"`
	MaxAttempts  int                  `json:"maxAttempts"`
	CreatedAt    time.Time            `json:"createdAt"`
	UpdatedAt    time.Time            `json:"updatedAt"`
	StartedAt    *time.Time           `json:"startedAt"`
	CompletedAt  *time.Time           `json:"completedAt"`
}

type mediaLibraryPageResponse struct {
	Items    []mediaLibraryItemResponse `json:"items"`
	Total    int                        `json:"total"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"pageSize"`
}

func (h *handler) handleQueryMediaLibrary(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	query, err := parseMediaLibraryQuery(request)
	if err != nil {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_query",
			err.Error(),
		)
		return
	}
	if !h.requireMediaLibraryQueryPermission(response, request, session, query) {
		return
	}
	page, err := h.library.Query(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
		query,
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	page, err = h.filterMediaLibraryPage(request, session, page)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeMediaLibraryPage(response, page)
}

func (h *handler) handleQueryProjectMediaLibrary(
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
	query, err := parseMediaLibraryQuery(request)
	if err != nil {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_query",
			err.Error(),
		)
		return
	}
	page, err := h.library.QueryProject(
		request.Context(),
		session.Workspace.ID,
		projectID,
		query,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	writeMediaLibraryPage(response, page)
}

func (h *handler) handleQueryProjectMediaCandidates(
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
		projectaccess.PermissionAssetsAdd,
	) {
		return
	}
	query, err := parseMediaLibraryQuery(request)
	if err != nil {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_query",
			err.Error(),
		)
		return
	}
	page, err := h.library.QueryProjectCandidates(
		request.Context(),
		session.Workspace.ID,
		projectID,
		query,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	page, err = h.filterMediaLibraryPage(request, session, page)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeMediaLibraryPage(response, page)
}

func writeMediaLibraryPage(response http.ResponseWriter, page media.LibraryPage) {
	items := make([]mediaLibraryItemResponse, 0, len(page.Items))
	for _, item := range page.Items {
		var probe *mediaProbeResponse
		if item.Probe != nil {
			converted := toMediaProbeResponses([]media.Metadata{*item.Probe})[0]
			probe = &converted
		}
		renditions := make([]renditionResponse, 0, len(item.Renditions))
		for _, rendition := range item.Renditions {
			renditions = append(renditions, toRenditionResponse(rendition))
		}
		jobs := make([]mediaLibraryJobResponse, 0, len(item.Jobs))
		for _, job := range item.Jobs {
			jobs = append(jobs, toMediaLibraryJobResponse(job))
		}
		items = append(items, mediaLibraryItemResponse{
			Object:      toStorageObjectResponse(item.Object),
			Asset:       toMediaLibraryAssetResponse(item.Asset),
			Probe:       probe,
			Renditions:  renditions,
			Jobs:        jobs,
			UploadCheck: toUploadCheckResponse(item.UploadCheck),
		})
	}
	writeJSON(response, http.StatusOK, mediaLibraryPageResponse{
		Items: items, Total: page.Total, Page: page.Page, PageSize: page.PageSize,
	})
}

func (h *handler) handleListAssetVersions(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireAssetProjectPermission(
		response,
		request,
		session,
		request.PathValue("assetId"),
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	items, err := h.library.ListVersions(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("assetId"),
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	result := make([]assetVersionResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toAssetVersionResponse(item))
	}
	writeJSON(response, http.StatusOK, assetVersionListResponse{Items: result})
}

func (h *handler) handleUploadAsset(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(
		response,
		request.Body,
		maxVersionBodyBytes,
	)
	reader, err := request.MultipartReader()
	if err != nil {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_asset_upload",
			"multipart form data is required",
		)
		return
	}

	var label, note, projectID *string
	seenFields := make(map[string]bool, 3)
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			h.badRequest(response, request, partErr)
			return
		}
		fieldName := part.FormName()
		switch fieldName {
		case "label", "note", "projectId":
			if seenFields[fieldName] {
				writeError(
					response,
					http.StatusBadRequest,
					requestID(response),
					"media.invalid_asset_upload",
					"duplicate multipart field",
				)
				return
			}
			seenFields[fieldName] = true
		case "file":
		default:
			writeError(
				response,
				http.StatusBadRequest,
				requestID(response),
				"media.invalid_asset_upload",
				"unsupported multipart field",
			)
			return
		}
		switch fieldName {
		case "label":
			value, readErr := readSmallFormValue(part)
			if readErr != nil {
				h.badRequest(response, request, readErr)
				return
			}
			label = &value
		case "note":
			value, readErr := readSmallFormValue(part)
			if readErr != nil {
				h.badRequest(response, request, readErr)
				return
			}
			note = &value
		case "projectId":
			value, readErr := readSmallFormValue(part)
			if readErr != nil {
				h.badRequest(response, request, readErr)
				return
			}
			projectID = &value
		case "file":
			if part.FileName() == "" {
				writeError(
					response,
					http.StatusBadRequest,
					requestID(response),
					"media.invalid_asset_upload",
					"file is required",
				)
				return
			}
			if !h.requireUploadProjectPermission(
				response,
				request,
				session,
				&projectID,
			) {
				return
			}
			target, targetErr := h.projectUploadTarget(
				request.Context(),
				session.Workspace.ID,
				projectID,
			)
			if targetErr != nil {
				h.handleLibraryError(response, request, targetErr)
				return
			}
			item, uploadErr := h.library.UploadAsset(
				request.Context(),
				media.UploadAssetInput{
					WorkspaceID: session.Workspace.ID,
					UserID:      session.User.ID,
					Filename:    part.FileName(),
					MIMEType:    part.Header.Get("Content-Type"),
					ProjectID:   projectID,
					Target:      target,
					Label:       label,
					Note:        note,
				},
				part,
			)
			if uploadErr != nil {
				h.handleLibraryError(response, request, uploadErr)
				return
			}
			if uploadCheckAllowsProcessing(item.UploadCheck) {
				if err := h.enqueueAssetVersionProcessing(
					request,
					session.Workspace.ID,
					item,
				); err != nil {
					_ = h.library.SetVersionProcessingStatus(
						contextWithoutCancel(request),
						session.Workspace.ID,
						item.ID,
						"failed",
					)
					h.internalError(response, request, err)
					return
				}
			}
			h.recordAudit(request, audit.RecordLogInput{
				WorkspaceID:  session.Workspace.ID,
				ActorType:    "user",
				ActorID:      session.User.ID,
				Action:       "asset.created",
				ResourceType: "asset",
				ResourceID:   item.AssetID,
				After: withUploadSecurityAuditFields(map[string]any{
					"versionNumber": item.VersionNumber,
				}, target),
			})
			h.recordAudit(request, audit.RecordLogInput{
				WorkspaceID:  session.Workspace.ID,
				ActorType:    "user",
				ActorID:      session.User.ID,
				Action:       "asset.version_uploaded",
				ResourceType: "asset_version",
				ResourceID:   item.ID,
				After: withUploadSecurityAuditFields(map[string]any{
					"assetId":       item.AssetID,
					"versionNumber": item.VersionNumber,
				}, target),
			})
			h.recordUploadCheckAudit(request, session, item)
			writeJSON(response, http.StatusCreated, toAssetVersionResponse(item))
			return
		}
	}
	writeError(
		response,
		http.StatusBadRequest,
		requestID(response),
		"media.invalid_asset_upload",
		"file is required",
	)
}

func (h *handler) handleUploadAssetVersion(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireAssetMutationPermission(
		response,
		request,
		session,
		request.PathValue("assetId"),
		projectaccess.PermissionAssetsUpload,
	) {
		return
	}
	request.Body = http.MaxBytesReader(
		response,
		request.Body,
		maxVersionBodyBytes,
	)
	reader, err := request.MultipartReader()
	if err != nil {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_version_upload",
			"multipart form data is required",
		)
		return
	}

	var revision int
	var label, note *string
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			h.badRequest(response, request, partErr)
			return
		}
		switch part.FormName() {
		case "revision":
			value, readErr := readSmallFormValue(part)
			if readErr != nil {
				h.badRequest(response, request, readErr)
				return
			}
			revision, err = strconv.Atoi(value)
			if err != nil || revision < 1 {
				writeError(
					response,
					http.StatusBadRequest,
					requestID(response),
					"media.invalid_version_upload",
					"revision must be a positive integer",
				)
				return
			}
		case "label":
			value, readErr := readSmallFormValue(part)
			if readErr != nil {
				h.badRequest(response, request, readErr)
				return
			}
			label = &value
		case "note":
			value, readErr := readSmallFormValue(part)
			if readErr != nil {
				h.badRequest(response, request, readErr)
				return
			}
			note = &value
		case "file":
			if revision < 1 || part.FileName() == "" {
				writeError(
					response,
					http.StatusBadRequest,
					requestID(response),
					"media.invalid_version_upload",
					"revision and file are required",
				)
				return
			}
			target, targetErr := h.assetUploadTarget(
				request.Context(),
				session.Workspace.ID,
				request.PathValue("assetId"),
			)
			if targetErr != nil {
				h.handleLibraryError(response, request, targetErr)
				return
			}
			item, uploadErr := h.library.UploadVersion(
				request.Context(),
				media.UploadAssetVersionInput{
					WorkspaceID: session.Workspace.ID,
					UserID:      session.User.ID,
					AssetID:     request.PathValue("assetId"),
					Revision:    revision,
					Filename:    part.FileName(),
					MIMEType:    part.Header.Get("Content-Type"),
					Target:      target,
					Label:       label,
					Note:        note,
				},
				part,
			)
			if uploadErr != nil {
				h.handleLibraryError(response, request, uploadErr)
				return
			}
			if uploadCheckAllowsProcessing(item.UploadCheck) {
				if err := h.enqueueAssetVersionProcessing(
					request,
					session.Workspace.ID,
					item,
				); err != nil {
					_ = h.library.SetVersionProcessingStatus(
						contextWithoutCancel(request),
						session.Workspace.ID,
						item.ID,
						"failed",
					)
					h.internalError(response, request, err)
					return
				}
			}
			h.recordAudit(request, audit.RecordLogInput{
				WorkspaceID:  session.Workspace.ID,
				ActorType:    "user",
				ActorID:      session.User.ID,
				Action:       "asset.version_uploaded",
				ResourceType: "asset_version",
				ResourceID:   item.ID,
				After: withUploadSecurityAuditFields(map[string]any{
					"assetId":       item.AssetID,
					"versionNumber": item.VersionNumber,
				}, target),
			})
			h.recordUploadCheckAudit(request, session, item)
			writeJSON(response, http.StatusCreated, toAssetVersionResponse(item))
			return
		}
	}
	writeError(
		response,
		http.StatusBadRequest,
		requestID(response),
		"media.invalid_version_upload",
		"file is required",
	)
}

func (h *handler) handleSetCurrentAssetVersion(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body setCurrentAssetVersionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if body.VersionID == "" || body.Revision < 1 {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_version_request",
			"versionId and revision are required",
		)
		return
	}
	if !h.requireAssetMutationPermission(
		response,
		request,
		session,
		request.PathValue("assetId"),
		projectaccess.PermissionAssetsUpload,
	) {
		return
	}
	item, err := h.library.SetCurrentVersion(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("assetId"),
		body.VersionID,
		body.Revision,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "asset.current_version_changed",
		ResourceType: "asset",
		ResourceID:   item.ID,
		After: map[string]any{
			"versionId": item.VersionID,
		},
	})
	writeJSON(response, http.StatusOK, toMediaLibraryAssetResponse(&item))
}

func (h *handler) handleUpdateMediaAsset(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body updateMediaAssetRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || body.Revision < 1 {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.asset_name_invalid",
			"asset name and revision are required",
		)
		return
	}
	if !h.requireAssetMutationPermission(
		response,
		request,
		session,
		request.PathValue("assetId"),
		projectaccess.PermissionAssetsUpload,
	) {
		return
	}
	item, err := h.library.UpdateAssetName(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("assetId"),
		body.Name,
		body.Revision,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "asset.renamed",
		ResourceType: "asset",
		ResourceID:   item.ID,
		After: map[string]any{
			"name": item.Name,
		},
	})
	writeJSON(response, http.StatusOK, toMediaLibraryAssetResponse(&item))
}

func (h *handler) enqueueAssetVersionProcessing(
	request *http.Request,
	workspaceID string,
	item media.AssetVersion,
) error {
	payload, err := json.Marshal(media.ProcessAssetVersionPayload{
		VersionID:         item.ID,
		StorageObjectID:   item.StorageObjectID,
		SourceFingerprint: item.SourceFingerprint,
	})
	if err != nil {
		return err
	}
	idempotencyKey := fmt.Sprintf("process-asset-version:%s:v1", item.ID)
	_, _, err = h.jobs.Create(request.Context(), job.CreateInput{
		WorkspaceID:    workspaceID,
		Type:           media.ProcessAssetVersionJobType,
		Priority:       media.AssetVersionProcessingPriority,
		IdempotencyKey: &idempotencyKey,
		SubjectType:    "storageObject",
		SubjectID:      item.StorageObjectID,
		Payload:        payload,
	})
	return err
}

func readSmallFormValue(reader io.Reader) (string, error) {
	value, err := io.ReadAll(io.LimitReader(reader, 8*1024+1))
	if err != nil {
		return "", err
	}
	if len(value) > 8*1024 {
		return "", errors.New("multipart field is too large")
	}
	return strings.TrimSpace(string(value)), nil
}

func contextWithoutCancel(request *http.Request) context.Context {
	return context.WithoutCancel(request.Context())
}

func (h *handler) requireMediaLibraryQueryPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	query media.LibraryQuery,
) bool {
	projectID := strings.TrimSpace(query.ProjectID)
	switch projectID {
	case "", "all":
		return true
	case "unassigned":
		if session.Role == "owner" {
			return true
		}
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"当前账号不能查看未归属媒体",
		)
		return false
	default:
		return h.requireProjectPermission(
			response,
			request,
			session,
			projectID,
			projectaccess.PermissionProjectRead,
		)
	}
}

func (h *handler) filterMediaLibraryPage(
	request *http.Request,
	session identity.Session,
	page media.LibraryPage,
) (media.LibraryPage, error) {
	if session.Role == "owner" {
		return page, nil
	}
	items := make([]media.LibraryItem, 0, len(page.Items))
	for _, item := range page.Items {
		allowed, err := h.mediaLibraryItemAllowed(request, session, item)
		if err != nil {
			return media.LibraryPage{}, err
		}
		if allowed {
			items = append(items, item)
		}
	}
	page.Items = items
	page.Total = len(items)
	return page, nil
}

func (h *handler) mediaLibraryItemAllowed(
	request *http.Request,
	session identity.Session,
	item media.LibraryItem,
) (bool, error) {
	if item.Asset == nil || item.Asset.ProjectID == nil {
		return false, nil
	}
	return h.projectAccess.Allowed(
		request.Context(),
		projectaccess.CheckInput{
			WorkspaceID:   session.Workspace.ID,
			ProjectID:     *item.Asset.ProjectID,
			UserID:        session.User.ID,
			WorkspaceRole: session.Role,
			Permission:    projectaccess.PermissionProjectRead,
		},
	)
}

func (h *handler) requireUploadProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	projectID **string,
) bool {
	if projectID == nil {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"请先选择一个有上传权限的项目",
		)
		return false
	}
	if *projectID == nil || strings.TrimSpace(**projectID) == "" {
		if session.Role == "owner" {
			*projectID = nil
			return true
		}
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"请先选择一个有上传权限的项目",
		)
		return false
	}
	trimmed := strings.TrimSpace(**projectID)
	*projectID = &trimmed
	return h.requireProjectPermission(
		response,
		request,
		session,
		trimmed,
		projectaccess.PermissionAssetsUpload,
	)
}

func (h *handler) requireAssetProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	assetID string,
	permission projectaccess.Permission,
) bool {
	projectIDs, err := h.library.AssetProjectIDs(
		request.Context(),
		session.Workspace.ID,
		assetID,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return false
	}
	if len(projectIDs) == 0 {
		if session.Role == "owner" {
			return true
		}
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"当前账号不能操作未归属媒体",
		)
		return false
	}
	for _, projectID := range projectIDs {
		allowed, err := h.projectAccess.Allowed(
			request.Context(),
			projectaccess.CheckInput{
				WorkspaceID:   session.Workspace.ID,
				ProjectID:     projectID,
				UserID:        session.User.ID,
				WorkspaceRole: session.Role,
				Permission:    permission,
			},
		)
		if err != nil {
			h.internalError(response, request, err)
			return false
		}
		if allowed {
			return true
		}
	}
	writeError(
		response,
		http.StatusForbidden,
		requestID(response),
		"permission.project_denied",
		"当前账号不能操作这个媒体",
	)
	return false
}

// requireAssetMutationPermission protects fields that live on the shared asset
// record. A copied asset can be visible in multiple projects, so permission in
// just one project must not allow changing what every other project sees.
func (h *handler) requireAssetMutationPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	assetID string,
	permission projectaccess.Permission,
) bool {
	projectIDs, err := h.library.AssetProjectIDs(
		request.Context(),
		session.Workspace.ID,
		assetID,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return false
	}
	if len(projectIDs) == 0 {
		if session.Role == "owner" {
			return true
		}
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"当前账号不能操作未归属媒体",
		)
		return false
	}
	for _, projectID := range projectIDs {
		allowed, err := h.projectAccess.Allowed(
			request.Context(),
			projectaccess.CheckInput{
				WorkspaceID:   session.Workspace.ID,
				ProjectID:     projectID,
				UserID:        session.User.ID,
				WorkspaceRole: session.Role,
				Permission:    permission,
			},
		)
		if err != nil {
			h.internalError(response, request, err)
			return false
		}
		if !allowed {
			writeError(
				response,
				http.StatusForbidden,
				requestID(response),
				"permission.project_denied",
				"当前账号没有权限修改该媒体所在的全部项目",
			)
			return false
		}
	}
	return true
}

func (h *handler) requireAssignProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	projectID *string,
) bool {
	if projectID == nil || strings.TrimSpace(*projectID) == "" {
		if session.Role == "owner" {
			return true
		}
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"当前账号不能移出未指定项目的媒体",
		)
		return false
	}
	return h.requireProjectPermission(
		response,
		request,
		session,
		strings.TrimSpace(*projectID),
		projectaccess.PermissionAssetsAdd,
	)
}

func (h *handler) handleAssignMediaAssetProject(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body mediaAssetProjectRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	asset, err := h.library.AssetForStorageObject(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("objectId"),
	)
	if errors.Is(err, media.ErrLibraryAssetNotFound) {
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"media.asset_not_found",
			"没有找到对应的逻辑资产",
		)
		return
	}
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	// A null projectId is a global unassign whose side effect touches every
	// active project relation. Guard it with the all-project helper rather than
	// the any-project helper so one project's assets.remove cannot trash a
	// shared asset's relations in projects the member cannot access.
	if body.ProjectID == nil || strings.TrimSpace(*body.ProjectID) == "" {
		if !h.requireAssetMutationPermission(
			response,
			request,
			session,
			asset.ID,
			projectaccess.PermissionAssetsRemove,
		) {
			return
		}
	} else {
		if !h.requireAssetProjectPermission(
			response,
			request,
			session,
			asset.ID,
			projectaccess.PermissionAssetsRemove,
		) {
			return
		}
		if !h.requireAssignProjectPermission(response, request, session, body.ProjectID) {
			return
		}
	}
	item, err := h.library.AssignProject(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("objectId"),
		body.ProjectID,
	)
	if errors.Is(err, media.ErrLibraryAssetNotFound) {
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"media.asset_not_found",
			"没有找到对应的逻辑资产",
		)
		return
	}
	if errors.Is(err, media.ErrLibraryProjectInvalid) {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.project_invalid",
			"项目不存在、已归档或不可用",
		)
		return
	}
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toMediaLibraryAssetResponse(&item))
}

func (h *handler) handleAddProjectAsset(
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
		projectaccess.PermissionAssetsAdd,
	) {
		return
	}
	var body projectAssetRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if strings.TrimSpace(body.AssetID) == "" {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.project_asset_invalid",
			"assetId is required",
		)
		return
	}
	if !h.requireAssetProjectPermission(
		response,
		request,
		session,
		body.AssetID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	item, err := h.library.AddAssetToProject(
		request.Context(),
		media.ProjectAssetInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			AssetID:     body.AssetID,
			UserID:      session.User.ID,
		},
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project_asset.added",
		ResourceType: "asset",
		ResourceID:   item.AssetID,
		After: map[string]any{
			"projectId": item.ProjectID,
		},
	})
	writeJSON(response, http.StatusCreated, toProjectAssetResponse(item))
}

func (h *handler) handleMoveProjectAsset(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	sourceProjectID := request.PathValue("projectId")
	var body moveProjectAssetRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	body.TargetProjectID = strings.TrimSpace(body.TargetProjectID)
	if body.TargetProjectID == "" {
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.project_asset_invalid",
			"targetProjectId is required",
		)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		sourceProjectID,
		projectaccess.PermissionAssetsRemove,
	) || !h.requireProjectPermission(
		response,
		request,
		session,
		body.TargetProjectID,
		projectaccess.PermissionAssetsAdd,
	) {
		return
	}
	item, err := h.library.MoveAssetToProject(
		request.Context(),
		media.MoveProjectAssetInput{
			WorkspaceID:     session.Workspace.ID,
			SourceProjectID: sourceProjectID,
			TargetProjectID: body.TargetProjectID,
			AssetID:         request.PathValue("assetId"),
			UserID:          session.User.ID,
		},
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project_asset.moved",
		ResourceType: "asset",
		ResourceID:   item.AssetID,
		Before: map[string]any{
			"projectId": sourceProjectID,
		},
		After: map[string]any{
			"projectId": item.ProjectID,
		},
	})
	writeJSON(response, http.StatusOK, toProjectAssetResponse(item))
}

func (h *handler) handleTrashProjectAsset(
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
		projectaccess.PermissionAssetsRemove,
	) {
		return
	}
	item, err := h.library.TrashAssetFromProject(
		request.Context(),
		media.ProjectAssetInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			AssetID:     request.PathValue("assetId"),
			UserID:      session.User.ID,
		},
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project_asset.trashed",
		ResourceType: "asset",
		ResourceID:   item.AssetID,
		Before: map[string]any{
			"projectId": item.ProjectID,
		},
	})
	writeJSON(response, http.StatusOK, toProjectAssetResponse(item))
}

func (h *handler) handleRestoreProjectAsset(
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
		projectaccess.PermissionAssetsAdd,
	) {
		return
	}
	item, err := h.library.RestoreAssetToProject(
		request.Context(),
		media.ProjectAssetInput{
			WorkspaceID: session.Workspace.ID,
			ProjectID:   projectID,
			AssetID:     request.PathValue("assetId"),
			UserID:      session.User.ID,
		},
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "project_asset.restored",
		ResourceType: "asset",
		ResourceID:   item.AssetID,
		After: map[string]any{
			"projectId": item.ProjectID,
		},
	})
	writeJSON(response, http.StatusOK, toProjectAssetResponse(item))
}

func (h *handler) handleListProjectAssetTrash(
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
	items, err := h.library.ListProjectTrash(
		request.Context(),
		session.Workspace.ID,
		projectID,
	)
	if err != nil {
		h.handleLibraryError(response, request, err)
		return
	}
	result := make([]projectAssetResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toProjectAssetResponse(item))
	}
	writeJSON(response, http.StatusOK, projectAssetListResponse{Items: result})
}

func (h *handler) assetUploadTarget(
	ctx context.Context,
	workspaceID string,
	assetID string,
) (*media.UploadTarget, error) {
	projectID, err := h.library.AssetProjectID(ctx, workspaceID, assetID)
	if err != nil {
		return nil, err
	}
	return h.projectUploadTarget(ctx, workspaceID, projectID)
}

func (h *handler) projectUploadTarget(
	ctx context.Context,
	workspaceID string,
	projectID *string,
) (*media.UploadTarget, error) {
	if projectID == nil || strings.TrimSpace(*projectID) == "" {
		return nil, nil
	}
	projectIDValue := strings.TrimSpace(*projectID)
	selections, err := h.projectStorage.ListSelections(
		ctx,
		workspaceID,
		projectIDValue,
	)
	if err != nil {
		if errors.Is(err, projectstorage.ErrInvalidInput) ||
			errors.Is(err, projectstorage.ErrNotFound) {
			return nil, media.ErrLibraryUploadTargetNeeded
		}
		return nil, err
	}
	for _, selection := range selections {
		if selection.Purpose != "upload" {
			continue
		}
		if selection.Grant.Status != "active" ||
			selection.Grant.AuthorizedRootID == nil {
			return nil, media.ErrLibraryUploadTargetInvalid
		}
		capacity, ok := h.localManagedUploadBucketCapacity(
			ctx,
			workspaceID,
			*selection.Grant.AuthorizedRootID,
		)
		if ok {
			return &media.UploadTarget{
				TargetKind:        "local_managed_bucket",
				ProjectID:         projectIDValue,
				StorageProviderID: selection.Grant.StorageProviderID,
				AuthorizedRootID:  *selection.Grant.AuthorizedRootID,
				UploadSecurityPolicy: normalizeUploadSecurityPolicy(
					capacity.Bucket.UploadSecurityPolicy,
				),
				LocalManagedBucketID: capacity.Bucket.ID,
				QuotaBytes:           capacity.QuotaBytes,
				UsedBytesEstimate:    capacity.UsedBytesEstimate,
				QuotaAvailableBytes:  capacity.QuotaAvailableBytes,
				DiskAvailableBytes:   capacity.DiskAvailableBytes,
				MaxSingleFileBytes:   media.DefaultMaxUploadFileBytes,
				MinimumFreeBytes:     media.DefaultMinimumFreeBytes,
			}, nil
		}

		if remoteProjectUploadGrantAllowed(selection.Grant) {
			return &media.UploadTarget{
				TargetKind:           "remote_storage",
				ProjectID:            projectIDValue,
				StorageProviderID:    selection.Grant.StorageProviderID,
				AuthorizedRootID:     *selection.Grant.AuthorizedRootID,
				UploadSecurityPolicy: "quick",
				MaxSingleFileBytes:   media.DefaultMaxUploadFileBytes,
				MinimumFreeBytes:     media.DefaultMinimumFreeBytes,
				UsedBytesEstimate:    0,
				QuotaBytes:           nil,
				QuotaAvailableBytes:  nil,
				DiskAvailableBytes:   nil,
				LocalManagedBucketID: "",
			}, nil
		}

		return nil, media.ErrLibraryUploadTargetInvalid
	}
	return nil, media.ErrLibraryUploadTargetNeeded
}

func remoteProjectUploadGrantAllowed(grant projectstorage.Grant) bool {
	if grant.AuthorizedRootID == nil || grant.RootStatus == nil {
		return false
	}
	if grant.Status != "active" ||
		grant.ProviderStatus != "active" ||
		*grant.RootStatus != "available" {
		return false
	}
	switch grant.ProviderKind {
	case "webdav", "s3":
		return true
	default:
		return false
	}
}

func uploadTargetKind(target *media.UploadTarget) string {
	if target == nil {
		return "managed_default"
	}
	switch strings.TrimSpace(target.TargetKind) {
	case "remote_storage":
		return "remote_storage"
	case "local_managed_bucket":
		return "local_managed_bucket"
	default:
		if strings.TrimSpace(target.LocalManagedBucketID) == "" {
			return "remote_storage"
		}
		return "local_managed_bucket"
	}
}

func (h *handler) localManagedUploadBucketCapacity(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (storage.LocalManagedBucketCapacity, bool) {
	capacity, err := h.storage.LocalManagedBucketCapacityForRoot(
		ctx,
		workspaceID,
		rootID,
	)
	if err != nil {
		return storage.LocalManagedBucketCapacity{}, false
	}
	if capacity.Bucket.ProjectAvailable &&
		capacity.Bucket.Status == "active" &&
		capacity.Bucket.Purpose == "upload" {
		return capacity, true
	}
	return storage.LocalManagedBucketCapacity{}, false
}

func withUploadSecurityAuditFields(
	fields map[string]any,
	target *media.UploadTarget,
) map[string]any {
	if fields == nil {
		fields = map[string]any{}
	}
	if target == nil {
		fields["uploadTarget"] = "managed_default"
		fields["uploadSecurityPolicy"] = "standard"
		return fields
	}
	fields["uploadTarget"] = uploadTargetKind(target)
	fields["uploadSecurityPolicy"] = normalizeUploadSecurityPolicy(
		target.UploadSecurityPolicy,
	)
	fields["storageProviderId"] = strings.TrimSpace(target.StorageProviderID)
	fields["authorizedRootId"] = strings.TrimSpace(target.AuthorizedRootID)
	if strings.TrimSpace(target.LocalManagedBucketID) != "" {
		fields["localManagedBucketId"] = strings.TrimSpace(target.LocalManagedBucketID)
	}
	return fields
}

func (h *handler) recordUploadCheckAudit(
	request *http.Request,
	session identity.Session,
	item media.AssetVersion,
) {
	if item.UploadCheck == nil {
		return
	}
	action := "upload_check." + item.UploadCheck.Status
	if item.UploadCheck.Status == "" {
		action = "upload_check.ready"
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       action,
		ResourceType: "upload_check",
		ResourceID:   item.UploadCheck.ID,
		After: map[string]any{
			"assetId":              item.AssetID,
			"assetVersionId":       item.ID,
			"storageObjectId":      item.StorageObjectID,
			"projectId":            item.UploadCheck.ProjectID,
			"uploadSecurityPolicy": item.UploadCheck.UploadSecurityPolicy,
			"status":               item.UploadCheck.Status,
			"resultCode":           item.UploadCheck.ResultCode,
		},
	})
}

func uploadCheckAllowsProcessing(item *media.UploadCheck) bool {
	return item == nil || item.Status == "ready"
}

func normalizeUploadSecurityPolicy(value string) string {
	normalized := strings.TrimSpace(value)
	switch normalized {
	case "quick", "standard", "enhanced":
		return normalized
	default:
		return "standard"
	}
}

func (h *handler) handleLibraryError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	var uploadTypeErr *media.UploadTypeError
	var uploadLimitErr *media.UploadLimitError
	switch {
	case errors.Is(err, media.ErrLibraryAssetNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"media.asset_not_found",
			"media asset was not found",
		)
	case errors.Is(err, media.ErrLibraryVersionNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"media.version_not_found",
			"media asset version was not found",
		)
	case errors.Is(err, media.ErrLibraryRevisionConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"resource.revision_conflict",
			"media asset was changed by another operation",
		)
	case errors.Is(err, media.ErrLibraryAssetNameInvalid):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.asset_name_invalid",
			"media asset name is invalid",
		)
	case errors.As(err, &uploadTypeErr):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.upload_type_rejected",
			uploadTypeErr.UserMessage(),
		)
	case errors.As(err, &uploadLimitErr):
		status := http.StatusConflict
		code := "project_storage.upload_quota_exceeded"
		switch {
		case errors.Is(err, media.ErrLibraryUploadFileTooLarge):
			status = http.StatusRequestEntityTooLarge
			code = "media.upload_too_large"
		case errors.Is(err, media.ErrLibraryUploadDiskSpaceLow):
			status = http.StatusInsufficientStorage
			code = "project_storage.disk_space_low"
		case errors.Is(err, media.ErrLibraryUploadBusy):
			status = http.StatusTooManyRequests
			code = "project_storage.staging_busy"
		}
		writeError(
			response,
			status,
			requestID(response),
			code,
			uploadLimitErr.UserMessage(),
		)
	case errors.Is(err, media.ErrLibraryUploadInvalid):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.invalid_version_upload",
			"asset version upload is invalid",
		)
	case errors.Is(err, media.ErrLibraryUploadUnavailable):
		writeError(
			response,
			http.StatusServiceUnavailable,
			requestID(response),
			"media.version_upload_unavailable",
			"asset version upload is unavailable",
		)
	case errors.Is(err, media.ErrLibraryUploadTargetNeeded):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"project_storage.upload_not_configured",
			"project upload storage is not configured",
		)
	case errors.Is(err, media.ErrLibraryUploadTargetInvalid):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"project_storage.upload_unavailable",
			"project upload storage is unavailable",
		)
	case errors.Is(err, media.ErrLibraryProjectInvalid):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.project_invalid",
			"project is not available",
		)
	case errors.Is(err, media.ErrProjectAssetInvalid):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"media.project_asset_invalid",
			"project asset request is invalid",
		)
	case errors.Is(err, media.ErrProjectAssetNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"media.project_asset_not_found",
			"project asset relation was not found",
		)
	default:
		h.internalError(response, request, err)
	}
}

func parseMediaLibraryQuery(request *http.Request) (media.LibraryQuery, error) {
	values := request.URL.Query()
	page, err := positiveInt(values.Get("page"), 1)
	if err != nil {
		return media.LibraryQuery{}, err
	}
	pageSize, err := positiveInt(values.Get("pageSize"), 48)
	if err != nil {
		return media.LibraryQuery{}, err
	}
	var modifiedFrom, modifiedTo *time.Time
	if value := values.Get("modifiedFrom"); value != "" {
		parsed, parseErr := time.Parse("2006-01-02", value)
		if parseErr != nil {
			return media.LibraryQuery{}, parseErr
		}
		modifiedFrom = &parsed
	}
	if value := values.Get("modifiedTo"); value != "" {
		parsed, parseErr := time.Parse("2006-01-02", value)
		if parseErr != nil {
			return media.LibraryQuery{}, parseErr
		}
		parsed = parsed.AddDate(0, 0, 1)
		modifiedTo = &parsed
	}
	return media.LibraryQuery{
		Search: values.Get("search"), MediaType: values.Get("mediaType"),
		State: values.Get("state"), ModifiedFrom: modifiedFrom,
		ModifiedTo: modifiedTo, Sort: values.Get("sort"),
		ProjectID: values.Get("projectId"),
		Page:      page, PageSize: pageSize,
	}, nil
}

func toMediaLibraryAssetResponse(
	item *media.LibraryAsset,
) *mediaLibraryAssetResponse {
	if item == nil {
		return nil
	}
	return &mediaLibraryAssetResponse{
		ID: item.ID, ProjectID: item.ProjectID, ProjectName: item.ProjectName,
		Name: item.Name, Type: item.Type, Revision: item.Revision,
		VersionID: item.VersionID, VersionNumber: item.VersionNumber,
	}
}

func toProjectAssetResponse(item media.ProjectAsset) projectAssetResponse {
	return projectAssetResponse{
		ID:             item.ID,
		WorkspaceID:    item.WorkspaceID,
		ProjectID:      item.ProjectID,
		ProjectName:    item.ProjectName,
		AssetID:        item.AssetID,
		AssetName:      item.AssetName,
		AssetType:      item.AssetType,
		Status:         item.Status,
		AddedBy:        item.AddedBy,
		TrashedBy:      item.TrashedBy,
		TrashedAt:      item.TrashedAt,
		TrashExpiresAt: item.TrashExpiresAt,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func toAssetVersionResponse(item media.AssetVersion) assetVersionResponse {
	var probe *mediaProbeResponse
	if item.Probe != nil {
		converted := toMediaProbeResponses([]media.Metadata{*item.Probe})[0]
		probe = &converted
	}
	renditions := make([]renditionResponse, 0, len(item.Renditions))
	for _, rendition := range item.Renditions {
		renditions = append(renditions, toRenditionResponse(rendition))
	}
	return assetVersionResponse{
		ID:                  item.ID,
		AssetID:             item.AssetID,
		VersionNumber:       item.VersionNumber,
		Label:               item.Label,
		Note:                item.Note,
		ProcessingStatus:    item.ProcessingStatus,
		ProcessingStage:     media.AssetVersionProcessingStage(item),
		SourceFilename:      item.SourceFilename,
		SourceMIME:          item.SourceMIME,
		SourceSizeBytes:     item.SourceSizeBytes,
		SourceFingerprint:   item.SourceFingerprint,
		StorageObjectID:     item.StorageObjectID,
		AuthorizedRootID:    item.AuthorizedRootID,
		StorageObjectKey:    item.StorageObjectKey,
		StorageObjectStatus: item.StorageObjectStatus,
		CreatedAt:           item.CreatedAt,
		IsCurrent:           item.IsCurrent,
		Probe:               probe,
		Renditions:          renditions,
		UploadCheck:         toUploadCheckResponse(item.UploadCheck),
	}
}

func toUploadCheckResponse(item *media.UploadCheck) *uploadCheckResponse {
	if item == nil {
		return nil
	}
	return &uploadCheckResponse{
		ID:                   item.ID,
		ProjectID:            item.ProjectID,
		AssetID:              item.AssetID,
		AssetVersionID:       item.AssetVersionID,
		StorageObjectID:      item.StorageObjectID,
		SourceType:           item.SourceType,
		UploadSecurityPolicy: item.UploadSecurityPolicy,
		Status:               item.Status,
		ResultCode:           item.ResultCode,
		Message:              item.Message,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
		CompletedAt:          item.CompletedAt,
		QuarantinedAt:        item.QuarantinedAt,
		RejectedAt:           item.RejectedAt,
	}
}

func positiveInt(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed < 1 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func toMediaLibraryJobResponse(item media.LibraryJob) mediaLibraryJobResponse {
	var progress *jobProgressResponse
	if item.ProgressCurrent != nil && item.ProgressTotal != nil && item.ProgressUnit != nil {
		progress = &jobProgressResponse{
			Current: *item.ProgressCurrent,
			Total:   *item.ProgressTotal,
			Unit:    *item.ProgressUnit,
		}
	}
	var jobErr *jobErrorResponse
	if item.ErrorCode != nil && item.ErrorMessage != nil {
		jobErr = &jobErrorResponse{Code: *item.ErrorCode, Message: *item.ErrorMessage}
	}
	return mediaLibraryJobResponse{
		ID: item.ID, Type: item.Type, Status: item.Status,
		Priority: item.Priority,
		Subject:  jobSubjectResponse{Type: item.SubjectType, ID: item.SubjectID},
		Progress: progress, Error: jobErr, AttemptCount: item.AttemptCount,
		MaxAttempts: item.MaxAttempts, CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt, StartedAt: item.StartedAt,
		CompletedAt: item.CompletedAt,
	}
}
