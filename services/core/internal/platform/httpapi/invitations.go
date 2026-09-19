package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/invitation"
	"review-studio.local/core/internal/notification"
)

type invitationResponse struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	TokenPrefix string     `json:"tokenPrefix"`
	Status      string     `json:"status"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	SendCount   int        `json:"sendCount"`
	LastSentAt  time.Time  `json:"lastSentAt"`
	AcceptedAt  *time.Time `json:"acceptedAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type invitationSecretResponse struct {
	Invitation invitationResponse `json:"invitation"`
	URL        string             `json:"url"`
}

type invitationListResponse struct {
	Items []invitationResponse `json:"items"`
}

type invitationCreateRequest struct {
	Email     string     `json:"email"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

type invitationTokenRequest struct {
	Token string `json:"token"`
}

type invitationAcceptRequest struct {
	Token       string `json:"token"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
	Locale      string `json:"locale"`
}

type invitationPreviewResponse struct {
	WorkspaceName string    `json:"workspaceName"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

func (h *handler) handleListInvitations(
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
	items, err := h.invitations.List(request.Context(), session.Workspace.ID)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]invitationResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toInvitationResponse(item))
	}
	writeJSON(response, http.StatusOK, invitationListResponse{Items: result})
}

func (h *handler) handleCreateInvitation(
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
	var body invitationCreateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	var expiresAt time.Time
	if body.ExpiresAt != nil {
		expiresAt = *body.ExpiresAt
	}
	item, err := h.invitations.Create(request.Context(), invitation.CreateInput{
		WorkspaceID: session.Workspace.ID,
		InvitedBy:   session.User.ID,
		Email:       body.Email,
		Role:        body.Role,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		h.handleInvitationError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "membership.invitation_created",
		ResourceType: "invitation",
		ResourceID:   item.Invitation.ID,
		After: map[string]any{
			"role": item.Invitation.Role,
		},
	})
	writeJSON(response, http.StatusCreated, invitationSecretResponse{
		Invitation: toInvitationResponse(item.Invitation),
		URL:        item.URL,
	})
}

func (h *handler) handleResendInvitation(
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
	item, err := h.invitations.Resend(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("invitationId"),
	)
	if err != nil {
		h.handleInvitationError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "membership.invitation_resent",
		ResourceType: "invitation",
		ResourceID:   item.Invitation.ID,
	})
	writeJSON(response, http.StatusOK, invitationSecretResponse{
		Invitation: toInvitationResponse(item.Invitation),
		URL:        item.URL,
	})
}

func (h *handler) handleRevokeInvitation(
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
	item, err := h.invitations.Revoke(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("invitationId"),
	)
	if err != nil {
		h.handleInvitationError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "membership.invitation_revoked",
		ResourceType: "invitation",
		ResourceID:   item.ID,
	})
	writeJSON(response, http.StatusOK, toInvitationResponse(item))
}

func (h *handler) handlePreviewInvitation(
	response http.ResponseWriter,
	request *http.Request,
) {
	var body invitationTokenRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.invitations.Preview(request.Context(), body.Token)
	if err != nil {
		h.handlePublicInvitationError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, invitationPreviewResponse{
		WorkspaceName: item.WorkspaceName,
		Email:         item.Email,
		Role:          item.Role,
		ExpiresAt:     item.ExpiresAt,
	})
}

func (h *handler) handleAcceptInvitation(
	response http.ResponseWriter,
	request *http.Request,
) {
	var body invitationAcceptRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	result, err := h.invitations.Accept(request.Context(), invitation.AcceptInput{
		Token:       body.Token,
		DisplayName: body.DisplayName,
		Password:    body.Password,
		Locale:      defaultString(body.Locale, "zh-CN"),
	})
	if err != nil {
		h.handlePublicInvitationError(response, request, err)
		return
	}
	h.setSessionCookie(response, request, result.Token, result.Session.ExpiresAt)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  result.Session.Workspace.ID,
		ActorType:    "user",
		ActorID:      result.Session.User.ID,
		Action:       "membership.invitation_accepted",
		ResourceType: "invitation",
		ResourceID:   result.Invitation.ID,
		After: map[string]any{
			"role": result.Session.Role,
		},
	})
	h.notifyWorkspaceManagers(request, notification.CreateForWorkspaceInput{
		WorkspaceID:   result.Session.Workspace.ID,
		ExcludeUserID: result.Session.User.ID,
		Type:          "membership.joined",
		ResourceType:  "membership",
		ResourceID:    result.Invitation.ID,
		Title:         "新成员已加入",
		Body:          result.Session.User.DisplayName + " 已接受团队邀请",
	})
	writeJSON(response, http.StatusCreated, toSessionResponse(result.Session))
}

func (h *handler) handleInvitationError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, invitation.ErrEmailInUse):
		writeError(response, http.StatusConflict, requestID(response),
			"invitation.email_in_use", "该邮箱已经是工作空间成员")
	case errors.Is(err, invitation.ErrPendingExists):
		writeError(response, http.StatusConflict, requestID(response),
			"invitation.pending_exists", "该邮箱已有待接受邀请")
	case errors.Is(err, invitation.ErrUnavailable),
		errors.Is(err, invitation.ErrNotFound):
		writeError(response, http.StatusConflict, requestID(response),
			"invitation.unavailable", "邀请已失效")
	case errors.Is(err, invitation.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func (h *handler) handlePublicInvitationError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, invitation.ErrExpired):
		writeError(response, http.StatusGone, requestID(response),
			"invitation.expired", "邀请已过期")
	case errors.Is(err, invitation.ErrUnavailable),
		errors.Is(err, invitation.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"invitation.unavailable", "邀请不存在或已失效")
	case errors.Is(err, invitation.ErrEmailInUse):
		writeError(response, http.StatusConflict, requestID(response),
			"invitation.email_in_use", "该邮箱已经存在账号")
	case errors.Is(err, invitation.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func toInvitationResponse(item invitation.Invitation) invitationResponse {
	return invitationResponse{
		ID:          item.ID,
		Email:       item.Email,
		Role:        item.Role,
		TokenPrefix: item.TokenPrefix,
		Status:      item.Status,
		ExpiresAt:   item.ExpiresAt,
		SendCount:   item.SendCount,
		LastSentAt:  item.LastSentAt,
		AcceptedAt:  item.AcceptedAt,
		RevokedAt:   item.RevokedAt,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}
