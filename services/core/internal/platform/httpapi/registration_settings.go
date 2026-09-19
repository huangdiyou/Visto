package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/workspacesettings"
)

type registrationSettingsResponse struct {
	WorkspaceID               string    `json:"workspaceId"`
	WorkspaceName             string    `json:"workspaceName"`
	TeamName                  string    `json:"teamName"`
	RegistrationEnabled       bool      `json:"registrationEnabled"`
	EmailVerificationRequired bool      `json:"emailVerificationRequired"`
	DefaultWorkspaceRole      string    `json:"defaultWorkspaceRole"`
	Revision                  int       `json:"revision"`
	UpdatedBy                 *string   `json:"updatedBy"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}

type registrationSettingsRequest struct {
	TeamName                  string `json:"teamName"`
	RegistrationEnabled       bool   `json:"registrationEnabled"`
	EmailVerificationRequired bool   `json:"emailVerificationRequired"`
	Revision                  int    `json:"revision"`
}

type publicRegistrationResponse struct {
	WorkspaceName             string `json:"workspaceName"`
	TeamName                  string `json:"teamName"`
	RegistrationEnabled       bool   `json:"registrationEnabled"`
	EmailVerificationRequired bool   `json:"emailVerificationRequired"`
	DefaultWorkspaceRole      string `json:"defaultWorkspaceRole"`
}

type publicRegistrationRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
	Locale      string `json:"locale"`
}

func (h *handler) handleGetRegistrationSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	item, err := h.workspaceSettings.GetRegistration(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.handleWorkspaceSettingsError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toRegistrationSettingsResponse(item))
}

func (h *handler) handleUpdateRegistrationSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body registrationSettingsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	teamName := strings.TrimSpace(body.TeamName)
	if teamName == "" {
		teamName = session.Workspace.TeamName
	}
	if teamName == "" {
		teamName = session.Workspace.Name
	}
	item, err := h.workspaceSettings.UpdateRegistration(
		request.Context(),
		workspacesettings.UpdateRegistrationInput{
			WorkspaceID:               session.Workspace.ID,
			TeamName:                  teamName,
			RegistrationEnabled:       body.RegistrationEnabled,
			EmailVerificationRequired: body.EmailVerificationRequired,
			Revision:                  body.Revision,
			UpdatedBy:                 session.User.ID,
		},
	)
	if err != nil {
		h.handleWorkspaceSettingsError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "workspace.registration_settings_updated",
		ResourceType: "workspace",
		ResourceID:   session.Workspace.ID,
		After: map[string]any{
			"registrationEnabled":       item.RegistrationEnabled,
			"emailVerificationRequired": item.EmailVerificationRequired,
			"teamName":                  item.TeamName,
		},
	})
	writeJSON(response, http.StatusOK, toRegistrationSettingsResponse(item))
}

func (h *handler) handlePublicRegistrationSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	item, err := h.workspaceSettings.PublicRegistration(request.Context())
	if err != nil {
		h.handleWorkspaceSettingsError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, publicRegistrationResponse{
		WorkspaceName:             item.WorkspaceName,
		TeamName:                  item.TeamName,
		RegistrationEnabled:       item.RegistrationEnabled,
		EmailVerificationRequired: item.EmailVerificationRequired,
		DefaultWorkspaceRole:      item.DefaultWorkspaceRole,
	})
}

func (h *handler) handlePublicRegistration(
	response http.ResponseWriter,
	request *http.Request,
) {
	settings, err := h.workspaceSettings.PublicRegistration(request.Context())
	if err != nil {
		h.handleWorkspaceSettingsError(response, request, err)
		return
	}
	if !settings.RegistrationEnabled {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"registration.closed",
			"当前工作空间未开放注册",
		)
		return
	}
	if settings.EmailVerificationRequired {
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"registration.email_verification_required",
			"当前工作空间要求邮箱验证，请先关闭该策略或接入邮箱验证流程",
		)
		return
	}
	var body publicRegistrationRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	registrationEmail := strings.ToLower(strings.TrimSpace(body.Email))
	if !h.consumeRateLimit(response, request, "registration-source", requestRateLimitSource(request), 20, time.Hour) {
		return
	}
	if registrationEmail != "" &&
		!h.consumeRateLimit(response, request, "registration-email", registrationEmail, 3, time.Hour) {
		return
	}
	_, err = h.identity.CreateAccount(
		request.Context(),
		identity.CreateAccountInput{
			WorkspaceID: settings.WorkspaceID,
			Email:       body.Email,
			DisplayName: body.DisplayName,
			Password:    body.Password,
			Locale:      defaultString(body.Locale, "zh-CN"),
			Role:        "member",
		},
	)
	if err != nil {
		h.handlePublicRegistrationError(response, request, err)
		return
	}
	result, err := h.identity.LoginAccount(
		request.Context(),
		identity.LoginInput{
			Email:    body.Email,
			Password: body.Password,
		},
	)
	if err != nil {
		h.handlePublicRegistrationError(response, request, err)
		return
	}
	h.setSessionCookie(response, request, result.Token, result.Session.ExpiresAt)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  result.Session.Workspace.ID,
		ActorType:    "user",
		ActorID:      result.Session.User.ID,
		Action:       "identity.open_registration_joined",
		ResourceType: "membership",
		ResourceID:   result.Session.User.ID,
	})
	writeJSON(response, http.StatusCreated, toSessionResponse(result.Session))
}

func (h *handler) requireOwner(
	response http.ResponseWriter,
	request *http.Request,
) (identity.Session, bool) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return identity.Session{}, false
	}
	if !strings.EqualFold(session.Role, "owner") {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.owner_required",
			"只有 Owner 可以修改全局系统设置",
		)
		return identity.Session{}, false
	}
	return session, true
}

func (h *handler) handleWorkspaceSettingsError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, workspacesettings.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"workspace.not_found", "工作空间不存在")
	case errors.Is(err, workspacesettings.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"workspace_settings.revision_conflict", "设置已被更新，请刷新后重试")
	case errors.Is(err, workspacesettings.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func (h *handler) handlePublicRegistrationError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, identity.ErrEmailInUse):
		writeError(response, http.StatusConflict, requestID(response),
			"registration.email_in_use", "该邮箱已经注册")
	case errors.Is(err, identity.ErrWorkspaceNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"workspace.not_found", "工作空间不存在")
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(response, http.StatusUnauthorized, requestID(response),
			"auth.invalid", "邮箱或密码不正确")
	default:
		if isValidationError(err) {
			h.badRequest(response, request, err)
			return
		}
		h.internalError(response, request, err)
	}
}

func toRegistrationSettingsResponse(
	item workspacesettings.RegistrationSettings,
) registrationSettingsResponse {
	return registrationSettingsResponse{
		WorkspaceID:               item.WorkspaceID,
		WorkspaceName:             item.WorkspaceName,
		TeamName:                  item.TeamName,
		RegistrationEnabled:       item.RegistrationEnabled,
		EmailVerificationRequired: item.EmailVerificationRequired,
		DefaultWorkspaceRole:      item.DefaultWorkspaceRole,
		Revision:                  item.Revision,
		UpdatedBy:                 item.UpdatedBy,
		UpdatedAt:                 item.UpdatedAt,
	}
}
