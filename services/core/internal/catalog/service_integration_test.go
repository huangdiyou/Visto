package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestProjectAndCollectionLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := catalogTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	fixedNow := time.Date(2026, time.June, 9, 9, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return fixedNow }

	description := "首轮交付"
	project, err := service.CreateProject(ctx, CreateProjectInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		Name:        " 夏季广告 ",
		Description: &description,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if project.Name != "夏季广告" || project.Revision != 1 {
		t.Fatalf("unexpected project: %#v", project)
	}
	if project.PrimaryOwnerUserID == nil || *project.PrimaryOwnerUserID != session.User.ID {
		t.Fatalf("project primary owner was not set: %#v", project)
	}
	var roleKey string
	if err := db.QueryRowContext(ctx, `
		SELECT role_key
		FROM project_memberships
		WHERE project_id = ? AND user_id = ? AND status = 'active'
	`, project.ID, session.User.ID).Scan(&roleKey); err != nil {
		t.Fatalf("read project membership: %v", err)
	}
	if roleKey != "primary_owner" {
		t.Fatalf("role key = %q, want primary_owner", roleKey)
	}

	updatedName := "夏季广告 2026"
	project, err = service.UpdateProject(ctx, UpdateProjectInput{
		WorkspaceID: session.Workspace.ID,
		ID:          project.ID,
		Name:        updatedName,
		Description: nil,
		Revision:    project.Revision,
	})
	if err != nil {
		t.Fatalf("update project: %v", err)
	}
	if project.Name != updatedName || project.Revision != 2 {
		t.Fatalf("unexpected updated project: %#v", project)
	}

	_, err = service.UpdateProject(ctx, UpdateProjectInput{
		WorkspaceID: session.Workspace.ID,
		ID:          project.ID,
		Name:        "stale",
		Revision:    1,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	collection, err := service.CreateCollection(ctx, CreateCollectionInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		ProjectID:   project.ID,
		Name:        "客户交付",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if collection.Position != 0 || collection.Kind != "delivery" {
		t.Fatalf("unexpected collection: %#v", collection)
	}

	project, err = service.ArchiveProject(ctx, ProjectStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          project.ID,
		Revision:    project.Revision,
	})
	if err != nil {
		t.Fatalf("archive project: %v", err)
	}
	if project.Status != "archived" {
		t.Fatalf("expected archived project, got %q", project.Status)
	}

	_, err = service.CreateCollection(ctx, CreateCollectionInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		ProjectID:   project.ID,
		Name:        "归档后不可新增",
	})
	if !errors.Is(err, ErrProjectArchived) {
		t.Fatalf("expected archived project error, got %v", err)
	}

	project, err = service.RestoreProject(ctx, ProjectStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          project.ID,
		Revision:    project.Revision,
	})
	if err != nil {
		t.Fatalf("restore project: %v", err)
	}

	if _, err := service.Project(ctx, "another-workspace", project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected workspace isolation, got %v", err)
	}

	if err := service.DeleteCollection(
		ctx,
		session.Workspace.ID,
		collection.ID,
		collection.Revision,
	); err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	if _, err := service.Collection(
		ctx,
		session.Workspace.ID,
		collection.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted collection to be hidden, got %v", err)
	}

	if err := service.DeleteProject(ctx, ProjectStateInput{
		WorkspaceID: session.Workspace.ID,
		ID:          project.ID,
		Revision:    project.Revision,
	}); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := service.Project(
		ctx,
		session.Workspace.ID,
		project.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted project to be hidden, got %v", err)
	}
}

