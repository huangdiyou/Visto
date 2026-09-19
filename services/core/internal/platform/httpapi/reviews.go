package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/projectaccess"
	reviewdomain "review-studio.local/core/internal/review"
	"review-studio.local/core/internal/reviewtemplate"
)

type reviewParticipantRequest struct {
	UserID      *string `json:"userId"`
	DisplayName string  `json:"displayName"`
	Role        string  `json:"role"`
}

type reviewItemRequest struct {
	AssetID        string `json:"assetId"`
	AssetVersionID string `json:"assetVersionId"`
}

type reviewSessionRequest struct {
	ProjectID         string                     `json:"projectId"`
	CollectionID      *string                    `json:"collectionId"`
	Name              string                     `json:"name"`
	DueAt             *time.Time                 `json:"dueAt"`
	TemplateID        *string                    `json:"templateId"`
	ResponsibleUserID *string                    `json:"responsibleUserId"`
	AllowDownload     bool                       `json:"allowDownload"`
	DecisionRule      string                     `json:"decisionRule"`
	Participants      []reviewParticipantRequest `json:"participants"`
	Items             []reviewItemRequest        `json:"items"`
	Revision          int                        `json:"revision,omitempty"`
}

type reviewParticipantResponse struct {
	ID          string  `json:"id"`
	UserID      *string `json:"userId"`
	DisplayName string  `json:"displayName"`
	Role        string  `json:"role"`
}

