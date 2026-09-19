package hostbackup

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// fixtureRoot is a storage root planted into a fixture database.
type fixtureRoot struct {
	ID      string
	Kind    string
	Purpose string
	Path    string
	Status  string
}

const fixtureSchema = `
CREATE TABLE storage_providers (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	status TEXT NOT NULL,
	config_json TEXT NOT NULL DEFAULT '{}',
	secret_ref TEXT,
	capabilities_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	deleted_at TEXT
);
CREATE TABLE local_path_secrets (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	path_text TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE authorized_roots (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	storage_provider_id TEXT NOT NULL,
	display_name TEXT NOT NULL,
	display_path TEXT NOT NULL,
	path_secret_ref TEXT,
	platform_bookmark BLOB,
	mode TEXT NOT NULL DEFAULT 'managed',
	scan_enabled INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'available',
	revision INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	deleted_at TEXT,
	purpose TEXT NOT NULL DEFAULT 'library'
);
`

// writeFixtureDataDirectoryAt creates the given data directory with a database
// containing the given roots.
func writeFixtureDataDirectoryAt(t *testing.T, dataDir string, roots []fixtureRoot) string {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, DatabaseFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(fixtureSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 41`); err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		providerID := "provider-" + root.ID
		secretID := "secret-" + root.ID
		if _, err := db.Exec(
			`INSERT INTO storage_providers (id, workspace_id, kind, name, status, created_at, updated_at)
			 VALUES (?, 'ws', ?, ?, 'active', '2026-01-01', '2026-01-01')`,
			providerID, root.Kind, "provider "+root.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO local_path_secrets (id, workspace_id, path_text, created_at, updated_at)
			 VALUES (?, 'ws', ?, '2026-01-01', '2026-01-01')`,
			secretID, root.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO authorized_roots (id, workspace_id, storage_provider_id, display_name, display_path, path_secret_ref, status, purpose, created_at, updated_at)
			 VALUES (?, 'ws', ?, ?, ?, ?, ?, ?, '2026-01-01', '2026-01-01')`,
			root.ID, providerID, "root "+root.ID, root.Path, secretID, root.Status, root.Purpose); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

func writeMetadataFile(t *testing.T, metadata *BackupMetadata) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.tar.gz.json")
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validMetadata(sourceDataDir string, roots []StoredStorageRoot) *BackupMetadata {
	return &BackupMetadata{
		SchemaVersion:        SchemaVersion,
		CreatedAt:            "2026-09-10T00:00:00Z",
		Archive:              "visto-server-data-20260910.tar.gz",
		SHA256:               "0011223344556677889900aabbccddeeff00112233445566778899aabbccddee",
		ArchiveRoot:          filepath.Base(sourceDataDir),
		Source:               BackupSource{Platform: PlatformMacOS, DataDir: sourceDataDir},
		ProgramVersion:       "1.0.0-rc.1",
		StorageRoots:         roots,
		StorageRootsRecorded: true,
	}
}

func findCheck(t *testing.T, verdict *Verdict, name string) PrecheckCheck {
	t.Helper()
	for _, check := range verdict.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %q missing from verdict", name)
	return PrecheckCheck{}
}

func assertRejected(t *testing.T, verdict *Verdict, reasonFragment string) {
	t.Helper()
	if !verdict.Rejected() {
		t.Fatalf("expected rejection, got decision %q with reasons %v", verdict.Decision, verdict.Reasons)
	}
	for _, reason := range verdict.Reasons {
		if strings.Contains(reason, reasonFragment) {
			return
		}
	}
	t.Fatalf("rejection reasons %v do not mention %q", verdict.Reasons, reasonFragment)
}
