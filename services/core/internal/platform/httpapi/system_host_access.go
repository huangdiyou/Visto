package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/systemsettings"
)

// systemHostAccessResponse is deliberately read-only (D2,
// docs/FREE_TIER_BOUNDARY_DESIGN.md §2.3). The switch grants host-level reach to
// an Owner web session, so the session that benefits from it must not be able to
// flip it: an attacker who captured an Owner session would otherwise be able to
// grant itself host access in one request. There is no PUT or PATCH counterpart
// anywhere in the router, and that absence is the security property. The value
// can change in exactly two places — the first-run wizard, which requires the
// host management token, and the deployment configuration.
//
// The response separates the stored decision from what the guard actually uses,
// so an operator who pinned the value by environment can see why the wizard's
// answer appears to have no effect.
type systemHostAccessResponse struct {
	// AllowWebHostPaths is the value the first-run wizard recorded.
	AllowWebHostPaths bool `json:"allowWebHostPaths"`
	// EffectiveAllowWebHostPaths is what requireHostManagement consults right
	// now, including the deployment override.
	EffectiveAllowWebHostPaths bool `json:"effectiveAllowWebHostPaths"`
	// EnvironmentForced reports VISTO_ALLOW_WEB_HOST_PATHS, which pins the value
	// and overrides whatever the wizard recorded.
	EnvironmentForced bool      `json:"environmentForced"`
	Revision          int       `json:"revision"`
	UpdatedBy         *string   `json:"updatedBy"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

func (h *handler) handleGetSystemHostAccess(
	response http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := h.requireOwner(response, request); !ok {
		return
	}
	item, err := h.systemSettings.GetHostAccess(request.Context())
	if err != nil {
		h.handleSystemHostAccessError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, h.toSystemHostAccessResponse(item))
}

func (h *handler) handleSystemHostAccessError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, systemsettings.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"system_settings.not_found", "系统主机访问设置不存在")
	default:
		h.internalError(response, request, err)
	}
}

func (h *handler) toSystemHostAccessResponse(
	item systemsettings.HostAccessSettings,
) systemHostAccessResponse {
	effective := item.AllowWebHostPaths
	if h.allowWebHostPathsOverride != nil {
		effective = *h.allowWebHostPathsOverride
	}
	return systemHostAccessResponse{
		AllowWebHostPaths:          item.AllowWebHostPaths,
		EffectiveAllowWebHostPaths: effective,
		EnvironmentForced:          h.allowWebHostPathsOverride != nil,
		Revision:                   item.Revision,
		UpdatedBy:                  item.UpdatedBy,
		UpdatedAt:                  item.UpdatedAt,
	}
}
