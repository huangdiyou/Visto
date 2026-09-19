package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/storage"
)

type authorizedRootResponse struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspaceId"`
	StorageProviderID string               `json:"storageProviderId"`
	DisplayName       string               `json:"displayName"`
	DisplayPath       string               `json:"displayPath"`
	Mode              string               `json:"mode"`
	ScanEnabled       bool                 `json:"scanEnabled"`
	Status            string               `json:"status"`
	Revision          int                  `json:"revision"`
	CreatedAt         time.Time            `json:"createdAt"`
	UpdatedAt         time.Time            `json:"updatedAt"`
	LastScanAt        *time.Time           `json:"lastScanAt"`
	LastScanStatus    *string              `json:"lastScanStatus"`
	LastScanSummary   *storage.ScanSummary `json:"lastScanSummary"`
}

type authorizedRootListResponse struct {
	Items []authorizedRootResponse `json:"items"`
}

type localObjectMetadataResponse struct {
	ObjectKey  string    `json:"objectKey"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	SizeBytes  int64     `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
	MIMEType   string    `json:"mimeType"`
}

type authorizedRootRequest struct {
	DisplayName string `json:"displayName"`
	LocalPath   string `json:"localPath"`
	Mode        string `json:"mode"`
	ScanEnabled bool   `json:"scanEnabled"`
}

type authorizedRootUpdateRequest struct {
	DisplayName string `json:"displayName"`
	ScanEnabled bool   `json:"scanEnabled"`
	Revision    int    `json:"revision"`
}

type storageObjectResponse struct {
	ID                string     `json:"id"`
	AuthorizedRootID  string     `json:"authorizedRootId"`
	ObjectKey         string     `json:"objectKey"`
	Status            string     `json:"status"`
	SizeBytes         int64      `json:"sizeBytes"`
	ModifiedAt        time.Time  `json:"modifiedAt"`
	MIMEType          string     `json:"mimeType"`
	FirstDiscoveredAt time.Time  `json:"firstDiscoveredAt"`
	LastSeenAt        *time.Time `json:"lastSeenAt"`
	MissingSince      *time.Time `json:"missingSince"`
}

type storageObjectListResponse struct {
	Items []storageObjectResponse `json:"items"`
}

func toStorageObjectResponse(object storage.StoredObject) storageObjectResponse {
	return storageObjectResponse{
		ID:                object.ID,
		AuthorizedRootID:  object.AuthorizedRootID,
		ObjectKey:         object.ObjectKey,
		Status:            object.Status,
		SizeBytes:         object.SizeBytes,
		ModifiedAt:        object.ModifiedAt,
		MIMEType:          object.MIMEType,
		FirstDiscoveredAt: object.FirstDiscoveredAt,
		LastSeenAt:        object.LastSeenAt,
		MissingSince:      object.MissingSince,
	}
}

type scanResultResponse struct {
	ID          string              `json:"id"`
	RootID      string              `json:"rootId"`
	Status      string              `json:"status"`
	Summary     storage.ScanSummary `json:"summary"`
	StartedAt   time.Time           `json:"startedAt"`
	CompletedAt time.Time           `json:"completedAt"`
}

func (h *handler) handleListAuthorizedRoots(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	roots, err := h.storage.ListRoots(request.Context(), session.Workspace.ID)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	items := make([]authorizedRootResponse, 0, len(roots))
	for _, root := range roots {
		items = append(items, toAuthorizedRootResponse(root))
	}
	writeJSON(response, http.StatusOK, authorizedRootListResponse{Items: items})
}

func (h *handler) handleCreateAuthorizedRoot(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if session.Role != "owner" {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.denied",
			"只有 Owner 可以授权本地目录",
		)
		return
	}
	if !h.requireHostManagement(response, request) {
		return
	}

	var body authorizedRootRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	root, err := h.storage.RegisterLocalRoot(
		request.Context(),
		storage.RegisterLocalRootInput{
			WorkspaceID: session.Workspace.ID,
			DisplayName: body.DisplayName,
			LocalPath:   body.LocalPath,
			Mode:        defaultString(body.Mode, "referenced"),
			ScanEnabled: body.ScanEnabled,
		},
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, toAuthorizedRootResponse(root))
}

