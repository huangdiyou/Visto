package notification

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"review-studio.local/core/internal/secretstore"
)

type Service struct {
	repository Repository
	secrets    secretstore.Store
	sender     Sender
	clock      func() time.Time
}

var deliveryErrorURLPattern = regexp.MustCompile(`https?://[^\s"']+`)

func NewService(repository Repository) *Service {
	return NewServiceWithDependencies(repository, nil, NoopSender{})
}

func NewServiceWithDependencies(
	repository Repository,
	secrets secretstore.Store,
	sender Sender,
) *Service {
	if sender == nil {
		sender = NoopSender{}
	}
	return &Service{
		repository: repository,
		secrets:    secrets,
		sender:     sender,
		clock:      time.Now,
	}
}

func (service *Service) SetSender(sender Sender) {
	if sender != nil {
		service.sender = sender
	}
}

func (service *Service) CreateForWorkspaceManagers(
	ctx context.Context,
	input CreateForWorkspaceInput,
) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Type = strings.TrimSpace(input.Type)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.WorkspaceID == "" || input.Type == "" ||
		input.ResourceType == "" || input.ResourceID == "" ||
		input.Title == "" || input.Body == "" {
		return ErrInvalidInput
	}
	recipients, err := service.repository.WorkspaceManagerIDs(
		ctx,
		input.WorkspaceID,
		strings.TrimSpace(input.ExcludeUserID),
	)
	if err != nil {
		return err
	}
	return service.createForRecipients(
		ctx,
		input.WorkspaceID,
		input.Type,
		input.ResourceType,
		input.ResourceID,
		input.Title,
		input.Body,
		recipients,
	)
}

func (service *Service) CreateForUsers(
	ctx context.Context,
	input CreateForUsersInput,
) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Type = strings.TrimSpace(input.Type)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.WorkspaceID == "" || input.Type == "" ||
		input.ResourceType == "" || input.ResourceID == "" ||
		input.Title == "" || input.Body == "" {
		return ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input.RecipientUserIDs))
	recipients := make([]string, 0, len(input.RecipientUserIDs))
	for _, recipient := range input.RecipientUserIDs {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if _, ok := seen[recipient]; ok {
			continue
		}
		seen[recipient] = struct{}{}
		recipients = append(recipients, recipient)
	}
	if len(recipients) == 0 {
		return nil
	}
	return service.createForRecipients(
		ctx,
		input.WorkspaceID,
		input.Type,
		input.ResourceType,
		input.ResourceID,
		input.Title,
		input.Body,
		recipients,
	)
}

func (service *Service) createForRecipients(
	ctx context.Context,
	workspaceID string,
	notificationType string,
	resourceType string,
	resourceID string,
	title string,
	body string,
	recipients []string,
) error {
	if len(recipients) == 0 {
		return nil
	}
	now := service.clock().UTC()
	items := make([]Notification, 0, len(recipients))
	outbox := make([]OutboxMessage, 0, len(recipients))
	for _, recipient := range recipients {
		id, err := notificationID()
		if err != nil {
			return err
		}
		items = append(items, Notification{
			ID:              id,
			WorkspaceID:     workspaceID,
			RecipientUserID: recipient,
			Type:            notificationType,
			ResourceType:    resourceType,
			ResourceID:      resourceID,
			Title:           title,
			Body:            body,
			CreatedAt:       now,
		})
		outboxID, err := notificationID()
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]string{
			"notificationId":  id,
			"recipientUserId": recipient,
			"resourceType":    resourceType,
			"resourceId":      resourceID,
			"title":           title,
			"body":            body,
		})
		if err != nil {
			return err
		}
		outbox = append(outbox, OutboxMessage{
			ID:             outboxID,
			WorkspaceID:    workspaceID,
			NotificationID: &id,
			EventType:      notificationType,
			Payload:        payload,
			IdempotencyKey: "notification:" + id,
			Status:         "pending",
			MaxAttempts:    5,
			AvailableAt:    now,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	return service.repository.CreateMany(ctx, items, outbox)
}

func (service *Service) List(
	ctx context.Context,
	workspaceID string,
	userID string,
	unreadOnly bool,
	limit int,
) ([]Notification, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return service.repository.List(ctx, workspaceID, userID, unreadOnly, limit)
}

func (service *Service) MarkRead(
	ctx context.Context,
	workspaceID string,
	userID string,
	id string,
) (Notification, error) {
	return service.repository.MarkRead(
		ctx,
		workspaceID,
		userID,
		id,
		service.clock().UTC(),
	)
}

func (service *Service) MarkAllRead(
	ctx context.Context,
	workspaceID string,
	userID string,
) error {
	return service.repository.MarkAllRead(
		ctx,
		workspaceID,
		userID,
		service.clock().UTC(),
	)
}

func (service *Service) ClaimDelivery(
	ctx context.Context,
	leaseDuration time.Duration,
) (OutboxMessage, bool, error) {
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}
	return service.repository.ClaimOutbox(
		ctx,
		service.clock().UTC(),
		leaseDuration,
	)
}

func (service *Service) SucceedDelivery(
	ctx context.Context,
	messageID string,
) error {
	return service.repository.CompleteOutbox(
		ctx,
		strings.TrimSpace(messageID),
		service.clock().UTC(),
	)
}

func (service *Service) FailDelivery(
	ctx context.Context,
	messageID string,
	deliveryError DeliveryError,
) error {
	deliveryError.Code = strings.TrimSpace(deliveryError.Code)
	deliveryError.Message = sanitizeDeliveryError(deliveryError.Message)
	if deliveryError.Code == "" {
		deliveryError.Code = "notification.delivery_failed"
	}
	now := service.clock().UTC()
	return service.repository.FailOutbox(
		ctx,
		strings.TrimSpace(messageID),
		deliveryError,
		now.Add(2*time.Second),
		now,
	)
}

func notificationID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func sanitizeDeliveryError(value string) string {
	value = strings.TrimSpace(value)
	value = deliveryErrorURLPattern.ReplaceAllStringFunc(value, redactDeliveryErrorURL)
	if len(value) > 1000 {
		value = value[:1000]
	}
	return value
}

func redactDeliveryErrorURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "<redacted-url>"
	}
	return parsed.Scheme + "://" + parsed.Host + "/<redacted>"
}
