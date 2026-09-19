package notification

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/secretstore"
)

func TestExternalChannelsDispatchAndDedupeWebhookDeliveries(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	setup, err := identityService.Setup(ctx, identity.SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		OwnerEmail:    "owner@example.com",
		Password:      "local-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	if _, err := identityService.CreateAccount(ctx, identity.CreateAccountInput{
		WorkspaceID: setup.Session.Workspace.ID,
		Email:       "admin@example.com",
		DisplayName: "Admin",
		Password:    "local-password-456",
		Locale:      "zh-CN",
		Role:        "admin",
	}); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	secrets, err := secretstore.NewEncryptedSQLiteStore(
		db,
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("create secret store: %v", err)
	}
	sender := &capturingSender{}
	service := NewServiceWithDependencies(
		NewSQLiteRepository(db),
		secrets,
		sender,
	)

	email, err := service.CreateChannel(ctx, CreateChannelInput{
		WorkspaceID:     setup.Session.Workspace.ID,
		Kind:            "email",
		Name:            "SMTP",
		SMTPHost:        "smtp.example.com",
		SMTPPort:        587,
		SMTPSecurity:    "starttls",
		SMTPFromAddress: "review@example.com",
		SMTPFromName:    "Review Studio",
		SMTPUsername:    "smtp-user",
		SMTPPassword:    "smtp-password",
		TestRecipient:   "owner@example.com",
	})
	if err != nil {
		t.Fatalf("create email channel: %v", err)
	}
	if email.Status != "disabled" || email.Config["smtpAuthConfigured"] != "true" {
		t.Fatalf("unexpected new email channel: %#v", email)
	}
	var encryptedSecret []byte
	if err := db.QueryRowContext(
		ctx,
		"SELECT ciphertext FROM secret_values LIMIT 1",
	).Scan(&encryptedSecret); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	if strings.Contains(string(encryptedSecret), "smtp-password") {
		t.Fatalf("SMTP password was stored in plaintext")
	}
	if _, err := service.TestChannel(ctx, setup.Session.Workspace.ID, email.ID); err != nil {
		t.Fatalf("test email channel: %v", err)
	}

	feishu, err := service.CreateChannel(ctx, CreateChannelInput{
		WorkspaceID:         setup.Session.Workspace.ID,
		Kind:                "feishu",
		Name:                "Feishu",
		WebhookURL:          "http://127.0.0.1/open-apis/bot/v2/hook/token?key=secret",
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("create feishu channel: %v", err)
	}
	if _, err := service.TestChannel(ctx, setup.Session.Workspace.ID, feishu.ID); err != nil {
		t.Fatalf("test feishu channel: %v", err)
	}

	if err := service.CreateForWorkspaceManagers(ctx, CreateForWorkspaceInput{
		WorkspaceID:  setup.Session.Workspace.ID,
		Type:         "review.thread_created",
		ResourceType: "review_session",
		ResourceID:   "review-1",
		Title:        "New feedback",
		Body:         "A reviewer left a comment.",
	}); err != nil {
		t.Fatalf("create manager notifications: %v", err)
	}

	for i := 0; i < 2; i++ {
		message, claimed, err := service.ClaimDelivery(ctx, 0)
		if err != nil || !claimed {
			t.Fatalf("claim outbox %d: claimed=%t err=%v", i, claimed, err)
		}
		if err := service.DispatchMessage(ctx, message); err != nil {
			t.Fatalf("dispatch outbox %d: %v", i, err)
		}
		if err := service.SucceedDelivery(ctx, message.ID); err != nil {
			t.Fatalf("complete outbox %d: %v", i, err)
		}
	}
	if _, claimed, err := service.ClaimDelivery(ctx, 0); err != nil || claimed {
		t.Fatalf("unexpected remaining outbox: claimed=%t err=%v", claimed, err)
	}

	deliveries, err := service.ListDeliveries(ctx, setup.Session.Workspace.ID, 20)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("expected two email deliveries and one deduped webhook, got %#v", deliveries)
	}
	for _, delivery := range deliveries {
		if delivery.Status != "succeeded" || delivery.Attempts != 1 {
			t.Fatalf("unexpected delivery: %#v", delivery)
		}
	}
	sent := sender.items()
	if len(sent) != 5 {
		t.Fatalf("expected 2 test sends and 3 notification sends, got %#v", sent)
	}
	var webhookNotifications int
	for _, item := range sent {
		if item.kind == "feishu" && item.eventType == "review.thread_created" {
			webhookNotifications++
		}
	}
	if webhookNotifications != 1 {
		t.Fatalf("expected one deduped webhook notification, got %d", webhookNotifications)
	}
}

