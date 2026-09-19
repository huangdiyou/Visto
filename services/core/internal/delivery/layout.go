package delivery

import "strings"

// Layout is the frozen on-host directory contract for one platform.
//
// Rules that hold for every platform:
//   - An application update replaces the program directory only. It never
//     overwrites ConfigDir, DataDir, BackupDir or the media runtime.
//   - RuntimeDir and CacheDir live outside the immutable program directory so
//     an application update can reuse an unchanged media runtime and cache.
//   - Uninstall keeps DataDir, BackupDir and, for a Visto-managed runtime,
//     asks explicitly before removing RuntimeDir.
type Layout struct {
	Platform string `json:"platform"`
	// ProgramDir is the install prefix: /opt/visto, /Library/Visto or the
	// Windows program directory.
	ProgramDir string `json:"programDir"`
	// ReleasesDir holds the immutable per-version release directories. Windows
	// keeps a single program directory and does not use it.
	ReleasesDir string `json:"releasesDir,omitempty"`
	// CurrentDir is the active program root: a symlink on Linux and macOS, the
	// program directory itself on Windows.
	CurrentDir string `json:"currentDir"`
	// ScriptDir is the directory administrators run the host scripts from.
	ScriptDir string `json:"scriptDir"`
	// ConfigDir holds host configuration. Linux and macOS read visto.env from
	// here; Windows ships visto-server.json in the program directory.
	ConfigDir string `json:"configDir"`
	// DataDir holds the database, secrets, uploads and derived media.
	DataDir string `json:"dataDir"`
	// BackupDir holds administrator backups and restore points.
	BackupDir string `json:"backupDir"`
	// RecoveryDir holds update recovery points and failure reports.
	RecoveryDir string `json:"recoveryDir"`
	// LogDir holds service logs when the platform writes them to disk.
	LogDir string `json:"logDir"`
	// RuntimeDir holds the Visto-managed media runtime.
	RuntimeDir string `json:"runtimeDir"`
	// CacheDir holds reusable download and transcode cache.
	CacheDir string `json:"cacheDir"`
}

// DefaultLayout returns the default directory contract for a platform using
// its default program prefix.
func DefaultLayout(platform Platform) Layout {
	return LayoutForPrefix(platform, platform.DefaultPrefix())
}

// LayoutForPrefix returns the directory contract for a platform and an
// explicit install prefix. Overrides are only honoured for the program prefix;
// data, backup, log, runtime and cache roots stay outside it so an application
// update cannot remove them.
func LayoutForPrefix(platform Platform, prefix string) Layout {
	prefix = strings.TrimRight(prefix, `/\`)
	switch platform.OS {
	case "linux":
		return Layout{
			Platform:    platform.ID,
			ProgramDir:  prefix,
			ReleasesDir: prefix + "/releases",
			CurrentDir:  prefix + "/current",
			ScriptDir:   platform.InstalledScriptDirectory(prefix),
			ConfigDir:   "/etc/visto",
			DataDir:     "/var/lib/visto",
			BackupDir:   "/var/backups/visto",
			RecoveryDir: prefix + "/recovery",
			LogDir:      "/var/log/visto",
			RuntimeDir:  "/var/lib/visto/runtime",
			CacheDir:    "/var/lib/visto/cache",
		}
	case "macos":
		support := "/Library/Application Support/Visto"
		return Layout{
			Platform:    platform.ID,
			ProgramDir:  prefix,
			ReleasesDir: prefix + "/releases",
			CurrentDir:  prefix + "/current",
			ScriptDir:   platform.InstalledScriptDirectory(prefix),
			ConfigDir:   prefix + "/config",
			DataDir:     support + "/data",
			BackupDir:   support + "/backups",
			RecoveryDir: prefix + "/recovery",
			LogDir:      "/Library/Logs/Visto",
			RuntimeDir:  support + "/runtime",
			CacheDir:    support + "/cache",
		}
	default:
		programData := `%ProgramData%\Visto`
		return Layout{
			Platform:    platform.ID,
			ProgramDir:  prefix,
			CurrentDir:  prefix,
			ScriptDir:   prefix,
			ConfigDir:   prefix,
			DataDir:     programData + `\data`,
			BackupDir:   programData + `\backups`,
			RecoveryDir: programData + `\recovery`,
			LogDir:      programData + `\logs`,
			RuntimeDir:  programData + `\runtime`,
			CacheDir:    programData + `\cache`,
		}
	}
}
