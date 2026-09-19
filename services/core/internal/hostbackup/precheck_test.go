package hostbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagedDirectoryHash(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	for _, entry := range entries {
		payload, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		digest.Write([]byte(entry.Name()))
		digest.Write(payload)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// fixtureRootSet builds the standard three-root scenario: the app-managed
// upload root inside dataDir, an owner-created local bucket outside of it, and
// one remote S3 root.
func fixtureRootSet(dataDir, externalBucket string) ([]fixtureRoot, []StoredStorageRoot) {
	roots := []fixtureRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: filepath.Join(dataDir, "sources"), Status: "available"},
		{ID: "root-external", Kind: "local", Purpose: "library", Path: externalBucket, Status: "available"},
		{ID: "root-remote", Kind: "s3", Purpose: "library", Path: "", Status: "available"},
	}
	stored := []StoredStorageRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: filepath.Join(dataDir, "sources"), IncludedInBackup: true},
		{ID: "root-external", Kind: "local", Purpose: "library", Path: externalBucket, IncludedInBackup: false},
		{ID: "root-remote", Kind: "s3", Purpose: "library", Path: "", IncludedInBackup: false},
	}
	return roots, stored
}

func mustPrecheck(t *testing.T, metadataPath, staged, target, platform string) *Verdict {
	t.Helper()
	verdict, err := Precheck(PrecheckConfig{
		MetadataPath:  metadataPath,
		StagedDataDir: staged,
		TargetDataDir: target,
		Platform:      platform,
	})
	if err != nil {
		t.Fatalf("Precheck returned an infrastructure error: %v", err)
	}
	return verdict
}

