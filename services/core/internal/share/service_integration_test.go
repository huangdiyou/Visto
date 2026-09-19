package share

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/ratelimit"
)

func TestShareLifecycleProtectsEntryAndPinnedItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	insertShareFixtures(t, db)

	now := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }

	created, err := service.Create(ctx, CreateInput{
		WorkspaceID:     "workspace-1",
		UserID:          "user-1",
		ReviewSessionID: "review-1",
		Name:            " Client review ",
		AllowComment:    true,
		AllowDownload:   false,
		RequireNickname: true,
		Password:        "4827",
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	if created.Share.Name != "Client review" ||
		created.Share.Status != "active" ||
		created.Share.ExpiresAt != nil ||
		!created.Share.PasswordProtected ||
		created.Link.Token == "" ||
		len(created.Share.Links) != 1 {
		t.Fatalf("unexpected created share: %#v", created)
	}

	var tokenDigest, passwordHash string
	if err := db.QueryRowContext(ctx, `
		SELECT share_links.token_digest, shares.password_hash
		FROM shares
		JOIN share_links ON share_links.share_id = shares.id
		WHERE shares.id = ?
	`, created.Share.ID).Scan(&tokenDigest, &passwordHash); err != nil {
		t.Fatalf("read stored share secrets: %v", err)
	}
	if tokenDigest == created.Link.Token ||
		passwordHash == "4827" ||
		tokenDigest != digestToken(created.Link.Token) {
		t.Fatal("share token or password was stored in plaintext")
	}

	opened, err := service.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open share: %v", err)
	}
	if !opened.PasswordRequired || opened.SessionToken == "" ||
		len(opened.Share.Items) != 1 ||
		opened.Share.Items[0].AssetName != "Pinned cut" ||
		opened.Share.Items[0].VersionNumber != 2 ||
		opened.Share.Items[0].SourceStorageObjectID != "object-1" {
		t.Fatalf("unexpected opened share: %#v", opened)
	}
	if _, err := service.PublicSession(
		ctx,
		opened.SessionToken,
	); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("expected password requirement, got %v", err)
	}
	if _, err := service.Verify(
		ctx,
		opened.SessionToken,
		"wrong-password",
		"198.51.100.10",
	); !errors.Is(err, ErrPasswordInvalid) {
		t.Fatalf("expected invalid password, got %v", err)
	}
	publicShare, err := service.Verify(
		ctx,
		opened.SessionToken,
		"4827",
		"198.51.100.10",
	)
	if err != nil {
		t.Fatalf("verify share password: %v", err)
	}
	if publicShare.AllowDownload || !publicShare.AllowComment ||
		!publicShare.RequireNickname ||
		publicShare.ReviewStatus != "open" {
		t.Fatalf("unexpected public policy: %#v", publicShare)
	}
	if publicShare.Items[0].PreviewRenditionKind == nil ||
		*publicShare.Items[0].PreviewRenditionKind != "hls" {
		t.Fatalf("expected HLS public preview, got %#v", publicShare.Items[0])
	}
	if _, err := service.PublicSession(ctx, opened.SessionToken); err != nil {
		t.Fatalf("reuse verified share session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE review_sessions
		SET status = 'closed', closed_at = ?
		WHERE id = 'review-1'
	`, formatTime(now)); err != nil {
		t.Fatalf("close shared review: %v", err)
	}
	readOnlyShare, err := service.PublicSession(ctx, opened.SessionToken)
	if err != nil {
		t.Fatalf("read closed review share: %v", err)
	}
	if readOnlyShare.ReviewStatus != "closed" || readOnlyShare.AllowComment {
		t.Fatalf("closed review remained commentable: %#v", readOnlyShare)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE review_sessions
		SET status = 'open', closed_at = NULL
		WHERE id = 'review-1'
	`); err != nil {
		t.Fatalf("reopen shared review fixture: %v", err)
	}

	updated, err := service.Update(ctx, UpdateInput{
		WorkspaceID:     "workspace-1",
		ID:              created.Share.ID,
		Name:            "Download review",
		AllowComment:    true,
		AllowDownload:   true,
		RequireNickname: false,
		ExpiresAt:       created.Share.ExpiresAt,
		Revision:        created.Share.Revision,
	})
	if err != nil || !updated.AllowDownload || updated.Revision != 2 {
		t.Fatalf("update share: %#v err=%v", updated, err)
	}
	if _, err := service.Update(ctx, UpdateInput{
		WorkspaceID: "workspace-1",
		ID:          created.Share.ID,
		Name:        "Stale",
		ExpiresAt:   created.Share.ExpiresAt,
		Revision:    created.Share.Revision,
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	revoked, err := service.Revoke(ctx, StateInput{
		WorkspaceID: "workspace-1",
		ID:          created.Share.ID,
		Revision:    updated.Revision,
	})
	if err != nil || revoked.Status != "revoked" {
		t.Fatalf("revoke share: %#v err=%v", revoked, err)
	}
	if _, err := service.PublicSession(
		ctx,
		opened.SessionToken,
	); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked session remained usable: %v", err)
	}
	if _, err := service.Open(
		ctx,
		created.Link.Token,
	); !errors.Is(err, ErrEntryUnavailable) {
		t.Fatalf("revoked entry remained usable: %v", err)
	}
}

func TestSharePasswordVerificationRateLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	insertShareFixtures(t, db)

	service := NewServiceWithSecretsAndRateLimits(
		NewSQLiteRepository(db),
		nil,
		ratelimit.New(db),
	)
	service.clock = func() time.Time {
		return time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	}
	created, err := service.Create(ctx, CreateInput{
		WorkspaceID:     "workspace-1",
		UserID:          "user-1",
		ReviewSessionID: "review-1",
		Name:            "Protected",
		Password:        "visitor-password",
	})
	if err != nil {
		t.Fatalf("create protected share: %v", err)
	}
	opened, err := service.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open protected share: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := service.Verify(
			ctx,
			opened.SessionToken,
			"wrong-password",
			"198.51.100.10",
		); !errors.Is(err, ErrPasswordInvalid) {
			t.Fatalf("attempt %d: expected invalid password, got %v", attempt, err)
		}
	}
	if _, err := service.Verify(
		ctx,
		opened.SessionToken,
		"visitor-password",
		"198.51.100.10",
	); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}

	restartedService := NewServiceWithSecretsAndRateLimits(
		NewSQLiteRepository(db),
		nil,
		ratelimit.New(db),
	)
	restartedService.clock = service.clock

	openedAgain, err := restartedService.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open another visitor session: %v", err)
	}
	if _, err := restartedService.Verify(
		ctx,
		openedAgain.SessionToken,
		"visitor-password",
		"198.51.100.10",
	); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected restarted service and reopened session to remain rate limited, got %v", err)
	}

	openedElsewhere, err := restartedService.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open another client session: %v", err)
	}
	if _, err := restartedService.Verify(
		ctx,
		openedElsewhere.SessionToken,
		"visitor-password",
		"198.51.100.11",
	); err != nil {
		t.Fatalf("a different client must not be globally locked: %v", err)
	}
}

func TestShareVisitorCodeRateLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	insertShareFixtures(t, db)

	newService := func() *Service {
		service := NewServiceWithSecretsAndRateLimits(
			NewSQLiteRepository(db),
			nil,
			ratelimit.New(db),
		)
		service.clock = func() time.Time {
			return time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
		}
		return service
	}

	service := newService()
	created, err := service.Create(ctx, CreateInput{
		WorkspaceID:     "workspace-1",
		UserID:          "user-1",
		ReviewSessionID: "review-1",
		Name:            "Coded review",
		RequireNickname: true,
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	code, err := service.CreateVisitorCode(ctx, CreateVisitorCodeInput{
		WorkspaceID: "workspace-1",
		ShareID:     created.Share.ID,
		UserID:      "user-1",
		DisplayName: "Client C",
	})
	if err != nil {
		t.Fatalf("create visitor code: %v", err)
	}
	correctCode := strings.ToLower(strings.ReplaceAll(code.Code, "-", ""))

	opened, err := service.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open share: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := service.Identify(ctx, IdentifyInput{
			SessionToken:   opened.SessionToken,
			Method:         "verification_code",
			Code:           "wrong-code",
			ClientIdentity: "203.0.113.30",
		}); !errors.Is(err, ErrVisitorCodeInvalid) {
			t.Fatalf("attempt %d: expected invalid visitor code, got %v", attempt, err)
		}
	}
	if _, err := service.Identify(ctx, IdentifyInput{
		SessionToken:   opened.SessionToken,
		Method:         "verification_code",
		Code:           correctCode,
		ClientIdentity: "203.0.113.30",
	}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}

	restarted := newService()
	openedAgain, err := restarted.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open another visitor session after restart: %v", err)
	}
	if _, err := restarted.Identify(ctx, IdentifyInput{
		SessionToken:   openedAgain.SessionToken,
		Method:         "verification_code",
		Code:           correctCode,
		ClientIdentity: "203.0.113.30",
	}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected restarted service and reopened session to remain rate limited, got %v", err)
	}

	openedElsewhere, err := restarted.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open another client session: %v", err)
	}
	if _, err := restarted.Identify(ctx, IdentifyInput{
		SessionToken:   openedElsewhere.SessionToken,
		Method:         "verification_code",
		Code:           correctCode,
		ClientIdentity: "203.0.113.31",
	}); err != nil {
		t.Fatalf("a different client must not be globally locked: %v", err)
	}
}

