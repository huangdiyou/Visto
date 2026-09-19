package storage

import (
	"os"
	"path/filepath"
	"strings"
)

// LocalRootLocation is the path-bearing view of a local root.
type LocalRootLocation struct {
	RootID      string
	WorkspaceID string
	DisplayName string
	PathText    string
}

// ForeignInstanceRoot is a local root whose stored path is the managed area of
// a different instance data directory.
type ForeignInstanceRoot struct {
	RootID       string
	WorkspaceID  string
	DisplayName  string
	PathText     string
	InstanceRoot string
}

var instanceDataSegments = []string{"uploads", "sources", "renditions", "tmp"}

var instanceRootMarkers = []string{
	filepath.Join("current", "bin", "visto-core"),
	filepath.Join("config", "visto.env"),
	"releases",
}

// DetectForeignInstanceRoots reports local roots left pointing at another
// instance's data directory.
//
// Restore swaps the data directory but leaves the absolute paths stored in
// local_path_secrets untouched, so restoring into a different directory keeps
// the new instance reading and writing the source instance's files. A path only
// counts when it is shaped like <prefix>/data/{uploads,sources,renditions,tmp},
// sits outside this instance's data directory, and its prefix is accepted by
// isInstanceRoot. That last check keeps an unrelated volume or share named
// ".../data/uploads" from being reported as a foreign instance. Pass a nil
// isInstanceRoot to accept every candidate prefix.
func DetectForeignInstanceRoots(
	roots []LocalRootLocation,
	dataDir string,
	isInstanceRoot func(string) bool,
) []ForeignInstanceRoot {
	currentDataDirectory := cleanAbsolute(dataDir)
	foreign := make([]ForeignInstanceRoot, 0, len(roots))
	for _, root := range roots {
		pathText := strings.TrimSpace(root.PathText)
		if pathText == "" || !filepath.IsAbs(pathText) {
			continue
		}
		cleaned := filepath.Clean(pathText)
		instanceRoot := instanceRootForDataPath(cleaned)
		if instanceRoot == "" {
			continue
		}
		if isWithinDirectory(cleaned, currentDataDirectory) {
			continue
		}
		if isInstanceRoot != nil && !isInstanceRoot(instanceRoot) {
			continue
		}
		foreign = append(foreign, ForeignInstanceRoot{
			RootID:       root.RootID,
			WorkspaceID:  root.WorkspaceID,
			DisplayName:  root.DisplayName,
			PathText:     cleaned,
			InstanceRoot: instanceRoot,
		})
	}
	return foreign
}

// LooksLikeInstanceRoot reports whether directory holds a Visto instance
// layout, which is how a real sibling instance is told apart from an unrelated
// directory tree that happens to end in data/uploads.
func LooksLikeInstanceRoot(directory string) bool {
	if strings.TrimSpace(directory) == "" {
		return false
	}
	for _, marker := range instanceRootMarkers {
		if _, err := os.Stat(filepath.Join(directory, marker)); err == nil {
			return true
		}
	}
	return false
}

// instanceRootForDataPath returns the instance directory when path sits inside
// that instance's data directory, and "" otherwise.
func instanceRootForDataPath(path string) string {
	for _, segment := range instanceDataSegments {
		marker := string(filepath.Separator) + "data" + string(filepath.Separator) + segment
		index := strings.Index(path, marker)
		if index <= 0 {
			continue
		}
		remainder := path[index+len(marker):]
		if remainder != "" && !strings.HasPrefix(remainder, string(filepath.Separator)) {
			continue
		}
		return path[:index]
	}
	return ""
}

func cleanAbsolute(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(absolute)
}

func isWithinDirectory(path string, directory string) bool {
	if directory == "" {
		return false
	}
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
