package delivery

import (
	"testing"
)

// TestPlatformMatrixPinsFiveTargets freezes the release order and the mapping
// from a platform ID onto its manifest kind, Go build identifiers and archive
// format. A change here changes published package names and release manifests.
func TestPlatformMatrixPinsFiveTargets(t *testing.T) {
	want := []struct {
		id       string
		kind     string
		goos     string
		goarch   string
		display  string
		ext      string
		fileName string
	}{
		{PlatformWindowsAMD64, KindWindowsServer, "windows", "amd64", "Windows x64", "zip", "Visto-Server_1.0.0_windows-x64.zip"},
		{PlatformLinuxAMD64, KindLinuxServer, "linux", "amd64", "Linux x64", "tar.gz", "Visto-Server_1.0.0_linux-amd64.tar.gz"},
		{PlatformLinuxARM64, KindLinuxServer, "linux", "arm64", "Linux ARM64", "tar.gz", "Visto-Server_1.0.0_linux-arm64.tar.gz"},
		{PlatformMacOSAMD64, KindMacOSServer, "darwin", "amd64", "macOS Intel", "tar.gz", "Visto-Server_1.0.0_macos-amd64.tar.gz"},
		{PlatformMacOSARM64, KindMacOSServer, "darwin", "arm64", "macOS Apple Silicon", "tar.gz", "Visto-Server_1.0.0_macos-arm64.tar.gz"},
	}

	platforms := Platforms()
	if len(platforms) != len(want) {
		t.Fatalf("Platforms() returned %d platforms, want %d", len(platforms), len(want))
	}
	for index, expected := range want {
		got := platforms[index]
		if got.ID != expected.id || got.Kind != expected.kind {
			t.Fatalf("platform %d = %s/%s, want %s/%s", index, got.ID, got.Kind, expected.id, expected.kind)
		}
		if got.GOOS != expected.goos || got.GOARCH != expected.goarch {
			t.Fatalf("%s Go target = %s/%s, want %s/%s", got.ID, got.GOOS, got.GOARCH, expected.goos, expected.goarch)
		}
		if got.DisplayName != expected.display {
			t.Fatalf("%s display name = %q, want %q", got.ID, got.DisplayName, expected.display)
		}
		if got.ArchiveExt != expected.ext {
			t.Fatalf("%s archive extension = %q, want %q", got.ID, got.ArchiveExt, expected.ext)
		}
		if name := got.ApplicationPackageFileName("1.0.0"); name != expected.fileName {
			t.Fatalf("%s package name = %q, want %q", got.ID, name, expected.fileName)
		}
	}
}

// TestGoDarwinBecomesMacOS is the contract that keeps Go's "darwin" out of
// release identifiers: only FromGoEnv may translate it, and it always lands on
// the "macos" spelling used by manifests, packages and update scripts.
func TestGoDarwinBecomesMacOS(t *testing.T) {
	cases := map[string]map[string]string{
		"windows": {"amd64": PlatformWindowsAMD64},
		"linux":   {"amd64": PlatformLinuxAMD64, "arm64": PlatformLinuxARM64},
		"darwin":  {"amd64": PlatformMacOSAMD64, "arm64": PlatformMacOSARM64},
	}
	for goos, archs := range cases {
		for goarch, want := range archs {
			platform, ok := FromGoEnv(goos, goarch)
			if !ok {
				t.Fatalf("FromGoEnv(%q, %q) was not resolved", goos, goarch)
			}
			if platform.ID != want {
				t.Fatalf("FromGoEnv(%q, %q) = %s, want %s", goos, goarch, platform.ID, want)
			}
		}
	}
	for _, goos := range []string{"darwin", "macos", "ios", "freebsd", ""} {
		if platform, ok := FromGoEnv(goos, "riscv64"); ok {
			t.Fatalf("FromGoEnv(%q, riscv64) = %s, want no match", goos, platform.ID)
		}
	}
}

// TestArchitecturesNeverCrossPackages is the guard against the failure
// exists to prevent: an amd64 host must never be handed an arm64 package.
func TestArchitecturesNeverCrossPackages(t *testing.T) {
	names := map[string]bool{}
	for _, platform := range Platforms() {
		name := platform.ApplicationPackageFileName("1.2.3")
		if names[name] {
			t.Fatalf("two platforms produce the same package name %q", name)
		}
		names[name] = true
	}
	for _, platform := range Platforms() {
		name := platform.ApplicationPackageFileName("1.2.3")
		for _, other := range Platforms() {
			if other.ID == platform.ID {
				continue
			}
			if other.ApplicationPackageFileName("1.2.3") == name {
				t.Fatalf("%s and %s share the package name %q", platform.ID, other.ID, name)
			}
		}
	}
	if _, ok := FromGoEnv("linux", "arm"); ok {
		t.Fatalf("32-bit ARM was accepted as a release platform")
	}
	if _, ok := PlatformByID("linux-arm"); ok {
		t.Fatalf("linux-arm was accepted as a manifest platform")
	}
}

