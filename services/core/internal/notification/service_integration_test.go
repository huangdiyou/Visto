package notification

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestNotificationOutboxRetriesWithoutBlockingNotification(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	setup, err := identity.NewService(identity.NewSQLiteRepository(db)).Setup(
		ctx,
		identity.SetupInput{
			WorkspaceName: "Studio",
			OwnerName:     "Owner",
			Password:      "local-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }
	if err := service.CreateForWorkspaceManagers(ctx, CreateForWorkspaceInput{
		WorkspaceID:  setup.Session.Workspace.ID,
		Type:         "review.thread_created",
		ResourceType: "review_session",
		ResourceID:   "review-1",
		Title:        "收到新评论",
		Body:         "访客提交了一条评论",
	}); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	items, unread, err := service.List(
		ctx,
		setup.Session.Workspace.ID,
		setup.Session.User.ID,
		false,
		10,
	)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(items) != 1 || unread != 1 {
		t.Fatalf("notification was not committed: len=%d unread=%d", len(items), unread)
	}
	var outboxCount int
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_outbox",
	).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox count = %d, want 1", outboxCount)
	}
	message, claimed, err := service.ClaimDelivery(ctx, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim delivery: claimed=%t err=%v", claimed, err)
	}
	if message.Attempts != 1 || message.Status != "processing" {
		t.Fatalf("unexpected claimed message: %#v", message)
	}
	if err := service.FailDelivery(ctx, message.ID, DeliveryError{
		Code:    "channel.offline",
		Message: "temporary outage",
	}); err != nil {
		t.Fatalf("fail delivery: %v", err)
	}
	if _, claimed, err := service.ClaimDelivery(ctx, time.Minute); err != nil || claimed {
		t.Fatalf("message retried before availability: claimed=%t err=%v", claimed, err)
	}
	now = now.Add(3 * time.Second)
	retry, claimed, err := service.ClaimDelivery(ctx, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim retry: claimed=%t err=%v", claimed, err)
	}
	if retry.Attempts != 2 {
		t.Fatalf("retry attempts = %d, want 2", retry.Attempts)
	}
	if err := service.SucceedDelivery(ctx, retry.ID); err != nil {
		t.Fatalf("complete delivery: %v", err)
	}
	if _, claimed, err := service.ClaimDelivery(ctx, time.Minute); err != nil || claimed {
		t.Fatalf("delivered message was reclaimed: claimed=%t err=%v", claimed, err)
	}
}
