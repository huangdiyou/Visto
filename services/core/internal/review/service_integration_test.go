package review

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/reviewtemplate"
)

func stringPointer(value string) *string {
	return &value
}

func TestReviewSessionLifecyclePinsAssetVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := "2026-06-11T01:00:00Z"
	fixtures := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO users (
				id, display_name, status, locale, created_at, updated_at
			) VALUES (?, ?, 'active', 'zh-CN', ?, ?)`,
			args: []any{"user-1", "Owner", now, now},
		},
		{
			query: `INSERT INTO users (
				id, display_name, status, locale, created_at, updated_at
			) VALUES (?, ?, 'active', 'zh-CN', ?, ?)`,
			args: []any{"user-2", "Reviewer", now, now},
		},
		{
			query: `INSERT INTO workspaces (
				id, name, status, default_locale, timezone, settings_json,
				created_at, updated_at
			) VALUES (?, ?, 'active', 'zh-CN', 'Asia/Shanghai', '{}', ?, ?)`,
			args: []any{"workspace-1", "Studio", now, now},
		},
		{
			query: `INSERT INTO memberships (
				id, workspace_id, user_id, role_key, status, created_at, updated_at
			) VALUES (?, ?, ?, 'owner', 'active', ?, ?)`,
			args: []any{"membership-1", "workspace-1", "user-1", now, now},
		},
		{
			query: `INSERT INTO memberships (
				id, workspace_id, user_id, role_key, status, created_at, updated_at
			) VALUES (?, ?, ?, 'member', 'active', ?, ?)`,
			args: []any{"membership-2", "workspace-1", "user-2", now, now},
		},
		{
			query: `INSERT INTO projects (
				id, workspace_id, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'active', ?, 1, ?, ?)`,
			args: []any{
				"project-1", "workspace-1", "Documentary", "user-1", now, now,
			},
		},
		{
			query: `INSERT INTO projects (
				id, workspace_id, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'active', ?, 1, ?, ?)`,
			args: []any{
				"project-2", "workspace-1", "Other", "user-1", now, now,
			},
		},
		{
			query: `INSERT INTO project_memberships (
				id, workspace_id, project_id, user_id, role_key,
				permissions_json, status, created_by, joined_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'member', '{}', 'active', ?, ?, ?, ?)`,
			args: []any{
				"project-member-1", "workspace-1", "project-1", "user-2",
				"user-1", now, now, now,
			},
		},
		{
			query: `INSERT INTO storage_providers (
				id, workspace_id, kind, name, status, config_json,
				capabilities_json, created_at, updated_at
			) VALUES (?, ?, 'local', ?, 'active', '{}', '{}', ?, ?)`,
			args: []any{"provider-1", "workspace-1", "Local", now, now},
		},
		{
			query: `INSERT INTO local_path_secrets (
				id, workspace_id, path_text, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?)`,
			args: []any{
				"secret-1", "workspace-1", `C:\review-test`, now, now,
			},
		},
		{
			query: `INSERT INTO authorized_roots (
				id, workspace_id, storage_provider_id, display_name,
				display_path, path_secret_ref, mode, scan_enabled, status,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'referenced', 1, 'available', 1, ?, ?)`,
			args: []any{
				"root-1", "workspace-1", "provider-1", "Media", "~/Media",
				"secret-1", now, now,
			},
		},
		{
			query: `INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id,
				object_key, kind, status, size_bytes, modified_at,
				quick_fingerprint, mime_type, first_discovered_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 'source', 'available', 10, ?, ?, ?, ?, ?, ?)`,
			args: []any{
				"object-1", "workspace-1", "provider-1", "root-1", "cut-v1.mp4",
				now, "fingerprint-1", "video/mp4", now, now, now,
			},
		},
		{
			query: `INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id,
				object_key, kind, status, size_bytes, modified_at,
				quick_fingerprint, mime_type, first_discovered_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 'source', 'available', 10, ?, ?, ?, ?, ?, ?)`,
			args: []any{
				"object-image", "workspace-1", "provider-1", "root-1",
				"frame.png", now, "fingerprint-image", "image/png",
				now, now, now,
			},
		},
		{
			query: `INSERT INTO assets (
				id, workspace_id, project_id, type, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'video', ?, 'active', ?, 1, ?, ?)`,
			args: []any{
				"asset-1", "workspace-1", "project-1", "Cut", "user-1", now, now,
			},
		},
		{
			query: `INSERT INTO assets (
				id, workspace_id, project_id, type, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'video', ?, 'active', ?, 1, ?, ?)`,
			args: []any{
				"asset-2", "workspace-1", "project-2", "Other cut",
				"user-1", now, now,
			},
		},
		{
			query: `INSERT INTO assets (
				id, workspace_id, project_id, type, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'image', ?, 'active', ?, 1, ?, ?)`,
			args: []any{
				"asset-image", "workspace-1", "project-1", "Key frame",
				"user-1", now, now,
			},
		},
		{
			query: `INSERT INTO project_assets (
				id, workspace_id, project_id, asset_id, status, added_by,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
			args: []any{
				"project-asset-1", "workspace-1", "project-1", "asset-1",
				"user-1", now, now,
			},
		},
		{
			query: `INSERT INTO project_assets (
				id, workspace_id, project_id, asset_id, status, added_by,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
			args: []any{
				"project-asset-2", "workspace-1", "project-2", "asset-2",
				"user-1", now, now,
			},
		},
		{
			query: `INSERT INTO project_assets (
				id, workspace_id, project_id, asset_id, status, added_by,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
			args: []any{
				"project-asset-image", "workspace-1", "project-1", "asset-image",
				"user-1", now, now,
			},
		},
		{
			query: `INSERT INTO asset_versions (
				id, workspace_id, asset_id, version_number, processing_status,
				source_filename, source_mime, source_size_bytes,
				source_fingerprint, created_by, created_at
			) VALUES (?, ?, ?, ?, 'ready', ?, 'video/mp4', 10, ?, ?, ?)`,
			args: []any{
				"version-1", "workspace-1", "asset-1", 1, "cut-v1.mp4",
				"fingerprint-1", "user-1", now,
			},
		},
		{
			query: `INSERT INTO asset_versions (
				id, workspace_id, asset_id, version_number, processing_status,
				source_filename, source_mime, source_size_bytes,
				source_fingerprint, created_by, created_at
			) VALUES (?, ?, ?, ?, 'ready', ?, 'video/mp4', 10, ?, ?, ?)`,
			args: []any{
				"version-2", "workspace-1", "asset-1", 2, "cut-v2.mp4",
				"fingerprint-2", "user-1", now,
			},
		},
		{
			query: `INSERT INTO asset_versions (
				id, workspace_id, asset_id, version_number, processing_status,
				source_filename, source_mime, source_size_bytes,
				source_fingerprint, created_by, created_at
			) VALUES (?, ?, ?, ?, 'ready', ?, 'video/mp4', 10, ?, ?, ?)`,
			args: []any{
				"other-version", "workspace-1", "asset-2", 1, "other.mp4",
				"fingerprint-other", "user-1", now,
			},
		},
		{
			query: `INSERT INTO asset_versions (
				id, workspace_id, asset_id, version_number, processing_status,
				source_filename, source_mime, source_size_bytes,
				source_fingerprint, created_by, created_at
			) VALUES (?, ?, ?, ?, 'ready', ?, 'image/png', 10, ?, ?, ?)`,
			args: []any{
				"version-image", "workspace-1", "asset-image", 1, "frame.png",
				"fingerprint-image", "user-1", now,
			},
		},
		{
			query: `INSERT INTO version_files (
				id, workspace_id, asset_version_id, storage_object_id,
				role, created_at
			) VALUES (?, ?, ?, ?, 'primary', ?)`,
			args: []any{
				"version-file-1", "workspace-1", "version-1", "object-1", now,
			},
		},
		{
			query: `INSERT INTO version_files (
				id, workspace_id, asset_version_id, storage_object_id,
				role, created_at
			) VALUES (?, ?, ?, ?, 'primary', ?)`,
			args: []any{
				"version-file-image", "workspace-1", "version-image",
				"object-image", now,
			},
		},
		{
			query: `INSERT INTO media_probes (
				storage_object_id, workspace_id, status, source_fingerprint,
				media_type, format_name, duration_us, width, height,
				video_codec, probed_at, updated_at
			) VALUES (?, ?, 'succeeded', ?, 'video', 'mp4', ?, 1920, 1080,
				'h264', ?, ?)`,
			args: []any{
				"object-1", "workspace-1", "fingerprint-1",
				int64(60_000_000), now, now,
			},
		},
		{
			query: `INSERT INTO media_probes (
				storage_object_id, workspace_id, status, source_fingerprint,
				media_type, format_name, width, height, probed_at, updated_at
			) VALUES (?, ?, 'succeeded', ?, 'image', 'png', 1600, 900, ?, ?)`,
			args: []any{
				"object-image", "workspace-1", "fingerprint-image", now, now,
			},
		},
	}
	for _, fixture := range fixtures {
		if _, err := db.ExecContext(ctx, fixture.query, fixture.args...); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, 6, 11, 2, 0, 0, 0, time.UTC)
	}
	dueAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	created, err := service.Create(ctx, CreateSessionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   "project-1",
		Name:        "First review",
		DueAt:       &dueAt,
		Participants: []ParticipantInput{
			{DisplayName: "Client", Role: "reviewer"},
			{DisplayName: "Producer", Role: "observer"},
		},
		Items: []ItemInput{
			{AssetID: "asset-1", AssetVersionID: "version-1"},
		},
	})
	if err != nil {
		t.Fatalf("create review session: %v", err)
	}
	if created.Status != "draft" ||
		created.Revision != 1 ||
		len(created.Items) != 1 ||
		created.Items[0].AssetVersionID != "version-1" ||
		created.Items[0].VersionNumber != 1 ||
		len(created.Participants) != 2 {
		t.Fatalf("unexpected created review: %#v", created)
	}
	otherProjectReview, err := service.Create(ctx, CreateSessionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   "project-2",
		Name:        "Other project review",
		Items: []ItemInput{
			{AssetID: "asset-2", AssetVersionID: "other-version"},
		},
	})
	if err != nil {
		t.Fatalf("create other project review session: %v", err)
	}
	memberPage, err := service.List(ctx, "workspace-1", ListFilter{
		UserID: "user-2", Limit: 100,
	})
	if err != nil || len(memberPage.Items) != 1 || memberPage.Items[0].ID != created.ID {
		t.Fatalf("member review isolation: %#v err=%v", memberPage, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE project_memberships
		SET permissions_json = '{"project.read":false}'
		WHERE id = 'project-member-1'
	`); err != nil {
		t.Fatalf("deny member project read: %v", err)
	}
	deniedPage, err := service.List(ctx, "workspace-1", ListFilter{
		UserID: "user-2", Limit: 100,
	})
	if err != nil || len(deniedPage.Items) != 0 {
		t.Fatalf("explicit project read denial: %#v err=%v", deniedPage, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE project_memberships
		SET permissions_json = '{}'
		WHERE id = 'project-member-1'
	`); err != nil {
		t.Fatalf("restore member project read: %v", err)
	}
	firstPage, err := service.List(ctx, "workspace-1", ListFilter{
		UserID: "user-1", WorkspaceOwner: true, Limit: 1,
	})
	if err != nil || len(firstPage.Items) != 1 || firstPage.NextOffset == nil {
		t.Fatalf("first review page: %#v err=%v", firstPage, err)
	}
	secondPage, err := service.List(ctx, "workspace-1", ListFilter{
		UserID: "user-1", WorkspaceOwner: true, Limit: 1, Offset: *firstPage.NextOffset,
	})
	if err != nil || len(secondPage.Items) != 1 || secondPage.NextOffset != nil ||
		secondPage.Items[0].ID == firstPage.Items[0].ID {
		t.Fatalf("second review page: %#v err=%v", secondPage, err)
	}
	byProject, err := service.List(
		ctx,
		"workspace-1",
		ListFilter{
			ProjectID: "project-1", UserID: "user-1", WorkspaceOwner: true,
		},
	)
	if err != nil || len(byProject.Items) != 1 || byProject.Items[0].ID != created.ID {
		t.Fatalf("list review by project: %#v err=%v", byProject, err)
	}
	byOtherProject, err := service.List(
		ctx,
		"workspace-1",
		ListFilter{
			ProjectID: "project-2", UserID: "user-1", WorkspaceOwner: true,
		},
	)
	if err != nil ||
		len(byOtherProject.Items) != 1 ||
		byOtherProject.Items[0].ID != otherProjectReview.ID {
		t.Fatalf(
			"list review by other project: %#v err=%v",
			byOtherProject,
			err,
		)
	}

	byVersion, err := service.List(
		ctx,
		"workspace-1",
		ListFilter{
			AssetVersionID: "version-1", UserID: "user-1", WorkspaceOwner: true,
		},
	)
	if err != nil || len(byVersion.Items) != 1 || byVersion.Items[0].ID != created.ID {
		t.Fatalf("list review by pinned version: %#v err=%v", byVersion, err)
	}
	otherVersion, err := service.List(
		ctx,
		"workspace-1",
		ListFilter{
			AssetVersionID: "version-2", UserID: "user-1", WorkspaceOwner: true,
		},
	)
	if err != nil || len(otherVersion.Items) != 0 {
		t.Fatalf("new asset version replaced pinned review: %#v err=%v", otherVersion, err)
	}

	nextDueAt := dueAt.Add(24 * time.Hour)
	updated, err := service.Update(ctx, UpdateSessionInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Name:        "Client review",
		DueAt:       &nextDueAt,
		Participants: []ParticipantInput{
			{DisplayName: "Client", Role: "reviewer"},
		},
		Revision: created.Revision,
	})
	if err != nil {
		t.Fatalf("update review session: %v", err)
	}
	if updated.Revision != 2 ||
		updated.Name != "Client review" ||
		updated.DueAt == nil ||
		!updated.DueAt.Equal(nextDueAt) ||
		len(updated.Participants) != 1 {
		t.Fatalf("unexpected updated review: %#v", updated)
	}
	_, err = service.Update(ctx, UpdateSessionInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Name:        "Stale",
		Revision:    created.Revision,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	opened, err := service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Revision:    updated.Revision,
	})
	if err != nil || opened.Status != "open" || opened.Revision != 3 {
		t.Fatalf("open review: %#v err=%v", opened, err)
	}
	attachmentDraft, err := service.Create(ctx, CreateSessionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   "project-1",
		Name:        "Public attachment quota",
		Items: []ItemInput{{
			AssetID:        "asset-1",
			AssetVersionID: "version-1",
		}},
	})
	if err != nil {
		t.Fatalf("create attachment quota review: %v", err)
	}
	attachmentReview, err := service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          attachmentDraft.ID,
		Revision:    attachmentDraft.Revision,
	})
	if err != nil {
		t.Fatalf("open attachment quota review: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO shares (
			id, workspace_id, review_session_id, name, status,
			allow_comment, allow_download, require_nickname, created_by,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, 'Public attachment quota', 'active', 1, 0, 0, ?, 1, ?, ?)
	`, "share-attachment-quota", "workspace-1", attachmentReview.ID,
		"user-1", now, now); err != nil {
		t.Fatalf("create attachment quota share: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO share_visitors (
			id, workspace_id, share_id, identity_method, created_at, last_seen_at
		) VALUES (?, ?, ?, 'anonymous', ?, ?)
	`, "visitor-attachment-quota", "workspace-1", "share-attachment-quota", now, now); err != nil {
		t.Fatalf("create attachment quota visitor: %v", err)
	}
	for group, attachmentCount := range []int{4, 4, 2} {
		attachmentIDs := make([]string, 0, attachmentCount)
		for index := 0; index < attachmentCount; index++ {
			attachment, err := service.CreatePendingAttachment(ctx, CreatePendingAttachmentInput{
				WorkspaceID:          "workspace-1",
				ReviewSessionID:      attachmentReview.ID,
				ReviewItemID:         attachmentReview.Items[0].ID,
				SourceType:           "share_visitor",
				ShareVisitorID:       "visitor-attachment-quota",
				AuthorizedRootID:     "root-1",
				ObjectKey:            fmt.Sprintf("quota/%d-%d.png", group, index),
				OriginalFilename:     "feedback.png",
				MIMEType:             "image/png",
				SizeBytes:            10 * 1024 * 1024,
				UploadSecurityPolicy: "standard",
				Status:               "ready",
			})
			if err != nil {
				t.Fatalf("create public attachment %d/%d: %v", group, index, err)
			}
			attachmentIDs = append(attachmentIDs, attachment.ID)
		}
		atUs := int64(group + 1)
		if _, err := service.CreateThread(ctx, CreateThreadInput{
			WorkspaceID:     "workspace-1",
			ReviewSessionID: attachmentReview.ID,
			ReviewItemID:    attachmentReview.Items[0].ID,
			AssetVersionID:  "version-1",
			AuthorKind:      "share_visitor",
			AuthorVisitorID: "visitor-attachment-quota",
			Body:            "Public screenshot feedback",
			Annotation: AnnotationInput{
				Kind:        "time_point",
				TimeStartUs: &atUs,
			},
			AttachmentIDs: attachmentIDs,
		}); err != nil {
			t.Fatalf("commit public attachment group %d: %v", group, err)
		}
	}
	if _, err := service.CreatePendingAttachment(ctx, CreatePendingAttachmentInput{
		WorkspaceID:          "workspace-1",
		ReviewSessionID:      attachmentReview.ID,
		ReviewItemID:         attachmentReview.Items[0].ID,
		SourceType:           "share_visitor",
		ShareVisitorID:       "visitor-attachment-quota",
		AuthorizedRootID:     "root-1",
		ObjectKey:            "quota/overflow.png",
		OriginalFilename:     "feedback.png",
		MIMEType:             "image/png",
		SizeBytes:            10 * 1024 * 1024,
		UploadSecurityPolicy: "standard",
		Status:               "ready",
	}); !errors.Is(err, ErrAttachmentQuotaExceeded) {
		t.Fatalf("expected committed public attachment quota rejection, got %v", err)
	}
	pointUs := int64(12_500_000)
	pointThread, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
		AssetVersionID:  "version-1",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Title appears too quickly",
		Annotation: AnnotationInput{
			Kind:        "time_point",
			TimeStartUs: &pointUs,
		},
	})
	if err != nil {
		t.Fatalf("create time point thread: %v", err)
	}
	if pointThread.Annotation.TimeStartUs == nil ||
		*pointThread.Annotation.TimeStartUs != pointUs ||
		len(pointThread.Comments) != 1 ||
		pointThread.Author.DisplayName != "Owner" {
		t.Fatalf("unexpected point thread: %#v", pointThread)
	}
	rangeStartUs := int64(20_000_000)
	rangeEndUs := int64(24_500_000)
	if _, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
		AssetVersionID:  "version-1",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Shorten this section",
		Annotation: AnnotationInput{
			Kind:        "time_range",
			TimeStartUs: &rangeStartUs,
			TimeEndUs:   &rangeEndUs,
		},
	}); err != nil {
		t.Fatalf("create time range thread: %v", err)
	}
	threads, err := service.ListThreads(ctx, ThreadListFilter{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
	})
	if err != nil || len(threads) != 2 ||
		threads[0].ID != pointThread.ID ||
		threads[1].Annotation.TimeEndUs == nil {
		t.Fatalf("unexpected time threads: %#v err=%v", threads, err)
	}
	replyThread, err := service.AddComment(ctx, ThreadCommentInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        pointThread.ID,
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "We can hold this title for two more seconds.",
	})
	if err != nil ||
		replyThread.Revision != pointThread.Revision+1 ||
		len(replyThread.Comments) != 2 ||
		replyThread.Comments[1].Author.DisplayName != "Owner" {
		t.Fatalf("unexpected replied thread: %#v err=%v", replyThread, err)
	}
	editedThread, err := service.UpdateComment(ctx, UpdateCommentInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        pointThread.ID,
		CommentID:       replyThread.Comments[1].ID,
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Hold this title for two more seconds.",
	})
	if err != nil ||
		len(editedThread.Comments) != 2 ||
		editedThread.Comments[1].EditedAt == nil ||
		editedThread.Comments[1].Body != "Hold this title for two more seconds." {
		t.Fatalf("unexpected edited thread: %#v err=%v", editedThread, err)
	}
	resolvedThread, err := service.ResolveThread(ctx, ThreadStateInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        pointThread.ID,
		UserID:          "user-1",
		Revision:        editedThread.Revision,
	})
	if err != nil ||
		resolvedThread.Status != "resolved" ||
		resolvedThread.ResolvedBy == nil ||
		resolvedThread.ResolvedBy.DisplayName != "Owner" ||
		resolvedThread.ResolvedAt == nil {
		t.Fatalf("unexpected resolved thread: %#v err=%v", resolvedThread, err)
	}
	if _, err := service.AddComment(ctx, ThreadCommentInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        pointThread.ID,
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Reply while resolved",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected resolved thread reply rejection, got %v", err)
	}
	reopenedThread, err := service.ReopenThread(ctx, ThreadStateInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        pointThread.ID,
		UserID:          "user-1",
		Revision:        resolvedThread.Revision,
	})
	if err != nil ||
		reopenedThread.Status != "open" ||
		reopenedThread.ResolvedAt != nil ||
		reopenedThread.ResolvedBy != nil {
		t.Fatalf("unexpected reopened thread: %#v err=%v", reopenedThread, err)
	}
	if err := service.DeleteComment(ctx, DeleteCommentInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        pointThread.ID,
		CommentID:       reopenedThread.Comments[1].ID,
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
	}); err != nil {
		t.Fatalf("delete reply: %v", err)
	}
	afterReplyDelete, err := service.ListThreads(ctx, ThreadListFilter{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
	})
	if err != nil ||
		len(afterReplyDelete) != 2 ||
		len(afterReplyDelete[0].Comments) != 1 {
		t.Fatalf("unexpected threads after reply deletion: %#v err=%v", afterReplyDelete, err)
	}
	deleteOnlyUs := int64(30_000_000)
	deleteOnlyThread, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
		AssetVersionID:  "version-1",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Delete this standalone note",
		Annotation: AnnotationInput{
			Kind:        "time_point",
			TimeStartUs: &deleteOnlyUs,
		},
	})
	if err != nil {
		t.Fatalf("create standalone thread: %v", err)
	}
	if err := service.DeleteComment(ctx, DeleteCommentInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ThreadID:        deleteOnlyThread.ID,
		CommentID:       deleteOnlyThread.Comments[0].ID,
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
	}); err != nil {
		t.Fatalf("delete standalone thread comment: %v", err)
	}
	afterThreadDelete, err := service.ListThreads(ctx, ThreadListFilter{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
	})
	if err != nil || len(afterThreadDelete) != 2 {
		t.Fatalf("deleted standalone thread remained visible: %#v err=%v", afterThreadDelete, err)
	}
	pastDurationUs := int64(61_000_000)
	if _, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: opened.ID,
		ReviewItemID:    opened.Items[0].ID,
		AssetVersionID:  "version-1",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Outside duration",
		Annotation: AnnotationInput{
			Kind:        "time_point",
			TimeStartUs: &pastDurationUs,
		},
	}); !errors.Is(err, ErrInvalidAnnotation) {
		t.Fatalf("expected duration validation, got %v", err)
	}
	_, err = service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Revision:    opened.Revision,
	})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected invalid open state, got %v", err)
	}

	closed, err := service.Close(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Revision:    opened.Revision,
	})
	if err != nil || closed.Status != "closed" || closed.ClosedAt == nil {
		t.Fatalf("close review: %#v err=%v", closed, err)
	}
	_, err = service.Update(ctx, UpdateSessionInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Name:        "Closed update",
		Revision:    closed.Revision,
	})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected closed review rejection, got %v", err)
	}
	reopened, err := service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          created.ID,
		Revision:    closed.Revision,
	})
	if err != nil ||
		reopened.Status != "open" ||
		reopened.ClosedAt != nil ||
		reopened.Revision != closed.Revision+1 {
		t.Fatalf("reopen review: %#v err=%v", reopened, err)
	}
	rejectedDecision, err := service.CreateDecision(ctx, CreateDecisionInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: reopened.ID,
		ReviewItemID:    reopened.Items[0].ID,
		ActorKind:       "user",
		ActorUserID:     "user-1",
		Decision:        "rejected",
		Note:            "The current direction cannot be delivered.",
	})
	if err != nil ||
		rejectedDecision.Decision != "rejected" ||
		rejectedDecision.Actor.DisplayName != "Owner" {
		t.Fatalf("create rejected decision: %#v err=%v", rejectedDecision, err)
	}
	afterRejected, err := service.Get(ctx, "workspace-1", reopened.ID)
	if err != nil ||
		afterRejected.Status != "changes_requested" ||
		afterRejected.Items[0].Status != "changes_requested" {
		t.Fatalf("unexpected rejected review state: %#v err=%v", afterRejected, err)
	}
	approvedDecision, err := service.CreateDecision(ctx, CreateDecisionInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: reopened.ID,
		ActorKind:       "user",
		ActorUserID:     "user-1",
		Decision:        "approved",
		Note:            "Approved after the requested revision.",
	})
	if err != nil || approvedDecision.Decision != "approved" {
		t.Fatalf("create approved decision: %#v err=%v", approvedDecision, err)
	}
	decisions, err := service.ListDecisions(ctx, DecisionListFilter{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: reopened.ID,
	})
	if err != nil ||
		len(decisions) != 2 ||
		decisions[0].Decision != "approved" ||
		decisions[1].Decision != "rejected" {
		t.Fatalf("unexpected decision history: %#v err=%v", decisions, err)
	}
	afterApproved, err := service.Get(ctx, "workspace-1", reopened.ID)
	if err != nil ||
		afterApproved.Status != "approved" ||
		afterApproved.Items[0].Status != "approved" {
		t.Fatalf("unexpected approved review state: %#v err=%v", afterApproved, err)
	}
	reopenedAfterDecision, err := service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          reopened.ID,
		Revision:    afterApproved.Revision,
	})
	if err != nil || reopenedAfterDecision.Status != "open" {
		t.Fatalf(
			"reopen approved review: %#v err=%v",
			reopenedAfterDecision,
			err,
		)
	}
	imageReview, err := service.Create(ctx, CreateSessionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   "project-1",
		Name:        "Image review",
		Items: []ItemInput{
			{AssetID: "asset-image", AssetVersionID: "version-image"},
		},
	})
	if err != nil {
		t.Fatalf("create image review: %v", err)
	}

	templateService := reviewtemplate.NewService(
		reviewtemplate.NewSQLiteRepository(db),
	)
	template, err := templateService.Create(ctx, reviewtemplate.CreateInput{
		WorkspaceID:      "workspace-1",
		UserID:           "user-1",
		Name:             "Two reviewer approval",
		ParticipantRoles: []string{"reviewer", "reviewer"},
		AllowDownload:    true,
		DecisionRule:     "all_reviewers",
	})
	if err != nil {
		t.Fatalf("create workflow template: %v", err)
	}
	workflowReview, err := service.Create(ctx, CreateSessionInput{
		WorkspaceID:       "workspace-1",
		UserID:            "user-1",
		ProjectID:         "project-1",
		Name:              "Workflow snapshot",
		TemplateID:        &template.ID,
		TemplateRevision:  &template.Revision,
		ResponsibleUserID: stringPointer("user-1"),
		AllowDownload:     template.AllowDownload,
		DecisionRule:      template.DecisionRule,
		Participants: []ParticipantInput{
			{
				UserID:      stringPointer("user-1"),
				DisplayName: "ignored owner name",
				Role:        "reviewer",
			},
			{
				UserID:      stringPointer("user-2"),
				DisplayName: "ignored reviewer name",
				Role:        "reviewer",
			},
		},
		Items: []ItemInput{
			{AssetID: "asset-1", AssetVersionID: "version-2"},
		},
	})
	if err != nil {
		t.Fatalf("create workflow review: %v", err)
	}
	if workflowReview.TemplateID == nil ||
		*workflowReview.TemplateID != template.ID ||
		workflowReview.TemplateRevision == nil ||
		*workflowReview.TemplateRevision != 1 ||
		!workflowReview.AllowDownload ||
		workflowReview.DecisionRule != "all_reviewers" ||
		workflowReview.ResponsibleName != "Owner" ||
		workflowReview.Participants[0].DisplayName != "Owner" {
		t.Fatalf("unexpected workflow snapshot: %#v", workflowReview)
	}
	_, err = templateService.Update(ctx, reviewtemplate.UpdateInput{
		WorkspaceID:      "workspace-1",
		ID:               template.ID,
		Name:             template.Name,
		ParticipantRoles: []string{"reviewer"},
		AllowDownload:    false,
		DecisionRule:     "responsible_only",
		Revision:         template.Revision,
	})
	if err != nil {
		t.Fatalf("update workflow template: %v", err)
	}
	unchangedSnapshot, err := service.Get(
		ctx,
		"workspace-1",
		workflowReview.ID,
	)
	if err != nil ||
		!unchangedSnapshot.AllowDownload ||
		unchangedSnapshot.DecisionRule != "all_reviewers" ||
		unchangedSnapshot.TemplateRevision == nil ||
		*unchangedSnapshot.TemplateRevision != 1 {
		t.Fatalf(
			"template edit changed existing review: %#v err=%v",
			unchangedSnapshot,
			err,
		)
	}
	workflowReview, err = service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          workflowReview.ID,
		Revision:    workflowReview.Revision,
	})
	if err != nil {
		t.Fatalf("open workflow review: %v", err)
	}
	if _, err := service.CreateDecision(ctx, CreateDecisionInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: workflowReview.ID,
		ActorKind:       "user",
		ActorUserID:     "user-1",
		Decision:        "approved",
	}); err != nil {
		t.Fatalf("first workflow approval: %v", err)
	}
	afterFirstApproval, err := service.Get(
		ctx,
		"workspace-1",
		workflowReview.ID,
	)
	if err != nil ||
		afterFirstApproval.Status != "open" ||
		afterFirstApproval.Items[0].Status != "in_review" {
		t.Fatalf(
			"workflow approved before all reviewers: %#v err=%v",
			afterFirstApproval,
			err,
		)
	}
	if _, err := service.CreateDecision(ctx, CreateDecisionInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: workflowReview.ID,
		ActorKind:       "user",
		ActorUserID:     "user-2",
		Decision:        "approved",
	}); err != nil {
		t.Fatalf("second workflow approval: %v", err)
	}
	afterAllApprovals, err := service.Get(
		ctx,
		"workspace-1",
		workflowReview.ID,
	)
	if err != nil ||
		afterAllApprovals.Status != "approved" ||
		afterAllApprovals.Items[0].Status != "approved" {
		t.Fatalf(
			"workflow did not approve after all reviewers: %#v err=%v",
			afterAllApprovals,
			err,
		)
	}
	imageReview, err = service.Open(ctx, SessionStateInput{
		WorkspaceID: "workspace-1",
		ID:          imageReview.ID,
		Revision:    imageReview.Revision,
	})
	if err != nil {
		t.Fatalf("open image review: %v", err)
	}
	pointThread, err = service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
		AssetVersionID:  "version-image",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Move this logo",
		Annotation: AnnotationInput{
			Kind: "point",
			Geometry: &AnnotationGeometry{
				Shape: "point",
				X:     0.25,
				Y:     0.4,
			},
		},
	})
	if err != nil {
		t.Fatalf("create image point thread: %v", err)
	}
	if pointThread.Annotation.Geometry == nil ||
		pointThread.Annotation.Geometry.X != 0.25 ||
		pointThread.Annotation.Geometry.Y != 0.4 {
		t.Fatalf("unexpected point geometry: %#v", pointThread.Annotation)
	}
	regionWidth := 0.3
	regionHeight := 0.2
	regionThread, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
		AssetVersionID:  "version-image",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Reduce this area",
		Annotation: AnnotationInput{
			Kind: "region",
			Geometry: &AnnotationGeometry{
				Shape:  "rect",
				X:      0.5,
				Y:      0.2,
				Width:  &regionWidth,
				Height: &regionHeight,
			},
		},
	})
	if err != nil {
		t.Fatalf("create image region thread: %v", err)
	}
	drawingThread, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
		AssetVersionID:  "version-image",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Use this drawing feedback",
		Annotation: AnnotationInput{
			Kind: "drawing",
			Geometry: &AnnotationGeometry{
				Shape: "drawing",
				Elements: []AnnotationDrawingElement{
					{
						Tool:        "brush",
						Color:       "#ff5c7a",
						StrokeWidth: 4,
						Points: []AnnotationPoint{
							{X: 0.1, Y: 0.1},
							{X: 0.2, Y: 0.2},
						},
					},
					{
						Tool:        "arrow",
						Color:       "#54d6ff",
						StrokeWidth: 4,
						Points: []AnnotationPoint{
							{X: 0.7, Y: 0.3},
							{X: 0.4, Y: 0.6},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create image drawing thread: %v", err)
	}
	imageThreads, err := service.ListThreads(ctx, ThreadListFilter{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
	})
	if err != nil ||
		len(imageThreads) != 3 ||
		imageThreads[0].Annotation.Geometry == nil ||
		imageThreads[1].Annotation.Geometry == nil ||
		imageThreads[2].Annotation.Geometry == nil {
		t.Fatalf("unexpected image threads: %#v err=%v", imageThreads, err)
	}
	foundRegion := false
	foundDrawing := false
	for _, thread := range imageThreads {
		if thread.ID == regionThread.ID &&
			thread.Annotation.Geometry != nil &&
			thread.Annotation.Geometry.Shape == "rect" {
			foundRegion = true
		}
		if thread.ID == drawingThread.ID &&
			thread.Annotation.Geometry != nil &&
			thread.Annotation.Geometry.Shape == "drawing" &&
			len(thread.Annotation.Geometry.Elements) == 2 {
			foundDrawing = true
		}
	}
	if !foundRegion {
		t.Fatalf("region thread was not persisted: %#v", imageThreads)
	}
	if !foundDrawing {
		t.Fatalf("drawing thread was not persisted: %#v", imageThreads)
	}
	overflowWidth := 0.2
	overflowHeight := 0.2
	if _, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
		AssetVersionID:  "version-image",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Invalid region",
		Annotation: AnnotationInput{
			Kind: "region",
			Geometry: &AnnotationGeometry{
				Shape:  "rect",
				X:      0.9,
				Y:      0.9,
				Width:  &overflowWidth,
				Height: &overflowHeight,
			},
		},
	}); !errors.Is(err, ErrInvalidAnnotation) {
		t.Fatalf("expected overflow geometry rejection, got %v", err)
	}
	if _, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
		AssetVersionID:  "version-image",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Invalid drawing",
		Annotation: AnnotationInput{
			Kind: "drawing",
			Geometry: &AnnotationGeometry{
				Shape: "drawing",
				Elements: []AnnotationDrawingElement{{
					Tool:        "brush",
					Color:       "red",
					StrokeWidth: 4,
					Points: []AnnotationPoint{
						{X: 0.1, Y: 0.1},
						{X: 0.2, Y: 0.2},
					},
				}},
			},
		},
	}); !errors.Is(err, ErrInvalidAnnotation) {
		t.Fatalf("expected invalid drawing rejection, got %v", err)
	}
	imageTimeUs := int64(1_000_000)
	if _, err := service.CreateThread(ctx, CreateThreadInput{
		WorkspaceID:     "workspace-1",
		ReviewSessionID: imageReview.ID,
		ReviewItemID:    imageReview.Items[0].ID,
		AssetVersionID:  "version-image",
		AuthorKind:      "user",
		AuthorUserID:    "user-1",
		Body:            "Wrong annotation type",
		Annotation: AnnotationInput{
			Kind:        "time_point",
			TimeStartUs: &imageTimeUs,
		},
	}); !errors.Is(err, ErrInvalidAnnotation) {
		t.Fatalf("expected image time annotation rejection, got %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE annotations
		SET geometry_json = '{"shape":"point","x":2,"y":0.4}'
		WHERE id = ?
	`, pointThread.Annotation.ID); err == nil {
		t.Fatal("database accepted invalid normalized geometry")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE annotations
		SET geometry_json = '{"shape":"drawing","elements":[]}'
		WHERE id = ?
	`, drawingThread.Annotation.ID); err == nil {
		t.Fatal("database accepted empty drawing geometry")
	}

	_, err = service.Create(ctx, CreateSessionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   "project-1",
		Name:        "Wrong project",
		Items: []ItemInput{
			{AssetID: "asset-2", AssetVersionID: "other-version"},
		},
	})
	if !errors.Is(err, ErrInvalidItem) {
		t.Fatalf("expected invalid project item, got %v", err)
	}
}
