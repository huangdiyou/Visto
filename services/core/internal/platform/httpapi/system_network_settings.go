package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/systemsettings"
)

// systemNetworkSettingsResponse separates the stored decision from what Core is
// actually enforcing, so the Owner page never has to guess why a toggle looks
// inconsistent with observed behavior.
type systemNetworkSettingsResponse struct {
	// RequireRemoteHTTPS is the stored Owner decision.
	RequireRemoteHTTPS bool `json:"requireRemoteHTTPS"`
	// EffectiveRequireRemoteHTTPS is what transportSecurity enforces right now.
	EffectiveRequireRemoteHTTPS bool `json:"effectiveRequireRemoteHTTPS"`
	// EnvironmentForced reports REVIEW_STUDIO_REQUIRE_HTTPS=1, which pins
	// enforcement on and disables the page toggle.
	EnvironmentForced bool `json:"environmentForced"`
	// CurrentRequestSecure lets the page block a remote plaintext Owner from
	// locking itself out.
	CurrentRequestSecure bool      `json:"currentRequestSecure"`
	Revision             int       `json:"revision"`
	UpdatedBy            *string   `json:"updatedBy"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type systemNetworkSettingsRequest struct {
	RequireRemoteHTTPS bool `json:"requireRemoteHTTPS"`
	Revision           int  `json:"revision"`
}

func (h *handler) handleGetSystemNetworkSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := h.requireOwner(response, request); !ok {
		return
	}
	item, err := h.systemSettings.GetNetwork(request.Context())
	if err != nil {
		h.handleSystemNetworkSettingsError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, h.toSystemNetworkSettingsResponse(request, item))
}

func (h *handler) handleUpdateSystemNetworkSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body systemNetworkSettingsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	// Self-lockout guard: enabling enforcement from a remote plaintext page would
	// make the very next request fail with 426, leaving no way back in. Only a
	// loopback request, a host-management request, or a request Core can prove
	// arrived over HTTPS may turn it on.
	if body.RequireRemoteHTTPS && !h.requestMayEnableHTTPSEnforcement(request) {
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"system_settings.insecure_origin",
			"当前连接不是 HTTPS，开启后你会立刻无法访问。请先配置 HTTPS 网关，或在运行 Core 的本机操作",
		)
		return
	}
	if h.environmentForcesHTTPS() && !body.RequireRemoteHTTPS {
		writeError(
			response,
			http.StatusConflict,
			requestID(response),
			"system_settings.environment_forced",
			"部署配置 REVIEW_STUDIO_REQUIRE_HTTPS=1 已强制开启，需移除环境变量并重启 Core 才能关闭",
		)
		return
	}
	update, err := h.systemSettings.UpdateNetwork(
		request.Context(),
		systemsettings.UpdateNetworkInput{
			RequireRemoteHTTPS: body.RequireRemoteHTTPS,
			Revision:           body.Revision,
			UpdatedBy:          session.User.ID,
		},
	)
	if err != nil {
		h.handleSystemNetworkSettingsError(response, request, err)
		return
	}

	// Store before writing the response so the next request already sees the new
	// decision. The setting takes effect immediately; Core is never restarted.
	h.requireRemoteHTTPS.Store(update.Current.RequireRemoteHTTPS)
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "system.network_settings_updated",
		ResourceType: "system_network_settings",
		ResourceID:   "1",
		Before: map[string]any{
			"requireRemoteHTTPS": update.Previous.RequireRemoteHTTPS,
		},
		After: map[string]any{
			"requireRemoteHTTPS": update.Current.RequireRemoteHTTPS,
		},
	})
	h.logger.Info(
		"system network settings updated",
		"request_id", requestContextID(request),
		"actor_id", session.User.ID,
		"previous_require_remote_https", update.Previous.RequireRemoteHTTPS,
		"require_remote_https", update.Current.RequireRemoteHTTPS,
		"environment_forced", h.environmentForcesHTTPS(),
	)
	writeJSON(response, http.StatusOK, h.toSystemNetworkSettingsResponse(request, update.Current))
}

func (h *handler) handleSystemNetworkSettingsError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, systemsettings.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"system_settings.not_found", "系统网络设置不存在")
	case errors.Is(err, systemsettings.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"system_settings.revision_conflict", "设置已被更新，请刷新后重试")
	case errors.Is(err, systemsettings.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func (h *handler) toSystemNetworkSettingsResponse(
	request *http.Request,
	item systemsettings.NetworkSettings,
) systemNetworkSettingsResponse {
	environmentForced := h.environmentForcesHTTPS()
	return systemNetworkSettingsResponse{
		RequireRemoteHTTPS:          item.RequireRemoteHTTPS,
		EffectiveRequireRemoteHTTPS: environmentForced || item.RequireRemoteHTTPS,
		EnvironmentForced:           environmentForced,
		CurrentRequestSecure:        h.requestMayEnableHTTPSEnforcement(request),
		Revision:                    item.Revision,
		UpdatedBy:                   item.UpdatedBy,
		UpdatedAt:                   item.UpdatedAt,
	}
}