// TestUnsupportedPlatformsFail pins that unknown platforms, unknown
// architectures and impossible combinations fail instead of falling back.
func TestUnsupportedPlatformsFail(t *testing.T) {
	rejected := []string{
		"",
		"darwin-arm64-manual",
		"linux-386",
		"linux-riscv64",
		"windows-arm64",
		"freebsd-amd64",
		"android-arm64",
		"linux",
		"darwin-arm64-manual",
	}
	for _, value := range rejected {
		if platform, err := NormalizePlatform(value); err == nil {
			t.Fatalf("NormalizePlatform(%q) = %s, want an error", value, platform.ID)
		}
	}
}

// TestOperatorAliasesResolveAtTheInputLayer allows a human to type the Go or
// marketing spelling, but only at the input layer: the resolved platform still
// carries the frozen identifier.
func TestOperatorAliasesResolveAtTheInputLayer(t *testing.T) {
	cases := map[string]string{
		"darwin-amd64":  PlatformMacOSAMD64,
		"darwin-arm64":  PlatformMacOSARM64,
		"macos-x64":     PlatformMacOSAMD64,
		"linux-x64":     PlatformLinuxAMD64,
		"linux-aarch64": PlatformLinuxARM64,
		"windows-x64":   PlatformWindowsAMD64,
		"MACOS-ARM64":   PlatformMacOSARM64,
		" Linux-AMD64 ": PlatformLinuxAMD64,
	}
	for input, want := range cases {
		platform, err := NormalizePlatform(input)
		if err != nil {
			t.Fatalf("NormalizePlatform(%q) failed: %v", input, err)
		}
		if platform.ID != want {
			t.Fatalf("NormalizePlatform(%q) = %s, want %s", input, platform.ID, want)
		}
	}
}

// TestValidateKindPlatformRejectsMismatches keeps a kind from one operating
// system from selecting another operating system's package.
func TestValidateKindPlatformRejectsMismatches(t *testing.T) {
	for _, platform := range Platforms() {
		if err := ValidateKindPlatform(platform.Kind, platform.ID); err != nil {
			t.Fatalf("ValidateKindPlatform(%s, %s) failed: %v", platform.Kind, platform.ID, err)
		}
	}
	mismatches := []struct{ kind, platform string }{
		{KindWindowsServer, PlatformLinuxAMD64},
		{KindLinuxServer, PlatformMacOSAMD64},
		{KindMacOSServer, PlatformWindowsAMD64},
		{KindLinuxServer, "windows-amd64"},
		{KindWindowsServer, "macos-arm64"},
		{KindMacOSServer, "darwin-arm64"},
	}
	for _, pair := range mismatches {
		err := ValidateKindPlatform(pair.kind, pair.platform)
		if err == nil {
			t.Fatalf("ValidateKindPlatform(%s, %s) was accepted", pair.kind, pair.platform)
		}
		classified, ok := err.(*Error)
		if !ok {
			t.Fatalf("ValidateKindPlatform(%s, %s) returned %T, want *delivery.Error", pair.kind, pair.platform, err)
		}
		if classified.Code != CodeUnsupportedPlatform {
			t.Fatalf("error code = %q, want %q", classified.Code, CodeUnsupportedPlatform)
		}
		if classified.ExitCode != ExitUnsupportedPlatform {
			t.Fatalf("exit code = %d, want %d", classified.ExitCode, ExitUnsupportedPlatform)
		}
	}
	// Non-native kinds (container images, media runtime packages) keep their
	// own validation and must not be rejected here.
	if err := ValidateKindPlatform("docker-core", "linux-amd64"); err != nil {
		t.Fatalf("ValidateKindPlatform(docker-core, linux-amd64) failed: %v", err)
	}
}

