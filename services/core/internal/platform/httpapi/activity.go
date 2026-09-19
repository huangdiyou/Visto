package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/projectaccess"
)

type accessEventResponse struct {
	ID               string    `json:"id"`
	ShareID          string    `json:"shareId"`
	ShareLinkID      *string   `json:"shareLinkId"`
	VisitorID        *string   `json:"visitorId"`
	VisitorSessionID *string   `json:"visitorSessionId"`
	VisitorName      string    `json:"visitorName"`
	EventType        string    `json:"eventType"`
	ResourceType     *string   `json:"resourceType"`
	ResourceID       *string   `json:"resourceId"`
	UserAgentSummary string    `json:"userAgentSummary"`
	OccurredAt       time.Time `json:"occurredAt"`
}

type accessEventListResponse struct {
	Items []accessEventResponse `json:"items"`
}

type auditLogResponse struct {
	ID           string            `json:"id"`
	ActorType    string            `json:"actorType"`
	ActorID      *string           `json:"actorId"`
	ActorName    string            `json:"actorName"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resourceType"`
	ResourceID   string            `json:"resourceId"`
	RequestID    string            `json:"requestId"`
	Details      map[string]string `json:"details"`
	OccurredAt   time.Time         `json:"occurredAt"`
}

type auditLogListResponse struct {
	Items []auditLogResponse `json:"items"`
}

type notificationResponse struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	ResourceType string     `json:"resourceType"`
	ResourceID   string     `json:"resourceId"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	ReadAt       *time.Time `json:"readAt"`
	CreatedAt    time.Time  `json:"createdAt"`
}

type notificationListResponse struct {
	Items       []notificationResponse `json:"items"`
	UnreadCount int                    `json:"unreadCount"`
}

func (h *handler) handleListShareAccessEvents(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionAuditRead,
	)
	if !ok {
		return
	}
	shareID := request.PathValue("shareId")
	if _, err := h.shares.Get(
		request.Context(),
		session.Workspace.ID,
		shareID,
	); err != nil {
		h.handleShareError(response, request, err)
		return
	}
	items, err := h.audit.ListAccess(
		request.Context(),
		session.Workspace.ID,
		shareID,
		queryLimit(request),
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]accessEventResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toAccessEventResponse(item))
	}
	writeJSON(response, http.StatusOK, accessEventListResponse{Items: result})
}

func (h *handler) handleListAuditLogs(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionAuditRead,
	)
	if !ok {
		return
	}
	items, err := h.audit.ListLogs(
		request.Context(),
		session.Workspace.ID,
		audit.LogListFilter{
			Limit:        queryLimit(request),
			ActorID:      strings.TrimSpace(request.URL.Query().Get("actorId")),
			Action:       strings.TrimSpace(request.URL.Query().Get("action")),
			ResourceType: strings.TrimSpace(request.URL.Query().Get("resourceType")),
			ResourceID:   strings.TrimSpace(request.URL.Query().Get("resourceId")),
		},
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeAuditLogList(response, items)
}

func (h *handler) handleListProjectActivity(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return
	}
	projectID := request.PathValue("projectId")
	if !h.requireProjectPermission(
		response,
		request,
		session,
		projectID,
		projectaccess.PermissionProjectRead,
	) {
		return
	}
	items, err := h.audit.ListLogs(
		request.Context(),
		session.Workspace.ID,
		audit.LogListFilter{
			Limit:      queryLimit(request),
			ResourceID: projectID,
		},
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeAuditLogList(response, items)
}

func writeAuditLogList(response http.ResponseWriter, items []audit.AuditLog) {
	result := make([]auditLogResponse, 0, len(items))
	for _, item := range items {
		result = append(result, auditLogResponse{
			ID:           item.ID,
			ActorType:    item.ActorType,
			ActorID:      item.ActorID,
			ActorName:    item.ActorName,
			Action:       item.Action,
			ResourceType: item.ResourceType,
			ResourceID:   item.ResourceID,
			RequestID:    item.RequestID,
			Details:      auditDetailsForResponse(item.Details),
			OccurredAt:   item.OccurredAt,
		})
	}
	writeJSON(response, http.StatusOK, auditLogListResponse{Items: result})
}

func auditDetailsForResponse(details map[string]string) map[string]string {
	if len(details) == 0 {
		return map[string]string{}
	}
	return details
}

func (h *handler) handleListNotifications(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionNotifications,
	)
	if !ok {
		return
	}
	items, unreadCount, err := h.notifications.List(
		request.Context(),
		session.Workspace.ID,
		session.User.ID,
		request.URL.Query().Get("unreadOnly") == "true",
		queryLimit(request),
	)
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	result := make([]notificationResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toNotificationResponse(item))
	}
	writeJSON(response, http.StatusOK, notificationListResponse{
		Items:       result,
		UnreadCount: unreadCount,
	})
}

func (h *handler) handleMarkNotificationRead(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionNotifications,
	)
	if !ok {
		return
	}
	item, err := h.notifications.MarkRead(
		request.Context(),
		session.Workspace.ID,
		session.User.ID,
		request.PathValue("notificationId"),
	)
	if errors.Is(err, notification.ErrNotFound) {
		writeError(
			response,
			http.StatusNotFound,
			requestID(response),
			"notification.not_found",
			"通知不存在",
		)
		return
	}
	if err != nil {
		h.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toNotificationResponse(item))
}

func (h *handler) handleMarkAllNotificationsRead(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requirePermission(
		response,
		request,
		authorization.PermissionNotifications,
	)
	if !ok {
		return
	}
	if err := h.notifications.MarkAllRead(
		request.Context(),
		session.Workspace.ID,
		session.User.ID,
	); err != nil {
		h.internalError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func toAccessEventResponse(item audit.AccessEvent) accessEventResponse {
	return accessEventResponse{
		ID:               item.ID,
		ShareID:          item.ShareID,
		ShareLinkID:      item.ShareLinkID,
		VisitorID:        item.VisitorID,
		VisitorSessionID: item.VisitorSessionID,
		VisitorName:      item.VisitorName,
		EventType:        item.EventType,
		ResourceType:     item.ResourceType,
		ResourceID:       item.ResourceID,
		UserAgentSummary: item.UserAgentSummary,
		OccurredAt:       item.OccurredAt,
	}
}

func toNotificationResponse(item notification.Notification) notificationResponse {
	return notificationResponse{
		ID:           item.ID,
		Type:         item.Type,
		ResourceType: item.ResourceType,
		ResourceID:   item.ResourceID,
		Title:        item.Title,
		Body:         item.Body,
		ReadAt:       item.ReadAt,
		CreatedAt:    item.CreatedAt,
	}
}

func queryLimit(request *http.Request) int {
	value, err := strconv.Atoi(request.URL.Query().Get("limit"))
	if err != nil {
		return 100
	}
	return value
}
