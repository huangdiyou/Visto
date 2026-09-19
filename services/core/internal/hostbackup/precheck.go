package hostbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Precheck decisions.
const (
	DecisionRestoreAllowed  = "restore_allowed"
	DecisionRestoreRejected = "restore_rejected"
)

// Check statuses reported per individual check.
const (
	CheckPass     = "pass"
	CheckRejected = "rejected"
	CheckWarning  = "warning"
	CheckInfo     = "info"
)

// PrecheckConfig describes one restore precheck run. StagedDataDir is the data
// directory extracted from the archive into an isolated location; TargetDataDir
// is the live data directory the service currently uses. The precheck only
// reads: it never stops a service, writes configuration, touches the live
// database or replaces data.
type PrecheckConfig struct {
	MetadataPath  string
	StagedDataDir string
	TargetDataDir string
	Platform      string
}

// PrecheckRoot is the restore-time classification of one storage root found in
// the archived database.
type PrecheckRoot struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	Kind             string `json:"kind"`
	Purpose          string `json:"purpose"`
	Path             string `json:"path"`
	IncludedInBackup bool   `json:"includedInBackup"`
	Exists           bool   `json:"exists"`
}

// PrecheckCheck is one named check with its outcome.
type PrecheckCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Verdict is the structured precheck result.
type Verdict struct {
	SchemaVersion             int             `json:"schemaVersion"`
	Decision                  string          `json:"decision"`
	Reasons                   []string        `json:"reasons"`
	Warnings                  []string        `json:"warnings"`
	Checks                    []PrecheckCheck `json:"checks"`
	StorageRoots              []PrecheckRoot  `json:"storageRoots"`
	SourcePlatform            string          `json:"sourcePlatform"`
	TargetPlatform            string          `json:"targetPlatform"`
	SourceDataDir             string          `json:"sourceDataDir"`
	TargetDataDir             string          `json:"targetDataDir"`
	NeedsUnsupportedMigration bool            `json:"needsUnsupportedMigration"`
}

// Rejected reports whether the precheck refused the restore.
func (verdict *Verdict) Rejected() bool {
	return verdict.Decision == DecisionRestoreRejected
}

// FirstReason returns the most important rejection reason for failure
// envelopes and script summaries.
func (verdict *Verdict) FirstReason() string {
	if len(verdict.Reasons) == 0 {
		return "restore precheck failed"
	}
	return verdict.Reasons[0]
}

// Precheck evaluates whether the staged backup may be restored into the target
// data directory. A rejection always means "current data was not changed": the
// caller must stop the service only after this returns an allowed verdict.
type rejection struct {
	reason string
	check  string
}