func TestListProjectsUsesProjectMembershipVisibility(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := catalogTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	fixedNow := time.Date(2026, time.June, 13, 9, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return fixedNow }

	first, err := service.CreateProject(ctx, CreateProjectInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		Name:        "A 项目",
	})
	if err != nil {
		t.Fatalf("create first project: %v", err)
	}
	second, err := service.CreateProject(ctx, CreateProjectInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		Name:        "B 项目",
	})
	if err != nil {
		t.Fatalf("create second project: %v", err)
	}

	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	member, err := identityService.CreateAccount(ctx, identity.CreateAccountInput{
		WorkspaceID: session.Workspace.ID,
		Email:       "member@example.com",
		DisplayName: "Member",
		Password:    "local-password-123",
		Locale:      "zh-CN",
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	memberProjects, err := service.ListProjects(ctx, ListProjectsInput{
		WorkspaceID:   session.Workspace.ID,
		UserID:        member.ID,
		WorkspaceRole: "member",
	})
	if err != nil {
		t.Fatalf("list member projects: %v", err)
	}
	if len(memberProjects) != 0 {
		t.Fatalf("member should not see projects before joining: %#v", memberProjects)
	}

	insertProjectMembership(
		t,
		db,
		session,
		first.ID,
		member.ID,
		"member",
		`{"assets.upload":true}`,
		nil,
		fixedNow,
	)
	expiredAt := fixedNow.Add(-time.Hour)
	insertProjectMembership(
		t,
		db,
		session,
		second.ID,
		member.ID,
		"guest",
		`{"assets.upload":true}`,
		&expiredAt,
		fixedNow,
	)

	memberProjects, err = service.ListProjects(ctx, ListProjectsInput{
		WorkspaceID:   session.Workspace.ID,
		UserID:        member.ID,
		WorkspaceRole: "member",
	})
	if err != nil {
		t.Fatalf("list joined member projects: %v", err)
	}
	if len(memberProjects) != 1 || memberProjects[0].ID != first.ID {
		t.Fatalf("member should only see active joined project: %#v", memberProjects)
	}

	ownerProjects, err := service.ListProjects(ctx, ListProjectsInput{
		WorkspaceID:   session.Workspace.ID,
		UserID:        session.User.ID,
		WorkspaceRole: "owner",
	})
	if err != nil {
		t.Fatalf("list owner projects: %v", err)
	}
	if len(ownerProjects) != 2 {
		t.Fatalf("owner should see all projects, got %#v", ownerProjects)
	}
}

func TestCollectionItemsAssignProjectAndKeepDenseOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := catalogTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	fixedNow := time.Date(2026, time.June, 9, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return fixedNow }

	project, err := service.CreateProject(ctx, CreateProjectInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		Name:        "图片审阅",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	collection, err := service.CreateCollection(ctx, CreateCollectionInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		ProjectID:   project.ID,
		Name:        "精选",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	insertTestAsset(t, db, session, "asset-1", "封面", "image", fixedNow)
	insertTestAsset(t, db, session, "asset-2", "预告", "video", fixedNow)

	first, err := service.AddCollectionItem(ctx, AddCollectionItemInput{
		WorkspaceID:  session.Workspace.ID,
		CollectionID: collection.ID,
		AssetID:      "asset-1",
	})
	if err != nil {
		t.Fatalf("add first item: %v", err)
	}
	second, err := service.AddCollectionItem(ctx, AddCollectionItemInput{
		WorkspaceID:  session.Workspace.ID,
		CollectionID: collection.ID,
		AssetID:      "asset-2",
	})
	if err != nil {
		t.Fatalf("add second item: %v", err)
	}

	var assignedProject sql.NullString
	if err := db.QueryRowContext(
		ctx,
		"SELECT project_id FROM assets WHERE id = 'asset-1'",
	).Scan(&assignedProject); err != nil {
		t.Fatalf("read assigned project: %v", err)
	}
	if !assignedProject.Valid || assignedProject.String != project.ID {
		t.Fatalf("asset was not assigned to project: %#v", assignedProject)
	}

	insertTestAsset(t, db, session, "asset-3", "错误固定版本", "video", fixedNow)
	invalidPinnedVersion := "version-for-another-asset"
	if _, err := service.AddCollectionItem(ctx, AddCollectionItemInput{
		WorkspaceID:     session.Workspace.ID,
		CollectionID:    collection.ID,
		AssetID:         "asset-3",
		PinnedVersionID: &invalidPinnedVersion,
	}); !errors.Is(err, ErrPinnedVersionInvalid) {
		t.Fatalf("invalid pinned version error = %v, want ErrPinnedVersionInvalid", err)
	}

	if err := service.ReorderCollectionItems(
		ctx,
		session.Workspace.ID,
		collection.ID,
		[]string{second.ID, first.ID},
	); err != nil {
		t.Fatalf("reorder items: %v", err)
	}

	items, err := service.ListCollectionItems(
		ctx,
		session.Workspace.ID,
		collection.ID,
	)
	if err != nil {
		t.Fatalf("list reordered items: %v", err)
	}
	if len(items) != 2 || items[0].ID != second.ID || items[0].Position != 0 {
		t.Fatalf("unexpected item order: %#v", items)
	}

	if err := service.DeleteCollectionItem(
		ctx,
		session.Workspace.ID,
		collection.ID,
		second.ID,
	); err != nil {
		t.Fatalf("delete collection item: %v", err)
	}
	items, err = service.ListCollectionItems(
		ctx,
		session.Workspace.ID,
		collection.ID,
	)
	if err != nil {
		t.Fatalf("list compacted items: %v", err)
	}
	if len(items) != 1 || items[0].ID != first.ID || items[0].Position != 0 {
		t.Fatalf("unexpected compacted order: %#v", items)
	}
}

func TestProjectOverviewAggregatesOnlyProjectWorkSignals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := catalogTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	fixedNow := time.Date(2026, time.June, 13, 10, 30, 0, 0, time.UTC)
	service.clock = func() time.Time { return fixedNow }

	project, err := service.CreateProject(ctx, CreateProjectInput{
		WorkspaceID: session.Workspace.ID,
		UserID:      session.User.ID,
		Name:        "发布片",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	emptyOverview, err := service.ProjectOverview(
		ctx,
		session.Workspace.ID,
		project.ID,
	)
	if err != nil {
		t.Fatalf("read empty project overview: %v", err)
	}
	if emptyOverview.Reviews.Total != 0 ||
		emptyOverview.OpenFeedback.Total != 0 ||
		emptyOverview.RecentVersions.Total != 0 ||
		emptyOverview.FailedTasks.Total != 0 {
		t.Fatalf("new project overview should be empty: %#v", emptyOverview)
	}

	createdAt := formatDatabaseTime(fixedNow.Add(-time.Hour))
	updatedAt := formatDatabaseTime(fixedNow.Add(-15 * time.Minute))
	dueAt := formatDatabaseTime(fixedNow.Add(24 * time.Hour))
	fixtures := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO assets (
				id, workspace_id, project_id, type, name, status, created_by,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, 'video', '发布片主版本', 'active', ?, 1, ?, ?)`,
			args: []any{
				"overview-asset", session.Workspace.ID, project.ID,
				session.User.ID, createdAt, updatedAt,
			},
		},
		{
			query: `INSERT INTO project_assets (
				id, workspace_id, project_id, asset_id, added_by, status,
				created_at, updated_at
			) VALUES ('overview-project-asset', ?, ?, 'overview-asset', ?, 'active', ?, ?)`,
			args: []any{
				session.Workspace.ID, project.ID, session.User.ID, createdAt, updatedAt,
			},
		},
		{
			query: `INSERT INTO review_sessions (
				id, workspace_id, project_id, name, status, due_at, created_by,
				responsible_user_id, revision, created_at, updated_at
			) VALUES (
				'overview-review', ?, ?, '客户终审', 'changes_requested', ?, ?, ?, 1, ?, ?
			)`,
			args: []any{
				session.Workspace.ID, project.ID, dueAt, session.User.ID,
				session.User.ID, createdAt, updatedAt,
			},
		},
		{
			query: `INSERT INTO storage_providers (
				id, workspace_id, kind, name, status, config_json,
				capabilities_json, created_at, updated_at
			) VALUES (
				'overview-provider', ?, 'local', '概览测试存储', 'active', '{}', '{}', ?, ?
			)`,
			args: []any{session.Workspace.ID, createdAt, updatedAt},
		},
		{
			query: `INSERT INTO local_path_secrets (
				id, workspace_id, path_text, created_at, updated_at
			) VALUES ('overview-path', ?, 'C:\overview-test', ?, ?)`,
			args: []any{session.Workspace.ID, createdAt, updatedAt},
		},
		{
			query: `INSERT INTO authorized_roots (
				id, workspace_id, storage_provider_id, display_name, display_path,
				path_secret_ref, mode, scan_enabled, status, revision, created_at, updated_at
			) VALUES (
				'overview-root', ?, 'overview-provider', '测试媒体', '~/Media',
				'overview-path', 'referenced', 1, 'available', 1, ?, ?
			)`,
			args: []any{session.Workspace.ID, createdAt, updatedAt},
		},
		{
			query: `INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id, object_key,
				kind, status, size_bytes, modified_at, quick_fingerprint, mime_type,
				first_discovered_at, created_at, updated_at
			) VALUES (
				'overview-object', ?, 'overview-provider', 'overview-root', 'release-v1.mp4',
				'source', 'available', 1024, ?, 'overview-fingerprint', 'video/mp4', ?, ?, ?
			)`,
			args: []any{
				session.Workspace.ID, updatedAt, createdAt, createdAt, updatedAt,
			},
		},
		{
			query: `INSERT INTO storage_objects (
				id, workspace_id, storage_provider_id, authorized_root_id, object_key,
				kind, status, size_bytes, modified_at, quick_fingerprint, mime_type,
				first_discovered_at, created_at, updated_at
			) VALUES (
				'overview-current-object', ?, 'overview-provider', 'overview-root', 'release-v7.mp4',
				'source', 'available', 1024, ?, 'overview-current-fingerprint', 'video/mp4', ?, ?, ?
			)`,
			args: []any{
				session.Workspace.ID, updatedAt, createdAt, createdAt, updatedAt,
			},
		},
	}
	for _, fixture := range fixtures {
		if _, err := db.ExecContext(ctx, fixture.query, fixture.args...); err != nil {
			t.Fatalf("insert overview fixture: %v", err)
		}
	}

	for versionNumber := 1; versionNumber <= 7; versionNumber++ {
		versionID := fmt.Sprintf("overview-version-%d", versionNumber)
		versionCreatedAt := formatDatabaseTime(
			fixedNow.Add(-time.Duration(8-versionNumber) * time.Minute),
		)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO asset_versions (
				id, workspace_id, asset_id, version_number, processing_status,
				source_filename, source_mime, source_size_bytes, source_fingerprint,
				created_by, created_at
			) VALUES (?, ?, 'overview-asset', ?, 'ready', ?, 'video/mp4', 1024, ?, ?, ?)
		`,
			versionID,
			session.Workspace.ID,
			versionNumber,
			fmt.Sprintf("release-v%d.mp4", versionNumber),
			fmt.Sprintf("overview-fingerprint-%d", versionNumber),
			session.User.ID,
			versionCreatedAt,
		); err != nil {
			t.Fatalf("insert overview version %d: %v", versionNumber, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE assets
		SET current_version_id = 'overview-version-7'
		WHERE id = 'overview-asset' AND workspace_id = ?
	`, session.Workspace.ID); err != nil {
		t.Fatalf("set overview current version: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO version_files (
			id, workspace_id, asset_version_id, storage_object_id, role, created_at
		) VALUES (
			'overview-version-file', ?, 'overview-version-1',
			'overview-object', 'primary', ?
		)
	`, session.Workspace.ID, createdAt); err != nil {
		t.Fatalf("insert overview version file: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO version_files (
			id, workspace_id, asset_version_id, storage_object_id, role, created_at
		) VALUES (
			'overview-current-version-file', ?, 'overview-version-7',
			'overview-current-object', 'primary', ?
		)
	`, session.Workspace.ID, createdAt); err != nil {
		t.Fatalf("insert overview current version file: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO review_items (
			id, workspace_id, review_session_id, asset_id, asset_version_id,
			position, status, created_at, updated_at
		) VALUES (
			'overview-review-item', ?, 'overview-review', 'overview-asset',
			'overview-version-7', 0, 'changes_requested', ?, ?
		)
	`, session.Workspace.ID, createdAt, updatedAt); err != nil {
		t.Fatalf("insert overview review item: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO comment_threads (
			id, workspace_id, review_session_id, review_item_id, asset_version_id,
			author_kind, author_user_id, status, revision, created_at, updated_at
		) VALUES (
			'overview-thread', ?, 'overview-review', 'overview-review-item',
			'overview-version-7', 'user', ?, 'open', 1, ?, ?
		)
	`, session.Workspace.ID, session.User.ID, createdAt, updatedAt); err != nil {
		t.Fatalf("insert overview thread: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO comments (
			id, workspace_id, thread_id, author_kind, author_user_id, body, created_at
		) VALUES (
			'overview-comment', ?, 'overview-thread', 'user', ?,
			'片尾字幕需要再停留两秒', ?
		)
	`, session.Workspace.ID, session.User.ID, updatedAt); err != nil {
		t.Fatalf("insert overview comment: %v", err)
	}
	pointUs := int64(75_000_000)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO annotations (
			id, workspace_id, thread_id, kind, time_start_us,
			geometry_version, created_at, updated_at
		) VALUES (
			'overview-annotation', ?, 'overview-thread', 'time_point', ?,
			1, ?, ?
		)
	`, session.Workspace.ID, pointUs, createdAt, updatedAt); err != nil {
		t.Fatalf("insert overview annotation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO jobs (
			id, workspace_id, type, status, subject_type, subject_id,
			payload_json, available_at, max_attempts, attempt_count,
			last_error_code, last_error_message, created_at, updated_at
		) VALUES (
			'overview-job', ?, 'media.generate_video_renditions', 'failed',
			'storageObject', 'overview-object', '{}', ?, 3, 3,
			'media.ffmpeg_failed', '无法生成网页预览', ?, ?
		)
	`, session.Workspace.ID, createdAt, createdAt, updatedAt); err != nil {
		t.Fatalf("insert overview failed job: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO jobs (
			id, workspace_id, type, status, subject_type, subject_id,
			payload_json, available_at, max_attempts, attempt_count,
			last_error_code, last_error_message, created_at, updated_at
		) VALUES (
			'overview-current-job', ?, 'media.generate_video_renditions', 'failed',
			'storageObject', 'overview-current-object', '{}', ?, 3, 3,
			'media.ffmpeg_failed', '当前版本无法生成网页预览', ?, ?
		)
	`, session.Workspace.ID, createdAt, createdAt, updatedAt); err != nil {
		t.Fatalf("insert overview current failed job: %v", err)
	}

	overview, err := service.ProjectOverview(ctx, session.Workspace.ID, project.ID)
	if err != nil {
		t.Fatalf("read project overview: %v", err)
	}
	if overview.ProjectID != project.ID || !overview.GeneratedAt.Equal(fixedNow) {
		t.Fatalf("unexpected overview identity: %#v", overview)
	}
	if overview.Reviews.Total != 1 ||
		len(overview.Reviews.Items) != 1 ||
		overview.Reviews.Items[0].OpenFeedbackCount != 1 {
		t.Fatalf("unexpected overview reviews: %#v", overview.Reviews)
	}
	if overview.OpenFeedback.Total != 1 ||
		len(overview.OpenFeedback.Items) != 1 ||
		overview.OpenFeedback.Items[0].Body != "片尾字幕需要再停留两秒" ||
		overview.OpenFeedback.Items[0].TimeStartUS == nil ||
		*overview.OpenFeedback.Items[0].TimeStartUS != pointUs {
		t.Fatalf("unexpected overview feedback: %#v", overview.OpenFeedback)
	}
	if overview.RecentVersions.Total != 7 ||
		len(overview.RecentVersions.Items) != 6 ||
		overview.RecentVersions.Items[0].VersionNumber != 7 {
		t.Fatalf("unexpected overview versions: %#v", overview.RecentVersions)
	}
	if overview.FailedTasks.Total != 1 ||
		len(overview.FailedTasks.Items) != 1 ||
		overview.FailedTasks.Items[0].ID != "overview-current-job" ||
		overview.FailedTasks.Items[0].AssetID != "overview-asset" {
		t.Fatalf("unexpected overview failed tasks: %#v", overview.FailedTasks)
	}
}

func catalogTestDatabase(
	t *testing.T,
	ctx context.Context,
) (*sql.DB, identity.Session) {
	t.Helper()

	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	result, err := identity.NewService(identity.NewSQLiteRepository(db)).Setup(
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

	return db, result.Session
}

func insertTestAsset(
	t *testing.T,
	db *sql.DB,
	session identity.Session,
	id string,
	name string,
	assetType string,
	now time.Time,
) {
	t.Helper()

	timestamp := now.Format("2006-01-02T15:04:05.000000Z")
	if _, err := db.Exec(`
		INSERT INTO assets (
			id, workspace_id, type, name, status, created_by,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'active', ?, 1, ?, ?)
	`, id, session.Workspace.ID, assetType, name, session.User.ID, timestamp, timestamp); err != nil {
		t.Fatalf("insert test asset: %v", err)
	}
}

func insertProjectMembership(
	t *testing.T,
	db *sql.DB,
	session identity.Session,
	projectID string,
	userID string,
	roleKey string,
	permissionsJSON string,
	expiresAt *time.Time,
	now time.Time,
) {
	t.Helper()

	timestamp := now.Format(time.RFC3339Nano)
	var expires any
	if expiresAt != nil {
		expires = expiresAt.Format(time.RFC3339Nano)
	}
	id := "membership-" + projectID + "-" + userID
	if _, err := db.Exec(`
		INSERT INTO project_memberships (
			id, workspace_id, project_id, user_id, role_key, permissions_json,
			status, created_by, expires_at, joined_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)
	`,
		id,
		session.Workspace.ID,
		projectID,
		userID,
		roleKey,
		permissionsJSON,
		session.User.ID,
		expires,
		timestamp,
		timestamp,
		timestamp,
	); err != nil {
		t.Fatalf("insert project membership: %v", err)
	}
}