func (h *handler) handleGetAuthorizedRoot(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	root, err := h.storage.Root(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toAuthorizedRootResponse(root))
}

func (h *handler) handleUpdateAuthorizedRoot(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireHostManagement(response, request) {
		return
	}

	var body authorizedRootUpdateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	root, err := h.storage.UpdateRoot(request.Context(), storage.UpdateRootInput{
		WorkspaceID: session.Workspace.ID,
		ID:          request.PathValue("rootId"),
		DisplayName: body.DisplayName,
		ScanEnabled: body.ScanEnabled,
		Revision:    body.Revision,
	})
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toAuthorizedRootResponse(root))
}

func (h *handler) handleDeleteAuthorizedRoot(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireHostManagement(response, request) {
		return
	}

	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if err := h.storage.DeleteRoot(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
		body.Revision,
	); err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleScanAuthorizedRoot(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireHostManagement(response, request) {
		return
	}
	result, err := h.storage.ScanRoot(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, scanResultResponse{
		ID:          result.ID,
		RootID:      result.RootID,
		Status:      result.Status,
		Summary:     result.Summary,
		StartedAt:   result.StartedAt,
		CompletedAt: result.CompletedAt,
	})
}

func (h *handler) handleListStorageObjects(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	// Raw root enumeration has no project context. Keep it on the Core host so
	// remote workspace members cannot use it to bypass project media access.
	if !h.requireHostManagement(response, request) {
		return
	}
	objects, err := h.storage.ListObjects(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	items := make([]storageObjectResponse, 0, len(objects))
	for _, object := range objects {
		items = append(items, toStorageObjectResponse(object))
	}
	writeJSON(response, http.StatusOK, storageObjectListResponse{Items: items})
}

func (h *handler) handleLocalObjectMetadata(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	// Object keys are storage-root scoped rather than project scoped.
	if !h.requireHostManagement(response, request) {
		return
	}
	adapter, err := h.storage.Adapter(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	info, err := adapter.Stat(request.Context(), request.URL.Query().Get("key"))
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toLocalObjectMetadataResponse(info))
}

func (h *handler) handleLocalObjectContent(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	// Serve project media through the asset endpoints; this legacy raw-object
	// endpoint is reserved for host-local storage administration.
	if !h.requireHostManagement(response, request) {
		return
	}
	adapter, err := h.storage.Adapter(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}

	objectKey := request.URL.Query().Get("key")
	info, err := adapter.Stat(request.Context(), objectKey)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	byteRange, partial, err := parseRangeHeader(
		request.Header.Get("Range"),
		info.SizeBytes,
	)
	if err != nil {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.SizeBytes))
		h.handleStorageError(response, request, err)
		return
	}

	reader, rangedInfo, err := adapter.OpenRange(
		request.Context(),
		objectKey,
		byteRange,
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	defer reader.Close()

	length := byteRange.Length
	if length == 0 {
		length = rangedInfo.SizeBytes - byteRange.Offset
	}
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Type", rangedInfo.MIMEType)
	response.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	response.Header().Set("Cache-Control", "private, no-store")
	if partial {
		response.Header().Set(
			"Content-Range",
			fmt.Sprintf(
				"bytes %d-%d/%d",
				byteRange.Offset,
				byteRange.Offset+length-1,
				rangedInfo.SizeBytes,
			),
		)
		response.WriteHeader(http.StatusPartialContent)
	} else {
		response.WriteHeader(http.StatusOK)
	}

	if _, err := io.Copy(response, reader); err != nil &&
		!errors.Is(err, request.Context().Err()) {
		h.logger.Warn(
			"local object stream interrupted",
			"request_id", requestID(response),
			"error", err,
		)
	}
}

func parseRangeHeader(value string, size int64) (storage.ByteRange, bool, error) {
	if size < 0 {
		return storage.ByteRange{}, false, storage.ErrRangeInvalid
	}
	if strings.TrimSpace(value) == "" {
		return storage.ByteRange{Offset: 0, Length: 0}, false, nil
	}
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return storage.ByteRange{}, false, storage.ErrRangeInvalid
	}

	specification := strings.TrimSpace(strings.TrimPrefix(value, "bytes="))
	startText, endText, ok := strings.Cut(specification, "-")
	if !ok {
		return storage.ByteRange{}, false, storage.ErrRangeInvalid
	}
	if startText == "" {
		suffixLength, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || suffixLength <= 0 || size == 0 {
			return storage.ByteRange{}, false, storage.ErrRangeInvalid
		}
		if suffixLength > size {
			suffixLength = size
		}
		return storage.ByteRange{
			Offset: size - suffixLength,
			Length: suffixLength,
		}, true, nil
	}

	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 || start >= size {
		return storage.ByteRange{}, false, storage.ErrRangeInvalid
	}
	if endText == "" {
		return storage.ByteRange{
			Offset: start,
			Length: size - start,
		}, true, nil
	}

	end, err := strconv.ParseInt(endText, 10, 64)
	if err != nil || end < start {
		return storage.ByteRange{}, false, storage.ErrRangeInvalid
	}
	if end >= size {
		end = size - 1
	}
	return storage.ByteRange{
		Offset: start,
		Length: end - start + 1,
	}, true, nil
}

