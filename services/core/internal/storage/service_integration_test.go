package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestAuthorizedRootPersistsWithoutExposingLocalPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "review-studio.db")
	localPath := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(localPath, "poster.jpg"),
		[]byte("fixture"),
		0o600,
	); err != nil {
		t.Fatalf("write local fixture: %v", err)
	}

	db, session := storageTestDatabase(t, ctx, databasePath)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time {
		return time.Date(2026, time.June, 9, 11, 0, 0, 0, time.UTC)
	}

	root, err := service.RegisterLocalRoot(ctx, RegisterLocalRootInput{
		WorkspaceID: session.Workspace.ID,
		DisplayName: "工作素材",
		LocalPath:   localPath,
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register local root: %v", err)
	}
	if root.DisplayPath == localPath {
		t.Fatalf("display path exposed the raw local path %q", root.DisplayPath)
	}

	roots, err := service.ListRoots(ctx, session.Workspace.ID)
	if err != nil {
		t.Fatalf("list roots: %v", err)
	}
	if len(roots) != 1 || roots[0].ID != root.ID {
		t.Fatalf("unexpected roots: %#v", roots)
	}
	if _, err := service.Root(ctx, "another-workspace", root.ID); !errors.Is(err, ErrRootNotFound) {
		t.Fatalf("expected workspace isolation, got %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := database.Open(ctx, database.Config{Path: databasePath})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened database: %v", err)
		}
	})

	restarted := NewService(NewSQLiteRepository(reopened))
	adapter, err := restarted.LocalAdapter(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("restore local adapter: %v", err)
	}
	info, err := adapter.Stat(ctx, "poster.jpg")
	if err != nil {
		t.Fatalf("stat through restarted adapter: %v", err)
	}
	if info.ObjectKey != "poster.jpg" {
		t.Fatalf("unexpected object key %q", info.ObjectKey)
	}
}

func TestMultipleRootsReuseOneLocalProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	service := NewService(NewSQLiteRepository(db))

	for _, name := range []string{"素材 A", "素材 B"} {
		if _, err := service.RegisterLocalRoot(ctx, RegisterLocalRootInput{
			WorkspaceID: session.Workspace.ID,
			DisplayName: name,
			LocalPath:   t.TempDir(),
			Mode:        "referenced",
			ScanEnabled: true,
		}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	var providerCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM storage_providers
		WHERE workspace_id = ? AND kind = 'local' AND deleted_at IS NULL
	`, session.Workspace.ID).Scan(&providerCount); err != nil {
		t.Fatalf("count providers: %v", err)
	}
	if providerCount != 1 {
		t.Fatalf("expected one local provider, got %d", providerCount)
	}
}

func TestDirectoryScanDetectsLifecycleChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	localPath := t.TempDir()
	fileA := filepath.Join(localPath, "clip-a.txt")
	fileB := filepath.Join(localPath, "clip-b.txt")
	if err := os.WriteFile(fileA, []byte("version-one"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	now := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }
	root, err := service.RegisterLocalRoot(ctx, RegisterLocalRootInput{
		WorkspaceID: session.Workspace.ID,
		DisplayName: "扫描素材",
		LocalPath:   localPath,
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("register root: %v", err)
	}

	first, err := service.ScanRoot(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.Summary.New != 1 || first.Summary.Discovered != 1 {
		t.Fatalf("unexpected first scan: %#v", first.Summary)
	}
	objects, err := service.ListObjects(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("list first scan objects: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectKey != "clip-a.txt" {
		t.Fatalf("unexpected first objects: %#v", objects)
	}
	stableID := objects[0].ID
	var assetID string
	var firstVersion int
	if err := db.QueryRowContext(ctx, `
		SELECT assets.id, asset_versions.version_number
		FROM version_files
		JOIN asset_versions ON asset_versions.id = version_files.asset_version_id
		JOIN assets
			ON assets.id = asset_versions.asset_id
			AND assets.current_version_id = asset_versions.id
		WHERE version_files.storage_object_id = ?
	`, stableID).Scan(&assetID, &firstVersion); err != nil {
		t.Fatalf("read first logical asset: %v", err)
	}
	if assetID == "" || firstVersion != 1 {
		t.Fatalf("unexpected first logical asset: %q v%d", assetID, firstVersion)
	}

	now = now.Add(time.Minute)
	if err := os.WriteFile(fileA, []byte("version-two"), 0o600); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	modified, err := service.ScanRoot(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("modified scan: %v", err)
	}
	if modified.Summary.Modified != 1 {
		t.Fatalf("expected one modified file: %#v", modified.Summary)
	}
	var versionCount int
	var currentVersion int
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM asset_versions WHERE asset_id = ?),
			asset_versions.version_number
		FROM assets
		JOIN asset_versions ON asset_versions.id = assets.current_version_id
		WHERE assets.id = ?
	`, assetID, assetID).Scan(&versionCount, &currentVersion); err != nil {
		t.Fatalf("read modified logical asset: %v", err)
	}
	if versionCount != 2 || currentVersion != 2 {
		t.Fatalf(
			"expected logical asset V2, got count=%d current=%d",
			versionCount,
			currentVersion,
		)
	}

	now = now.Add(time.Minute)
	if err := os.Rename(fileA, fileB); err != nil {
		t.Fatalf("move file: %v", err)
	}
	moved, err := service.ScanRoot(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("moved scan: %v", err)
	}
	if moved.Summary.Moved != 1 {
		t.Fatalf("expected one moved file: %#v", moved.Summary)
	}
	objects, err = service.ListObjects(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("list moved objects: %v", err)
	}
	if len(objects) != 1 ||
		objects[0].ID != stableID ||
		objects[0].ObjectKey != "clip-b.txt" {
		t.Fatalf("move did not preserve object identity: %#v", objects)
	}

	now = now.Add(time.Minute)
	if err := os.Remove(fileB); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	missing, err := service.ScanRoot(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("missing scan: %v", err)
	}
	if missing.Summary.Missing != 1 {
		t.Fatalf("expected one missing file: %#v", missing.Summary)
	}

	now = now.Add(time.Minute)
	if err := os.WriteFile(fileB, []byte("version-two"), 0o600); err != nil {
		t.Fatalf("restore file: %v", err)
	}
	recovered, err := service.ScanRoot(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("recovered scan: %v", err)
	}
	if recovered.Summary.Recovered != 1 {
		t.Fatalf("expected one recovered file: %#v", recovered.Summary)
	}
	objects, err = service.ListObjects(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("list recovered objects: %v", err)
	}
	if len(objects) != 1 ||
		objects[0].ID != stableID ||
		objects[0].Status != "available" {
		t.Fatalf("recovery did not preserve object identity: %#v", objects)
	}

	refreshedRoot, err := service.Root(ctx, session.Workspace.ID, root.ID)
	if err != nil {
		t.Fatalf("read scanned root: %v", err)
	}
	if refreshedRoot.LastScanStatus == nil ||
		*refreshedRoot.LastScanStatus != "succeeded" ||
		refreshedRoot.LastScanSummary == nil {
		t.Fatalf("scan state was not persisted: %#v", refreshedRoot)
	}
}

