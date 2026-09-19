package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"review-studio.local/core/internal/delivery"
	"review-studio.local/core/internal/hostbackup"
	"review-studio.local/core/internal/hostctl"
)

// runBackup implements the read-only host backup commands. They never stop a
// service or replace data: `backup metadata` turns a quiesced data directory
// into a schemaVersion 2 metadata document, and `backup precheck` decides
// whether a staged archive may be restored into a target data directory.
func runBackup(arguments []string, jsonOutput bool, config hostctl.Config, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		printUsage(stderr)
		return delivery.ExitInvalidArguments
	}
	switch arguments[0] {
	case "metadata":
		return runBackupMetadata(arguments[1:], jsonOutput, config, stdout, stderr)
	case "precheck":
		return runBackupPrecheck(arguments[1:], jsonOutput, stdout, stderr)
	default:
		printUsage(stderr)
		return delivery.ExitInvalidArguments
	}
}

func runBackupMetadata(arguments []string, jsonOutput bool, config hostctl.Config, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("visto-server backup metadata", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "data directory that was archived (absolute path)")
	archiveName := flags.String("archive", "", "archive file name")
	checksum := flags.String("sha256", "", "archive SHA-256 checksum (hex)")
	outputPath := flags.String("output", "", "metadata output path; stdout when empty")
	archiveRoot := flags.String("archive-root", "", "top-level directory inside the archive (default: data directory name)")
	noArchiveRoot := flags.Bool("no-archive-root", false, "record that the archive has no root directory (Docker)")
	composeProject := flags.String("compose-project", "", "Docker Compose project name")
	dataVolume := flags.String("data-volume", "", "Docker data volume name")
	if err := flags.Parse(arguments); err != nil {
		return delivery.ExitInvalidArguments
	}
	input := hostbackup.ComposeInput{
		DataDir:         *dataDir,
		ArchiveName:     *archiveName,
		SHA256:          strings.ToLower(strings.TrimSpace(*checksum)),
		Platform:        hostbackup.PlatformOf(runtime.GOOS),
		ProgramVersion:  config.Version,
		ArchiveRoot:     *archiveRoot,
		OmitArchiveRoot: *noArchiveRoot,
		ComposeProject:  *composeProject,
		DataVolume:      *dataVolume,
	}

	facts, factsErr := hostbackup.ReadDatabaseFacts(input.DataDir)
	if factsErr != nil {
		return fail(stdout, stderr, jsonOutput, "backup metadata",
			delivery.NewError(delivery.CodeInternalError, "backup failed: cannot verify database facts: "+factsErr.Error()))
	}
	roots := facts.Roots
	metadata, err := hostbackup.Compose(input, roots, factsErr, time.Now())
	if err != nil {
		return fail(stdout, stderr, jsonOutput, "backup metadata",
			delivery.NewError(delivery.CodeInvalidArguments, err.Error()))
	}
	payload, err := hostbackup.WriteMetadata(metadata, *outputPath)
	if err != nil {
		return fail(stdout, stderr, jsonOutput, "backup metadata",
			delivery.NewError(delivery.CodeInternalError, err.Error()))
	}
	if strings.TrimSpace(*outputPath) == "" {
		_, _ = stdout.Write(payload)
	}
	return delivery.ExitOK
}

func runBackupPrecheck(arguments []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("visto-server backup precheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	metadataPath := flags.String("metadata", "", "backup metadata JSON path")
	stagedDataDir := flags.String("staged-data-dir", "", "data directory extracted from the archive into an isolated location")
	targetDataDir := flags.String("target-data-dir", "", "live data directory the restore would replace")
	if err := flags.Parse(arguments); err != nil {
		return delivery.ExitInvalidArguments
	}
	if strings.TrimSpace(*metadataPath) == "" ||
		strings.TrimSpace(*stagedDataDir) == "" ||
		strings.TrimSpace(*targetDataDir) == "" {
		fmt.Fprintln(stderr, "backup precheck requires --metadata, --staged-data-dir and --target-data-dir.")
		return delivery.ExitInvalidArguments
	}

	verdict, err := hostbackup.Precheck(hostbackup.PrecheckConfig{
		MetadataPath:  *metadataPath,
		StagedDataDir: *stagedDataDir,
		TargetDataDir: *targetDataDir,
		Platform:      hostbackup.PlatformOf(runtime.GOOS),
	})
	if err != nil {
		return fail(stdout, stderr, jsonOutput, "backup precheck",
			delivery.NewError(delivery.CodeInternalError, err.Error()))
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(verdict); err != nil {
			return delivery.ExitInternal
		}
	} else {
		fmt.Fprintln(stdout, hostbackup.RenderVerdict(verdict))
	}
	if verdict.Rejected() {
		return delivery.ExitRestorePrecheckFailed
	}
	return delivery.ExitOK
}
