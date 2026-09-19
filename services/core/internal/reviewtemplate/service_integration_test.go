package reviewtemplate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"review-studio.local/core/internal/platform/database"
)

func TestTemplateLifecycleAndRevisionConflicts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	const now = "2026-06-11T01:00:00Z"
	for _, statement := range []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', '` + now + `', '` + now + `')`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Studio', 'active', 'zh-CN', 'Asia/Shanghai',
			'{}', '` + now + `', '` + now + `'
		)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	service := NewService(NewSQLiteRepository(db))
	dueDays := 3
	created, err := service.Create(ctx, CreateInput{
		WorkspaceID:      "workspace-1",
		UserID:           "user-1",
		Name:             "Client approval",
		ParticipantRoles: []string{"reviewer", "observer"},
		AllowDownload:    true,
		DueDays:          &dueDays,
		DecisionRule:     "any_reviewer",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if created.Revision != 1 || created.DueDays == nil ||
		*created.DueDays != 3 || !created.AllowDownload {
		t.Fatalf("unexpected created template: %#v", created)
	}

	if _, err := service.Create(ctx, CreateInput{
		WorkspaceID:      "workspace-1",
		UserID:           "user-1",
		Name:             "client APPROVAL",
		ParticipantRoles: []string{"reviewer"},
		DecisionRule:     "any_reviewer",
	}); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("expected name conflict, got %v", err)
	}

	updated, err := service.Update(ctx, UpdateInput{
		WorkspaceID:      "workspace-1",
		ID:               created.ID,
		Name:             "Client final approval",
		ParticipantRoles: []string{"reviewer", "reviewer"},
		AllowDownload:    false,
		DecisionRule:     "all_reviewers",
		Revision:         created.Revision,
	})
	if err != nil || updated.Revision != 2 ||
		updated.DecisionRule != "all_reviewers" {
		t.Fatalf("update template: %#v err=%v", updated, err)
	}
	if _, err := service.Update(ctx, UpdateInput{
		WorkspaceID:      "workspace-1",
		ID:               created.ID,
		Name:             "Stale",
		ParticipantRoles: []string{"reviewer"},
		DecisionRule:     "any_reviewer",
		Revision:         created.Revision,
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	if err := service.Delete(ctx, DeleteInput{
		WorkspaceID: "workspace-1",
		ID:          updated.ID,
		Revision:    updated.Revision,
	}); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	if _, err := service.Get(ctx, "workspace-1", updated.ID); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("expected deleted template to be hidden, got %v", err)
	}
}