func TestLocalManagedBucketDeletionRequiresNoReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, session := storageTestDatabase(
		t,
		ctx,
		filepath.Join(t.TempDir(), "review-studio.db"),
	)
	service := NewService(NewSQLiteRepository(db))

	emptyBucketPath := filepath.Join(t.TempDir(), "empty-bucket")
	emptyBucket, err := service.CreateLocalManagedBucket(
		ctx,
		CreateLocalManagedBucketInput{
			WorkspaceID:          session.Workspace.ID,
			DisplayName:          "Empty upload bucket",
			LocalPath:            emptyBucketPath,
			Purpose:              "upload",
			UploadSecurityPolicy: "standard",
			ProjectAvailable:     false,
			CreatedBy:            session.User.ID,
		},
	)
	if err != nil {
		t.Fatalf("create empty bucket: %v", err)
	}
	impact, err := service.LocalManagedBucketDeleteImpact(
		ctx,
		session.Workspace.ID,
		emptyBucket.ID,
	)
	if err != nil {
		t.Fatalf("load empty bucket delete impact: %v", err)
	}
	if !impact.CanDelete || impact.Counts.StorageObjects != 0 {
		t.Fatalf("expected empty bucket to be deletable: %#v", impact)
	}
	if err := service.DeleteLocalManagedBucket(
		ctx,
		LocalManagedBucketStateInput{
			WorkspaceID: session.Workspace.ID,
			ID:          emptyBucket.ID,
			Revision:    emptyBucket.Revision,
		},
	); err != nil {
		t.Fatalf("delete empty bucket: %v", err)
	}
	if _, err := service.LocalManagedBucket(
		ctx,
		session.Workspace.ID,
		emptyBucket.ID,
	); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("expected deleted bucket to disappear, got %v", err)
	}
	if _, err := os.Stat(emptyBucketPath); err != nil {
		t.Fatalf("delete should keep physical directory: %v", err)
	}

	referencedBucketPath := filepath.Join(t.TempDir(), "referenced-bucket")
	referencedBucket, err := service.CreateLocalManagedBucket(
		ctx,
		CreateLocalManagedBucketInput{
			WorkspaceID:          session.Workspace.ID,
			DisplayName:          "Referenced upload bucket",
			LocalPath:            referencedBucketPath,
			Purpose:              "upload",
			UploadSecurityPolicy: "standard",
			ProjectAvailable:     false,
			CreatedBy:            session.User.ID,
		},
	)
	if err != nil {
		t.Fatalf("create referenced bucket: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(referencedBucketPath, "source.mp4"),
		[]byte("media fixture"),
		0o600,
	); err != nil {
		t.Fatalf("write referenced fixture: %v", err)
	}
	if _, err := service.ScanRoot(
		ctx,
		session.Workspace.ID,
		referencedBucket.AuthorizedRootID,
	); err != nil {
		t.Fatalf("scan referenced bucket: %v", err)
	}
	impact, err = service.LocalManagedBucketDeleteImpact(
		ctx,
		session.Workspace.ID,
		referencedBucket.ID,
	)
	if err != nil {
		t.Fatalf("load referenced bucket delete impact: %v", err)
	}
	if impact.CanDelete || impact.Counts.StorageObjects != 1 ||
		impact.Counts.AssetVersions != 1 {
		t.Fatalf("expected referenced bucket to be blocked: %#v", impact)
	}
	if err := service.DeleteLocalManagedBucket(
		ctx,
		LocalManagedBucketStateInput{
			WorkspaceID: session.Workspace.ID,
			ID:          referencedBucket.ID,
			Revision:    referencedBucket.Revision,
		},
	); !errors.Is(err, ErrProviderInUse) {
		t.Fatalf("expected referenced bucket delete guard, got %v", err)
	}
}

func storageTestDatabase(
	t *testing.T,
	ctx context.Context,
	path string,
) (*sql.DB, identity.Session) {
	t.Helper()

	db, err := database.Open(ctx, database.Config{Path: path})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
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
