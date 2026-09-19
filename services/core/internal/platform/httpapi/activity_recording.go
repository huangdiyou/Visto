package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/notification"
	sharedomain "review-studio.local/core/internal/share"
)

func (h *handler) recordAccess(
	request *http.Request,
	item sharedomain.PublicShare,
	eventType string,
	resourceType string,
	resourceID string,
) {
	_, err := h.audit.RecordAccess(request.Context(), audit.RecordAccessInput{
		WorkspaceID:      item.WorkspaceID,
		ShareID:          item.ID,
		ShareLinkID:      item.ShareLinkID,
		VisitorID:        item.Visitor.ID,
		VisitorSessionID: item.VisitorSessionID,
		EventType:        eventType,
		ResourceType:     resourceType,
		ResourceID:       resourceID,
		IPHash:           requestIPHash(request, item.WorkspaceID),
		UserAgentSummary: summarizeUserAgent(request.UserAgent()),
	})
	if err != nil {
		h.logger.Warn(
			"access event recording failed",
			"request_id", requestContextID(request),
			"event_type", eventType,
			"error", err,
		)
	}
}

func (h *handler) recordAudit(
	request *http.Request,
	input audit.RecordLogInput,
) {
	input.RequestID = request.Header.Get("X-Request-Id")
	if input.RequestID == "" {
		input.RequestID = requestContextID(request)
	}
	input.IPHash = requestIPHash(request, input.WorkspaceID)
	if _, err := h.audit.RecordLog(request.Context(), input); err != nil {
		h.logger.Warn(
			"audit recording failed",
			"request_id", input.RequestID,
			"action", input.Action,
			"error", err,
		)
	}
}

func (h *handler) notifyWorkspaceManagers(
	request *http.Request,
	input notification.CreateForWorkspaceInput,
) {
	if err := h.notifications.CreateForWorkspaceManagers(
		request.Context(),
		input,
	); err != nil {
		h.logger.Warn(
			"notification creation failed",
			"request_id", requestContextID(request),
			"type", input.Type,
			"error", err,
		)
	}
}

func requestContextID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDContextKey{}).(string)
	return value
}

func requestIPHash(request *http.Request, workspaceID string) string {
	address, ok := requestClientIP(request)
	if !ok {
		return ""
	}
	value := sha256.Sum256([]byte(workspaceID + "|" + address.String()))
	return hex.EncodeToString(value[:])
}

func summarizeUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "未知设备"
	}
	browser := "浏览器"
	switch {
	case strings.Contains(value, "Edg/"):
		browser = "Edge"
	case strings.Contains(value, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(value, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(value, "Safari/"):
		browser = "Safari"
	}
	platform := "未知系统"
	switch {
	case strings.Contains(value, "Windows"):
		platform = "Windows"
	case strings.Contains(value, "Mac OS X"):
		platform = "macOS"
	case strings.Contains(value, "Android"):
		platform = "Android"
	case strings.Contains(value, "iPhone"), strings.Contains(value, "iPad"):
		platform = "iOS"
	case strings.Contains(value, "Linux"):
		platform = "Linux"
	}
	return fmt.Sprintf("%s · %s", browser, platform)
}
