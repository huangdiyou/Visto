package hostbackup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseMetadataAcceptsCurrentFormat(t *testing.T) {
	path := writeMetadataFile(t, validMetadata("/srv/visto/data", nil))
	parsed, err := ParseMetadata(path)
	if err != nil {
		t.Fatalf("ParseMetadata failed: %v", err)
	}
	if parsed.Current == nil || parsed.Legacy != nil {
		t.Fatalf("expected current metadata, got %+v", parsed)
	}
	if parsed.Current.Source.DataDir != "/srv/visto/data" {
		t.Fatalf("source data dir = %q", parsed.Current.Source.DataDir)
	}
}

func TestParseMetadataClassifiesLegacyFormat(t *testing.T) {
	legacy := `{"schemaVersion":1,"createdAt":"2026-01-01T00:00:00Z","archive":"visto-server-data-x.tar.gz","sha256":"` +
		strings.Repeat("0", 64) + `","archiveRoot":"data"}`
	path := filepath.Join(t.TempDir(), "meta.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseMetadata(path)
	if err != nil {
		t.Fatalf("legacy metadata must parse, got %v", err)
	}
	if parsed.Legacy == nil || parsed.Current != nil {
		t.Fatalf("expected legacy classification, got %+v", parsed)
	}
}

func TestParseMetadataRejectsUnknownAndBrokenFormats(t *testing.T) {
	cases := map[string]string{
		"future format": `{"schemaVersion":3}`,
		"no version":    `{"archive":"x.tar.gz"}`,
		"broken json":   `{schemaVersion`,
	}
	for name, payload := range cases {
		path := filepath.Join(t.TempDir(), "meta.json")
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseMetadata(path); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestParseMetadataRejectsIncompleteCurrentFormat(t *testing.T) {
	base := validMetadata("/srv/visto/data", nil)
	base.SHA256 = "not-a-hash"
	path := writeMetadataFile(t, base)
	if _, err := ParseMetadata(path); err == nil {
		t.Fatal("metadata with a broken checksum must be rejected")
	}
}

func TestComposeRecordsNormalizedSourceAndRoots(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	writeFixtureDataDirectoryAt(t, dataDir, []fixtureRoot{
		{ID: "root-managed", Kind: "local", Purpose: "managed_versions", Path: filepath.Join(dataDir, "sources"), Status: "available"},
		{ID: "root-external", Kind: "local", Purpose: "library", Path: "/Volumes/media/library", Status: "available"},
		{ID: "root-remote", Kind: "s3", Purpose: "library", Path: "", Status: "available"},
	})
	facts, err := ReadDatabaseFacts(dataDir)
	if err != nil {
		t.Fatalf("ReadDatabaseFacts failed: %v", err)
	}
	metadata, err := Compose(ComposeInput{
		DataDir:        dataDir,
		ArchiveName:    "visto-server-data-x.tar.gz",
		SHA256:         strings.Repeat("a", 64),
		Platform:       PlatformMacOS,
		ProgramVersion: "1.0.0-rc.1",
	}, facts.Roots, nil, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Compose failed: %v", err)
	}
	if metadata.SchemaVersion != SchemaVersion {
		t.Fatalf("schemaVersion = %d", metadata.SchemaVersion)
	}
	if !metadata.StorageRootsRecorded || len(metadata.StorageRoots) != 3 {
		t.Fatalf("expected three recorded roots, got %+v", metadata.StorageRoots)
	}
	byID := map[string]StoredStorageRoot{}
	for _, root := range metadata.StorageRoots {
		byID[root.ID] = root
	}
	if !byID["root-managed"].IncludedInBackup {
		t.Fatal("managed root inside the data directory must be recorded as included")
	}
	if byID["root-external"].IncludedInBackup {
		t.Fatal("external root must be recorded as not included")
	}
	if byID["root-remote"].IncludedInBackup {
		t.Fatal("remote roots are never part of a data-directory backup")
	}
	if metadata.ArchiveRoot != filepath.Base(dataDir) {
		t.Fatalf("archiveRoot = %q", metadata.ArchiveRoot)
	}
}

func TestComposeRejectsUnreadableDatabase(t *testing.T) {
	emptyDir := t.TempDir()
	metadata, err := Compose(ComposeInput{
		DataDir:     emptyDir,
		ArchiveName: "visto-server-data-x.tar.gz",
		SHA256:      strings.Repeat("b", 64),
		Platform:    PlatformLinux,
	}, nil, os.ErrNotExist, time.Now())
	if err == nil || metadata != nil {
		t.Fatal("unverified database facts must fail without metadata")
	}
}

func TestComposeRejectsRelativeDataDirAndBadChecksum(t *testing.T) {
	if _, err := Compose(ComposeInput{
		DataDir: "relative/data", ArchiveName: "a.tar.gz",
		SHA256: strings.Repeat("a", 64), Platform: PlatformLinux,
	}, nil, nil, time.Now()); err == nil {
		t.Fatal("relative data directories must be rejected")
	}
	if _, err := Compose(ComposeInput{
		DataDir: "/srv/visto/data", ArchiveName: "a.tar.gz",
		SHA256: "zz", Platform: PlatformLinux,
	}, nil, nil, time.Now()); err == nil {
		t.Fatal("malformed checksums must be rejected")
	}
	if _, err := Compose(ComposeInput{
		DataDir: "/srv/visto/data", ArchiveName: "nested/a.tar.gz",
		SHA256: strings.Repeat("a", 64), Platform: PlatformLinux,
	}, nil, nil, time.Now()); err == nil {
		t.Fatal("archive names with path separators must be rejected")
	}
}

func TestWriteMetadataRestrictsFilePermissions(t *testing.T) {
	metadata := validMetadata("/srv/visto/data", nil)
	output := filepath.Join(t.TempDir(), "meta.json")
	if _, err := WriteMetadata(metadata, output); err != nil {
		t.Fatalf("WriteMetadata failed: %v", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("metadata permissions %v must be owner-only", perm)
	}
}