func Precheck(config PrecheckConfig) (*Verdict, error) {
	verdict := &Verdict{
		Decision:       DecisionRestoreAllowed,
		Reasons:        []string{},
		Warnings:       []string{},
		Checks:         []PrecheckCheck{},
		StorageRoots:   []PrecheckRoot{},
		TargetPlatform: config.Platform,
		TargetDataDir:  NormalizeDirectory(config.TargetDataDir).Path,
	}
	addCheck := func(name, status, detail string) {
		verdict.Checks = append(verdict.Checks, PrecheckCheck{Name: name, Status: status, Detail: detail})
	}
	reject := func(rejection rejection) {
		verdict.Decision = DecisionRestoreRejected
		verdict.Reasons = append(verdict.Reasons, rejection.reason)
		addCheck(rejection.check, CheckRejected, rejection.reason)
	}

	parsed, err := ParseMetadata(config.MetadataPath)
	if err != nil {
		verdict.SchemaVersion = 0
		reject(rejection{reason: err.Error(), check: "metadata"})
		return verdict, nil
	}
	if parsed.Legacy != nil {
		verdict.SchemaVersion = parsed.Legacy.SchemaVersion
		reject(rejection{reason: ReasonLegacyBackup, check: "metadata"})
		addCheck("metadata", CheckInfo, "legacy backups cannot prove their source data directory")
		return verdict, nil
	}
	metadata := parsed.Current
	verdict.SchemaVersion = metadata.SchemaVersion
	verdict.SourcePlatform = metadata.Source.Platform
	verdict.SourceDataDir = metadata.Source.DataDir
	addCheck("metadata", CheckPass, fmt.Sprintf(
		"schemaVersion %d, created %s, program %s",
		metadata.SchemaVersion, metadata.CreatedAt, metadata.ProgramVersion))

	sourceDataDir := NormalizeDirectory(metadata.Source.DataDir)

	// The archive layout and the recorded source must agree about the data
	// directory name; a mismatch means the metadata and the archive were put
	// together from different sources.
	if metadata.ArchiveRoot != "" &&
		filepath.Base(sourceDataDir.Path) != filepath.Clean(metadata.ArchiveRoot) {
		reject(rejection{
			reason: fmt.Sprintf(
				"backup metadata records source data directory %s but archive root %q; metadata and archive contradict each other. Current data was not changed.",
				sourceDataDir.Path, metadata.ArchiveRoot),
			check: "archive_root",
		})
	} else {
		addCheck("archive_root", CheckPass, metadata.ArchiveRoot)
	}

	if metadata.Source.Platform != config.Platform {
		verdict.NeedsUnsupportedMigration = true
		reject(rejection{
			reason: fmt.Sprintf(
				"backup was taken on %s but this host restores as %s; cross-platform restore is not supported. Current data was not changed.",
				metadata.Source.Platform, config.Platform),
			check: "platform",
		})
	} else {
		addCheck("platform", CheckPass, metadata.Source.Platform)
	}

	if !sourceDataDir.SameAs(NormalizeDirectory(config.TargetDataDir), config.Platform) {
		verdict.NeedsUnsupportedMigration = true
		reject(rejection{
			reason: fmt.Sprintf(
				"backup was taken from data directory %s but this instance uses %s; restoring into a different data directory is not supported because stored local storage roots would keep pointing at the old location. Current data was not changed. Use a migration capability instead of a restore.",
				sourceDataDir.Path, NormalizeDirectory(config.TargetDataDir).Path),
			check: "data_directory",
		})
	} else {
		addCheck("data_directory", CheckPass, sourceDataDir.Path)
	}

	// The staged copy must exist and, when the archive declares a root
	// directory, actually carry that name. Docker archives hold volume
	// contents without a root directory and skip the name comparison.
	stagedNormalized := NormalizeDirectory(config.StagedDataDir)
	stagedStat, stagedErr := os.Stat(config.StagedDataDir)
	switch {
	case stagedErr != nil || !stagedStat.IsDir():
		reject(rejection{
			reason: fmt.Sprintf(
				"staged backup data directory %s is missing or not a directory; the archive layout does not match the restore layout. Current data was not changed.",
				stagedNormalized.Path),
			check: "staged_data_directory",
		})
	case metadata.ArchiveRoot != "" && filepath.Base(stagedNormalized.Path) != filepath.Clean(metadata.ArchiveRoot):
		reject(rejection{
			reason: fmt.Sprintf(
				"archive data directory %q does not match the declared archive root %q; metadata and archive contradict each other. Current data was not changed.",
				filepath.Base(stagedNormalized.Path), metadata.ArchiveRoot),
			check: "staged_data_directory",
		})
	default:
		addCheck("staged_data_directory", CheckPass, stagedNormalized.Path)
	}

	facts, factsErr := ReadDatabaseFacts(config.StagedDataDir)
	if factsErr != nil {
		reject(rejection{
			reason: fmt.Sprintf(
				"the archived database could not be verified (%s); the restore cannot prove the stored storage paths match this instance. Current data was not changed.",
				factsErr),
			check: "database",
		})
		return verdict, nil
	}
	databaseDetail := fmt.Sprintf("schema generation %d, consistency check %s", facts.SchemaVersion, facts.QuickCheck)
	if facts.QuickCheck != "ok" {
		reject(rejection{
			reason: fmt.Sprintf(
				"the archived database failed its consistency check (%s); restoring it would replace the current data with a damaged database. Current data was not changed.",
				facts.QuickCheck),
			check: "database",
		})
	} else {
		addCheck("database", CheckPass, databaseDetail)
	}

	rootRejection := checkRoots(metadata, facts, sourceDataDir, config.Platform, verdict, addCheck)
	if rootRejection != nil {
		reject(*rootRejection)
	}

	warnAboutRemotes(facts, verdict)
	return verdict, nil
}

