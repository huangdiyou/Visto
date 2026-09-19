// Package delivery freezes the platform and artifact contract shared
// by the release manifest, the installer, the update scripts, the Owner update
// page and the `visto-server` host command.
//
// The normative text lives in docs/NATIVE_SERVER_CONTRACT.md. This package is
// the executable part of that contract: it is the only place allowed to map an
// operating system and architecture onto a package name, a manifest kind or a
// default directory. Nothing here downloads, installs or mutates a host.
package delivery

import (
	"fmt"
	"path"
	"runtime"
	"strings"
)

// Platform identifiers. These five values are the only ones a release
// manifest, an installer, an update script, a CI matrix or a release page may
// use to select a package. They are matched strictly: "x64", "darwin" and
// other display spellings are not accepted here.
const (
	PlatformWindowsAMD64 = "windows-amd64"
	PlatformLinuxAMD64   = "linux-amd64"
	PlatformLinuxARM64   = "linux-arm64"
	PlatformMacOSAMD64   = "macos-amd64"
	PlatformMacOSARM64   = "macos-arm64"
)

// Manifest artifact kinds for the native Server packages. Container and media
// runtime artifacts use their own kinds and are not constrained by the native
// platform table.
const (
	KindWindowsServer = "windows-server"
	KindLinuxServer   = "linux-server"
	KindMacOSServer   = "macos-server"
)

// Package name prefix and version grammar. Versions are SemVer without a "v"
// prefix inside file names; the Git tag keeps the "v" prefix.
const (
	ApplicationPackagePrefix  = "Visto-Server"
	MediaRuntimePackagePrefix = "Visto-Media-Runtime"
)

// Platform is one frozen release target.
type Platform struct {
	// ID is the manifest platform value, for example "macos-arm64".
	ID string
	// Kind is the manifest artifact kind, for example "macos-server".
	Kind string
	// OS and Arch are the release identifiers: "windows", "linux" or "macos"
	// and "amd64" or "arm64".
	OS   string
	Arch string
	// GOOS and GOARCH are the Go build identifiers for this target. Go spells
	// the macOS target "darwin"; the release contract spells it "macos".
	GOOS   string
	GOARCH string
	// DisplayName is the only place a friendlier spelling such as "x64" or
	// "Apple Silicon" may appear.
	DisplayName string
	// ArchiveExt is the application archive suffix.
	ArchiveExt string
	// FileNameToken is the platform token inside the application package file
	// name. It equals ID for every platform introduced after this contract,
	// and stays "windows-x64" for the Windows package because that name is
	// already produced by scripts/build-server-win.ps1, referenced by
	// DEPLOY_WINDOWS_SERVER.md and published in earlier candidates.
	FileNameToken string
}

// platformTable is the frozen five-platform matrix. Order is stable and is the
// order release pages and CI matrices must use.
var platformTable = []Platform{
	{
		ID:            PlatformWindowsAMD64,
		Kind:          KindWindowsServer,
		OS:            "windows",
		Arch:          "amd64",
		GOOS:          "windows",
		GOARCH:        "amd64",
		DisplayName:   "Windows x64",
		ArchiveExt:    "zip",
		FileNameToken: "windows-x64",
	},
	{
		ID:            PlatformLinuxAMD64,
		Kind:          KindLinuxServer,
		OS:            "linux",
		Arch:          "amd64",
		GOOS:          "linux",
		GOARCH:        "amd64",
		DisplayName:   "Linux x64",
		ArchiveExt:    "tar.gz",
		FileNameToken: PlatformLinuxAMD64,
	},
	{
		ID:            PlatformLinuxARM64,
		Kind:          KindLinuxServer,
		OS:            "linux",
		Arch:          "arm64",
		GOOS:          "linux",
		GOARCH:        "arm64",
		DisplayName:   "Linux ARM64",
		ArchiveExt:    "tar.gz",
		FileNameToken: PlatformLinuxARM64,
	},
	{
		ID:            PlatformMacOSAMD64,
		Kind:          KindMacOSServer,
		OS:            "macos",
		Arch:          "amd64",
		GOOS:          "darwin",
		GOARCH:        "amd64",
		DisplayName:   "macOS Intel",
		ArchiveExt:    "tar.gz",
		FileNameToken: PlatformMacOSAMD64,
	},
	{
		ID:            PlatformMacOSARM64,
		Kind:          KindMacOSServer,
		OS:            "macos",
		Arch:          "arm64",
		GOOS:          "darwin",
		GOARCH:        "arm64",
		DisplayName:   "macOS Apple Silicon",
		ArchiveExt:    "tar.gz",
		FileNameToken: PlatformMacOSARM64,
	},
}

// NativeKinds lists the artifact kinds that must carry a native platform ID.
var NativeKinds = []string{KindWindowsServer, KindLinuxServer, KindMacOSServer}

// Platforms returns the frozen matrix in release order.
func Platforms() []Platform {
	platforms := make([]Platform, len(platformTable))
	copy(platforms, platformTable)
	return platforms
}

// PlatformByID resolves a manifest platform value. Matching is exact: an
// unknown value, a display name or another spelling is an error and never
// falls back to another platform.
func PlatformByID(id string) (Platform, bool) {
	for _, platform := range platformTable {
		if platform.ID == id {
			return platform, true
		}
	}
	return Platform{}, false
}

