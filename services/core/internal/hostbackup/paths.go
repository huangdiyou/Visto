package hostbackup

import (
	"os"
	"path/filepath"
	"strings"
)

// NormalizedDirectory is a data directory path brought into a canonical form.
// Path is absolute and cleaned. Resolved reports whether the filesystem could
// resolve every segment; a missing leaf keeps the cleaned suffix on top of the
// nearest existing resolved ancestor.
type NormalizedDirectory struct {
	Path     string
	Resolved bool
}

// NormalizeDirectory brings a directory path into its canonical form. Existing
// paths are resolved through the filesystem, which removes trailing
// separators, collapses ".." segments and follows symlinks. On volumes that
// treat names case-insensitively the result is rewritten to the on-disk
// casing, so the same directory always normalizes to the same string while a
// case-sensitive volume keeps byte-exact identities. A missing leaf resolves
// its nearest existing ancestor and re-appends the cleaned remainder.
func NormalizeDirectory(path string) NormalizedDirectory {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return NormalizedDirectory{}
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		absolute = filepath.Clean(trimmed)
	}
	resolved, resolveErr := filepath.EvalSymlinks(absolute)
	if resolveErr == nil {
		return NormalizedDirectory{
			Path:     caseCorrectPath(filepath.Clean(resolved)),
			Resolved: true,
		}
	}
	// The leaf (or a middle segment) does not exist. Resolve the nearest
	// existing ancestor so a not-yet-created target directory still compares
	// against the real location it would be created in.
	missing := ""
	current := absolute
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		missing = filepath.Join(filepath.Base(current), missing)
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			ancestor := caseCorrectPath(filepath.Clean(resolved))
			return NormalizedDirectory{Path: filepath.Join(ancestor, missing)}
		}
		current = parent
	}
	return NormalizedDirectory{Path: filepath.Clean(absolute)}
}

// SameAs reports whether two normalized directories name the same directory.
// When both sides were fully resolved through the filesystem they must be
// byte-identical: NormalizeDirectory already rewrote case-insensitive volumes
// to the on-disk casing, and on a case-sensitive volume two paths differing
// only by case are genuinely different directories. A side with an unresolved
// suffix falls back to the platform's case rules for that suffix, because the
// on-disk casing cannot be proven.
func (normalized NormalizedDirectory) SameAs(other NormalizedDirectory, platform string) bool {
	if normalized.Path == "" || other.Path == "" {
		return false
	}
	if normalized.Resolved && other.Resolved {
		return normalized.Path == other.Path
	}
	if normalized.Path == other.Path {
		return true
	}
	return platformFold(platform) && strings.EqualFold(normalized.Path, other.Path)
}

// WithinDirectory reports whether path lies inside directory. Both arguments
// must be normalized .Path values; containment is decided with filepath.Rel,
// never with a string prefix, so sibling directories such as /data and
// /data-other are not confused.
func WithinDirectory(path, directory string) bool {
	if directory == "" || path == "" {
		return false
	}
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// volumeFoldsCase probes whether the volume holding directory treats names
// case-insensitively by statting a case variant of the final segment. When the
// probe is inconclusive (segment without case variants) it reports false so
// comparisons stay byte-exact.
func volumeFoldsCase(directory string) bool {
	parent := filepath.Dir(directory)
	base := filepath.Base(directory)
	variant := caseVariant(base)
	if variant == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(parent, variant)); err == nil {
		return true
	}
	// The final segment may be a symlink that exists but does not stat; only
	// trust a negative probe when the real name is also missing.
	if _, err := os.Lstat(directory); err != nil {
		return false
	}
	return false
}

// caseVariant returns base with the case of its first cased letter flipped, or
// "" when the name has no cased letters.
func caseVariant(base string) string {
	for index, character := range base {
		if character >= 'a' && character <= 'z' {
			return base[:index] + string(character-'a'+'A') + base[index+1:]
		}
		if character >= 'A' && character <= 'Z' {
			return base[:index] + string(character-'A'+'a') + base[index+1:]
		}
	}
	return ""
}

// caseCorrectPath rewrites every segment of an existing path to its on-disk
// casing when the volume folds case. On a case-sensitive volume the given
// casing already is the on-disk casing and the path is returned unchanged.
func caseCorrectPath(path string) string {
	if !volumeFoldsCase(path) {
		return path
	}
	prefix := ""
	rest := path
	if volume := filepath.VolumeName(path); volume != "" {
		prefix = volume
		rest = strings.TrimPrefix(path, volume)
	} else if strings.HasPrefix(rest, "/") {
		prefix = "/"
	}
	segments := splitPathSegments(rest)
	current := prefix
	for index, segment := range segments {
		corrected, ok := matchOnDiskCase(current, segment)
		if !ok {
			// Missing leaf or unreadable directory: keep the remainder as
			// recorded instead of guessing.
			return filepath.Join(append([]string{current}, segments[index:]...)...)
		}
		current = filepath.Join(current, corrected)
	}
	return current
}

func splitPathSegments(path string) []string {
	segments := strings.Split(path, string(filepath.Separator))
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		kept = append(kept, segment)
	}
	return kept
}

// matchOnDiskCase finds the on-disk spelling of name inside directory,
// preferring an exact match and otherwise accepting the single case-insensitive
// match. Folding volumes cannot hold two names that differ only by case, so an
// accepted fold match is unambiguous there; case-sensitive volumes never reach
// this function because the probe above reported them as exact.
func matchOnDiskCase(directory, name string) (string, bool) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", false
	}
	folded := ""
	for _, entry := range entries {
		if entry.Name() == name {
			return name, true
		}
		if folded == "" && strings.EqualFold(entry.Name(), name) {
			folded = entry.Name()
		}
	}
	if folded != "" {
		return folded, true
	}
	return "", false
}

// PlatformOf maps a GOOS value onto the release-contract platform family used
// in backup metadata.
func PlatformOf(goos string) string {
	switch goos {
	case "darwin":
		return PlatformMacOS
	case "linux":
		return PlatformLinux
	case "windows":
		return PlatformWindows
	default:
		return goos
	}
}

func platformFold(platform string) bool {
	return platform == PlatformMacOS || platform == PlatformWindows
}
