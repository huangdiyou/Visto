package hostbackup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ComposeInput describes what a backup script knows when it asks for a
// metadata document. ArchiveName and SHA256 come from the finished archive;
// DataDir is the quiesced data directory that was archived.
type ComposeInput struct {
	DataDir        string
	ArchiveName    string
	SHA256         string
	Platform       string
	ProgramVersion string
	// ArchiveRoot is the top-level directory inside the archive. Native and
	// Windows backups wrap the data directory, Docker archives contain the
	// volume contents directly and leave this empty.
	ArchiveRoot string
	// OmitArchiveRoot records that the archive has no root directory at all
	// (Docker volume-content archives); no default name is derived.
	OmitArchiveRoot bool
	// Docker deployment identity, empty on native installs.
	ComposeProject string
	DataVolume     string
}

// Compose requires verified database facts; an unusable recovery point is a backup failure.
func Compose(input ComposeInput, roots []StorageRootFact, factsErr error, now time.Time) (*BackupMetadata, error) {
	if strings.TrimSpace(input.DataDir) == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if !filepath.IsAbs(filepath.Clean(input.DataDir)) {
		return nil, fmt.Errorf("data directory must be recorded as an absolute path: %s", input.DataDir)
	}
	if strings.TrimSpace(input.ArchiveName) == "" {
		return nil, fmt.Errorf("archive name is required")
	}
	if strings.ContainsAny(input.ArchiveName, `/\`) || input.ArchiveName != filepath.Base(input.ArchiveName) {
		return nil, fmt.Errorf("archive name must be a file name without path separators")
	}
	if !isSHA256(input.SHA256) {
		return nil, fmt.Errorf("archive checksum must be a hex-encoded SHA-256")
	}

	normalized := NormalizeDirectory(input.DataDir)
	metadata := &BackupMetadata{
		SchemaVersion:  SchemaVersion,
		CreatedAt:      now.UTC().Format("2006-01-02T15:04:05Z"),
		Archive:        input.ArchiveName,
		SHA256:         strings.ToLower(input.SHA256),
		ArchiveRoot:    input.ArchiveRoot,
		Source:         BackupSource{Platform: input.Platform, DataDir: normalized.Path},
		ProgramVersion: input.ProgramVersion,
		ComposeProject: input.ComposeProject,
		DataVolume:     input.DataVolume,
	}
	if input.ArchiveRoot == "" && !input.OmitArchiveRoot {
		metadata.ArchiveRoot = filepath.Base(normalized.Path)
	}
	if factsErr != nil {
		return nil, fmt.Errorf("cannot record verified database facts: %w", factsErr)
	}
	stored := make([]StoredStorageRoot, 0, len(roots))
	for _, root := range roots {
		stored = append(stored, StoredStorageRoot{
			ID:               root.RootID,
			Kind:             root.Kind,
			Purpose:          root.Purpose,
			Path:             root.PathText,
			IncludedInBackup: WithinDirectory(NormalizeDirectory(root.PathText).Path, normalized.Path),
		})
	}
	metadata.StorageRoots = stored
	metadata.StorageRootsRecorded = true
	return metadata, nil
}

// WriteMetadata serializes the document. With an output path the file is
// written with owner-only permissions because it carries absolute host paths.
func WriteMetadata(metadata *BackupMetadata, outputPath string) ([]byte, error) {
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup metadata: %w", err)
	}
	payload = append(payload, '\n')
	if strings.TrimSpace(outputPath) == "" {
		return payload, nil
	}
	if err := os.WriteFile(outputPath, payload, 0o600); err != nil {
		return nil, fmt.Errorf("write backup metadata: %w", err)
	}
	return payload, nil
}

// sanitizeNote strips characters that would break the embedded JSON string.
func sanitizeNote(reason string) string {
	return strings.TrimSpace(strings.Map(func(character rune) rune {
		switch character {
		case '"', '\\', '\n', '\r', '\t':
			return ' '
		}
		return character
	}, reason))
}