// checkRoots verifies that the archived database agrees with the metadata about
// the source data directory and classifies every root for the operator report.
func checkRoots(
	metadata *BackupMetadata,
	facts *DatabaseFacts,
	sourceDataDir NormalizedDirectory,
	platform string,
	verdict *Verdict,
	addCheck func(name, status, detail string),
) *rejection {
	managed := []StorageRootFact{}
	classified := make([]PrecheckRoot, 0, len(facts.Roots))
	for _, root := range facts.Roots {
		rootPath := NormalizeDirectory(root.PathText).Path
		included := root.Kind == "local" && root.PathText != "" &&
			WithinDirectory(rootPath, sourceDataDir.Path)
		if root.Purpose == "managed_versions" && root.Kind == "local" {
			managed = append(managed, root)
		}
		exists := false
		if root.PathText != "" {
			if _, err := os.Stat(root.PathText); err == nil {
				exists = true
			}
		}
		classified = append(classified, PrecheckRoot{
			ID:               root.RootID,
			DisplayName:      root.DisplayName,
			Kind:             root.Kind,
			Purpose:          root.Purpose,
			Path:             rootPath,
			IncludedInBackup: included,
			Exists:           exists,
		})
	}
	sort.Slice(classified, func(left, right int) bool {
		return classified[left].ID < classified[right].ID
	})
	verdict.StorageRoots = classified

	for _, root := range managed {
		if !WithinDirectory(NormalizeDirectory(root.PathText).Path, sourceDataDir.Path) {
			return &rejection{
				reason: fmt.Sprintf(
					"the archived database records the managed upload root at %s, outside the recorded source data directory %s; metadata and database contradict each other. Current data was not changed.",
					root.PathText, sourceDataDir.Path),
				check: "managed_root",
			}
		}
	}
	if len(managed) == 0 {
		addCheck("managed_root", CheckWarning,
			"no managed upload root found in the archived database; the backup may predate the first upload")
	} else {
		addCheck("managed_root", CheckPass,
			fmt.Sprintf("%d managed upload root(s) inside the source data directory", len(managed)))
	}

	// Cross-check the roots recorded at backup time against the archived
	// database. Both come from the same quiesced database, so any difference
	// means the metadata and the archive were assembled from different
	// sources.
	if metadata.StorageRootsRecorded {
		recorded := map[string]StoredStorageRoot{}
		for _, root := range metadata.StorageRoots {
			recorded[root.ID] = root
		}
		observed := map[string]StorageRootFact{}
		for _, root := range facts.Roots {
			observed[root.RootID] = root
		}
		if !sameRootSets(recorded, observed, sourceDataDir.Path) {
			return &rejection{
				reason: "the storage roots recorded in the backup metadata do not match the archived database; metadata and database contradict each other. Current data was not changed.",
				check:  "storage_roots",
			}
		}
		addCheck("storage_roots", CheckPass,
			fmt.Sprintf("%d recorded root(s) match the archived database", len(recorded)))
	} else {
		addCheck("storage_roots", CheckWarning,
			"backup metadata does not record storage roots; the archived database is the only path evidence")
	}

	for _, root := range classified {
		switch {
		case root.IncludedInBackup:
			continue
		case root.Kind != "local":
			continue
		case !root.Exists:
			verdict.Warnings = append(verdict.Warnings, fmt.Sprintf(
				"local storage root %q (%s) is outside the backup and its path does not currently exist: %s",
				root.DisplayName, root.ID, root.Path))
		default:
			verdict.Warnings = append(verdict.Warnings, fmt.Sprintf(
				"local storage root %q (%s) lives outside the backup at %s; restore keeps it there and never moves or copies it",
				root.DisplayName, root.ID, root.Path))
		}
	}
	return nil
}

func sameRootSets(recorded map[string]StoredStorageRoot, observed map[string]StorageRootFact, sourceDataDir string) bool {
	if len(recorded) != len(observed) {
		return false
	}
	for id, fact := range observed {
		stored, ok := recorded[id]
		if !ok {
			return false
		}
		if stored.Kind != fact.Kind || stored.Purpose != fact.Purpose {
			return false
		}
		if NormalizeDirectory(stored.Path).Path != NormalizeDirectory(fact.PathText).Path {
			return false
		}
		if stored.IncludedInBackup != (fact.Kind == "local" && fact.PathText != "" &&
			WithinDirectory(NormalizeDirectory(fact.PathText).Path, sourceDataDir)) {
			return false
		}
	}
	return true
}

func warnAboutRemotes(facts *DatabaseFacts, verdict *Verdict) {
	remotes := map[string]int{}
	for _, root := range facts.Roots {
		switch root.Kind {
		case "webdav", "s3", "official_cloud":
			remotes[root.Kind]++
		}
	}
	if len(remotes) == 0 {
		return
	}
	kinds := make([]string, 0, len(remotes))
	for kind, count := range remotes {
		kinds = append(kinds, fmt.Sprintf("%s x%d", kind, count))
	}
	sort.Strings(kinds)
	verdict.Warnings = append(verdict.Warnings,
		"remote storage credentials ("+strings.Join(kinds, ", ")+") are restored with the backup; "+
			"the remote bucket contents themselves are not part of a data-directory backup")
}