func TestSMTPChannelBindsCredentialsToEndpointAndRejectsPlaintextAuth(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	setup, err := identityService.Setup(ctx, identity.SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		OwnerEmail:    "owner@example.com",
		Password:      "local-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	secrets, err := secretstore.NewEncryptedSQLiteStore(
		db,
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("create secret store: %v", err)
	}
	service := NewServiceWithDependencies(
		NewSQLiteRepository(db),
		secrets,
		&capturingSender{},
	)

	email, err := service.CreateChannel(ctx, CreateChannelInput{
		WorkspaceID:     setup.Session.Workspace.ID,
		Kind:            "email",
		Name:            "SMTP",
		SMTPHost:        "smtp-a.example.com",
		SMTPPort:        587,
		SMTPSecurity:    "starttls",
		SMTPFromAddress: "review@example.com",
		SMTPUsername:    "smtp-user",
		SMTPPassword:    "smtp-password",
	})
	if err != nil {
		t.Fatalf("create email channel: %v", err)
	}

	if _, err = service.UpdateChannel(ctx, UpdateChannelInput{
		WorkspaceID:     setup.Session.Workspace.ID,
		ID:              email.ID,
		Name:            "SMTP",
		SMTPHost:        "smtp-b.example.com",
		SMTPPort:        587,
		SMTPSecurity:    "starttls",
		SMTPFromAddress: "review@example.com",
		Revision:        email.Revision,
	}); !errors.Is(err, ErrSMTPEndpointChanged) {
		t.Fatalf("expected SMTP endpoint-change rejection, got %v", err)
	}

	if _, err = service.CreateChannel(ctx, CreateChannelInput{
		WorkspaceID:     setup.Session.Workspace.ID,
		Kind:            "email",
		Name:            "Plain SMTP",
		SMTPHost:        "smtp-a.example.com",
		SMTPPort:        25,
		SMTPSecurity:    "plain",
		SMTPFromAddress: "review@example.com",
		SMTPUsername:    "smtp-user",
		SMTPPassword:    "smtp-password",
	}); !errors.Is(err, ErrSMTPPlaintextAuth) {
		t.Fatalf("expected plaintext-auth rejection, got %v", err)
	}
}

func TestFailedExternalDeliveryCanBeRetried(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	setup, err := identityService.Setup(ctx, identity.SetupInput{
		WorkspaceName: "Studio",
		OwnerName:     "Owner",
		OwnerEmail:    "owner@example.com",
		Password:      "local-password-123",
		Locale:        "zh-CN",
		Timezone:      "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	secrets, err := secretstore.NewEncryptedSQLiteStore(
		db,
		[]byte("abcdef0123456789abcdef0123456789"),
	)
	if err != nil {
		t.Fatalf("create secret store: %v", err)
	}
	sender := &toggleSender{}
	service := NewServiceWithDependencies(
		NewSQLiteRepository(db),
		secrets,
		sender,
	)
	channel, err := service.CreateChannel(ctx, CreateChannelInput{
		WorkspaceID:     setup.Session.Workspace.ID,
		Kind:            "email",
		Name:            "SMTP",
		SMTPHost:        "smtp.example.com",
		SMTPPort:        587,
		SMTPSecurity:    "starttls",
		SMTPFromAddress: "review@example.com",
		TestRecipient:   "owner@example.com",
	})
	if err != nil {
		t.Fatalf("create email channel: %v", err)
	}
	if _, err := service.TestChannel(ctx, setup.Session.Workspace.ID, channel.ID); err != nil {
		t.Fatalf("test email channel: %v", err)
	}
	sender.setFail(true)
	if err := service.CreateForWorkspaceManagers(ctx, CreateForWorkspaceInput{
		WorkspaceID:  setup.Session.Workspace.ID,
		Type:         "review.decision_submitted",
		ResourceType: "review_session",
		ResourceID:   "review-1",
		Title:        "Decision submitted",
		Body:         "A reviewer submitted a decision.",
	}); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	message, claimed, err := service.ClaimDelivery(ctx, 0)
	if err != nil || !claimed {
		t.Fatalf("claim outbox: claimed=%t err=%v", claimed, err)
	}
	if err := service.DispatchMessage(ctx, message); err == nil {
		t.Fatalf("expected delivery failure")
	}
	if err := service.FailDelivery(ctx, message.ID, DeliveryError{
		Code:    "notification.smtp_failed",
		Message: "temporary SMTP outage",
	}); err != nil {
		t.Fatalf("fail outbox: %v", err)
	}
	deliveries, err := service.ListDeliveries(ctx, setup.Session.Workspace.ID, 20)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "failed" {
		t.Fatalf("expected failed delivery, got %#v", deliveries)
	}

	if _, err := service.RetryDelivery(
		ctx,
		setup.Session.Workspace.ID,
		deliveries[0].ID,
	); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}
	sender.setFail(false)
	retry, claimed, err := service.ClaimDelivery(ctx, 0)
	if err != nil || !claimed {
		t.Fatalf("claim retried outbox: claimed=%t err=%v", claimed, err)
	}
	if err := service.DispatchMessage(ctx, retry); err != nil {
		t.Fatalf("dispatch retry: %v", err)
	}
	if err := service.SucceedDelivery(ctx, retry.ID); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
	deliveries, err = service.ListDeliveries(ctx, setup.Session.Workspace.ID, 20)
	if err != nil {
		t.Fatalf("list retried deliveries: %v", err)
	}
	if len(deliveries) != 1 ||
		deliveries[0].Status != "succeeded" ||
		deliveries[0].Attempts != 2 {
		t.Fatalf("unexpected retried delivery: %#v", deliveries)
	}
}

type capturedSend struct {
	kind      string
	eventType string
	title     string
}

type capturingSender struct {
	mu    sync.Mutex
	sends []capturedSend
}

func (sender *capturingSender) Send(
	_ context.Context,
	target DeliveryTarget,
	payload DeliveryPayload,
) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.sends = append(sender.sends, capturedSend{
		kind:      target.Channel.Kind,
		eventType: payload.EventType,
		title:     payload.Title,
	})
	return nil
}

func (sender *capturingSender) items() []capturedSend {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	result := make([]capturedSend, len(sender.sends))
	copy(result, sender.sends)
	return result
}

type toggleSender struct {
	mu   sync.Mutex
	fail bool
}

func (sender *toggleSender) setFail(value bool) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.fail = value
}

func (sender *toggleSender) Send(
	context.Context,
	DeliveryTarget,
	DeliveryPayload,
) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.fail {
		return DeliveryError{
			Code:    "notification.smtp_failed",
			Message: "temporary SMTP outage",
		}
	}
	return nil
}