func TestPrecheckAllowsSamePathRestore(t *testing.T) {
	externalBucket := filepath.Join(t.TempDir(), "media-library")
	if err := os.Mkdir(externalBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	roots, stored := fixtureRootSet(dataDir, externalBucket)
	writeFixtureDataDirectoryAt(t, dataDir, roots)
	metadataPath := writeMetadataFile(t, validMetadata(dataDir, stored))

	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	if verdict.Rejected() {
		t.Fatalf("same-path restore must pass, got reasons: %v", verdict.Reasons)
	}
	if verdict.NeedsUnsupportedMigration {
		t.Fatal("same-path restore must not need a migration")
	}
	if got := findCheck(t, verdict, "data_directory"); got.Status != CheckPass {
		t.Fatalf("data_directory check = %+v", got)
	}
	if got := findCheck(t, verdict, "managed_root"); got.Status != CheckPass {
		t.Fatalf("managed_root check = %+v", got)
	}
	byID := map[string]PrecheckRoot{}
	for _, root := range verdict.StorageRoots {
		byID[root.ID] = root
	}
	if !byID["root-managed"].IncludedInBackup {
		t.Fatal("managed root must be classified as inside the backup")
	}
	if byID["root-external"].IncludedInBackup {
		t.Fatal("external bucket must not be classified as backed up")
	}
	if !byID["root-external"].Exists {
		t.Fatal("external bucket path exists and should be reported as existing")
	}
	if len(verdict.Warnings) == 0 || !strings.Contains(strings.Join(verdict.Warnings, "\n"), "never moves or copies") {
		t.Fatalf("external buckets must be reported as kept in place, warnings: %v", verdict.Warnings)
	}
}

func TestPrecheckRejectsCrossDirectoryRestore(t *testing.T) {
	externalBucket := filepath.Join(t.TempDir(), "media-library")
	if err := os.Mkdir(externalBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	roots, stored := fixtureRootSet("/srv/visto-old/data", externalBucket)
	dataDir := writeFixtureDataDirectoryAt(t, filepath.Join(t.TempDir(), "data"), roots)
	metadataPath := writeMetadataFile(t, validMetadata("/srv/visto-old/data", stored))

	before := stagedDirectoryHash(t, dataDir)
	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	assertRejected(t, verdict, "restoring into a different data directory is not supported")
	if !verdict.NeedsUnsupportedMigration {
		t.Fatal("cross-directory restore must be flagged as needing an unsupported migration")
	}
	if after := stagedDirectoryHash(t, dataDir); after != before {
		t.Fatal("a rejected precheck must not modify the staged data")
	}
}

func TestPrecheckRejectsCrossDirectoryRestoreEvenWhenSourceDirExists(t *testing.T) {
	externalBucket := filepath.Join(t.TempDir(), "media-library")
	if err := os.Mkdir(externalBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	// The original data directory still exists on this host, which must not
	// make a restore into a different directory acceptable.
	sourceDataDir := filepath.Join(t.TempDir(), "old", "data")
	sourceRoots, stored := fixtureRootSet(sourceDataDir, externalBucket)
	writeFixtureDataDirectoryAt(t, sourceDataDir, sourceRoots)
	metadataPath := writeMetadataFile(t, validMetadata(sourceDataDir, stored))

	targetDataDir := filepath.Join(t.TempDir(), "new", "data")
	writeFixtureDataDirectoryAt(t, targetDataDir, []fixtureRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: filepath.Join(targetDataDir, "sources")},
	})

	verdict := mustPrecheck(t, metadataPath, sourceDataDir, targetDataDir, PlatformMacOS)
	assertRejected(t, verdict, "restoring into a different data directory is not supported")
}

func TestPrecheckProtectsCustomInstallDirectories(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "store")
	roots, stored := fixtureRootSet(dataDir, "")
	writeFixtureDataDirectoryAt(t, dataDir, roots)

	// A custom data directory restored to itself passes.
	metadata := validMetadata(dataDir, stored)
	metadata.Source.Platform = PlatformLinux
	metadataPath := writeMetadataFile(t, metadata)
	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformLinux)
	if verdict.Rejected() {
		t.Fatalf("custom same-path restore must pass, got %v", verdict.Reasons)
	}

	// The same custom directory moved elsewhere is rejected before any swap.
	moved := validMetadata("/mnt/custom-visto/install-43/store", stored)
	moved.ArchiveRoot = "store"
	movedPath := writeMetadataFile(t, moved)
	verdict = mustPrecheck(t, movedPath, dataDir, dataDir, PlatformLinux)
	assertRejected(t, verdict, "different data directory")
}

func TestPrecheckRejectsLegacyBackups(t *testing.T) {
	legacy := `{"schemaVersion":1,"createdAt":"2026-01-01T00:00:00Z","archive":"a.tar.gz","sha256":"` +
		strings.Repeat("0", 64) + `","archiveRoot":"data"}`
	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	verdict := mustPrecheck(t, path, t.TempDir(), t.TempDir(), PlatformMacOS)
	assertRejected(t, verdict, "legacy format")
}

func TestPrecheckRejectsMetadataDatabaseContradiction(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	writeFixtureDataDirectoryAt(t, dataDir, []fixtureRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: "/elsewhere/instance/data/sources"},
	})
	metadata := validMetadata(dataDir, []StoredStorageRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions",
			Path: filepath.Join(dataDir, "sources"), IncludedInBackup: true},
	})
	metadataPath := writeMetadataFile(t, metadata)

	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	assertRejected(t, verdict, "contradict")
}

