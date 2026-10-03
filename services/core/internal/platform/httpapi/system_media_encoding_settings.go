package httpapi

import (
	"errors"
	"net/http"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/systemsettings"
)

// systemMediaEncodingSettingsResponse is what the Owner's "媒体编码" pane reads.
//
// PreferredEncoder is the stored Owner decision ("" means "follow the probe").
// EffectiveEncoder is what the next job will actually use, so the page never has
// to re-derive the priority order itself
// (docs/MEDIA_ENCODING_SELECTION_DESIGN.md 3.2).
type systemMediaEncodingSettingsResponse struct {
	PreferredEncoder string     `json:"preferredEncoder"`
	EffectiveEncoder string     `json:"effectiveEncoder"`
	ActiveEncoder    string     `json:"activeEncoder"`
	DetectedEncoders []string   `json:"detectedEncoders"`
	DetectedAt       *time.Time `json:"detectedAt"`
	FailureCount     int        `json:"failureCount"`
	TrippedEncoder   *string    `json:"trippedEncoder"`
	TrippedReason    *string    `json:"trippedReason"`
	TrippedAt        *time.Time `json:"trippedAt"`
	Revision         int        `json:"revision"`
	UpdatedBy        *string    `json:"updatedBy"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type systemMediaEncodingSettingsRequest struct {
	// PreferredEncoder is the Owner's choice. The empty string means "follow the
	// probe", which is how an Owner returns to automatic selection.
	PreferredEncoder string `json:"preferredEncoder"`
	Revision         int    `json:"revision"`
}

func (h *handler) handleGetSystemMediaEncodingSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := h.requireOwner(response, request); !ok {
		return
	}
	item, err := h.systemSettings.GetMediaEncoding(request.Context())
	if err != nil {
		h.handleSystemMediaEncodingSettingsError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, h.toSystemMediaEncodingSettingsResponse(item))
}

func (h *handler) handleUpdateSystemMediaEncodingSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body systemMediaEncodingSettingsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	update, err := h.systemSettings.UpdateMediaEncoding(
		request.Context(),
		systemsettings.UpdateMediaEncodingInput{
			PreferredEncoder: body.PreferredEncoder,
			Revision:         body.Revision,
			UpdatedBy:        session.User.ID,
		},
	)
	if err != nil {
		h.handleSystemMediaEncodingSettingsError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "system.media_encoding_updated",
		ResourceType: "system_media_encoding_settings",
		ResourceID:   "1",
		Before: map[string]any{
			"preferredEncoder": update.Previous.PreferredEncoder,
		},
		After: map[string]any{
			"preferredEncoder": update.Current.PreferredEncoder,
		},
	})
	// The page promises the change applies immediately, so push it into the
	// running processor. The stored value is written either way; a build without
	// an applier just cannot honour the promise until the next start, and says so.
	if h.applyMediaEncoder != nil && !h.applyMediaEncoder(update.Current.PreferredEncoder) {
		h.logger.Warn(
			"stored media encoder choice was not applied to the running processor",
			"request_id", requestContextID(request),
			"preferred_encoder", update.Current.PreferredEncoder,
		)
		writeError(response, http.StatusServiceUnavailable, requestID(response),
			"system_settings.encoder_apply_failed", "编码设置已保存，但未能应用到运行中的处理器；请检查运行时并重新选择")
		return
	}
	h.logger.Info(
		"system media encoding settings updated",
		"request_id", requestContextID(request),
		"actor_id", session.User.ID,
		"previous_preferred_encoder", update.Previous.PreferredEncoder,
		"preferred_encoder", update.Current.PreferredEncoder,
	)
	// Applying the choice also moves the runtime choice, so the row read before
	// the apply no longer describes what the next job will use. Re-read it rather
	// than answering with a state the page would then have to correct itself.
	settled := update.Current
	if current, readErr := h.systemSettings.GetMediaEncoding(request.Context()); readErr == nil {
		settled = current
	}
	writeJSON(response, http.StatusOK, h.toSystemMediaEncodingSettingsResponse(settled))
}

func (h *handler) handleSystemMediaEncodingSettingsError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, systemsettings.ErrMediaEncodingNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"system_settings.not_found", "系统媒体编码设置不存在")
	case errors.Is(err, systemsettings.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"system_settings.revision_conflict", "设置已被更新，请刷新后重试")
	case errors.Is(err, systemsettings.ErrInvalidInput):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

// effectiveMediaEncoder applies the documented priority: the encoder the breaker
// moved the instance to, then the Owner's explicit choice, then whatever Core is
// configured with. A breaker choice wins because it reflects what actually ran.
func (h *handler) effectiveMediaEncoder(item systemsettings.MediaEncodingSettings) string {
	switch {
	case item.ActiveEncoder != "":
		return item.ActiveEncoder
	case item.PreferredEncoder != "":
		return item.PreferredEncoder
	default:
		return h.videoAcceleration.Encoder
	}
}

func (h *handler) toSystemMediaEncodingSettingsResponse(
	item systemsettings.MediaEncodingSettings,
) systemMediaEncodingSettingsResponse {
	detected := item.DetectedEncoders
	if detected == nil {
		detected = []string{}
	}
	return systemMediaEncodingSettingsResponse{
		PreferredEncoder: item.PreferredEncoder,
		EffectiveEncoder: h.effectiveMediaEncoder(item),
		ActiveEncoder:    item.ActiveEncoder,
		DetectedEncoders: detected,
		DetectedAt:       item.DetectedAt,
		FailureCount:     item.FailureCount,
		TrippedEncoder:   item.TrippedEncoder,
		TrippedReason:    item.TrippedReason,
		TrippedAt:        item.TrippedAt,
		Revision:         item.Revision,
		UpdatedBy:        item.UpdatedBy,
		UpdatedAt:        item.UpdatedAt,
	}
}
