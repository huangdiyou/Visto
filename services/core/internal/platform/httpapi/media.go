package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
)

type mediaProbeResponse struct {
	StorageObjectID string    `json:"storageObjectId"`
	Status          string    `json:"status"`
	MediaType       string    `json:"mediaType"`
	FormatName      *string   `json:"formatName"`
	FormatLongName  *string   `json:"formatLongName"`
	DurationUS      *int64    `json:"durationUs"`
	BitRate         *int64    `json:"bitRate"`
	Width           *int      `json:"width"`
	Height          *int      `json:"height"`
	RotationDegrees *int      `json:"rotationDegrees"`
	FrameRate       *float64  `json:"frameRate"`
	VideoCodec      *string   `json:"videoCodec"`
	AudioCodec      *string   `json:"audioCodec"`
	ErrorCode       *string   `json:"errorCode"`
	ErrorMessage    *string   `json:"errorMessage"`
	ProbedAt        time.Time `json:"probedAt"`
}

type mediaProbeListResponse struct {
	Items []mediaProbeResponse `json:"items"`
}

type mediaProbeBatchResponse struct {
	RootID    string               `json:"rootId"`
	Available int                  `json:"available"`
	Attempted int                  `json:"attempted"`
	Succeeded int                  `json:"succeeded"`
	Failed    int                  `json:"failed"`
	Skipped   int                  `json:"skipped"`
	Remaining int                  `json:"remaining"`
	Items     []mediaProbeResponse `json:"items"`
}

func (h *handler) handleListMediaProbes(
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
	items, err := h.media.ListRoot(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("rootId"),
	)
	if err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, mediaProbeListResponse{
		Items: toMediaProbeResponses(items),
	})
}

func (h *handler) handleProbeAuthorizedRoot(
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
	rootID := request.PathValue("rootId")
	if _, err := h.storage.Root(
		request.Context(),
		session.Workspace.ID,
		rootID,
	); err != nil {
		h.handleStorageError(response, request, err)
		return
	}
	payload, err := json.Marshal(media.ProbeRootJobPayload{RootID: rootID})
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	idempotencyKey := "probe-root:" + rootID
	item, _, err := h.jobs.Create(request.Context(), job.CreateInput{
		WorkspaceID:    session.Workspace.ID,
		Type:           media.ProbeRootJobType,
		Priority:       10,
		IdempotencyKey: &idempotencyKey,
		SubjectType:    "authorizedRoot",
		SubjectID:      rootID,
		Payload:        payload,
	})
	if err != nil {
		h.handleJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, jobEnvelope{
		Job: toJobResponse(item),
	})
}

func toMediaProbeResponses(items []media.Metadata) []mediaProbeResponse {
	response := make([]mediaProbeResponse, 0, len(items))
	for _, item := range items {
		response = append(response, mediaProbeResponse{
			StorageObjectID: item.StorageObjectID,
			Status:          item.Status,
			MediaType:       item.MediaType,
			FormatName:      item.FormatName,
			FormatLongName:  item.FormatLongName,
			DurationUS:      item.DurationUS,
			BitRate:         item.BitRate,
			Width:           item.Width,
			Height:          item.Height,
			RotationDegrees: item.RotationDegrees,
			FrameRate:       item.FrameRate,
			VideoCodec:      item.VideoCodec,
			AudioCodec:      item.AudioCodec,
			ErrorCode:       item.ErrorCode,
			ErrorMessage:    item.ErrorMessage,
			ProbedAt:        item.ProbedAt,
		})
	}
	return response
}