func (h *handler) handleStorageError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, storage.ErrRootNotFound),
		errors.Is(err, storage.ErrObjectNotFound),
		errors.Is(err, storage.ErrBucketNotFound),
		errors.Is(err, os.ErrNotExist):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"resource.not_found",
			"文件或授权目录不存在",
		)
	case errors.Is(err, storage.ErrPathInvalid),
		errors.Is(err, storage.ErrPathEscapesRoot),
		errors.Is(err, storage.ErrNotRegularFile),
		errors.Is(err, storage.ErrInvalidRootInput):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"storage.path_invalid",
			"文件路径无效",
		)
	case errors.Is(err, storage.ErrRangeInvalid):
		writeError(
			response,
			http.StatusRequestedRangeNotSatisfiable,
			requestID(response),
			"storage.range_invalid",
			"请求的文件范围无效",
		)
	case errors.Is(err, storage.ErrRevisionConflict),
		errors.Is(err, storage.ErrBucketRevisionConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"resource.revision_conflict",
			"目录设置已被更新，请刷新后重试",
		)
	case errors.Is(err, storage.ErrScanLimitExceeded):
		writeError(
			response,
			http.StatusRequestEntityTooLarge,
			requestID(response),
			"storage.scan_limit_exceeded",
			"目录超出当前扫描限制",
		)
	case errors.Is(err, storage.ErrRootUnavailable),
		errors.Is(err, storage.ErrPathChanged),
		errors.Is(err, os.ErrPermission):
		writeError(
			response,
			http.StatusServiceUnavailable,
			requestID(response),
			"storage.unavailable",
			"授权目录暂时不可用",
		)
	default:
		h.internalError(response, request, err)
	}
}

func toAuthorizedRootResponse(root storage.AuthorizedRoot) authorizedRootResponse {
	return authorizedRootResponse{
		ID:                root.ID,
		WorkspaceID:       root.WorkspaceID,
		StorageProviderID: root.StorageProviderID,
		DisplayName:       root.DisplayName,
		DisplayPath:       root.DisplayPath,
		Mode:              root.Mode,
		ScanEnabled:       root.ScanEnabled,
		Status:            root.Status,
		Revision:          root.Revision,
		CreatedAt:         root.CreatedAt,
		UpdatedAt:         root.UpdatedAt,
		LastScanAt:        root.LastScanAt,
		LastScanStatus:    root.LastScanStatus,
		LastScanSummary:   root.LastScanSummary,
	}
}

func toLocalObjectMetadataResponse(info storage.FileInfo) localObjectMetadataResponse {
	return localObjectMetadataResponse{
		ObjectKey:  info.ObjectKey,
		Name:       info.Name,
		Kind:       info.Kind,
		SizeBytes:  info.SizeBytes,
		ModifiedAt: info.ModifiedAt,
		MIMEType:   info.MIMEType,
	}
}
