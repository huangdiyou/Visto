package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/projectaccess"
	reviewdomain "review-studio.local/core/internal/review"
	sharedomain "review-studio.local/core/internal/share"
)

type reviewDecisionRequest struct {
	ReviewItemID string `json:"reviewItemId"`
	Decision     string `json:"decision"`
	Note         string `json:"note"`
}

type reviewDecisionResponse struct {
	ID              string                `json:"id"`
	ReviewSessionID string                `json:"reviewSessionId"`
	ReviewItemID    *string               `json:"reviewItemId"`
	Actor           commentAuthorResponse `json:"actor"`
	Decision        string                `json:"decision"`
	Note            *string               `json:"note"`
	CreatedAt       time.Time             `json:"createdAt"`
}

type reviewDecisionListResponse struct {
	Items []reviewDecisionResponse `json:"items"`
}

func (h *handler) handleListReviewDecisions(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionProjectRead,
	); !ok {
		return
	}
	limit, offset := requestListBounds(request)
	items, err := h.reviews.ListDecisions(
		request.Context(),
		reviewdomain.DecisionListFilter{
			WorkspaceID:     session.Workspace.ID,
			ReviewSessionID: request.PathValue("reviewId"),
			ReviewItemID: strings.TrimSpace(
				request.URL.Query().Get("reviewItemId"),
			),
			Limit:  limit,
			Offset: offset,
		},
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toReviewDecisionListResponse(items))
}

func (h *handler) handleCreateReviewDecision(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	var body reviewDecisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	if _, ok := h.requireReviewSessionProjectPermission(
		response,
		request,
		session,
		request.PathValue("reviewId"),
		projectaccess.PermissionReviewsDecide,
	); !ok {
		return
	}
	item, err := h.reviews.CreateDecision(
		request.Context(),
		reviewdomain.CreateDecisionInput{
			WorkspaceID:     session.Workspace.ID,
			ReviewSessionID: request.PathValue("reviewId"),
			ReviewItemID:    body.ReviewItemID,
			ActorKind:       "user",
			ActorUserID:     session.User.ID,
			Decision:        body.Decision,
			Note:            body.Note,
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
		Action:       "review.decision_submitted",
		ResourceType: "review_session",
		ResourceID:   item.ReviewSessionID,
		After: map[string]any{
			"reviewItemId": item.ReviewItemID,
			"decision":     item.Decision,
		},
	})
	writeJSON(response, http.StatusCreated, toReviewDecisionResponse(item))
}

func (h *handler) handleCreatePublicDecision(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, ok := h.publicShareSession(response, request)
	if !ok {
		return
	}
	if share.RequireNickname && !share.Visitor.Identified {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrIdentityRequired,
		)
		return
	}
	if share.Visitor.ID == "" {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrSessionNotFound,
		)
		return
	}
	// submitting a decision is a text change and spends the same
	// guest budget as comments. Charged before the body is read and before any
	// write, audit record, or notification.
	if !h.consumeGuestTextChangeBudget(
		response,
		request,
		share.ID,
		share.Visitor.ID,
	) {
		return
	}
	var body reviewDecisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	body.ReviewItemID = strings.TrimSpace(body.ReviewItemID)
	if body.ReviewItemID != "" && !publicShareContainsItem(share, body.ReviewItemID) {
		h.handlePublicShareError(
			response,
			request,
			sharedomain.ErrItemNotFound,
		)
		return
	}
	if !share.AllowComment {
		h.handlePublicShareError(response, request, sharedomain.ErrCommentDenied)
		return
	}
	item, err := h.reviews.CreateDecision(
		request.Context(),
		reviewdomain.CreateDecisionInput{
			WorkspaceID:     share.WorkspaceID,
			ReviewSessionID: share.ReviewSessionID,
			ReviewItemID:    body.ReviewItemID,
			ActorKind:       "share_visitor",
			ActorVisitorID:  share.Visitor.ID,
			Decision:        body.Decision,
			Note:            body.Note,
		},
	)
	if err != nil {
		h.handleReviewError(response, request, err)
		return
	}
	h.recordAccess(
		request,
		share,
		"decision_submitted",
		"review_session",
		item.ReviewSessionID,
	)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  share.WorkspaceID,
		ActorType:    "visitor",
		ActorID:      share.Visitor.ID,
		Action:       "review.decision_submitted",
		ResourceType: "review_session",
		ResourceID:   item.ReviewSessionID,
		After: map[string]any{
			"reviewItemId": item.ReviewItemID,
			"decision":     item.Decision,
		},
	})
	h.notifyWorkspaceManagers(request, notification.CreateForWorkspaceInput{
		WorkspaceID:  share.WorkspaceID,
		Type:         "review.decision_submitted",
		ResourceType: "review_session",
		ResourceID:   item.ReviewSessionID,
		Title:        "访客提交了审阅结论",
		Body: fmt.Sprintf(
			"%s 对“%s”提交了%s结论。",
			publicVisitorName(share),
			share.ReviewName,
			decisionLabel(item.Decision),
		),
	})
	writeJSON(response, http.StatusCreated, toReviewDecisionResponse(item))
}

func publicShareContainsItem(item sharedomain.PublicShare, itemID string) bool {
	for _, candidate := range item.Items {
		if candidate.ID == itemID {
			return true
		}
	}
	return false
}

func toReviewDecisionListResponse(
	items []reviewdomain.Decision,
) reviewDecisionListResponse {
	result := reviewDecisionListResponse{
		Items: make([]reviewDecisionResponse, 0, len(items)),
	}
	for _, item := range items {
		result.Items = append(result.Items, toReviewDecisionResponse(item))
	}
	return result
}

func toReviewDecisionResponse(
	item reviewdomain.Decision,
) reviewDecisionResponse {
	return reviewDecisionResponse{
		ID:              item.ID,
		ReviewSessionID: item.ReviewSessionID,
		ReviewItemID:    item.ReviewItemID,
		Actor: commentAuthorResponse{
			Kind:        item.Actor.Kind,
			DisplayName: item.Actor.DisplayName,
		},
		Decision:  item.Decision,
		Note:      item.Note,
		CreatedAt: item.CreatedAt,
	}
}

func decisionLabel(value string) string {
	switch value {
	case "approved":
		return "“通过”"
	case "changes_requested":
		return "“需修改”"
	case "rejected":
		return "“拒绝”"
	default:
		return "新的"
	}
}
