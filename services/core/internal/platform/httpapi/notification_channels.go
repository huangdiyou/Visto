package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/notification"
)

type notificationChannelRequest struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	SMTPHost            string `json:"smtpHost"`
	SMTPPort            int    `json:"smtpPort"`
	SMTPSecurity        string `json:"smtpSecurity"`
	SMTPFromAddress     string `json:"smtpFromAddress"`
	SMTPFromName        string `json:"smtpFromName"`
	SMTPUsername        string `json:"smtpUsername"`
	SMTPPassword        string `json:"smtpPassword"`
	TestRecipient       string `json:"testRecipient"`
	WebhookURL          string `json:"webhookUrl"`
	AllowPrivateNetwork bool   `json:"allowPrivateNetwork"`
	Revision            int    `json:"revision,omitempty"`
}

type notificationChannelResponse struct {
	ID                  string     `json:"id"`
	WorkspaceID         string     `json:"workspaceId"`
	Kind                string     `json:"kind"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	SMTPHost            string     `json:"smtpHost,omitempty"`
	SMTPPort            int        `json:"smtpPort,omitempty"`
	SMTPSecurity        string     `json:"smtpSecurity,omitempty"`
	SMTPFromAddress     string     `json:"smtpFromAddress,omitempty"`
	SMTPFromName        string     `json:"smtpFromName,omitempty"`
	SMTPAuthConfigured  bool       `json:"smtpAuthConfigured"`
	TestRecipient       string     `json:"testRecipient,omitempty"`
	WebhookHost         string     `json:"webhookHost,omitempty"`
	AllowPrivateNetwork bool       `json:"allowPrivateNetwork"`
	Revision            int        `json:"revision"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	LastTestAt          *time.Time `json:"lastTestAt"`
	LastTestStatus      *string    `json:"lastTestStatus"`
	LastErrorCode       *string    `json:"lastErrorCode"`
	LastErrorMessage    *string    `json:"lastErrorMessage"`
}

type notificationChannelListResponse struct {
	Items []notificationChannelResponse `json:"items"`
}

type notificationChannelTestResponse struct {
	ChannelID string    `json:"channelId"`
	Status    string    `json:"status"`
	LatencyMS int64     `json:"latencyMs"`
	TestedAt  time.Time `json:"testedAt"`
}

type notificationPreferencesRequest struct {
	EmailEnabled      bool `json:"emailEnabled"`
	FeishuEnabled     bool `json:"feishuEnabled"`
	WechatWorkEnabled bool `json:"wechatWorkEnabled"`
}

type notificationPreferencesResponse struct {
	UserID            string    `json:"userId"`
	EmailEnabled      bool      `json:"emailEnabled"`
	FeishuEnabled     bool      `json:"feishuEnabled"`
	WechatWorkEnabled bool      `json:"wechatWorkEnabled"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type notificationDeliveryResponse struct {
	ID              string     `json:"id"`
	OutboxID        string     `json:"outboxId"`
	NotificationID  *string    `json:"notificationId"`
	RecipientUserID *string    `json:"recipientUserId"`
	ChannelID       *string    `json:"channelId"`
	ChannelKind     string     `json:"channelKind"`
	ChannelName     string     `json:"channelName"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"maxAttempts"`
	LastErrorCode   *string    `json:"lastErrorCode"`
	LastError       *string    `json:"lastError"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	DeliveredAt     *time.Time `json:"deliveredAt"`
}

type notificationDeliveryListResponse struct {
	Items []notificationDeliveryResponse `json:"items"`
}

func (h *handler) handleListNotificationChannels(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	items, err := h.notifications.ListChannels(
		request.Context(),
		session.Workspace.ID,
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	result := make([]notificationChannelResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toNotificationChannelResponse(item))
	}
	writeJSON(response, http.StatusOK, notificationChannelListResponse{Items: result})
}

