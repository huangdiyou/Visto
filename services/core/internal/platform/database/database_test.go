package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestOpenAppliesMigrationsAndPersistsData(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "review-studio.db")
	ctx := context.Background()

	db, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (
			id, display_name, status, locale, created_at, updated_at
		) VALUES (
			'user-1', 'Owner', 'active', 'zh-CN',
			'2026-06-09T00:00:00Z', '2026-06-09T00:00:00Z'
		)
	`); err != nil {
		t.Fatalf("insert migrated table: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()

	var displayName string
	if err := reopened.QueryRowContext(
		ctx,
		"SELECT display_name FROM users WHERE id = 'user-1'",
	).Scan(&displayName); err != nil {
		t.Fatalf("read persisted data: %v", err)
	}

	if displayName != "Owner" {
		t.Fatalf("unexpected display name %q", displayName)
	}

	var migrationCount int
	if err := reopened.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}

	if migrationCount != 43 {
		t.Fatalf("expected forty-three migrations, got %d", migrationCount)
	}
}

func TestProjectScopedPermissionsMigrationBackfillsExistingData(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "review-studio.db")
	ctx := context.Background()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if err := configure(ctx, db); err != nil {
		t.Fatalf("configure database: %v", err)
	}
	if err := applyMigrationsThrough(ctx, db, 27); err != nil {
		t.Fatalf("apply old migrations: %v", err)
	}

	insertOldProjectData(t, ctx, db)

	if err := db.Close(); err != nil {
		t.Fatalf("close old database: %v", err)
	}

	upgraded, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("open upgraded database: %v", err)
	}
	defer upgraded.Close()

	var primaryOwner string
	if err := upgraded.QueryRowContext(
		ctx,
		"SELECT primary_owner_user_id FROM projects WHERE id = 'project-1'",
	).Scan(&primaryOwner); err != nil {
		t.Fatalf("read primary owner: %v", err)
	}
	if primaryOwner != "user-owner" {
		t.Fatalf("primary owner = %q, want user-owner", primaryOwner)
	}

	var projectRole, projectMemberStatus string
	if err := upgraded.QueryRowContext(ctx, `
		SELECT role_key, status
		FROM project_memberships
		WHERE project_id = 'project-1' AND user_id = 'user-owner'
	`).Scan(&projectRole, &projectMemberStatus); err != nil {
		t.Fatalf("read project membership: %v", err)
	}
	if projectRole != "primary_owner" || projectMemberStatus != "active" {
		t.Fatalf(
			"unexpected project membership role=%q status=%q",
			projectRole,
			projectMemberStatus,
		)
	}

	var projectAssetStatus string
	if err := upgraded.QueryRowContext(ctx, `
		SELECT status
		FROM project_assets
		WHERE project_id = 'project-1' AND asset_id = 'asset-1'
	`).Scan(&projectAssetStatus); err != nil {
		t.Fatalf("read project asset: %v", err)
	}
	if projectAssetStatus != "active" {
		t.Fatalf("project asset status = %q, want active", projectAssetStatus)
	}

	var registrationEnabled int
	var defaultRole string
	if err := upgraded.QueryRowContext(ctx, `
		SELECT registration_enabled, default_workspace_role
		FROM workspace_registration_settings
		WHERE workspace_id = 'workspace-1'
	`).Scan(&registrationEnabled, &defaultRole); err != nil {
		t.Fatalf("read registration settings: %v", err)
	}
	if registrationEnabled != 0 || defaultRole != "member" {
		t.Fatalf(
			"unexpected registration settings enabled=%d default=%q",
			registrationEnabled,
			defaultRole,
		)
	}
}

func TestRemoteProviderWideGrantMigrationCreatesRoot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "review-studio.db")
	ctx := context.Background()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if err := configure(ctx, db); err != nil {
		t.Fatalf("configure database: %v", err)
	}
	if err := applyMigrationsThrough(ctx, db, 34); err != nil {
		t.Fatalf("apply old migrations: %v", err)
	}

	for _, statement := range []string{
		`INSERT INTO users (
			id, email, display_name, status, locale, created_at, updated_at
		) VALUES (
			'user-owner', 'owner@example.com', 'Owner', 'active', 'zh-CN',
			'2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'
		)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN', 'Asia/Shanghai', '{}',
			'2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'
		)`,
		`INSERT INTO storage_providers (
			id, workspace_id, kind, name, status, config_json,
			capabilities_json, created_at, updated_at
		) VALUES (
			'provider-webdav', 'workspace-1', 'webdav', 'Remote DAV',
			'active', '{}', '{}',
			'2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'
		)`,
		`INSERT INTO project_storage_grants (
			id, workspace_id, storage_provider_id, authorized_root_id,
			status, granted_by, created_at, updated_at
		) VALUES (
			'grant-webdav', 'workspace-1', 'provider-webdav', NULL,
			'active', 'user-owner',
			'2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'
		)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert remote grant fixture: %v\n%s", err, statement)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close old database: %v", err)
	}

	upgraded, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("open upgraded database: %v", err)
	}
	defer upgraded.Close()

	var rootID, displayName, displayPath, mode, status, pathText string
	if err := upgraded.QueryRowContext(ctx, `
		SELECT
			project_storage_grants.authorized_root_id,
			authorized_roots.display_name,
			authorized_roots.display_path,
			authorized_roots.mode,
			authorized_roots.status,
			local_path_secrets.path_text
		FROM project_storage_grants
		JOIN authorized_roots
			ON authorized_roots.id = project_storage_grants.authorized_root_id
			AND authorized_roots.workspace_id = project_storage_grants.workspace_id
		JOIN local_path_secrets
			ON local_path_secrets.id = authorized_roots.path_secret_ref
			AND local_path_secrets.workspace_id = authorized_roots.workspace_id
		WHERE project_storage_grants.id = 'grant-webdav'
	`).Scan(&rootID, &displayName, &displayPath, &mode, &status, &pathText); err != nil {
		t.Fatalf("read migrated remote root: %v", err)
	}

	if rootID != "remote-root-grant-webdav" {
		t.Fatalf("root id = %q, want remote-root-grant-webdav", rootID)
	}
	if displayName != "Remote DAV" || displayPath != "/" || mode != "managed" || status != "available" || pathText != "" {
		t.Fatalf(
			"unexpected remote root display=%q path=%q mode=%q status=%q secret=%q",
			displayName,
			displayPath,
			mode,
			status,
			pathText,
		)
	}
}