func TestShareVisitorIdentitySupportsNicknameAndCode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	insertShareFixtures(t, db)

	now := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }

	created, err := service.Create(ctx, CreateInput{
		WorkspaceID:     "workspace-1",
		UserID:          "user-1",
		ReviewSessionID: "review-1",
		Name:            "Identity review",
		AllowComment:    true,
		RequireNickname: true,
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	opened, err := service.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open share: %v", err)
	}
	if opened.PasswordRequired ||
		opened.Share.Visitor.IdentityMethod != "anonymous" ||
		opened.Share.Visitor.Identified {
		t.Fatalf("unexpected opened visitor: %#v", opened)
	}
	if _, err := service.Identify(ctx, IdentifyInput{
		SessionToken: opened.SessionToken,
		Method:       "anonymous",
	}); !errors.Is(err, ErrIdentityRequired) {
		t.Fatalf("expected required identity, got %v", err)
	}
	nickname, err := service.Identify(ctx, IdentifyInput{
		SessionToken: opened.SessionToken,
		Method:       "nickname",
		DisplayName:  " Client A ",
	})
	if err != nil {
		t.Fatalf("identify nickname: %v", err)
	}
	if nickname.Visitor.DisplayName == nil ||
		*nickname.Visitor.DisplayName != "Client A" ||
		nickname.Visitor.IdentityMethod != "nickname" ||
		!nickname.Visitor.Identified ||
		nickname.Visitor.Verified {
		t.Fatalf("unexpected nickname visitor: %#v", nickname.Visitor)
	}

	code, err := service.CreateVisitorCode(ctx, CreateVisitorCodeInput{
		WorkspaceID: "workspace-1",
		ShareID:     created.Share.ID,
		UserID:      "user-1",
		DisplayName: "Client B",
	})
	if err != nil {
		t.Fatalf("create visitor code: %v", err)
	}
	if code.Code == "" || code.CodePrefix == "" {
		t.Fatalf("visitor code secret was not returned: %#v", code)
	}
	second, err := service.Open(ctx, created.Link.Token)
	if err != nil {
		t.Fatalf("open second share session: %v", err)
	}
	if _, err := service.Identify(ctx, IdentifyInput{
		SessionToken:   second.SessionToken,
		Method:         "verification_code",
		Code:           "wrong-code",
		ClientIdentity: "203.0.113.20",
	}); !errors.Is(err, ErrVisitorCodeInvalid) {
		t.Fatalf("expected invalid visitor code, got %v", err)
	}
	verified, err := service.Identify(ctx, IdentifyInput{
		SessionToken:   second.SessionToken,
		Method:         "verification_code",
		Code:           strings.ToLower(strings.ReplaceAll(code.Code, "-", "")),
		ClientIdentity: "203.0.113.20",
	})
	if err != nil {
		t.Fatalf("identify with visitor code: %v", err)
	}
	if verified.Visitor.DisplayName == nil ||
		*verified.Visitor.DisplayName != "Client B" ||
		verified.Visitor.IdentityMethod != "verification_code" ||
		!verified.Visitor.Identified ||
		!verified.Visitor.Verified {
		t.Fatalf("unexpected verified visitor: %#v", verified.Visitor)
	}
}

func TestShareRequiresOpenReviewSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	insertShareFixtures(t, db)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE review_sessions SET status = 'draft' WHERE id = 'review-1'`,
	); err != nil {
		t.Fatalf("mark review draft: %v", err)
	}

	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	}
	_, err = service.Create(ctx, CreateInput{
		WorkspaceID:     "workspace-1",
		UserID:          "user-1",
		ReviewSessionID: "review-1",
		Name:            "Draft share",
	})
	if !errors.Is(err, ErrInvalidReview) {
		t.Fatalf("expected invalid draft review, got %v", err)
	}
}

func insertShareFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	now := "2026-06-11T07:00:00Z"
	rootPath := t.TempDir()
	fixtures := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO users (
				id, display_name, status, locale, created_at, updated_at
			) VALUES (?, ?, 'active', 'zh-CN', ?, ?)`,
			[]any{"user-1", "Owner", now, now},
		},
		{
			`INSERT INTO workspaces (
				id, name, status, default_locale, timezone, settings_json,
				created_at, updated_at
			) VALUES (?, ?, 'active', 'zh-CN', 'Asia/Shanghai', '{}', ?, ?)`,
			[]any{"workspace-1", "Studio", now, now},
		},
		{
			`INSERT INTO memberships (
				id, workspace_id, user_id, role_key, status, created_at, updated_at
			) VALUES (?, ?, ?, 'owner', 'active', ?, ?)`,
			[]any{"membership-1", "workspace-1", "user-1", now, now},
		},
		{
			`INSERT INTO projects (
				id, workspace_id, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'active', ?, 1, ?, ?)`,
			[]any{"project-1", "workspace-1", "Documentary", "user-1", now, now},
		},
		{
			`INSERT INTO storage_providers (
				id, workspace_id, kind, name, status, config_json,
				capabilities_json, created_at, updated_at
			) VALUES (?, ?, 'local', ?, 'active', '{}', '{}', ?, ?)`,
			[]any{"provider-1", "workspace-1", "Local", now, now},
		},
		{
			`INSERT INTO local_path_secrets (
				id, workspace_id, path_text, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?)`,
			[]any{"secret-1", "workspace-1", rootPath, now, now},
		},
		{
			`INSERT INTO authorized_roots (
				id, workspace_id, storage_provider_id, display_name,
				display_path, path_secret_ref, mode, scan_enabled, status,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'referenced', 1, 'available', 1, ?, ?)`,
			[]any{
				"root-1", "workspace-1", "provider-1", "Media", "~/Media",
				"secret-1", now, now,
			},
		},
		{
			`INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id,
				object_key, kind, status, size_bytes, modified_at,
				quick_fingerprint, mime_type, first_discovered_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 'source', 'available', 10, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"object-1", "workspace-1", "provider-1", "root-1", "cut-v2.mp4",
				now, "fingerprint-2", "video/mp4", now, now, now,
			},
		},
		{
			`INSERT INTO assets (
				id, workspace_id, project_id, type, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'video', ?, 'active', ?, 1, ?, ?)`,
			[]any{
				"asset-1", "workspace-1", "project-1", "Pinned cut",
				"user-1", now, now,
			},
		},
		{
			`INSERT INTO asset_versions (
				id, workspace_id, asset_id, version_number, processing_status,
				source_filename, source_mime, source_size_bytes,
				source_fingerprint, created_by, created_at
			) VALUES (?, ?, ?, 2, 'ready', ?, 'video/mp4', 10, ?, ?, ?)`,
			[]any{
				"version-2", "workspace-1", "asset-1", "cut-v2.mp4",
				"fingerprint-2", "user-1", now,
			},
		},
		{
			`INSERT INTO version_files (
				id, workspace_id, asset_version_id, storage_object_id,
				role, created_at
			) VALUES (?, ?, ?, ?, 'primary', ?)`,
			[]any{"version-file-2", "workspace-1", "version-2", "object-1", now},
		},
		{
			`INSERT INTO media_probes (
				storage_object_id, workspace_id, status, source_fingerprint,
				media_type, format_name, format_long_name, duration_us,
				bit_rate, width, height, video_codec, audio_codec,
				raw_metadata_json, probed_at, updated_at
			) VALUES (
				?, ?, 'succeeded', ?, 'video', 'mov,mp4,m4a,3gp,3g2,mj2',
				'QuickTime / MOV', 3000000, 2000000, 3840, 2160,
				'hevc', 'aac', '{}', ?, ?
			)`,
			[]any{"object-1", "workspace-1", "fingerprint-2", now, now},
		},
		{
			`INSERT INTO renditions (
				id, workspace_id, source_storage_object_id, asset_version_id,
				kind, profile_key, profile_version, source_fingerprint,
				status, object_key, mime_type, width, height, duration_us,
				size_bytes, metadata_json, created_at, updated_at, completed_at
			) VALUES (
				?, ?, ?, ?, 'hls', 'hls_h264_aac_720p_4s', 1, ?,
				'ready', ?, 'application/vnd.apple.mpegurl', 1280, 720,
				3000000, 2048, '{}', ?, ?, ?
			)`,
			[]any{
				"rendition-hls-1", "workspace-1", "object-1", "version-2",
				"fingerprint-2", "workspace-1/object-1/hls/index.m3u8",
				now, now, now,
			},
		},
		{
			`INSERT INTO review_sessions (
				id, workspace_id, project_id, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, 'open', ?, 1, ?, ?)`,
			[]any{
				"review-1", "workspace-1", "project-1", "Client review",
				"user-1", now, now,
			},
		},
		{
			`INSERT INTO review_items (
				id, workspace_id, review_session_id, asset_id,
				asset_version_id, position, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 0, 'pending', ?, ?)`,
			[]any{
				"review-item-1", "workspace-1", "review-1", "asset-1",
				"version-2", now, now,
			},
		},
	}
	for _, fixture := range fixtures {
		if _, err := db.Exec(fixture.query, fixture.args...); err != nil {
			t.Fatalf("insert share fixture: %v", err)
		}
	}
}
