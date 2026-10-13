package httpapi

import (
	"context"
	"net/http"
	"sync"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/encoderselection"
)

// mediaEncodingProbeGuard keeps a manual re-probe single-flight. Each sweep
// starts real FFmpeg processes, so an Owner holding down the button must not be
// able to spawn them in parallel.
type mediaEncodingProbeGuard struct {
	mu      sync.Mutex
	running bool
}

func (guard *mediaEncodingProbeGuard) start() bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.running {
		return false
	}
	guard.running = true
	return true
}

func (guard *mediaEncodingProbeGuard) finish() {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.running = false
}

// handleReprobeSystemMediaEncoding starts a probe and returns immediately: a
// sweep can take minutes, and the page already learns the outcome by reading
// detectedEncoders and detectedAt.
func (h *handler) handleReprobeSystemMediaEncoding(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	if h.runEncoderProbe == nil {
		writeError(response, http.StatusServiceUnavailable, requestID(response),
			"system_settings.probe_unavailable", "此构建无法运行编码器探测")
		return
	}
	if !h.mediaEncodingProbe.start() {
		writeError(response, http.StatusConflict, requestID(response),
			"system_settings.probe_in_progress", "编码器探测正在进行中")
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID:  session.Workspace.ID,
		ActorType:    "user",
		ActorID:      session.User.ID,
		Action:       "system.media_encoding_reprobe_requested",
		ResourceType: "system_media_encoding_settings",
		ResourceID:   "1",
	})
	// The sweep outlives the request, so it cannot inherit the request context.
	sweepContext, cancel := context.WithTimeout(
		context.WithoutCancel(request.Context()), encoderselection.ProbeTimeout)
	go func() {
		defer cancel()
		defer h.mediaEncodingProbe.finish()
		h.runMediaEncodingProbe(sweepContext, session.User.ID)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"started": true})
}

// runMediaEncodingProbe hands the sweep to the selection service, which owns
// what the result means. A failure is logged and dropped: a probe is a
// diagnosis, and a diagnosis that could not run must not fail anything else.
func (h *handler) runMediaEncodingProbe(ctx context.Context, actorID string) {
	if err := h.runEncoderProbe(ctx, actorID); err != nil {
		h.logger.Warn("media encoder probe failed", "error", err)
	}
}