func applyMigrationsThrough(
	ctx context.Context,
	db *sql.DB,
	maxVersion int,
) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		if version > maxVersion {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if err := applyMigration(
			ctx,
			db,
			version,
			entry.Name(),
			string(body),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertOldProjectData(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	statements := []string{
		`INSERT INTO users (
			id, email, display_name, status, locale, created_at, updated_at
		) VALUES (
			'user-owner', 'owner@example.com', 'Owner', 'active', 'zh-CN',
			'2026-06-09T00:00:00Z', '2026-06-09T00:00:00Z'
		)`,
		`INSERT INTO workspaces (
			id, name, status, default_locale, timezone, settings_json,
			created_at, updated_at
		) VALUES (
			'workspace-1', 'Workspace', 'active', 'zh-CN', 'Asia/Shanghai', '{}',
			'2026-06-09T00:00:00Z', '2026-06-09T00:00:00Z'
		)`,
		`INSERT INTO memberships (
			id, workspace_id, user_id, role_key, status, joined_at,
			created_at, updated_at
		) VALUES (
			'membership-1', 'workspace-1', 'user-owner', 'owner', 'active',
			'2026-06-09T00:00:00Z',
			'2026-06-09T00:00:00Z', '2026-06-09T00:00:00Z'
		)`,
		`INSERT INTO projects (
			id, workspace_id, name, status, created_by, revision,
			created_at, updated_at
		) VALUES (
			'project-1', 'workspace-1', 'Project', 'active', 'user-owner', 1,
			'2026-06-10T00:00:00Z', '2026-06-10T00:00:00Z'
		)`,
		`INSERT INTO assets (
			id, workspace_id, project_id, type, name, status, created_by,
			revision, created_at, updated_at
		) VALUES (
			'asset-1', 'workspace-1', 'project-1', 'video', 'Clip', 'active',
			'user-owner', 1,
			'2026-06-11T00:00:00Z', '2026-06-11T00:00:00Z'
		)`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("insert old data: %v\n%s", err, statement)
		}
	}
}
