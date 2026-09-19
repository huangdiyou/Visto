package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/media"
)

type uploadCheckListItemResponse struct {
	UploadCheck     uploadCheckResponse `json:"uploadCheck"`
	AssetName       string              `json:"assetName"`
	SourceFilename  string              `json:"sourceFilename"`
	SourceSizeBytes int64               `json:"sourceSizeBytes"`
	ProjectName     *string             `json:"projectName"`
	UploadedByName  *string             `json:"uploadedByName"`
}

type uploadCheckListResponse struct {
	Items    []uploadCheckListItemResponse `json:"items"`
	Total    int                           `json:"total"`
	Page     int                           `json:"page"`
	PageSize int                           `json:"pageSize"`
}

type uploadCheckActionRequest struct {
	Message string `json:"message"`
}

func (h *handler) handleListUploadChecks(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	values := request.URL.Query()
	page, err := positiveInt(values.Get("page"), 1)
	if err != nil {
		h.badRequest(response, request, err)
		return
	}
	pageSize, err := positiveInt(values.Get("pageSize"), 50)
	if err != nil {
		h.badRequest(response, request, err)
		return
	}
	pageResult, err := h.library.ListUploadChecks(
		request.Context(),
		session.Workspace.ID,
		media.UploadCheckQuery{
			Status:    values.Get("status"),
			ProjectID: values.Get("projectId"),
			Page:      page,
			PageSize:  pageSize,
		},
	)
	if err != nil {
		h.handleUploadCheckError(response, request, err)
		return
	}
	items := make([]uploadCheckListItemResponse, 0, len(pageResult.Items))
	for _, item := range pageResult.Items {
		items = append(items, toUploadCheckListItemResponse(item))
	}
	writeJSON(response, http.StatusOK, uploadCheckListResponse{
		Items: items, Total: pageResult.Total,
		Page: pageResult.Page, PageSize: pageResult.PageSize,
	})
}

func (h *handler) handleReleaseUploadCheck(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	message, ok := decodeUploadCheckActionMessage(response, request)
	if !ok {
		return
	}
	check, err := h.library.ResolveUploadCheck(
		request.Context(),
		media.ResolveUploadCheckInput{
			WorkspaceID: session.Workspace.ID,
			ID:          request.PathValue("checkId"),
			Message:     message,
		},
	)
	if err != nil {
		h.handleUploadCheckError(response, request, err)
		return
	}
	h.recordManualUploadCheckAudit(request, session.User.ID, check)
	writeJSON(response, http.StatusOK, toUploadCheckResponse(&check))
}

func (h *handler) handleRejectUploadCheck(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	message, ok := decodeUploadCheckActionMessage(response, request)
	if !ok {
		return
	}
	check, err := h.library.RejectUploadCheck(
		request.Context(),
		media.RejectUploadCheckInput{
			WorkspaceID: session.Workspace.ID,
			ID:          request.PathValue("checkId"),
			Message:     message,
		},
	)
	if err != nil {
		h.handleUploadCheckError(response, request, err)
		return
	}
	h.recordManualUploadCheckAudit(request, session.User.ID, check)
	writeJSON(response, http.StatusOK, toUploadCheckResponse(&check))
}

func decodeUploadCheckActionMessage(
	response http.ResponseWriter,
	request *http.Request,
) (*string, bool) {
	var body uploadCheckActionRequest
	if request.Body == nil {
		return nil, true
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, http.ErrBodyReadAfterClose) {
			return nil, true
		}
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"request.invalid",
			"request body must be valid JSON",
		)
		return nil, false
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		return nil, true
	}
	return &message, true
}

func (h *handler) recordManualUploadCheckAudit(
	request *http.Request,
	userID string,
	check media.UploadCheck,
) {
	action := "upload_check." + check.Status
	if check.Status == "" {
		action = "upload_check.ready"
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  check.WorkspaceID,
		ActorType:    "user",
		ActorID:      userID,
		Action:       action,
		ResourceType: "upload_check",
		ResourceID:   check.ID,
		After: map[string]any{
			"projectId":            check.ProjectID,
			"assetId":              check.AssetID,
			"assetVersionId":       check.AssetVersionID,
			"storageObjectId":      check.StorageObjectID,
			"uploadSecurityPolicy": check.UploadSecurityPolicy,
			"status":               check.Status,
			"resultCode":           check.ResultCode,
		},
	})
}

func toUploadCheckListItemResponse(
	item media.UploadCheckListItem,
) uploadCheckListItemResponse {
	return uploadCheckListItemResponse{
		UploadCheck:     *toUploadCheckResponse(&item.Check),
		AssetName:       item.AssetName,
		SourceFilename:  item.SourceFilename,
		SourceSizeBytes: item.SourceSizeBytes,
		ProjectName:     item.ProjectName,
		UploadedByName:  item.UploadedByName,
	}
}

func (h *handler) handleUploadCheckError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, media.ErrUploadCheckNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"upload_check.not_found",
			"upload check was not found",
		)
	case errors.Is(err, media.ErrUploadCheckConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"upload_check.status_conflict",
			"upload check is no longer quarantined",
		)
	case errors.Is(err, media.ErrUploadCheckInvalid):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"upload_check.invalid",
			"upload check request is invalid",
		)
	default:
		h.internalError(response, request, err)
	}
}
