package notification

import (
	"encoding/json"
	"time"
)

type Notification struct {
	ID              string
	WorkspaceID     string
	RecipientUserID string
	Type            string
	ResourceType    string
	ResourceID      string
	Title           string
	Body            string
	ReadAt          *time.Time
	CreatedAt       time.Time
}

type Channel struct {
	ID               string
	WorkspaceID      string
	Kind             string
	Name             string
	Status           string
	Config           map[string]string
	Revision         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastTestAt       *time.Time
	LastTestStatus   *string
	LastErrorCode    *string
	LastErrorMessage *string
}

type CreateChannelInput struct {
	WorkspaceID         string
	Kind                string
	Name                string
	SMTPHost            string
	SMTPPort            int
	SMTPSecurity        string
	SMTPFromAddress     string
	SMTPFromName        string
	SMTPUsername        string
	SMTPPassword        string
	TestRecipient       string
	WebhookURL          string
	AllowPrivateNetwork bool
}

type UpdateChannelInput struct {
	WorkspaceID         string
	ID                  string
	Name                string
	SMTPHost            string
	SMTPPort            int
	SMTPSecurity        string
	SMTPFromAddress     string
	SMTPFromName        string
	SMTPUsername        string
	SMTPPassword        string
	TestRecipient       string
	WebhookURL          string
	AllowPrivateNetwork bool
	Revision            int
}

type ChannelStateInput struct {
	WorkspaceID string
	ID          string
	Revision    int
}

type PreferenceInput struct {
	WorkspaceID       string
	UserID            string
	EmailEnabled      bool
	FeishuEnabled     bool
	WechatWorkEnabled bool
}

type Preferences struct {
	UserID            string
	EmailEnabled      bool
	FeishuEnabled     bool
	WechatWorkEnabled bool
	UpdatedAt         time.Time
}

type Delivery struct {
	ID              string
	WorkspaceID     string
	OutboxID        string
	NotificationID  *string
	RecipientUserID *string
	ChannelID       *string
	ChannelKind     string
	ChannelName     string
	IdempotencyKey  string
	Status          string
	Attempts        int
	MaxAttempts     int
	LastErrorCode   *string
	LastError       *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeliveredAt     *time.Time
}

type DeliveryTarget struct {
	Channel         Channel
	SecretRef       string
	Secret          json.RawMessage
	RecipientUserID string
	RecipientEmail  *string
	RecipientName   string
	IdempotencyKey  string
}

type DeliveryPayload struct {
	EventType    string
	ResourceType string
	ResourceID   string
	Title        string
	Body         string
}

type ChannelTestReport struct {
	ChannelID string
	Status    string
	Latency   time.Duration
	TestedAt  time.Time
}

type CreateForWorkspaceInput struct {
	WorkspaceID   string
	ExcludeUserID string
	Type          string
	ResourceType  string
	ResourceID    string
	Title         string
	Body          string
}

type CreateForUsersInput struct {
	WorkspaceID      string
	RecipientUserIDs []string
	Type             string
	ResourceType     string
	ResourceID       string
	Title            string
	Body             string
}

type OutboxMessage struct {
	ID             string
	WorkspaceID    string
	NotificationID *string
	EventType      string
	Payload        json.RawMessage
	IdempotencyKey string
	Status         string
	Attempts       int
	MaxAttempts    int
	AvailableAt    time.Time
	LockedAt       *time.Time
	LastErrorCode  *string
	LastError      *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeliveredAt    *time.Time
}

type DeliveryError struct {
	Code    string
	Message string
}
