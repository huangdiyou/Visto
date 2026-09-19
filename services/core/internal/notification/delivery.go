package notification

import (
	"context"
	"encoding/json"
	"strings"
)

type outboxPayload struct {
	NotificationID  string `json:"notificationId"`
	RecipientUserID string `json:"recipientUserId"`
	ResourceType    string `json:"resourceType"`
	ResourceID      string `json:"resourceId"`
	Title           string `json:"title"`
	Body            string `json:"body"`
}

type parsedOutboxPayload struct {
	NotificationID  string
	RecipientUserID string
	DeliveryPayload
}

func (service *Service) DispatchMessage(
	ctx context.Context,
	message OutboxMessage,
) error {
	payload, err := parseOutboxPayload(message)
	if err != nil {
		return err
	}
	targets, err := service.repository.DeliveryTargets(ctx, message)
	if err != nil {
		return err
	}
	targetByKey := make(map[string]DeliveryTarget, len(targets))
	now := service.clock().UTC()
	for _, target := range targets {
		if target.SecretRef != "" {
			secret, err := service.loadChannelSecret(
				ctx,
				message.WorkspaceID,
				target.SecretRef,
			)
			if err != nil {
				return err
			}
			target.Secret = secret
		}
		key := deliveryIdempotencyKey(message, payload, target)
		target.IdempotencyKey = key
		targetByKey[key] = target
		id, err := notificationID()
		if err != nil {
			return err
		}
		notificationID := nullableString(payload.NotificationID)
		recipientID := nullableString(payload.RecipientUserID)
		channelID := nullableString(target.Channel.ID)
		if err := service.repository.UpsertDelivery(ctx, createDeliveryRecord{
			ID:              id,
			WorkspaceID:     message.WorkspaceID,
			OutboxID:        message.ID,
			NotificationID:  notificationID,
			RecipientUserID: recipientID,
			ChannelID:       channelID,
			ChannelKind:     target.Channel.Kind,
			IdempotencyKey:  key,
			MaxAttempts:     message.MaxAttempts,
			Now:             now,
		}); err != nil {
			return err
		}
	}

	deliveries, err := service.repository.ListDeliveriesForOutbox(ctx, message.ID)
	if err != nil {
		return err
	}
	var firstFailure error
	for _, delivery := range deliveries {
		if delivery.Status == "succeeded" || delivery.Status == "skipped" {
			continue
		}
		target, ok := targetByKey[delivery.IdempotencyKey]
		if !ok {
			continue
		}
		started, err := service.repository.StartDelivery(
			ctx,
			delivery.ID,
			service.clock().UTC(),
		)
		if err != nil {
			if firstFailure == nil {
				firstFailure = err
			}
			continue
		}
		if err := service.sender.Send(ctx, target, payload.DeliveryPayload); err != nil {
			deliveryErr := deliveryErrorFrom(err)
			_ = service.repository.FailDeliveryRecord(
				context.WithoutCancel(ctx),
				started.ID,
				deliveryErr,
				service.clock().UTC(),
			)
			if firstFailure == nil {
				firstFailure = deliveryErr
			}
			continue
		}
		if err := service.repository.CompleteDelivery(
			context.WithoutCancel(ctx),
			started.ID,
			service.clock().UTC(),
		); err != nil && firstFailure == nil {
			firstFailure = err
		}
	}
	return firstFailure
}

func (service *Service) ListDeliveries(
	ctx context.Context,
	workspaceID string,
	limit int,
) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return service.repository.ListDeliveries(
		ctx,
		strings.TrimSpace(workspaceID),
		limit,
	)
}

func (service *Service) RetryDelivery(
	ctx context.Context,
	workspaceID string,
	id string,
) (Delivery, error) {
	return service.repository.RetryDelivery(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(id),
		service.clock().UTC(),
	)
}

func parseOutboxPayload(message OutboxMessage) (parsedOutboxPayload, error) {
	var payload outboxPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return parsedOutboxPayload{}, DeliveryError{
			Code:    "notification.payload_invalid",
			Message: "notification payload is invalid",
		}
	}
	title := strings.TrimSpace(payload.Title)
	body := strings.TrimSpace(payload.Body)
	if title == "" || body == "" {
		return parsedOutboxPayload{}, DeliveryError{
			Code:    "notification.payload_invalid",
			Message: "notification title or body is missing",
		}
	}
	return parsedOutboxPayload{
		NotificationID:  strings.TrimSpace(payload.NotificationID),
		RecipientUserID: strings.TrimSpace(payload.RecipientUserID),
		DeliveryPayload: DeliveryPayload{
			EventType:    message.EventType,
			ResourceType: strings.TrimSpace(payload.ResourceType),
			ResourceID:   strings.TrimSpace(payload.ResourceID),
			Title:        title,
			Body:         body,
		},
	}, nil
}

func deliveryIdempotencyKey(
	message OutboxMessage,
	payload parsedOutboxPayload,
	target DeliveryTarget,
) string {
	if target.Channel.Kind == "email" {
		return "notification:" + message.ID + ":channel:" + target.Channel.ID
	}
	return "notification:" + target.Channel.ID + ":" +
		message.EventType + ":" +
		payload.ResourceType + ":" +
		payload.ResourceID
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
