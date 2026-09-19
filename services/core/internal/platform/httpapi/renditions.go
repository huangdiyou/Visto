package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/storage"
)

type renditionResponse struct {
	ID                    string     `json:"id"`
	SourceStorageObjectID string     `json:"sourceStorageObjectId"`
	Kind                  string     `json:"kind"`
	ProfileKey            string     `json:"profileKey"`
	ProfileVersion        int        `json:"profileVersion"`
	SourceFingerprint     string     `json:"sourceFingerprint"`
	Status                string     `json:"status"`
	MIMEType              *string    `json:"mimeType"`
	Width                 *int       `json:"width"`
	Height                *int       `json:"height"`
	DurationUS            *int64     `json:"durationUs"`
	SizeBytes             *int64     `json:"sizeBytes"`
	MetadataJSON          string     `json:"metadataJson"`
	ErrorCode             *string    `json:"errorCode"`
	ErrorMessage          *string    `json:"errorMessage"`
	ContentURL            *string    `json:"contentUrl"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	CompletedAt           *time.Time `json:"completedAt"`
}

func (h *handler) handleGenerateVideoRenditions(
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
	candidates, err := h.renditions.VideoCandidates(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleRenditionError(response, request, err)
		return
	}
	items := make([]jobResponse, 0, len(candidates))
	for _, object := range candidates {
		payload, marshalErr := json.Marshal(media.GenerateImageRenditionsPayload{
			StorageObjectID:   object.ID,
			SourceFingerprint: object.QuickFingerprint,
		})
		if marshalErr != nil {
			h.internalError(response, request, marshalErr)
			return
		}
		idempotencyKey := fmt.Sprintf(
			"video-enhancements:%s:%s:v1",
			object.ID,
			object.QuickFingerprint,
		)
		item, _, createErr := h.jobs.Create(request.Context(), job.CreateInput{
			WorkspaceID:    session.Workspace.ID,
			Type:           media.GenerateVideoEnhancementsJobType,
			Priority:       media.VideoEnhancementPriority,
			IdempotencyKey: &idempotencyKey,
			SubjectType:    "storageObject",
			SubjectID:      object.ID,
			Payload:        payload,
		})
		if createErr != nil {
			h.handleJobError(response, request, createErr)
			return
		}
		items = append(items, toJobResponse(item))
	}
	writeJSON(response, http.StatusAccepted, renditionJobsResponse{Jobs: items})
}

type renditionListResponse struct {
	Items []renditionResponse `json:"items"`
}

type renditionJobsResponse struct {
	Jobs []jobResponse `json:"jobs"`
}

func (h *handler) handleListRenditions(
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
	items, err := h.renditions.ListRoot(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleRenditionError(response, request, err)
		return
	}
	result := make([]renditionResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toRenditionResponse(item))
	}
	writeJSON(response, http.StatusOK, renditionListResponse{Items: result})
}

func (h *handler) handleGenerateRenditions(
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
	candidates, err := h.renditions.Candidates(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleRenditionError(response, request, err)
		return
	}
	items := make([]jobResponse, 0, len(candidates))
	for _, object := range candidates {
		payload, marshalErr := json.Marshal(media.GenerateImageRenditionsPayload{
			StorageObjectID:   object.ID,
			SourceFingerprint: object.QuickFingerprint,
		})
		if marshalErr != nil {
			h.internalError(response, request, marshalErr)
			return
		}
		idempotencyKey := fmt.Sprintf(
			"image-renditions:%s:%s:v1",
			object.ID,
			object.QuickFingerprint,
		)
		item, _, createErr := h.jobs.Create(request.Context(), job.CreateInput{
			WorkspaceID:    session.Workspace.ID,
			Type:           media.GenerateImageRenditionsJobType,
			Priority:       media.ImageRenditionPriority,
			IdempotencyKey: &idempotencyKey,
			SubjectType:    "storageObject",
			SubjectID:      object.ID,
			Payload:        payload,
		})
		if createErr != nil {
			h.handleJobError(response, request, createErr)
			return
		}
		items = append(items, toJobResponse(item))
	}
	writeJSON(response, http.StatusAccepted, renditionJobsResponse{Jobs: items})
}

func (h *handler) handleRenditionContent(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireRenditionProjectPermission(
		response,
		request,
		session,
		request.PathValue("renditionId"),
	) {
		return
	}
	item, reader, _, err := h.renditions.Open(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("renditionId"),
	)
	if err != nil {
		h.handleRenditionError(response, request, err)
		return
	}
	defer reader.Close()

	mimeType := "application/octet-stream"
	if item.MIMEType != nil {
		mimeType = *item.MIMEType
	}
	response.Header().Set("Content-Type", mimeType)
	response.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(
		response,
		request,
		item.ID+".webp",
		item.UpdatedAt,
		reader,
	)
}

func (h *handler) handleRenditionSegment(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if !h.requireRenditionProjectPermission(
		response,
		request,
		session,
		request.PathValue("renditionId"),
	) {
		return
	}
	segment, reader, _, err := h.renditions.OpenSegment(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("renditionId"),
		request.PathValue("fileName"),
	)
	if err != nil {
		h.handleRenditionError(response, request, err)
		return
	}
	defer reader.Close()
	response.Header().Set("Content-Type", segment.MIMEType)
	response.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(
		response,
		request,
		segment.FileName,
		segment.CreatedAt,
		reader,
	)
}

func (h *handler) requireRenditionProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	renditionID string,
) bool {
	item, err := h.renditions.Get(
		request.Context(),
		session.Workspace.ID,
		renditionID,
	)
	if err != nil {
		h.handleRenditionError(response, request, err)
		return false
	}
	var assetID string
	if item.AssetVersionID != nil && strings.TrimSpace(*item.AssetVersionID) != "" {
		assetID, err = h.library.AssetIDForVersion(
			request.Context(),
			session.Workspace.ID,
			*item.AssetVersionID,
		)
	} else {
		var asset media.LibraryAsset
		asset, err = h.library.AssetForStorageObject(
			request.Context(),
			session.Workspace.ID,
			item.SourceStorageObjectID,
		)
		assetID = asset.ID
	}
	if err != nil {
		h.handleLibraryError(response, request, err)
		return false
	}
	return h.requireAssetProjectPermission(
		response,
		request,
		session,
		assetID,
		projectaccess.PermissionProjectRead,
	)
}

func (h *handler) handleRenditionError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, media.ErrRenditionNotFound),
		errors.Is(err, storage.ErrObjectNotFound),
		errors.Is(err, os.ErrNotExist):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"rendition.not_found",
			"预览文件不存在",
		)
	default:
		h.handleStorageError(response, request, err)
	}
}

func toRenditionResponse(item media.Rendition) renditionResponse {
	var contentURL *string
	if item.Status == "ready" {
		value := "/api/v1/renditions/" + item.ID + "/content"
		if item.Kind == media.RenditionHLS {
			value = "/api/v1/renditions/" + item.ID + "/hls/index.m3u8"
		}
		value += fmt.Sprintf("?v=%d", item.UpdatedAt.UnixNano())
		contentURL = &value
	}
	return renditionResponse{
		ID:                    item.ID,
		SourceStorageObjectID: item.SourceStorageObjectID,
		Kind:                  item.Kind,
		ProfileKey:            item.ProfileKey,
		ProfileVersion:        item.ProfileVersion,
		SourceFingerprint:     item.SourceFingerprint,
		Status:                item.Status,
		MIMEType:              item.MIMEType,
		Width:                 item.Width,
		Height:                item.Height,
		DurationUS:            item.DurationUS,
		SizeBytes:             item.SizeBytes,
		MetadataJSON:          item.MetadataJSON,
		ErrorCode:             item.ErrorCode,
		ErrorMessage:          item.ErrorMessage,
		ContentURL:            contentURL,
		UpdatedAt:             item.UpdatedAt,
		CompletedAt:           item.CompletedAt,
	}
}
