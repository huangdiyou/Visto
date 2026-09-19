package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/workspace"
)

type membershipResponse struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Email       *string    `json:"email"`
	DisplayName string     `json:"displayName"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	Revision    int        `json:"revision"`
	JoinedAt    *time.Time `json:"joinedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	DisabledAt  *time.Time `json:"disabledAt"`
}

type membershipListResponse struct {
	Items []membershipResponse `json:"items"`
}

type membershipUpdateRequest struct {
	Role     string `json:"role"`
	Status   string `json:"status"`
	Revision int    `json:"revision"`
}

func (h *handler) handleListMemberships(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionMembersManage,
	)
	if !ok {
		return
	}
	items, err := h.members.ListMemberships(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]membershipResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toMembershipResponse(item))
	}
	writeJSON(response, http.StatusOK, membershipListResponse{Items: result})
}

func (h *handler) handleUpdateMembership(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionMembersManage,
	)
	if !ok {
		return
	}
	var body membershipUpdateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.members.UpdateMembership(
		request.Context(),
		workspace.UpdateMembershipInput{
			WorkspaceID: session.Workspace.ID,
			ID:          request.PathValue("membershipId"),
			Role:        body.Role,
			Status:      body.Status,
			Revision:    body.Revision,
		},
	)
	if err != nil {
		h.handleMembershipError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "membership.updated",
		ResourceType: "membership",
		ResourceID:   item.ID,
		After: map[string]any{
			"role":   item.Role,
			"status": item.Status,
		},
	})
	writeJSON(response, http.StatusOK, toMembershipResponse(item))
}

func (h *handler) handleMembershipError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"membership.not_found",
			"成员不存在",
		)
	case errors.Is(err, workspace.ErrRevisionConflict):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"membership.revision_conflict",
			"成员信息已更新，请刷新后重试",
		)
	case errors.Is(err, workspace.ErrLastOwner):
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"membership.last_owner",
			"工作空间必须至少保留一名有效 Owner",
		)
	case errors.Is(err, workspace.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func toMembershipResponse(item workspace.Membership) membershipResponse {
	return membershipResponse{
		ID:          item.ID,
		UserID:      item.UserID,
		Email:       item.Email,
		DisplayName: item.DisplayName,
		Role:        item.Role,
		Status:      item.Status,
		Revision:    item.Revision,
		JoinedAt:    item.JoinedAt,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
		DisabledAt:  item.DisabledAt,
	}
}
