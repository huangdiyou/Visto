package hostbackup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDirectoryResolvesSymlinksAndCase(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "RealData")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "LinkData")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	normalized := NormalizeDirectory(link)
	if !normalized.Resolved {
		t.Fatalf("expected %s to resolve", link)
	}
	wantReal := NormalizeDirectory(real).Path
	if normalized.Path != wantReal {
		t.Fatalf("NormalizeDirectory(%q) = %q, want %q", link, normalized.Path, wantReal)
	}

	// Case-insensitive volumes return the on-disk casing; the normalized form
	// must equal the real directory byte-for-byte either way. On a
	// case-sensitive volume the lowercase name genuinely does not exist, so
	// the assertion would test nothing.
	if _, err := os.Stat(filepath.Join(root, "realdata")); err == nil {
		lower := NormalizeDirectory(filepath.Join(root, "realdata"))
		if lower.Path != wantReal {
			t.Fatalf("NormalizeDirectory of case variant = %q, want %q", lower.Path, wantReal)
		}
	}
}

func TestNormalizeDirectoryHandlesDotDotAndTrailingSeparators(t *testing.T) {
	root := t.TempDir()
	real := NormalizeDirectory(filepath.Join(root, "data")).Path
	if err := os.Mkdir(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	sneaky := NormalizeDirectory(filepath.Join(inside, "..", "data") + string(filepath.Separator))
	if sneaky.Path != real {
		t.Fatalf("dot-dot path normalized to %q, want %q", sneaky.Path, real)
	}
}

func TestNormalizeDirectoryMissingLeafKeepsResolvedAncestor(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := NormalizeDirectory(root).Path

	normalized := NormalizeDirectory(filepath.Join(root, "not-yet", "data"))
	if normalized.Resolved {
		t.Fatalf("expected unresolved leaf for %q", normalized.Path)
	}
	if normalized.Path != filepath.Join(resolvedRoot, "not-yet", "data") {
		t.Fatalf("normalized missing leaf = %q, want under %q", normalized.Path, resolvedRoot)
	}
}

func TestSameDirectoryResolvedPathsAreByteExact(t *testing.T) {
	// Two fully resolved paths must only match when identical, even on
	// platforms with case-insensitive filesystems: different case on a
	// case-sensitive volume means different directories.
	left := NormalizedDirectory{Path: "/srv/Visto/Data", Resolved: true}
	right := NormalizedDirectory{Path: "/srv/visto/data", Resolved: true}
	for _, platform := range []string{PlatformLinux, PlatformMacOS, PlatformWindows} {
		if left.SameAs(right, platform) {
			t.Fatalf("resolved case variant reported equal on %s", platform)
		}
		if !left.SameAs(left, platform) {
			t.Fatalf("identical path reported different on %s", platform)
		}
	}
}

func TestSameDirectoryUnresolvedSuffixUsesPlatformCaseRules(t *testing.T) {
	left := NormalizedDirectory{Path: "/srv/visto/Data", Resolved: false}
	right := NormalizedDirectory{Path: "/srv/visto/data", Resolved: true}
	if !left.SameAs(right, PlatformMacOS) {
		t.Fatal("unresolved suffix should fold case on macos")
	}
	if !left.SameAs(right, PlatformWindows) {
		t.Fatal("unresolved suffix should fold case on windows")
	}
	if left.SameAs(right, PlatformLinux) {
		t.Fatal("unresolved suffix must compare exactly on linux")
	}
}

func TestWithinDirectoryRejectsPrefixSiblings(t *testing.T) {
	if !WithinDirectory("/srv/visto/data/sources", "/srv/visto/data") {
		t.Fatal("managed root inside data directory reported outside")
	}
	if WithinDirectory("/srv/visto/data-other/sources", "/srv/visto/data") {
		t.Fatal("prefix sibling reported inside the data directory")
	}
	if !WithinDirectory("/srv/visto/data", "/srv/visto/data") {
		t.Fatal("the directory itself counts as inside")
	}
	if WithinDirectory("../escape", "/srv/visto/data") {
		t.Fatal("relative escape reported inside")
	}
}

func TestPlatformOf(t *testing.T) {
	cases := map[string]string{
		"darwin":  PlatformMacOS,
		"linux":   PlatformLinux,
		"windows": PlatformWindows,
	}
	for goos, want := range cases {
		if got := PlatformOf(goos); got != want {
			t.Fatalf("PlatformOf(%q) = %q, want %q", goos, got, want)
		}
	}
}
