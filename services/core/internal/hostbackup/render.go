package hostbackup

import (
	"fmt"
	"strings"
)

// RenderVerdict formats a verdict for operators. Scripts relay these lines
// verbatim, so they must state the outcome, every rejection reason and the
// storage-root coverage without requiring JSON tooling.
func RenderVerdict(verdict *Verdict) string {
	lines := []string{}
	if verdict.Rejected() {
		lines = append(lines, "Restore precheck: REJECTED")
	} else {
		lines = append(lines, "Restore precheck: PASS")
	}
	if verdict.SourceDataDir != "" || verdict.TargetDataDir != "" {
		lines = append(lines, fmt.Sprintf("Source data directory: %s", displayPath(verdict.SourceDataDir)))
		lines = append(lines, fmt.Sprintf("Target data directory: %s", displayPath(verdict.TargetDataDir)))
		lines = append(lines, fmt.Sprintf("Platform: source %s / target %s", displayPlatform(verdict.SourcePlatform), displayPlatform(verdict.TargetPlatform)))
	}
	for _, check := range verdict.Checks {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", check.Status, check.Name, check.Detail))
	}
	for index, root := range verdict.StorageRoots {
		scope := "outside the backup"
		if root.IncludedInBackup {
			scope = "inside the backup"
		}
		state := ""
		if !root.IncludedInBackup && root.Kind == "local" && !root.Exists {
			state = " (path does not currently exist)"
		}
		lines = append(lines, fmt.Sprintf(
			"- storage root %d [%s] %s: %s%s",
			index+1, root.Kind, root.DisplayName, scope, state))
		if root.Path != "" {
			lines = append(lines, fmt.Sprintf("  path: %s", root.Path))
		}
	}
	for _, warning := range verdict.Warnings {
		lines = append(lines, fmt.Sprintf("WARNING: %s", warning))
	}
	if verdict.Rejected() {
		for _, reason := range verdict.Reasons {
			lines = append(lines, fmt.Sprintf("REJECTED: %s", reason))
		}
		if verdict.NeedsUnsupportedMigration {
			lines = append(lines, "This restore needs a data-directory migration that is not supported yet; nothing was changed.")
		}
	} else {
		lines = append(lines, "All restore path conditions passed. The caller may stop the service and replace the data directory.")
	}
	return strings.Join(lines, "\n")
}

func displayPlatform(platform string) string {
	if platform == "" {
		return "unknown"
	}
	return platform
}

func displayPath(path string) string {
	if path == "" {
		return "unknown"
	}
	return path
}
