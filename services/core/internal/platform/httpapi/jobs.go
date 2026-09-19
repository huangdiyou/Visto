package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"review-studio.local/core/internal/job"
)

type jobResponse struct {
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

type jobSubjectResponse struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type jobProgressResponse struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	Unit    string `json:"unit"`
}

type jobErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jobEnvelope struct {
	Job jobResponse `json:"job"`
}

type jobListResponse struct {
	Items []jobResponse `json:"items"`
}

func (h *handler) handleListJobs(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	limit := 0
	if value := request.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			h.badRequest(response, request, err)
			return
		}
		limit = parsed
	}
	items, err := h.jobs.List(request.Context(), job.ListInput{
		WorkspaceID: session.Workspace.ID,
		Status:      request.URL.Query().Get("status"),
		Limit:       limit,
	})
	if err != nil {
		h.handleJobError(response, request, err)
		return
	}
	result := make([]jobResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toJobResponse(item))
	}
	writeJSON(response, http.StatusOK, jobListResponse{Items: result})
}

func (h *handler) handleGetJob(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	item, err := h.jobs.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("jobId"),
	)
	if err != nil {
		h.handleJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toJobResponse(item))
}

func (h *handler) handleCancelJob(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	item, err := h.jobs.Cancel(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("jobId"),
	)
	if err != nil {
		h.handleJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toJobResponse(item))
}

func (h *handler) handleRetryJob(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	item, err := h.jobs.Retry(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("jobId"),
	)
	if err != nil {
		h.handleJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toJobResponse(item))
}

func (h *handler) handleJobError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, job.ErrNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"resource.not_found",
			"任务不存在",
		)
	case errors.Is(err, job.ErrInvalidInput):
		writeError(
			response,
			http.StatusBadRequest,
			requestID(response),
			"request.invalid",
			"任务请求无效",
		)
	case errors.Is(err, job.ErrNotRetryable):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"job.not_retryable",
			"当前任务不能重试",
		)
	case errors.Is(err, job.ErrInvalidTransition):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"request.conflict",
			"任务状态已经变化",
		)
	default:
		h.internalError(response, request, err)
	}
}

func toJobResponse(item job.Job) jobResponse {
	response := jobResponse{
		ID:       item.ID,
		Type:     item.Type,
		Status:   item.Status,
		Priority: item.Priority,
		Subject: jobSubjectResponse{
			Type: item.SubjectType,
			ID:   item.SubjectID,
		},
		AttemptCount: item.AttemptCount,
		MaxAttempts:  item.MaxAttempts,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
		StartedAt:    item.StartedAt,
		CompletedAt:  item.CompletedAt,
	}
	if item.Progress != nil {
		response.Progress = &jobProgressResponse{
			Current: item.Progress.Current,
			Total:   item.Progress.Total,
			Unit:    item.Progress.Unit,
		}
	}
	if item.LastError != nil {
		response.Error = &jobErrorResponse{
			Code:    item.LastError.Code,
			Message: item.LastError.Message,
		}
	}
	return response
}
