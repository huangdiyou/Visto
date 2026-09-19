// Package hostbackup implements the data side of the host backup and restore
// scripts: backup metadata, storage-root facts read from an isolated database
// copy, and the restore path precheck.
//
// The restore contract only allows a backup to be swapped back into the exact
// data directory it was taken from. Cross-directory restore would silently
// keep user-created storage roots pointing at the old host paths, so the
// precheck must refuse it before the scripts stop the service or replace any
// data. Every rejection here is a "keep the current data untouched" decision,
// never a warning after the fact.
package hostbackup

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SchemaVersion is the current backup metadata format. Version 1 archives only
// record the archive root name and cannot prove which data directory a backup
// came from, so they are always rejected by the restore precheck.
const SchemaVersion = 2

// LegacySchemaVersion is the pre-path-protection metadata format.
const LegacySchemaVersion = 1

// Platform identifiers use the release-contract family names, not GOOS: a
// darwin binary records "macos".
const (
	PlatformLinux   = "linux"
	PlatformMacOS   = "macos"
	PlatformWindows = "windows"
)

// ReasonLegacyBackup is the operator-facing rejection for schemaVersion 1
// backups. It is shared by the precheck command and the scripts so a legacy
// archive is refused with the same explanation everywhere.
const ReasonLegacyBackup = "backup metadata uses the legacy format (schemaVersion 1) without a source data directory, " +
	"so the restore cannot prove it would land in the original data directory; " +
	"current data was not changed. Create a fresh backup only if the original data still exists and is readable; " +
	"if only the legacy archive remains, preserve it and its metadata. This release has no supported recovery or conversion path for that archive; do not relabel it as v2."

// ReasonUnsupportedMetadata is the rejection for unknown future formats.
const ReasonUnsupportedMetadata = "backup metadata schemaVersion is not supported; current data was not changed."

// BackupSource records where a backup was taken. DataDir is the normalized
// (absolute, symlink-resolved) data directory of the source instance.
type BackupSource struct {
	Platform string `json:"platform"`
	DataDir  string `json:"dataDir"`
}

// StoredStorageRoot mirrors one local root at backup time. Path is the raw
// path as stored in the database; normalization happens at precheck time so
// the two sides are compared with one implementation.
type StoredStorageRoot struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Purpose          string `json:"purpose"`
	Path             string `json:"path"`
	IncludedInBackup bool   `json:"includedInBackup"`
}

// BackupMetadata is the schemaVersion 2 backup metadata document. It contains
// absolute host paths, so it is sensitive operations material: the scripts keep
// it next to the archive with owner-only permissions and it must never be
// returned by a web surface, committed to Git or shipped inside a diagnostics
// bundle.
type BackupMetadata struct {
	SchemaVersion        int                 `json:"schemaVersion"`
	CreatedAt            string              `json:"createdAt"`
	Archive              string              `json:"archive"`
	SHA256               string              `json:"sha256"`
	ArchiveRoot          string              `json:"archiveRoot,omitempty"`
	Source               BackupSource        `json:"source"`
	ProgramVersion       string              `json:"programVersion"`
	StorageRoots         []StoredStorageRoot `json:"storageRoots,omitempty"`
	StorageRootsRecorded bool                `json:"storageRootsRecorded"`
	StorageRootsNote     string              `json:"storageRootsNote,omitempty"`
	ComposeProject       string              `json:"composeProject,omitempty"`
	DataVolume           string              `json:"dataVolume,omitempty"`
}

// LegacyMetadata carries the fields the scripts already validated on a
// schemaVersion 1 archive so the legacy rejection can stay specific.
type LegacyMetadata struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Archive        string `json:"archive"`
	SHA256         string `json:"sha256"`
	ArchiveRoot    string `json:"archiveRoot,omitempty"`
	ComposeProject string `json:"composeProject,omitempty"`
	DataVolume     string `json:"dataVolume,omitempty"`
}

// ParsedMetadata is either a current or a legacy metadata document.
type ParsedMetadata struct {
	Current *BackupMetadata
	Legacy  *LegacyMetadata
}

// ParseMetadata reads a metadata document and classifies it as current,
// legacy or unsupported. Unsupported formats return an error; legacy formats
// return the parsed legacy fields so callers keep their existing validations.
func ParseMetadata(path string) (*ParsedMetadata, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup metadata: %w", err)
	}
	return ParseMetadataBytes(raw)
}

// ParseMetadataBytes classifies metadata bytes; see ParseMetadata.
func ParseMetadataBytes(raw []byte) (*ParsedMetadata, error) {
	var probe struct {
		SchemaVersion *int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("backup metadata is not valid JSON: %w", err)
	}
	if probe.SchemaVersion == nil {
		return nil, fmt.Errorf("backup metadata does not record a schemaVersion")
	}
	switch *probe.SchemaVersion {
	case SchemaVersion:
		metadata := &BackupMetadata{}
		if err := json.Unmarshal(raw, metadata); err != nil {
			return nil, fmt.Errorf("backup metadata does not match schemaVersion %d: %w", SchemaVersion, err)
		}
		if problem := validateCurrentMetadata(metadata); problem != "" {
			return nil, fmt.Errorf("%s", problem)
		}
		return &ParsedMetadata{Current: metadata}, nil
	case LegacySchemaVersion:
		legacy := &LegacyMetadata{}
		if err := json.Unmarshal(raw, legacy); err != nil {
			return nil, fmt.Errorf("legacy backup metadata is not valid JSON: %w", err)
		}
		return &ParsedMetadata{Legacy: legacy}, nil
	default:
		return nil, fmt.Errorf("%s (found schemaVersion %d)", ReasonUnsupportedMetadata, *probe.SchemaVersion)
	}
}

func validateCurrentMetadata(metadata *BackupMetadata) string {
	if metadata.SchemaVersion != SchemaVersion {
		return "backup metadata schemaVersion mismatch"
	}
	if strings.TrimSpace(metadata.Archive) == "" {
		return "backup metadata does not record the archive name"
	}
	if !isSHA256(metadata.SHA256) {
		return "backup metadata does not record a valid SHA-256"
	}
	if strings.TrimSpace(metadata.CreatedAt) == "" {
		return "backup metadata does not record a creation time"
	}
	if problem := validateSource(metadata.Source); problem != "" {
		return problem
	}
	return ""
}

func validateSource(source BackupSource) string {
	if source.Platform != PlatformLinux && source.Platform != PlatformMacOS && source.Platform != PlatformWindows {
		return "backup metadata does not record a known source platform"
	}
	if !strings.HasPrefix(source.DataDir, "/") && !isWindowsAbsolute(source.DataDir) {
		return "backup metadata does not record an absolute source data directory"
	}
	return ""
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range strings.ToLower(value) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func isWindowsAbsolute(path string) bool {
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}