// TestInstalledScriptDirectoryMatchesUpdateScripts pins the path the update
// scripts are documented to live at after an install.
func TestInstalledScriptDirectoryMatchesUpdateScripts(t *testing.T) {
	cases := []struct {
		platform Platform
		prefix   string
		want     string
	}{
		{mustPlatform(t, PlatformLinuxAMD64), "/opt/visto", "/opt/visto/current/scripts"},
		{mustPlatform(t, PlatformLinuxARM64), "/opt/visto", "/opt/visto/current/scripts"},
		{mustPlatform(t, PlatformMacOSAMD64), "/Library/Visto", "/Library/Visto/current/scripts"},
		{mustPlatform(t, PlatformMacOSARM64), "/Library/Visto", "/Library/Visto/current/scripts"},
		{mustPlatform(t, PlatformWindowsAMD64), `C:\Program Files\Visto Server`, `C:\Program Files\Visto Server`},
	}
	for _, testCase := range cases {
		layout := DefaultLayout(testCase.platform)
		if layout.ProgramDir != testCase.prefix {
			t.Fatalf("%s program directory = %q, want %q", testCase.platform.ID, layout.ProgramDir, testCase.prefix)
		}
		if layout.CurrentDir+"/scripts" != testCase.want && testCase.platform.OS != "windows" {
			t.Fatalf("%s script directory = %q", testCase.platform.ID, layout.ScriptDir)
		}
		if got := testCase.platform.InstalledScriptDirectory(testCase.prefix); got != testCase.want {
			t.Fatalf("%s installed script directory = %q, want %q", testCase.platform.ID, got, testCase.want)
		}
		if layout.ScriptDir != testCase.want {
			t.Fatalf("%s layout script directory = %q, want %q", testCase.platform.ID, layout.ScriptDir, testCase.want)
		}
	}
}

// TestDefaultLayoutKeepsDataOutsideTheProgramDirectory is the rule that lets an
// application update replace the program directory without touching data,
// backups or a reusable media runtime.
func TestDefaultLayoutKeepsDataOutsideTheProgramDirectory(t *testing.T) {
	for _, platform := range Platforms() {
		layout := DefaultLayout(platform)
		for name, value := range map[string]string{
			"dataDir":    layout.DataDir,
			"backupDir":  layout.BackupDir,
			"runtimeDir": layout.RuntimeDir,
			"cacheDir":   layout.CacheDir,
		} {
			if value == "" {
				t.Fatalf("%s %s is empty", platform.ID, name)
			}
			if len(value) > len(layout.ProgramDir) && value[:len(layout.ProgramDir)] == layout.ProgramDir {
				t.Fatalf("%s %s %q lives inside the program directory", platform.ID, name, value)
			}
		}
		if platform.OS != "windows" {
			if layout.ReleasesDir != layout.ProgramDir+"/releases" {
				t.Fatalf("%s releases directory = %q", platform.ID, layout.ReleasesDir)
			}
			if layout.RecoveryDir != layout.ProgramDir+"/recovery" {
				t.Fatalf("%s recovery directory = %q", platform.ID, layout.RecoveryDir)
			}
			continue
		}
		if layout.ReleasesDir != "" {
			t.Fatalf("Windows must not use a Unix release directory, got %q", layout.ReleasesDir)
		}
	}
}

// TestMediaRuntimePackageNames keeps the runtime archive addressable per
// platform without reusing the application package name.
func TestMediaRuntimePackageNames(t *testing.T) {
	windows := mustPlatform(t, PlatformWindowsAMD64)
	if got, want := windows.MediaRuntimePackageFileName("ffmpeg-8.1-visto.1"), "Visto-Media-Runtime_ffmpeg-8.1-visto.1_windows-amd64.zip"; got != want {
		t.Fatalf("windows runtime package = %q, want %q", got, want)
	}
	linux := mustPlatform(t, PlatformLinuxARM64)
	if got, want := linux.MediaRuntimePackageFileName("ffmpeg-8.1-visto.1"), "Visto-Media-Runtime_ffmpeg-8.1-visto.1_linux-arm64.tar.xz"; got != want {
		t.Fatalf("linux runtime package = %q, want %q", got, want)
	}
}

func TestExitCodeForCoversEveryErrorCode(t *testing.T) {
	for _, code := range []string{
		CodeInvalidArguments, CodeUnsupportedPlatform, CodePermissionDenied,
		CodeRuntimeUnavailable, CodeNetworkFailure, CodeVerificationFailed,
		CodeInsufficientStorage, CodeServiceFailure, CodeCancelled,
	} {
		if got := ExitCodeFor(code); got == ExitInternal {
			t.Fatalf("error code %q was not mapped to a specific exit code", code)
		}
	}
	if got := ExitCodeFor(CodeNotImplemented); got != ExitInternal {
		t.Fatalf("not_implemented exit code = %d, want %d", got, ExitInternal)
	}
}

func mustPlatform(t *testing.T, id string) Platform {
	t.Helper()
	platform, ok := PlatformByID(id)
	if !ok {
		t.Fatalf("unknown platform %q", id)
	}
	return platform
}