func TestPrecheckRejectsRootSetMismatch(t *testing.T) {
	externalBucket := filepath.Join(t.TempDir(), "media-library")
	if err := os.Mkdir(externalBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	roots, stored := fixtureRootSet(dataDir, externalBucket)
	writeFixtureDataDirectoryAt(t, dataDir, roots)
	// Metadata claims one root fewer than the database contains: the metadata
	// was not written for this archive.
	metadataPath := writeMetadataFile(t, validMetadata(dataDir, stored[:1]))

	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	assertRejected(t, verdict, "do not match the archived database")
}

func TestPrecheckRejectsUnreadableDatabase(t *testing.T) {
	staged := t.TempDir()
	if err := os.WriteFile(filepath.Join(staged, DatabaseFileName), []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadataPath := writeMetadataFile(t, validMetadata(staged, nil))
	verdict := mustPrecheck(t, metadataPath, staged, staged, PlatformMacOS)
	assertRejected(t, verdict, "could not be verified")
}

func TestPrecheckRejectsMissingStagedDirectoryAndPlatformMismatch(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	writeFixtureDataDirectoryAt(t, dataDir, []fixtureRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: filepath.Join(dataDir, "sources")},
	})
	metadataPath := writeMetadataFile(t, validMetadata(dataDir, nil))

	verdict := mustPrecheck(t, metadataPath, filepath.Join(dataDir, "does-not-exist"), dataDir, PlatformMacOS)
	assertRejected(t, verdict, "missing or not a directory")

	verdict = mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformLinux)
	assertRejected(t, verdict, "cross-platform restore is not supported")
}

func TestPrecheckRejectsBrokenMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")
	if err := os.WriteFile(path, []byte("{\"schemaVersion\":3}"), 0o600); err != nil {
		t.Fatal(err)
	}
	verdict := mustPrecheck(t, path, t.TempDir(), t.TempDir(), PlatformMacOS)
	assertRejected(t, verdict, "schemaVersion")
}

func TestPrecheckRejectsArchiveRootMismatch(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	writeFixtureDataDirectoryAt(t, dataDir, []fixtureRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: filepath.Join(dataDir, "sources")},
	})
	metadata := validMetadata(dataDir, nil)
	metadata.ArchiveRoot = "somewhere-else"
	metadataPath := writeMetadataFile(t, metadata)

	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	assertRejected(t, verdict, "contradict")
}

func TestPrecheckWarnsAboutRemoteCredentials(t *testing.T) {
	externalBucket := filepath.Join(t.TempDir(), "media-library")
	if err := os.Mkdir(externalBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	roots, stored := fixtureRootSet(dataDir, externalBucket)
	writeFixtureDataDirectoryAt(t, dataDir, roots)
	metadataPath := writeMetadataFile(t, validMetadata(dataDir, stored))

	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	joined := strings.Join(verdict.Warnings, "\n")
	if !strings.Contains(joined, "remote storage credentials") || !strings.Contains(joined, "not part of a data-directory backup") {
		t.Fatalf("remote roots must produce the credentials warning, got: %v", verdict.Warnings)
	}
}

func TestRenderVerdictStatesOutcomeAndRoots(t *testing.T) {
	externalBucket := filepath.Join(t.TempDir(), "media-library")
	if err := os.Mkdir(externalBucket, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	roots, stored := fixtureRootSet(dataDir, externalBucket)
	writeFixtureDataDirectoryAt(t, dataDir, roots)
	metadataPath := writeMetadataFile(t, validMetadata(dataDir, stored))

	verdict := mustPrecheck(t, metadataPath, dataDir, dataDir, PlatformMacOS)
	rendered := RenderVerdict(verdict)
	if !strings.Contains(rendered, "Restore precheck: PASS") {
		t.Fatalf("rendered verdict missing PASS: %s", rendered)
	}
	if !strings.Contains(rendered, "inside the backup") || !strings.Contains(rendered, "outside the backup") {
		t.Fatalf("rendered verdict must classify storage roots: %s", rendered)
	}

	rejected := &Verdict{
		Decision:                  DecisionRestoreRejected,
		Reasons:                   []string{"boom"},
		NeedsUnsupportedMigration: true,
	}
	rendered = RenderVerdict(rejected)
	if !strings.Contains(rendered, "REJECTED") || !strings.Contains(rendered, "nothing was changed") {
		t.Fatalf("rendered rejection must state that nothing was changed: %s", rendered)
	}
}