func (h *handler) handleCreateNotificationChannel(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body notificationChannelRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.notifications.CreateChannel(
		request.Context(),
		notification.CreateChannelInput{
			WorkspaceID:         session.Workspace.ID,
			Kind:                body.Kind,
			Name:                body.Name,
			SMTPHost:            body.SMTPHost,
			SMTPPort:            body.SMTPPort,
			SMTPSecurity:        body.SMTPSecurity,
			SMTPFromAddress:     body.SMTPFromAddress,
			SMTPFromName:        body.SMTPFromName,
			SMTPUsername:        body.SMTPUsername,
			SMTPPassword:        body.SMTPPassword,
			TestRecipient:       body.TestRecipient,
			WebhookURL:          body.WebhookURL,
			AllowPrivateNetwork: body.AllowPrivateNetwork,
		},
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "notification.channel_created",
		ResourceType: "notification_channel", ResourceID: item.ID,
		After: map[string]any{"kind": item.Kind, "name": item.Name},
	})
	writeJSON(response, http.StatusCreated, toNotificationChannelResponse(item))
}

func (h *handler) handleUpdateNotificationChannel(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body notificationChannelRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.notifications.UpdateChannel(
		request.Context(),
		notification.UpdateChannelInput{
			WorkspaceID:         session.Workspace.ID,
			ID:                  request.PathValue("channelId"),
			Name:                body.Name,
			SMTPHost:            body.SMTPHost,
			SMTPPort:            body.SMTPPort,
			SMTPSecurity:        body.SMTPSecurity,
			SMTPFromAddress:     body.SMTPFromAddress,
			SMTPFromName:        body.SMTPFromName,
			SMTPUsername:        body.SMTPUsername,
			SMTPPassword:        body.SMTPPassword,
			TestRecipient:       body.TestRecipient,
			WebhookURL:          body.WebhookURL,
			AllowPrivateNetwork: body.AllowPrivateNetwork,
			Revision:            body.Revision,
		},
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "notification.channel_updated",
		ResourceType: "notification_channel", ResourceID: item.ID,
		After: map[string]any{"kind": item.Kind, "name": item.Name},
	})
	writeJSON(response, http.StatusOK, toNotificationChannelResponse(item))
}

func (h *handler) handleDeleteNotificationChannel(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	var body revisionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	id := request.PathValue("channelId")
	if err := h.notifications.DeleteChannel(
		request.Context(),
		notification.ChannelStateInput{
			WorkspaceID: session.Workspace.ID,
			ID:          id,
			Revision:    body.Revision,
		},
	); err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "notification.channel_deleted",
		ResourceType: "notification_channel", ResourceID: id,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (h *handler) handleTestNotificationChannel(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	id := request.PathValue("channelId")
	report, err := h.notifications.TestChannel(
		request.Context(),
		session.Workspace.ID,
		id,
	)
	if err != nil {
		h.recordAudit(request, audit.RecordLogInput{
			WorkspaceID: session.Workspace.ID, ActorType: "user",
			ActorID: session.User.ID, Action: "notification.channel_test_failed",
			ResourceType: "notification_channel", ResourceID: id,
		})
		h.handleNotificationAdminError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "notification.channel_test_succeeded",
		ResourceType: "notification_channel", ResourceID: id,
	})
	writeJSON(response, http.StatusOK, notificationChannelTestResponse{
		ChannelID: report.ChannelID,
		Status:    report.Status,
		LatencyMS: report.Latency.Milliseconds(),
		TestedAt:  report.TestedAt,
	})
}

func (h *handler) handleGetNotificationPreferences(
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
	item, err := h.notifications.Preferences(
		request.Context(),
		session.Workspace.ID,
		session.User.ID,
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toNotificationPreferencesResponse(item))
}

func (h *handler) handleUpdateNotificationPreferences(
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
	var body notificationPreferencesRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.badRequest(response, request, err)
		return
	}
	item, err := h.notifications.SetPreferences(
		request.Context(),
		notification.PreferenceInput{
			WorkspaceID:       session.Workspace.ID,
			UserID:            session.User.ID,
			EmailEnabled:      body.EmailEnabled,
			FeishuEnabled:     body.FeishuEnabled,
			WechatWorkEnabled: body.WechatWorkEnabled,
		},
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, toNotificationPreferencesResponse(item))
}

