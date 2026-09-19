package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestListLogsFiltersActorActionAndResource(t *testing.T) {
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
			Password:      "owner-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}

	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)
	}
	records := []RecordLogInput{
		{
			WorkspaceID:  setup.Session.Workspace.ID,
			ActorType:    "user",
			ActorID:      setup.Session.User.ID,
			Action:       "project.created",
			ResourceType: "project",
			ResourceID:   "project-1",
		},
		{
			WorkspaceID:  setup.Session.Workspace.ID,
			ActorType:    "visitor",
			ActorID:      "visitor-1",
			Action:       "review.comment_added",
			ResourceType: "comment",
			ResourceID:   "comment-1",
		},
	}
	for _, record := range records {
		if _, err := service.RecordLog(ctx, record); err != nil {
			t.Fatalf("record audit log: %v", err)
		}
	}

	tests := []struct {
		name   string
		filter LogListFilter
		action string
	}{
		{
			name:   "actor",
			filter: LogListFilter{ActorID: setup.Session.User.ID},
			action: "project.created",
		},
		{
			name:   "action",
			filter: LogListFilter{Action: "review.comment_added"},
			action: "review.comment_added",
		},
		{
			name: "resource",
			filter: LogListFilter{
				ResourceType: "project",
				ResourceID:   "project-1",
			},
			action: "project.created",
		},
		{
			name: "combined",
			filter: LogListFilter{
				ActorID:      setup.Session.User.ID,
				Action:       "project.created",
				ResourceType: "project",
			},
			action: "project.created",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, err := service.ListLogs(
				ctx,
				setup.Session.Workspace.ID,
				test.filter,
			)
			if err != nil {
				t.Fatalf("list audit logs: %v", err)
			}
			if len(items) != 1 || items[0].Action != test.action {
				t.Fatalf("unexpected filtered logs: %#v", items)
			}
		})
	}
}

func TestListLogsReturnsSafeUploadCheckDetails(t *testing.T) {
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
			Password:      "owner-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}

	service := NewService(NewSQLiteRepository(db))
	if _, err := service.RecordLog(ctx, RecordLogInput{
		WorkspaceID:  setup.Session.Workspace.ID,
		ActorType:    "user",
		ActorID:      setup.Session.User.ID,
		Action:       "upload_check.quarantined",
		ResourceType: "upload_check",
		ResourceID:   "check-1",
		After: map[string]any{
			"assetId":              "asset-1",
			"assetVersionId":       "version-1",
			"storageObjectId":      "object-1",
			"projectId":            "project-1",
			"uploadSecurityPolicy": "enhanced",
			"status":               "quarantined",
			"resultCode":           "malware_scan_unavailable",
			"secretValue":          "must-not-leak",
		},
	}); err != nil {
		t.Fatalf("record upload check audit log: %v", err)
	}
	if _, err := service.RecordLog(ctx, RecordLogInput{
		WorkspaceID:  setup.Session.Workspace.ID,
		ActorType:    "user",
		ActorID:      setup.Session.User.ID,
		Action:       "project.created",
		ResourceType: "project",
		ResourceID:   "project-1",
		After: map[string]any{
			"uploadSecurityPolicy": "enhanced",
			"status":               "quarantined",
		},
	}); err != nil {
		t.Fatalf("record project audit log: %v", err)
	}

	items, err := service.ListLogs(ctx, setup.Session.Workspace.ID, LogListFilter{
		ResourceType: "upload_check",
	})
	if err != nil {
		t.Fatalf("list upload check logs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("upload check log count = %d, want 1", len(items))
	}
	details := items[0].Details
	if details["uploadSecurityPolicy"] != "enhanced" ||
		details["status"] != "quarantined" ||
		details["resultCode"] != "malware_scan_unavailable" ||
		details["assetVersionId"] != "version-1" {
		t.Fatalf("unexpected upload check details: %#v", details)
	}
	if _, ok := details["secretValue"]; ok {
		t.Fatalf("unexpected secret detail leaked: %#v", details)
	}

	projectItems, err := service.ListLogs(ctx, setup.Session.Workspace.ID, LogListFilter{
		ResourceType: "project",
	})
	if err != nil {
		t.Fatalf("list project logs: %v", err)
	}
	if len(projectItems) != 1 || len(projectItems[0].Details) != 0 {
		t.Fatalf("unexpected project details: %#v", projectItems)
	}
}