type reviewItemResponse struct {
	ID             string    `json:"id"`
	AssetID        string    `json:"assetId"`
	AssetVersionID string    `json:"assetVersionId"`
	AssetName      string    `json:"assetName"`
	VersionNumber  int       `json:"versionNumber"`
	Position       int       `json:"position"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type reviewSessionResponse struct {
	ID                string                      `json:"id"`
	WorkspaceID       string                      `json:"workspaceId"`
	ProjectID         string                      `json:"projectId"`
	ProjectName       string                      `json:"projectName"`
	CollectionID      *string                     `json:"collectionId"`
	Name              string                      `json:"name"`
	Status            string                      `json:"status"`
	DueAt             *time.Time                  `json:"dueAt"`
	TemplateID        *string                     `json:"templateId"`
	TemplateRevision  *int                        `json:"templateRevision"`
	ResponsibleUserID *string                     `json:"responsibleUserId"`
	ResponsibleName   string                      `json:"responsibleName"`
	AllowDownload     bool                        `json:"allowDownload"`
	DecisionRule      string                      `json:"decisionRule"`
	Revision          int                         `json:"revision"`
	CreatedAt         time.Time                   `json:"createdAt"`
	UpdatedAt         time.Time                   `json:"updatedAt"`
	ClosedAt          *time.Time                  `json:"closedAt"`
	Items             []reviewItemResponse        `json:"items"`
	Participants      []reviewParticipantResponse `json:"participants"`
}

type reviewSessionListResponse struct {
	Items      []reviewSessionResponse `json:"items"`
	NextOffset *int                    `json:"nextOffset,omitempty"`
}

func (h *handler) handleListReviewSessions(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	limit, offset, err := reviewListPage(request)
	if err != nil {
		h.badRequest(response, request, err)
		return
	}
	page, err := h.reviews.List(
		request.Context(),
		session.Workspace.ID,
		reviewdomain.ListFilter{
			ProjectID: strings.TrimSpace(
				request.URL.Query().Get("projectId"),
			),
			AssetVersionID: strings.TrimSpace(
				request.URL.Query().Get("assetVersionId"),
			),
			UserID:         session.User.ID,
			WorkspaceOwner: strings.EqualFold(session.Role, "owner"),
			Limit:          limit,
			Offset:         offset,
		},
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]reviewSessionResponse, 0, len(page.Items))
	for _, item := range page.Items {
		result = append(result, toReviewSessionResponse(item))
	}
	writeJSON(response, http.StatusOK, reviewSessionListResponse{
		Items: result, NextOffset: page.NextOffset,
	})
}

func reviewListPage(request *http.Request) (int, int, error) {
	limit := 100
	offset := 0
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return 0, 0, errors.New("limit must be between 1 and 200")
		}
		limit = value
	}
	if raw := strings.TrimSpace(request.URL.Query().Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, 0, errors.New("offset must be zero or positive")
		}
		offset = value
	}
	return limit, offset, nil
}

func (h *handler) handleCreateReviewSession(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body reviewSessionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		body.ProjectID,
		projectaccess.PermissionReviewsCreate,
	) {
		return
	}
	templateID, templateRevision, allowDownload, decisionRule, err :=
		h.reviewTemplateSnapshot(request, session.Workspace.ID, &body)
	if err != nil {
		if errors.Is(err, reviewtemplate.ErrNotFound) {
			h.handleReviewTemplateError(response, request, err)
		} else {
			h.handleReviewError(response, request, err)
		}
		return
	}
	item, err := h.reviews.Create(
		request.Context(),
		reviewdomain.CreateSessionInput{
			WorkspaceID:       session.Workspace.ID,
			UserID:            session.User.ID,
			ProjectID:         body.ProjectID,
			CollectionID:      body.CollectionID,
			Name:              body.Name,
			DueAt:             body.DueAt,
			TemplateID:        templateID,
			TemplateRevision:  templateRevision,
			ResponsibleUserID: body.ResponsibleUserID,
			AllowDownload:     allowDownload,
			DecisionRule:      decisionRule,
			Participants:      toReviewParticipantInputs(body.Participants),
			Items:             toReviewItemInputs(body.Items),
		},
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.session_created",
		ResourceType: "review_session",
		ResourceID:   item.ID,
		After: map[string]any{
			"name":   item.Name,
			"status": item.Status,
		},
	})
	writeJSON(response, http.StatusCreated, toReviewSessionResponse(item))
}

func (h *handler) handleGetReviewSession(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	item, err := h.reviews.Get(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("reviewId"),
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		item.ProjectID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	writeJSON(response, http.StatusOK, toReviewSessionResponse(item))
}

func (h *handler) handleUpdateReviewSession(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body reviewSessionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsCreate,
	); !ok {
		return
	}
	item, err := h.reviews.Update(
		request.Context(),
		reviewdomain.UpdateSessionInput{
			WorkspaceID:       session.Workspace.ID,
			ID:                request.PathValue("reviewId"),
			Name:              body.Name,
			DueAt:             body.DueAt,
			ResponsibleUserID: body.ResponsibleUserID,
			Participants:      toReviewParticipantInputs(body.Participants),
			Revision:          body.Revision,
		},
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "review.session_updated",
		ResourceType: "review_session",
		ResourceID:   item.ID,
		After: map[string]any{
			"name":   item.Name,
			"status": item.Status,
		},
	})
	writeJSON(response, http.StatusOK, toReviewSessionResponse(item))
}

func (h *handler) handleOpenReviewSession(
	response http.ResponseWriter,
	request *http.Request,
) {
	h.handleReviewSessionState(response, request, "open")
}

func (h *handler) handleCloseReviewSession(
	response http.ResponseWriter,
	request *http.Request,
) {
	h.handleReviewSessionState(response, request, "closed")
}

func (h *handler) handleReviewSessionState(
	response http.ResponseWriter,
	request *http.Request,
	status string,
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
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsCreate,
	); !ok {
		return
	}
	input := reviewdomain.SessionStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          request.PathValue("reviewId"),
		Revision:    body.Revision,
	}
	var item reviewdomain.Session
	var err error
	if status == "open" {
		item, err = h.reviews.Open(request.Context(), input)
	} else {
		item, err = h.reviews.Close(request.Context(), input)
	}
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	auditAction := "review.session_opened"
	if status != "open" {
		auditAction = "review.session_closed"
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       auditAction,
		ResourceType: "review_session",
		ResourceID:   item.ID,
		After: map[string]any{
			"status": item.Status,
		},
	})
	writeJSON(response, http.StatusOK, toReviewSessionResponse(item))
}

func (h *handler) requireReviewSessionProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	reviewID string,
	permission projectaccess.Permission,
) (reviewdomain.Session, bool) {
	item, err := h.reviews.Get(
		request.Context(),
		session.Workspace.ID,
		reviewID,
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return reviewdomain.Session{}, false
	}
	if !h.requireProjectPermission(
		response,
		request,
		session,
		item.ProjectID,
		permission,
	) {
		return reviewdomain.Session{}, false
	}
	return item, true
}

func (h *handler) reviewTemplateSnapshot(
	request *http.Request,
	workspaceID string,
	body *reviewSessionRequest,
) (*string, *int, bool, string, error) {
	if body.TemplateID == nil || strings.TrimSpace(*body.TemplateID) == "" {
		decisionRule := strings.TrimSpace(body.DecisionRule)
		if decisionRule == "" {
			decisionRule = "any_reviewer"
		}
		if decisionRule == "responsible_only" &&
			(body.ResponsibleUserID == nil ||
				strings.TrimSpace(*body.ResponsibleUserID) == "") {
			return nil, nil, false, "", errors.New(
				"responsible member is required by this decision rule",
			)
		}
		return nil, nil, body.AllowDownload, decisionRule, nil
	}
	templateID := strings.TrimSpace(*body.TemplateID)
	template, err := h.reviewTemplates.Get(
		request.Context(),
		workspaceID,
		templateID,
	)
	if err != nil {
		return nil, nil, false, "", err
	}

	expectedRoles := make(map[string]int, len(template.ParticipantRoles))
	for _, role := range template.ParticipantRoles {
		expectedRoles[role]++
	}
	actualRoles := make(map[string]int, len(body.Participants))
	for _, participant := range body.Participants {
		if participant.UserID == nil ||
			strings.TrimSpace(*participant.UserID) == "" {
			return nil, nil, false, "", errors.New(
				"template participants must be team members",
			)
		}
		actualRoles[strings.TrimSpace(participant.Role)]++
	}
	if len(actualRoles) != len(expectedRoles) {
		return nil, nil, false, "", errors.New(
			"template participant roles do not match",
		)
	}
	for role, expected := range expectedRoles {
		if actualRoles[role] != expected {
			return nil, nil, false, "", errors.New(
				"template participant roles do not match",
			)
		}
	}
	if template.DecisionRule == "responsible_only" &&
		(body.ResponsibleUserID == nil ||
			strings.TrimSpace(*body.ResponsibleUserID) == "") {
		return nil, nil, false, "", errors.New(
			"responsible member is required by this template",
		)
	}
	if body.DueAt == nil && template.DueDays != nil {
		value := time.Now().UTC().AddDate(0, 0, *template.DueDays)
		body.DueAt = &value
	}
	revision := template.Revision
	return &templateID, &revision, template.AllowDownload,
		template.DecisionRule, nil
}

func toReviewSessionResponse(item reviewdomain.Session) reviewSessionResponse {
	result := reviewSessionResponse{
		ID:                item.ID,
		WorkspaceID:       item.WorkspaceID,
		ProjectID:         item.ProjectID,
		ProjectName:       item.ProjectName,
		CollectionID:      item.CollectionID,
		Name:              item.Name,
		Status:            item.Status,
		DueAt:             item.DueAt,
		TemplateID:        item.TemplateID,
		TemplateRevision:  item.TemplateRevision,
		ResponsibleUserID: item.ResponsibleUserID,
		ResponsibleName:   item.ResponsibleName,
		AllowDownload:     item.AllowDownload,
		DecisionRule:      item.DecisionRule,
		Revision:          item.Revision,
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
		ClosedAt:          item.ClosedAt,
		Items:             make([]reviewItemResponse, 0, len(item.Items)),
		Participants:      make([]reviewParticipantResponse, 0, len(item.Participants)),
	}
	for _, reviewItem := range item.Items {
		result.Items = append(result.Items, reviewItemResponse{
			ID:             reviewItem.ID,
			AssetID:        reviewItem.AssetID,
			AssetVersionID: reviewItem.AssetVersionID,
			AssetName:      reviewItem.AssetName,
			VersionNumber:  reviewItem.VersionNumber,
			Position:       reviewItem.Position,
			Status:         reviewItem.Status,
			CreatedAt:      reviewItem.CreatedAt,
			UpdatedAt:      reviewItem.UpdatedAt,
		})
	}
	for _, participant := range item.Participants {
		result.Participants = append(result.Participants, reviewParticipantResponse{
			ID:          participant.ID,
			UserID:      participant.UserID,
			DisplayName: participant.DisplayName,
			Role:        participant.Role,
		})
	}
	return result
}

func toReviewParticipantInputs(
	items []reviewParticipantRequest,
) []reviewdomain.ParticipantInput {
	result := make([]reviewdomain.ParticipantInput, 0, len(items))
	for _, item := range items {
		result = append(result, reviewdomain.ParticipantInput{
			UserID:      item.UserID,
			DisplayName: item.DisplayName,
			Role:        item.Role,
		})
	}
	return result
}

func toReviewItemInputs(items []reviewItemRequest) []reviewdomain.ItemInput {
	result := make([]reviewdomain.ItemInput, 0, len(items))
	for _, item := range items {
		result = append(result, reviewdomain.ItemInput{
			AssetID:        item.AssetID,
			AssetVersionID: item.AssetVersionID,
		})
	}
	return result
}

func (h *handler) handleReviewError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, reviewdomain.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"review.not_found", "review session was not found")
	case errors.Is(err, reviewdomain.ErrNameConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"review.name_conflict", "当前项目已经有同名审阅")
	case errors.Is(err, reviewdomain.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"review.revision_conflict", "review session changed; refresh and retry")
	case errors.Is(err, reviewdomain.ErrInvalidProject):
		writeError(response, http.StatusBadRequest, requestID(response),
			"review.invalid_project", "review project or collection is invalid")
	case errors.Is(err, reviewdomain.ErrInvalidItem):
		writeError(response, http.StatusBadRequest, requestID(response),
			"review.invalid_item", "review item must belong to the selected project")
	case errors.Is(err, reviewdomain.ErrInvalidState):
		writeError(response, http.StatusConflict, requestID(response),
			"review.invalid_state", "review session cannot perform this action")
	case errors.Is(err, reviewdomain.ErrInvalidAuthor):
		writeError(response, http.StatusForbidden, requestID(response),
			"review.invalid_author", "comment author is not valid for this review")
	case errors.Is(err, reviewdomain.ErrInvalidAnnotation):
		writeError(response, http.StatusBadRequest, requestID(response),
			"review.invalid_annotation", "comment time annotation is invalid")
	case errors.Is(err, reviewdomain.ErrCommentNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"review.comment_not_found", "comment thread or comment was not found")
	case errors.Is(err, reviewdomain.ErrCommentForbidden):
		writeError(response, http.StatusForbidden, requestID(response),
			"review.comment_forbidden", "comment can only be changed by its author")
	case errors.Is(err, reviewdomain.ErrAttachmentNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"review.attachment_not_found", "comment attachment was not found")
	case errors.Is(err, reviewdomain.ErrAttachmentQuotaExceeded):
		writeError(response, http.StatusTooManyRequests, requestID(response),
			"review.attachment_quota_exceeded", "comment attachment storage limit reached for this review")
	case errors.Is(err, reviewdomain.ErrInvalidAttachment):
		writeError(response, http.StatusBadRequest, requestID(response),
			"review.invalid_attachment", "comment attachment is invalid")
	case errors.Is(err, reviewdomain.ErrDecisionForbidden):
		writeError(response, http.StatusForbidden, requestID(response),
			"review.decision_forbidden", "当前决策规则不允许此用户提交结论")
	case errors.Is(err, reviewdomain.ErrInvalidParticipant):
		writeError(response, http.StatusBadRequest, requestID(response),
			"review.invalid_participant", "负责人和参与者必须是当前团队的有效成员")
	default:
		if isValidationError(err) {
			h.badRequest(response, request, err)
			return
		}
		h.internalError(response, request, err)
	}
}