func (h *handler) handleListNotificationDeliveries(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	items, err := h.notifications.ListDeliveries(
		request.Context(),
		session.Workspace.ID,
		queryLimit(request),
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	result := make([]notificationDeliveryResponse, 0, len(items))
	for _, item := range items {
		result = append(result, toNotificationDeliveryResponse(item))
	}
	writeJSON(response, http.StatusOK, notificationDeliveryListResponse{Items: result})
}

func (h *handler) handleRetryNotificationDelivery(
	response http.ResponseWriter,
	request *http.Request,
) {
	session, ok := h.requireOwner(response, request)
	if !ok {
		return
	}
	item, err := h.notifications.RetryDelivery(
		request.Context(),
		session.Workspace.ID,
		request.PathValue("deliveryId"),
	)
	if err != nil {
		h.handleNotificationAdminError(response, request, err)
		return
	}
	h.recordAudit(request, audit.RecordLogInput{
		WorkspaceID: session.Workspace.ID, ActorType: "user",
		ActorID: session.User.ID, Action: "notification.delivery_retried",
		ResourceType: "notification_delivery", ResourceID: item.ID,
	})
	writeJSON(response, http.StatusOK, toNotificationDeliveryResponse(item))
}

func toNotificationChannelResponse(
	item notification.Channel,
) notificationChannelResponse {
	return notificationChannelResponse{
		ID:                  item.ID,
		WorkspaceID:         item.WorkspaceID,
		Kind:                item.Kind,
		Name:                item.Name,
		Status:              item.Status,
		SMTPHost:            item.Config["smtpHost"],
		SMTPPort:            parseOptionalInt(item.Config["smtpPort"]),
		SMTPSecurity:        item.Config["smtpSecurity"],
		SMTPFromAddress:     item.Config["smtpFromAddress"],
		SMTPFromName:        item.Config["smtpFromName"],
		SMTPAuthConfigured:  item.Config["smtpAuthConfigured"] == "true",
		TestRecipient:       item.Config["testRecipient"],
		WebhookHost:         item.Config["webhookHost"],
		AllowPrivateNetwork: item.Config["allowPrivateNetwork"] == "true",
		Revision:            item.Revision,
		CreatedAt:           item.CreatedAt,
		UpdatedAt:           item.UpdatedAt,
		LastTestAt:          item.LastTestAt,
		LastTestStatus:      item.LastTestStatus,
		LastErrorCode:       item.LastErrorCode,
		LastErrorMessage:    item.LastErrorMessage,
	}
}

func toNotificationPreferencesResponse(
	item notification.Preferences,
) notificationPreferencesResponse {
	return notificationPreferencesResponse{
		UserID:            item.UserID,
		EmailEnabled:      item.EmailEnabled,
		FeishuEnabled:     item.FeishuEnabled,
		WechatWorkEnabled: item.WechatWorkEnabled,
		UpdatedAt:         item.UpdatedAt,
	}
}

func toNotificationDeliveryResponse(
	item notification.Delivery,
) notificationDeliveryResponse {
	return notificationDeliveryResponse{
		ID:              item.ID,
		OutboxID:        item.OutboxID,
		NotificationID:  item.NotificationID,
		RecipientUserID: item.RecipientUserID,
		ChannelID:       item.ChannelID,
		ChannelKind:     item.ChannelKind,
		ChannelName:     item.ChannelName,
		Status:          item.Status,
		Attempts:        item.Attempts,
		MaxAttempts:     item.MaxAttempts,
		LastErrorCode:   item.LastErrorCode,
		LastError:       item.LastError,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		DeliveredAt:     item.DeliveredAt,
	}
}

func (h *handler) handleNotificationAdminError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, notification.ErrChannelNotFound),
		errors.Is(err, notification.ErrDeliveryNotFound),
		errors.Is(err, notification.ErrNotFound):
		writeError(response, http.StatusNotFound, requestID(response),
			"notification.not_found", "notification resource not found")
	case errors.Is(err, notification.ErrChannelNameConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"notification.channel_name_conflict", "notification channel name already exists")
	case errors.Is(err, notification.ErrRevisionConflict):
		writeError(response, http.StatusConflict, requestID(response),
			"resource.revision_conflict", "notification resource was updated")
	case errors.Is(err, notification.ErrDeliveryNotRetryable):
		writeError(response, http.StatusConflict, requestID(response),
			"notification.delivery_not_retryable", "delivery is not retryable")
	case errors.Is(err, notification.ErrInvalidInput),
		errors.Is(err, notification.ErrChannelKindUnsupported):
		h.badRequest(response, request, err)
	default:
		h.internalError(response, request, err)
	}
}

func parseOptionalInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}
