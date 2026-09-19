package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/storage"
)

func minimalMP4Bytes() []byte {
	return []byte{
		0x00, 0x00, 0x00, 0x18,
		'f', 't', 'y', 'p',
		'i', 's', 'o', 'm',
		0x00, 0x00, 0x02, 0x00,
		'i', 's', 'o', 'm',
		'm', 'p', '4', '1',
	}
}

func TestLibraryQueryHandlesTenThousandObjects(t *testing.T) {
	db, err := database.Open(context.Background(), database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := "2026-06-10T02:00:00Z"
	fixtures := []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', ?, ?)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN',
			'Asia/Shanghai', '{}', ?, ?
		)`,
		`INSERT INTO storage_providers (
			id, workspace_id, kind, name, status, config_json,
			capabilities_json, created_at, updated_at
		) VALUES (
			'provider-1', 'workspace-1', 'local', 'Local', 'active',
			'{}', '{}', ?, ?
		)`,
		`INSERT INTO local_path_secrets (
			id, workspace_id, path_text, created_at, updated_at
		) VALUES ('secret-1', 'workspace-1', '/tmp/media', ?, ?)`,
		`INSERT INTO authorized_roots (
			id, workspace_id, storage_provider_id, path_secret_ref,
			display_name, display_path, mode, scan_enabled, status,
			revision, created_at, updated_at
		) VALUES (
			'root-1', 'workspace-1', 'provider-1', 'secret-1',
			'Media', 'Media', 'referenced', 1, 'available', 1, ?, ?
		)`,
	}
	for _, statement := range fixtures {
		if _, err := db.ExecContext(ctx, statement, now, now); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO storage_objects (
			id, workspace_id, storage_provider_id, authorized_root_id,
			object_key, kind, status, size_bytes, modified_at,
			quick_fingerprint, mime_type, first_discovered_at,
			created_at, updated_at
		) VALUES (?, 'workspace-1', 'provider-1', 'root-1', ?, 'source',
			'available', ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		t.Fatalf("prepare seed: %v", err)
	}
	for index := 0; index < 10_000; index++ {
		name := fmt.Sprintf("archive/%05d-frame.jpg", index)
		if index == 9_876 {
			name = "campaign/hero-final.jpg"
		}
		if _, err := statement.ExecContext(
			ctx,
			fmt.Sprintf("object-%05d", index),
			name,
			index+1,
			now,
			fmt.Sprintf("fingerprint-%05d", index),
			"image/jpeg",
			now,
			now,
			now,
		); err != nil {
			t.Fatalf("seed object %d: %v", index, err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatalf("close seed statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}

	repository := NewSQLiteLibraryRepository(db)
	started := time.Now()
	page, err := repository.Query(ctx, "workspace-1", "root-1", LibraryQuery{
		Search: "hero-final", MediaType: "image", State: "waiting",
		Sort: "name_asc", Page: 1, PageSize: 48,
	})
	if err != nil {
		t.Fatalf("query library: %v", err)
	}
	elapsed := time.Since(started)
	if page.Total != 1 || len(page.Items) != 1 ||
		page.Items[0].Object.ObjectKey != "campaign/hero-final.jpg" {
		t.Fatalf("unexpected library result: %#v", page)
	}
	if elapsed > time.Second {
		t.Fatalf("10k item query took too long: %s", elapsed)
	}
	t.Logf("10k item filtered query completed in %s", elapsed)
}

func TestUploadVersionKeepsAssetInOriginLibrary(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(tempDir, "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := "2026-06-10T02:00:00Z"
	fixtures := []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', ?, ?)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN',
			'Asia/Shanghai', '{}', ?, ?
		)`,
		`INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, created_at, updated_at
		) VALUES (
			'membership-1', 'workspace-1', 'user-1', 'owner', 'active', ?, ?
		)`,
	}
	for _, statement := range fixtures {
		if _, err := db.ExecContext(ctx, statement, now, now); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	mediaDir := filepath.Join(tempDir, "media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatalf("create media directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(mediaDir, "cut-v1.mp4"),
		[]byte("version-one"),
		0o600,
	); err != nil {
		t.Fatalf("write first version: %v", err)
	}
	storageService := storage.NewService(storage.NewSQLiteRepository(db))
	root, err := storageService.RegisterLocalRoot(ctx, storage.RegisterLocalRootInput{
		WorkspaceID: "workspace-1",
		DisplayName: "Media",
		LocalPath:   mediaDir,
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register media root: %v", err)
	}
	if _, err := storageService.ScanRoot(ctx, "workspace-1", root.ID); err != nil {
		t.Fatalf("scan media root: %v", err)
	}

	repository := NewSQLiteLibraryRepository(db)
	page, err := repository.Query(
		ctx,
		"workspace-1",
		root.ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil || len(page.Items) != 1 || page.Items[0].Asset == nil {
		t.Fatalf("read first asset: page=%#v err=%v", page, err)
	}
	asset := *page.Items[0].Asset
	sourceStore, err := NewManagedSourceStore(filepath.Join(tempDir, "sources"))
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	service := NewLibraryServiceWithSourceStore(repository, sourceStore)
	version, err := service.UploadVersion(ctx, UploadAssetVersionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		AssetID:     asset.ID,
		Revision:    asset.Revision,
		Filename:    "cut-v2.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes()))
	if err != nil {
		t.Fatalf("upload second version: %v", err)
	}
	if version.VersionNumber != 2 || !version.IsCurrent {
		t.Fatalf("unexpected uploaded version: %#v", version)
	}
	if version.UploadCheck == nil ||
		version.UploadCheck.Status != "ready" ||
		version.UploadCheck.UploadSecurityPolicy != "standard" ||
		version.UploadCheck.AssetVersionID != version.ID ||
		version.UploadCheck.StorageObjectID != version.StorageObjectID {
		t.Fatalf("unexpected uploaded version check: %#v", version.UploadCheck)
	}

	versions, err := service.ListVersions(ctx, "workspace-1", asset.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 ||
		versions[0].VersionNumber != 2 ||
		versions[1].VersionNumber != 1 {
		t.Fatalf("unexpected version history: %#v", versions)
	}
	if versions[0].UploadCheck == nil ||
		versions[0].UploadCheck.ID != version.UploadCheck.ID {
		t.Fatalf("version history did not include upload check: %#v", versions[0])
	}
	page, err = repository.Query(
		ctx,
		"workspace-1",
		root.ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil || len(page.Items) != 1 || page.Items[0].Asset == nil {
		t.Fatalf("read asset after upload: page=%#v err=%v", page, err)
	}
	if page.Items[0].Asset.VersionNumber != 2 ||
		page.Items[0].Object.ID != version.StorageObjectID {
		t.Fatalf("library did not follow current version: %#v", page.Items[0])
	}
	roots, err := storageService.ListRoots(ctx, "workspace-1")
	if err != nil {
		t.Fatalf("list visible roots: %v", err)
	}
	if len(roots) != 2 || roots[0].ID != root.ID || roots[1].Mode != "managed" {
		t.Fatalf("managed upload root should be visible without paths: %#v", roots)
	}
	managedPage, err := repository.Query(
		ctx,
		"workspace-1",
		roots[1].ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil {
		t.Fatalf("query managed upload root: %v", err)
	}
	if managedPage.Total != 0 || len(managedPage.Items) != 0 {
		t.Fatalf("version upload should not duplicate origin asset: %#v", managedPage)
	}

	_, err = service.UploadVersion(ctx, UploadAssetVersionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		AssetID:     asset.ID,
		Revision:    asset.Revision,
		Filename:    "stale.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes()))
	if !errors.Is(err, ErrLibraryRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	current, err := repository.assetByID(ctx, "workspace-1", asset.ID)
	if err != nil {
		t.Fatalf("read current asset: %v", err)
	}
	_, err = service.UploadVersion(ctx, UploadAssetVersionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		AssetID:     asset.ID,
		Revision:    current.Revision,
		Filename:    "wrong-type.png",
		MIMEType:    "image/png",
	}, bytes.NewReader([]byte("not-an-image")))
	if !errors.Is(err, ErrLibraryUploadTypeRejected) {
		t.Fatalf("expected upload type rejection, got %v", err)
	}
}

func TestUploadAssetCreatesManagedLibraryItem(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(tempDir, "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := "2026-06-11T02:00:00Z"
	fixtures := []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', ?, ?)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN',
			'Asia/Shanghai', '{}', ?, ?
		)`,
		`INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, created_at, updated_at
		) VALUES (
			'membership-1', 'workspace-1', 'user-1', 'owner', 'active', ?, ?
		)`,
	}
	for _, statement := range fixtures {
		if _, err := db.ExecContext(ctx, statement, now, now); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	repository := NewSQLiteLibraryRepository(db)
	sourceStore, err := NewManagedSourceStore(filepath.Join(tempDir, "sources"))
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	service := NewLibraryServiceWithSourceStore(repository, sourceStore)
	version, err := service.UploadAsset(ctx, UploadAssetInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		Filename:    "remote-cut.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes()))
	if err != nil {
		t.Fatalf("upload asset: %v", err)
	}
	if version.VersionNumber != 1 || !version.IsCurrent {
		t.Fatalf("unexpected uploaded asset version: %#v", version)
	}
	if version.UploadCheck == nil ||
		version.UploadCheck.Status != "ready" ||
		version.UploadCheck.ResultCode == nil ||
		*version.UploadCheck.ResultCode != "type_checks_passed" {
		t.Fatalf("unexpected upload check: %#v", version.UploadCheck)
	}

	roots, err := storage.NewService(storage.NewSQLiteRepository(db)).
		ListRoots(ctx, "workspace-1")
	if err != nil {
		t.Fatalf("list roots: %v", err)
	}
	if len(roots) != 1 || roots[0].Mode != "managed" {
		t.Fatalf("expected managed upload root, got %#v", roots)
	}
	page, err := repository.Query(
		ctx,
		"workspace-1",
		roots[0].ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil {
		t.Fatalf("query managed root: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Asset == nil {
		t.Fatalf("uploaded asset was not visible: %#v", page)
	}
	if page.Items[0].Asset.ID != version.AssetID ||
		page.Items[0].Object.ID != version.StorageObjectID {
		t.Fatalf("managed library item does not point at upload: %#v", page.Items[0])
	}
	if page.Items[0].UploadCheck == nil ||
		page.Items[0].UploadCheck.ID != version.UploadCheck.ID {
		t.Fatalf("managed library item did not include upload check: %#v", page.Items[0])
	}
	asset := page.Items[0].Asset
	renamed, err := service.UpdateAssetName(
		ctx,
		"workspace-1",
		asset.ID,
		"剪辑 A 面",
		asset.Revision,
	)
	if err != nil {
		t.Fatalf("rename asset: %v", err)
	}
	if renamed.Name != "剪辑 A 面" || renamed.Revision != asset.Revision+1 {
		t.Fatalf("unexpected renamed asset: %#v", renamed)
	}
	_, err = service.UpdateAssetName(
		ctx,
		"workspace-1",
		asset.ID,
		"过期名称",
		asset.Revision,
	)
	if !errors.Is(err, ErrLibraryRevisionConflict) {
		t.Fatalf("expected stale rename conflict, got %v", err)
	}
	page, err = repository.Query(
		ctx,
		"workspace-1",
		roots[0].ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil {
		t.Fatalf("query managed root after rename: %v", err)
	}
	if page.Items[0].Asset == nil || page.Items[0].Asset.Name != "剪辑 A 面" {
		t.Fatalf("renamed asset was not visible: %#v", page.Items[0].Asset)
	}

	_, err = service.UploadAsset(ctx, UploadAssetInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		Filename:    "looks-like-image.png",
		MIMEType:    "image/png",
	}, bytes.NewReader([]byte("<script>alert(1)</script>")))
	if !errors.Is(err, ErrLibraryUploadTypeRejected) {
		t.Fatalf("expected disguised upload rejection, got %v", err)
	}
	page, err = repository.Query(
		ctx,
		"workspace-1",
		roots[0].ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil {
		t.Fatalf("query managed root after rejected upload: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("rejected upload should not create media item: %#v", page)
	}
}

func TestResolveUploadCheckRollsBackWhenProcessingJobCannotBeCreated(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(tempDir, "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := "2026-06-23T02:00:00Z"
	fixtures := []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', ?, ?)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN',
			'Asia/Shanghai', '{}', ?, ?
		)`,
		`INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, created_at, updated_at
		) VALUES (
			'membership-1', 'workspace-1', 'user-1', 'owner', 'active', ?, ?
		)`,
	}
	for _, statement := range fixtures {
		if _, err := db.ExecContext(ctx, statement, now, now); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	repository := NewSQLiteLibraryRepository(db)
	sourceStore, err := NewManagedSourceStore(filepath.Join(tempDir, "sources"))
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	service := NewLibraryServiceWithSourceStore(repository, sourceStore)
	version, err := service.UploadAsset(ctx, UploadAssetInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		Filename:    "quarantined.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes()))
	if err != nil {
		t.Fatalf("upload asset: %v", err)
	}
	if version.UploadCheck == nil {
		t.Fatal("uploaded asset did not create upload check")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE upload_checks
		SET status = 'quarantined',
			result_code = 'malware_scan_unavailable',
			updated_at = ?,
			completed_at = ?,
			quarantined_at = ?
		WHERE id = ?
	`, now, now, now, version.UploadCheck.ID); err != nil {
		t.Fatalf("quarantine upload check: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO jobs (
			id, workspace_id, type, status, priority, idempotency_key,
			subject_type, subject_id, payload_json, available_at,
			max_attempts, attempt_count, created_at, updated_at
		) VALUES (
			'duplicate-job', 'workspace-1', ?, 'queued', 12,
			'duplicate-key', 'storageObject', ?, '{}', ?, 3, 0, ?, ?
		)
	`, ProcessAssetVersionJobType, version.StorageObjectID, now, now, now); err != nil {
		t.Fatalf("insert duplicate job fixture: %v", err)
	}

	_, err = repository.ResolveUploadCheckForProcessing(ctx, resolveUploadCheckRecord{
		WorkspaceID: "workspace-1",
		ID:          version.UploadCheck.ID,
		ResultCode:  "manual_released",
		JobID:       "duplicate-job",
		Now:         time.Date(2026, 6, 23, 2, 1, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected processing job creation error")
	}
	check, err := repository.getUploadCheck(ctx, "workspace-1", version.UploadCheck.ID)
	if err != nil {
		t.Fatalf("reload upload check: %v", err)
	}
	if check.Status != "quarantined" ||
		check.ResultCode == nil ||
		*check.ResultCode != "malware_scan_unavailable" {
		t.Fatalf("upload check was partially released: %#v", check)
	}
}

func TestProjectLibraryAggregatesAssetsAndExcludesCandidates(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(tempDir, "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := "2026-06-13T02:00:00Z"
	fixtures := []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', ?, ?)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN',
			'Asia/Shanghai', '{}', ?, ?
		)`,
		`INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, created_at, updated_at
		) VALUES (
			'membership-1', 'workspace-1', 'user-1', 'owner', 'active', ?, ?
		)`,
		`INSERT INTO projects (
			id, workspace_id, name, description, status, created_by,
			created_at, updated_at
		) VALUES (
			'project-a', 'workspace-1', 'Project A', NULL, 'active', 'user-1',
			?, ?
		)`,
		`INSERT INTO projects (
			id, workspace_id, name, description, status, created_by,
			created_at, updated_at
		) VALUES (
			'project-b', 'workspace-1', 'Project B', NULL, 'active', 'user-1',
			?, ?
		)`,
	}
	for _, statement := range fixtures {
		if _, err := db.ExecContext(ctx, statement, now, now); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	repository := NewSQLiteLibraryRepository(db)
	sourceStore, err := NewManagedSourceStore(filepath.Join(tempDir, "sources"))
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	service := NewLibraryServiceWithSourceStore(repository, sourceStore)
	projectA := "project-a"
	projectB := "project-b"
	versionA, err := service.UploadAsset(ctx, UploadAssetInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   &projectA,
		Filename:    "project-a-cut.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes()))
	if err != nil {
		t.Fatalf("upload project A asset: %v", err)
	}
	versionB, err := service.UploadAsset(ctx, UploadAssetInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		ProjectID:   &projectB,
		Filename:    "project-b-cut.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes()))
	if err != nil {
		t.Fatalf("upload project B asset: %v", err)
	}

	page, err := service.QueryProject(
		ctx,
		"workspace-1",
		projectA,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil {
		t.Fatalf("query project library: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 ||
		page.Items[0].Asset == nil ||
		page.Items[0].Asset.ID != versionA.AssetID ||
		page.Items[0].Object.ID != versionA.StorageObjectID {
		t.Fatalf("unexpected project A library page: %#v", page)
	}

	candidates, err := service.QueryProjectCandidates(
		ctx,
		"workspace-1",
		projectA,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil {
		t.Fatalf("query project candidates: %v", err)
	}
	if candidates.Total != 1 || len(candidates.Items) != 1 ||
		candidates.Items[0].Asset == nil ||
		candidates.Items[0].Asset.ID != versionB.AssetID {
		t.Fatalf("unexpected project A candidates: %#v", candidates)
	}
}

func TestNormalizeVersionProcessingStatusWaitsForCompleteRenditions(t *testing.T) {
	videoProbe := &Metadata{Status: "succeeded", MediaType: "video"}
	partial := AssetVersion{
		ProcessingStatus: "pending",
		Probe:            videoProbe,
		SourceSizeBytes:  64 << 20,
		Renditions: []Rendition{
			{Kind: RenditionPoster, ProfileKey: "webp_poster_1280", Status: "ready"},
			{Kind: RenditionProxy, ProfileKey: "h264_aac_720p", Status: "ready"},
		},
	}
	normalizeVersionProcessingStatus(&partial)
	if partial.ProcessingStatus != "partial" {
		t.Fatalf("expected partial status, got %q", partial.ProcessingStatus)
	}

	processing := partial
	processing.ProcessingStatus = "processing"
	normalizeVersionProcessingStatus(&processing)
	if processing.ProcessingStatus != "partial" {
		t.Fatalf("expected preview-ready compatibility status, got %q", processing.ProcessingStatus)
	}

	complete := partial
	complete.ProcessingStatus = "pending"
	complete.Renditions = append(complete.Renditions,
		Rendition{
			Kind: RenditionStoryboard, ProfileKey: "webp_storyboard_20", Status: "ready",
		},
	)
	normalizeVersionProcessingStatus(&complete)
	if complete.ProcessingStatus != "ready" {
		t.Fatalf("expected ready status, got %q", complete.ProcessingStatus)
	}

	hevc := "hevc"
	width := 3840
	height := 2160
	hlsComplete := AssetVersion{
		ProcessingStatus: "pending",
		SourceSizeBytes:  64 << 20,
		Probe: &Metadata{
			Status: "succeeded", MediaType: "video",
			Width: &width, Height: &height, VideoCodec: &hevc,
		},
		Renditions: []Rendition{
			{Kind: RenditionPoster, ProfileKey: "webp_poster_1280", Status: "ready"},
			{Kind: RenditionHLS, ProfileKey: "hls_h264_aac_720p_4s", Status: "ready"},
			{Kind: RenditionStoryboard, ProfileKey: "webp_storyboard_20", Status: "ready"},
		},
	}
	normalizeVersionProcessingStatus(&hlsComplete)
	if hlsComplete.ProcessingStatus != "ready" {
		t.Fatalf("expected HLS strategy ready status, got %q", hlsComplete.ProcessingStatus)
	}

	storyboardFailed := partial
	storyboardFailed.ProcessingStatus = "failed"
	storyboardFailed.Renditions = append(storyboardFailed.Renditions,
		Rendition{
			Kind: RenditionStoryboard, ProfileKey: "webp_storyboard_20", Status: "failed",
		},
	)
	normalizeVersionProcessingStatus(&storyboardFailed)
	if storyboardFailed.ProcessingStatus != "partial" {
		t.Fatalf("expected enhancement failure to keep preview-compatible partial status, got %q", storyboardFailed.ProcessingStatus)
	}
}

func managedRootLocation(t *testing.T, ctx context.Context, db *sql.DB) (string, string) {
	t.Helper()
	var rootID string
	var pathText string
	if err := db.QueryRowContext(ctx, `
		SELECT authorized_roots.id, local_path_secrets.path_text
		FROM authorized_roots
		JOIN local_path_secrets
			ON local_path_secrets.id = authorized_roots.path_secret_ref
			AND local_path_secrets.workspace_id = authorized_roots.workspace_id
		WHERE authorized_roots.workspace_id = 'workspace-1'
			AND authorized_roots.purpose = 'managed_versions'
			AND authorized_roots.deleted_at IS NULL
	`).Scan(&rootID, &pathText); err != nil {
		t.Fatalf("read managed upload root location: %v", err)
	}
	return rootID, pathText
}

// A restore swaps the data directory but keeps the absolute paths stored in
// local_path_secrets, so an instance restored into a different directory would
// keep uploading into the directory it was restored from. The managed upload
// root is app-derived, so it re-links to the running instance's data directory
// on the next upload instead of staying pinned to the source instance.
func TestUploadRelinksManagedRootAfterDataDirectoryMove(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(tempDir, "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := "2026-06-10T02:00:00Z"
	fixtures := []string{
		`INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES ('user-1', 'Owner', 'active', 'zh-CN', ?, ?)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN',
			'Asia/Shanghai', '{}', ?, ?
		)`,
		`INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, created_at, updated_at
		) VALUES (
			'membership-1', 'workspace-1', 'user-1', 'owner', 'active', ?, ?
		)`,
	}
	for _, statement := range fixtures {
		if _, err := db.ExecContext(ctx, statement, now, now); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
	}

	originDir := filepath.Join(tempDir, "instance-a", "media")
	if err := os.MkdirAll(originDir, 0o700); err != nil {
		t.Fatalf("create media directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(originDir, "cut-v1.mp4"),
		[]byte("version-one"),
		0o600,
	); err != nil {
		t.Fatalf("write first version: %v", err)
	}
	storageService := storage.NewService(storage.NewSQLiteRepository(db))
	root, err := storageService.RegisterLocalRoot(ctx, storage.RegisterLocalRootInput{
		WorkspaceID: "workspace-1",
		DisplayName: "Media",
		LocalPath:   originDir,
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register media root: %v", err)
	}
	if _, err := storageService.ScanRoot(ctx, "workspace-1", root.ID); err != nil {
		t.Fatalf("scan media root: %v", err)
	}

	repository := NewSQLiteLibraryRepository(db)
	page, err := repository.Query(
		ctx,
		"workspace-1",
		root.ID,
		LibraryQuery{Page: 1, PageSize: 48},
	)
	if err != nil || len(page.Items) != 1 || page.Items[0].Asset == nil {
		t.Fatalf("read origin asset: page=%#v err=%v", page, err)
	}
	asset := *page.Items[0].Asset

	firstStore, err := NewManagedSourceStore(filepath.Join(tempDir, "instance-a", "sources"))
	if err != nil {
		t.Fatalf("create first source store: %v", err)
	}
	firstService := NewLibraryServiceWithSourceStore(repository, firstStore)
	if _, err := firstService.UploadVersion(ctx, UploadAssetVersionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		AssetID:     asset.ID,
		Revision:    asset.Revision,
		Filename:    "cut-v2.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes())); err != nil {
		t.Fatalf("upload into the first data directory: %v", err)
	}

	firstRootID, firstPath := managedRootLocation(t, ctx, db)
	wantFirstPath := filepath.Join(tempDir, "instance-a", "sources", "workspace-1")
	if firstPath != wantFirstPath {
		t.Fatalf("managed root path = %q, want %q", firstPath, wantFirstPath)
	}

	// Restore the existing managed files as well as the database. Keep a byte
	// snapshot of the source to detect accidental writes back to that instance.
	originalFiles := make(map[string][]byte)
	err = filepath.Walk(firstPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(firstPath, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		originalFiles[rel] = data
		destination := filepath.Join(tempDir, "instance-b", "sources", "workspace-1", rel)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
	if err != nil || len(originalFiles) == 0 {
		t.Fatalf("copy restore fixture: %v, files=%d", err, len(originalFiles))
	}

	// The same database now runs against a different data directory, which is
	// what a restore into a new directory looks like.
	secondStore, err := NewManagedSourceStore(filepath.Join(tempDir, "instance-b", "sources"))
	if err != nil {
		t.Fatalf("create second source store: %v", err)
	}
	secondService := NewLibraryServiceWithSourceStore(repository, secondStore)
	current, err := repository.assetByID(ctx, "workspace-1", asset.ID)
	if err != nil {
		t.Fatalf("read current asset: %v", err)
	}
	if _, err := secondService.UploadVersion(ctx, UploadAssetVersionInput{
		WorkspaceID: "workspace-1",
		UserID:      "user-1",
		AssetID:     asset.ID,
		Revision:    current.Revision,
		Filename:    "cut-v3.mp4",
		MIMEType:    "video/mp4",
	}, bytes.NewReader(minimalMP4Bytes())); err != nil {
		t.Fatalf("upload into the moved data directory: %v", err)
	}

	secondRootID, secondPath := managedRootLocation(t, ctx, db)
	if secondRootID != firstRootID {
		t.Fatalf(
			"relinking must reuse the managed root: %q -> %q",
			firstRootID,
			secondRootID,
		)
	}
	wantSecondPath := filepath.Join(tempDir, "instance-b", "sources", "workspace-1")
	if secondPath != wantSecondPath {
		t.Fatalf(
			"managed root still points at %q, want %q",
			secondPath,
			wantSecondPath,
		)
	}
	for rel, want := range originalFiles {
		got, err := os.ReadFile(filepath.Join(secondPath, rel))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("restored original %s changed or unreadable: %v", rel, err)
		}
	}
	sourceCount := 0
	err = filepath.Walk(firstPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		sourceCount++
		rel, err := filepath.Rel(firstPath, path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		want, exists := originalFiles[rel]
		if !exists || !bytes.Equal(got, want) {
			return fmt.Errorf("source instance changed: %s", rel)
		}
		return nil
	})
	if err != nil || sourceCount != len(originalFiles) {
		t.Fatalf("source instance preservation: %v, files=%d", err, sourceCount)
	}

}
