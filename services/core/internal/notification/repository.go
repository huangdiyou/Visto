package notification

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidInput           = errors.New("notification input is invalid")
	ErrNotFound               = errors.New("notification not found")
	ErrChannelNotFound        = errors.New("notification channel not found")
	ErrChannelNameConflict    = errors.New("notification channel name conflict")
	ErrChannelKindUnsupported = errors.New("notification channel kind is unsupported")
	ErrRevisionConflict       = errors.New("notification revision conflict")
	ErrDeliveryNotFound       = errors.New("notification delivery not found")
	ErrDeliveryNotRetryable   = errors.New("notification delivery is not retryable")
	ErrSMTPEndpointChanged    = errors.New("SMTP endpoint changed; re-enter credentials")
	ErrSMTPPlaintextAuth      = errors.New("plaintext SMTP with authentication is forbidden")
)

type Repository interface {
	WorkspaceManagerIDs(context.Context, string, string) ([]string, error)
	CreateMany(context.Context, []Notification, []OutboxMessage) error
	List(context.Context, string, string, bool, int) ([]Notification, int, error)
	MarkRead(context.Context, string, string, string, time.Time) (Notification, error)
	MarkAllRead(context.Context, string, string, time.Time) error

	ListChannels(context.Context, string) ([]Channel, error)
	Channel(context.Context, string, string) (Channel, error)
	ResolvedChannel(context.Context, string, string) (channelRecord, error)
	CreateChannel(context.Context, createChannelRecord) (Channel, error)
	UpdateChannel(context.Context, updateChannelRecord) (Channel, error)
	UpdateChannelTest(
		context.Context,
		string,
		string,
		string,
		string,
		string,
		time.Time,
	) (Channel, error)
	DeleteChannel(context.Context, ChannelStateInput, time.Time) error
	Preferences(context.Context, string, string) (Preferences, error)
	SetPreferences(context.Context, PreferenceInput, time.Time) (Preferences, error)

	ClaimOutbox(context.Context, time.Time, time.Duration) (OutboxMessage, bool, error)
	CompleteOutbox(context.Context, string, time.Time) error
	FailOutbox(context.Context, string, DeliveryError, time.Time, time.Time) error
	DeliveryTargets(context.Context, OutboxMessage) ([]DeliveryTarget, error)
	UpsertDelivery(context.Context, createDeliveryRecord) error
	ListDeliveriesForOutbox(context.Context, string) ([]Delivery, error)
	ListDeliveries(context.Context, string, int) ([]Delivery, error)
	StartDelivery(context.Context, string, time.Time) (Delivery, error)
	CompleteDelivery(context.Context, string, time.Time) error
	FailDeliveryRecord(context.Context, string, DeliveryError, time.Time) error
	RetryDelivery(context.Context, string, string, time.Time) (Delivery, error)
}

type channelRecord struct {
	Channel
	SecretRef string
}

type createChannelRecord struct {
	Channel
	SecretRef string
	Now       time.Time
}

type updateChannelRecord struct {
	WorkspaceID string
	ID          string
	Name        string
	Config      map[string]string
	SecretRef   *string
	Revision    int
	Now         time.Time
}

type createDeliveryRecord struct {
	ID              string
	WorkspaceID     string
	OutboxID        string
	NotificationID  *string
	RecipientUserID *string
	ChannelID       *string
	ChannelKind     string
	IdempotencyKey  string
	MaxAttempts     int
	Now             time.Time
}