// IsNativeKind reports whether kind is one of the native Server artifact
// kinds. Container and media runtime kinds are out of scope for the native
// platform table.
func IsNativeKind(kind string) bool {
	for _, native := range NativeKinds {
		if native == kind {
			return true
		}
	}
	return false
}

// FromGoEnv maps a Go GOOS/GOARCH pair onto a release platform. This is the
// only place where Go's "darwin" becomes the release identifier "macos".
func FromGoEnv(goos, goarch string) (Platform, bool) {
	for _, platform := range platformTable {
		if platform.GOOS == goos && platform.GOARCH == goarch {
			return platform, true
		}
	}
	return Platform{}, false
}

// Current resolves the platform this binary was built for.
func Current() (Platform, bool) {
	return FromGoEnv(runtime.GOOS, runtime.GOARCH)
}

// OS and architecture aliases accepted at the operator input layer only. They
// exist so a human typing "darwin-arm64" or "linux-x64" on a command line is
// not rejected, while manifests, file names and CI matrices keep using the
// frozen identifiers. The alias is resolved to a Platform before it is used
// for anything else.
var osAliases = map[string]string{
	"windows": "windows",
	"win":     "windows",
	"linux":   "linux",
	"macos":   "macos",
	"mac":     "macos",
	"darwin":  "macos",
}

var archAliases = map[string]string{
	"amd64":   "amd64",
	"x64":     "amd64",
	"x86_64":  "amd64",
	"arm64":   "arm64",
	"aarch64": "arm64",
}

// NormalizePlatform resolves an operator-supplied platform expression such as
// "darwin-arm64", "linux-x64" or "MACOS-ARM64" onto a frozen platform. It is
// the input layer only: callers must pass Platform.ID, not the raw input, to
// manifests, file names and release tooling.
func NormalizePlatform(value string) (Platform, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return Platform{}, NewError(CodeUnsupportedPlatform, "no platform was selected")
	}
	osName, arch, found := strings.Cut(normalized, "-")
	if !found {
		return Platform{}, NewError(CodeUnsupportedPlatform,
			fmt.Sprintf("platform %q must look like <os>-<arch>, for example %s", value, PlatformLinuxARM64))
	}
	resolvedOS, ok := osAliases[strings.TrimSpace(osName)]
	if !ok {
		return Platform{}, NewError(CodeUnsupportedPlatform,
			fmt.Sprintf("unsupported operating system %q in platform %q", osName, value))
	}
	resolvedArch, ok := archAliases[strings.TrimSpace(arch)]
	if !ok {
		return Platform{}, NewError(CodeUnsupportedPlatform,
			fmt.Sprintf("unsupported architecture %q in platform %q", arch, value))
	}
	for _, platform := range platformTable {
		if platform.OS == resolvedOS && platform.Arch == resolvedArch {
			return platform, nil
		}
	}
	return Platform{}, NewError(CodeUnsupportedPlatform,
		fmt.Sprintf("platform %q resolves to %s-%s, which Visto Server does not support", value, resolvedOS, resolvedArch))
}

// ValidateKindPlatform rejects a manifest kind/platform pair that cannot be a
// native Server package: an unknown platform, or a kind and platform that
// belong to different operating systems. Non-native kinds (container images,
// media runtime packages) are left to their own validation.
func ValidateKindPlatform(kind, platform string) error {
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	if !IsNativeKind(kind) {
		return nil
	}
	resolved, ok := PlatformByID(platform)
	if !ok {
		return NewError(CodeUnsupportedPlatform,
			fmt.Sprintf("artifact platform %q is not one of the supported native platforms", platform))
	}
	if resolved.Kind != kind {
		return NewError(CodeUnsupportedPlatform,
			fmt.Sprintf("artifact kind %q does not match platform %q", kind, platform))
	}
	return nil
}

// ApplicationPackageFileName returns the application archive name for a
// release version, for example "Visto-Server_1.0.0_macos-arm64.tar.gz".
func (platform Platform) ApplicationPackageFileName(version string) string {
	return fmt.Sprintf("%s_%s_%s.%s", ApplicationPackagePrefix, version, platform.FileNameToken, platform.ArchiveExt)
}

// MediaRuntimeArchiveExt is the archive suffix of a Visto-managed media
// runtime package.
func (platform Platform) MediaRuntimeArchiveExt() string {
	if platform.OS == "windows" {
		return "zip"
	}
	return "tar.xz"
}

// MediaRuntimePackageFileName returns the media runtime archive name for a
// runtime version such as "ffmpeg-8.1-visto.1".
func (platform Platform) MediaRuntimePackageFileName(runtimeVersion string) string {
	return fmt.Sprintf("%s_%s_%s.%s", MediaRuntimePackagePrefix, runtimeVersion, platform.ID, platform.MediaRuntimeArchiveExt())
}

// DefaultPrefix is the default program prefix for this platform.
func (platform Platform) DefaultPrefix() string {
	switch platform.OS {
	case "linux":
		return "/opt/visto"
	case "macos":
		return "/Library/Visto"
	default:
		return `C:\Program Files\Visto Server`
	}
}

// InstalledScriptDirectory returns the directory an installed package exposes
// its host scripts from. Linux and macOS use an immutable release directory
// behind a "current" link; Windows keeps the scripts next to bin/ and web/ in
// the program directory.
func (platform Platform) InstalledScriptDirectory(prefix string) string {
	if platform.OS == "windows" {
		return strings.TrimRight(prefix, `\/`)
	}
	return path.Join(strings.TrimRight(prefix, "/"), "current", "scripts")
}
