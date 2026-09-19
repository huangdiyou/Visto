package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectForeignInstanceRoots(t *testing.T) {
	const (
		sourceInstance  = "/Library/Visto-NSD-OPS-61455377"
		restoreInstance = "/Library/Visto-NSD-OPS-RST-61455377"
	)
	restoreDataDir := restoreInstance + "/data"
	acceptEveryPrefix := func(string) bool { return true }

	cases := []struct {
		name        string
		path        string
		isInstance  func(string) bool
		wantForeign bool
		wantRoot    string
	}{
		{
			name:        "bucket root left in the source data directory",
			path:        sourceInstance + "/data/uploads",
			isInstance:  acceptEveryPrefix,
			wantForeign: true,
			wantRoot:    sourceInstance,
		},
		{
			name:        "managed source root left in the source data directory",
			path:        sourceInstance + "/data/sources/01a0890d-9d99-796a-833c-70e83c536d2e",
			isInstance:  acceptEveryPrefix,
			wantForeign: true,
			wantRoot:    sourceInstance,
		},
		{
			name:        "managed rendition root left in the source data directory",
			path:        sourceInstance + "/data/renditions/asset/version/thumb.webp",
			isInstance:  acceptEveryPrefix,
			wantForeign: true,
			wantRoot:    sourceInstance,
		},
		{
			name:        "root inside this instance data directory",
			path:        restoreInstance + "/data/sources/01a0890d-9d99-796a-833c-70e83c536d2e",
			isInstance:  acceptEveryPrefix,
			wantForeign: false,
		},
		{
			name:        "external volume that is not shaped like an instance",
			path:        "/Volumes/Media/uploads",
			isInstance:  acceptEveryPrefix,
			wantForeign: false,
		},
		{
			name:        "shaped path whose prefix is not an instance",
			path:        "/Volumes/Archive/data/uploads",
			isInstance:  func(string) bool { return false },
			wantForeign: false,
		},
		{
			name:        "prefix that merely resembles a data segment name",
			path:        "/Volumes/Archive/data/uploads-archive",
			isInstance:  acceptEveryPrefix,
			wantForeign: false,
		},
		{
			name:        "relative path is ignored",
			path:        "data/uploads",
			isInstance:  acceptEveryPrefix,
			wantForeign: false,
		},
		{
			name:        "empty path is ignored",
			path:        "",
			isInstance:  acceptEveryPrefix,
			wantForeign: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			roots := []LocalRootLocation{{
				RootID:      "root-1",
				WorkspaceID: "workspace-1",
				DisplayName: "OPS Baseline Storage",
				PathText:    testCase.path,
			}}
			foreign := DetectForeignInstanceRoots(roots, restoreDataDir, testCase.isInstance)
			if !testCase.wantForeign {
				if len(foreign) != 0 {
					t.Fatalf("expected no foreign root, got %#v", foreign)
				}
				return
			}
			if len(foreign) != 1 {
				t.Fatalf("expected one foreign root, got %#v", foreign)
			}
			if foreign[0].InstanceRoot != testCase.wantRoot {
				t.Fatalf(
					"instance root = %q, want %q",
					foreign[0].InstanceRoot,
					testCase.wantRoot,
				)
			}
			if foreign[0].PathText != filepath.Clean(testCase.path) {
				t.Fatalf("path text = %q", foreign[0].PathText)
			}
		})
	}
}

func TestDetectForeignInstanceRootsAcceptsNilInstanceCheck(t *testing.T) {
	roots := []LocalRootLocation{{
		RootID:   "root-1",
		PathText: "/srv/visto-a/data/uploads",
	}}
	foreign := DetectForeignInstanceRoots(roots, "/srv/visto-b/data", nil)
	if len(foreign) != 1 || foreign[0].InstanceRoot != "/srv/visto-a" {
		t.Fatalf("expected the source instance to be reported, got %#v", foreign)
	}
}

func TestLooksLikeInstanceRoot(t *testing.T) {
	directory := t.TempDir()
	if LooksLikeInstanceRoot(directory) {
		t.Fatalf("a plain directory must not look like an instance root")
	}
	if LooksLikeInstanceRoot("") {
		t.Fatalf("an empty path must not look like an instance root")
	}
	if err := os.MkdirAll(filepath.Join(directory, "releases"), 0o700); err != nil {
		t.Fatalf("create releases directory: %v", err)
	}
	if !LooksLikeInstanceRoot(directory) {
		t.Fatalf("a directory holding releases must look like an instance root")
	}

	configDirectory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDirectory, "config"), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config", "visto.env"),
		[]byte("REVIEW_STUDIO_ADDR=127.0.0.1:8787\n"),
		0o600,
	); err != nil {
		t.Fatalf("write instance env file: %v", err)
	}
	if !LooksLikeInstanceRoot(configDirectory) {
		t.Fatalf("a directory holding config/visto.env must look like an instance root")
	}
}
