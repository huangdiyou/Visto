package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/reviewtemplate"
)

type reviewTemplateRequest struct {
	Name             string   `json:"name"`
	Description      *string  `json:"description"`
	ParticipantRoles []string `json:"participantRoles"`
	AllowDownload    bool     `json:"allowDownload"`
	DueDays          *int     `json:"dueDays"`
	DecisionRule     string   `json:"decisionRule"`
	Revision         int      `json:"revision,omitempty"`
}

type reviewTemplateResponse struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspaceId"`
	Name             string    `json:"name"`
	Description      *string   `json:"description"`
	ParticipantRoles []string  `json:"participantRoles"`
	AllowDownload    bool      `json:"allowDownload"`
	DueDays          *int      `json:"dueDays"`
	DecisionRule     string    `json:"decisionRule"`
	Revision         int       `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type reviewTemplateListResponse struct {
	Items []reviewTemplateResponse `json:"items"`
}

func (h *handler) handleListReviewTemplates(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionReviewsRead,
	)
	if !ok {
		return
	}
	items, err := h.reviewTemplates.List(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]reviewTemplateResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toReviewTemplateResponse(item))
	}
	writeJSON(response, http.StatusOK, reviewTemplateListResponse{Items: result})
}

func (h *handler) handleCreateReviewTemplate(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionReviewsWrite,
	)
	if !ok {
		return
	}
	var body reviewTemplateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.reviewTemplates.Create(
		request.Context(),
		reviewtemplate.CreateInput{
			WorkspaceID:      session.Workspace.ID,
			UserID:           session.User.ID,
			Name:             body.Name,
			Description:      body.Description,
			ParticipantRoles: body.ParticipantRoles,
			AllowDownload:    body.AllowDownload,
			DueDays:          body.DueDays,
			DecisionRule:     body.DecisionRule,
		},
	)
	if err != nil {
		h.handleReviewTemplateError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.template_created",
		ResourceType: "review_template",
		ResourceID:   item.ID,
		After: map[string]any{
			"allowDownload": item.AllowDownload,
			"decisionRule":  item.DecisionRule,
		},
	})
	writeJSON(response, http.StatusCreated, toReviewTemplateResponse(item))
}

func (h *handler) handleUpdateReviewTemplate(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionReviewsWrite,
	)
	if !ok {
		return
	}
	var body reviewTemplateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.reviewTemplates.Update(
		request.Context(),
		reviewtemplate.UpdateInput{
			WorkspaceID:      session.Workspace.ID,
			ID:               request.PathValue("templateId"),
			Name:             body.Name,
			Description:      body.Description,
			ParticipantRoles: body.ParticipantRoles,
			AllowDownload:    body.AllowDownload,
			DueDays:          body.DueDays,
			DecisionRule:     body.DecisionRule,
			Revision:         body.Revision,
		},
	)
	if err != nil {
		h.handleReviewTemplateError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.template_updated",
		ResourceType: "review_template",
		ResourceID:   item.ID,
		After: map[string]any{
			"allowDownload": item.AllowDownload,
			"decisionRule":  item.DecisionRule,
			"revision":      item.Revision,
		},
	})
	writeJSON(response, http.StatusOK, toReviewTemplateResponse(item))
}

func (h *handler) handleDeleteReviewTemplate(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionReviewsWrite,
	)
	if !ok {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	id := request.PathValue("templateId")
	if err := h.reviewTemplates.Delete(
		request.Context(),
		reviewtemplate.DeleteInput{
			WorkspaceID: session.Workspace.ID,
			ID:          id,
			Revision:    body.Revision,
		},
	); err != nil {
		h.handleReviewTemplateError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.template_deleted",
		ResourceType: "review_template",
		ResourceID:   id,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleReviewTemplateError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, reviewtemplate.ErrNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"review_template.not_found",
			"审阅模板不存在",
		)
	case errors.Is(err, reviewtemplate.ErrRevisionConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"review_template.revision_conflict",
			"模板已更新，请刷新后重试",
		)
	case errors.Is(err, reviewtemplate.ErrNameConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"review_template.name_conflict",
			"已经存在同名审阅模板",
		)
	case errors.Is(err, reviewtemplate.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func toReviewTemplateResponse(
	item reviewtemplate.Template,
) reviewTemplateResponse {
	return reviewTemplateResponse{
		ID:               item.ID,
		WorkspaceID:      item.WorkspaceID,
		Name:             item.Name,
		Description:      item.Description,
		ParticipantRoles: item.ParticipantRoles,
		AllowDownload:    item.AllowDownload,
		DueDays:          item.DueDays,
		DecisionRule:     item.DecisionRule,
		Revision:         item.Revision,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}
