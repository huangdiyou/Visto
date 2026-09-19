package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/serverupdate"
)

// newOwnerCookie completes first-run setup, which is the only way to obtain an
// Owner session.
func newOwnerCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Update Status Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-update@example.com",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}
	return findSessionCookie(t, setup.Result().Cookies())
}

func decodeUpdateStatus(t *testing.T, body []byte) systemUpdateStatusResponse {
	t.Helper()
	var decoded systemUpdateStatusResponse
	if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&decoded); err != nil {
		t.Fatalf("decode update status: %v", err)
	}
	return decoded
}

// TestSystemUpdateStatusIsOwnerOnly pins that release metadata is not public:
// it names the deployment kind and the configured update sources.
func TestSystemUpdateStatusIsOwnerOnly(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Update Status Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-update@example.com",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	anonymous := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/update-status", "", nil,
	)
	if anonymous.Code == http.StatusOK {
		t.Fatalf("anonymous update status status = %d, want rejection", anonymous.Code)
	}

	owner := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/update-status", "", ownerCookie,
	)
	if owner.Code != http.StatusOK {
		t.Fatalf("owner update status status = %d, want 200: %s", owner.Code, owner.Body.String())
	}

	if _, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "member-update@example.com",
			DisplayName: "Update Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	); err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"member-update@example.com","password":"member-password-123"}`,
		nil,
	)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login failed with %d: %s", memberLogin.Code, memberLogin.Body.String())
	}
	memberCookie := findSessionCookie(t, memberLogin.Result().Cookies())
	memberStatus := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/update-status", "", memberCookie,
	)
	if memberStatus.Code != http.StatusForbidden {
		t.Fatalf("member update status status = %d, want 403: %s",
			memberStatus.Code, memberStatus.Body.String())
	}
}

// TestSystemUpdateStatusWithoutConfiguredSources documents the offline default:
// no public platform is contacted and the page still offers copyable commands.
func TestSystemUpdateStatusWithoutConfiguredSources(t *testing.T) {
	handler := NewHandler(testConfig(t))
	ownerCookie := newOwnerCookie(t, handler)

	response := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/update-status", "", ownerCookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("update status status = %d, want 200: %s", response.Code, response.Body.String())
	}
	status := decodeUpdateStatus(t, response.Body.Bytes())

	if status.Check.Status != updateStatusNotConfigured {
		t.Fatalf("check status = %q, want %q", status.Check.Status, updateStatusNotConfigured)
	}
	if status.Checked {
		t.Fatal("no check was performed, checked must be false")
	}
	if status.Check.CheckedAt != nil {
		t.Fatalf("unexpected checkedAt %v", status.Check.CheckedAt)
	}
	if status.Latest != nil {
		t.Fatalf("latest release must be nil without a manifest: %#v", status.Latest)
	}
	if len(status.Commands) == 0 || len(status.SecurityNotes) == 0 || len(status.Offline.Steps) == 0 {
		t.Fatalf("commands, security notes, and offline steps are required: %#v", status)
	}
	if status.Policy.WebInstallSupported || status.Policy.SilentUpdate ||
		status.Policy.DownloadsPackage || status.Policy.ExecutesHostCommands ||
		!status.Policy.HostAdminRequired {
		t.Fatalf("unexpected update policy: %#v", status.Policy)
	}
	if status.CurrentVersion != "test" {
		t.Fatalf("current version = %q, want test", status.CurrentVersion)
	}
}

// TestSystemUpdateStatusReportsUnavailableSourceAndCoversErrorDetail keeps raw
// fetch errors in the log: a page must never render internal source or transport
// detail, but it must tell the Owner that a check was attempted.
func TestSystemUpdateStatusReportsUnavailableSourceAndCoversErrorDetail(t *testing.T) {
	config := testConfig(t)
	config.UpdateSources = []string{"https://127.0.0.1:1/server/stable"}
	handler := NewHandler(config)
	ownerCookie := newOwnerCookie(t, handler)

	checked := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/update-status?check=1", "", ownerCookie,
	)
	if checked.Code != http.StatusOK {
		t.Fatalf("update status status = %d, want 200: %s", checked.Code, checked.Body.String())
	}
	status := decodeUpdateStatus(t, checked.Body.Bytes())
	if status.Check.Status != updateStatusUnavailable {
		t.Fatalf("check status = %q, want %q", status.Check.Status, updateStatusUnavailable)
	}
	if !status.Checked || status.Check.CheckedAt == nil {
		t.Fatalf("a forced check must report that it ran: %#v", status.Check)
	}
	if strings.Contains(status.Check.Message, "127.0.0.1") ||
		strings.Contains(status.Check.Message, "connect") {
		t.Fatalf("check message must not leak transport detail: %q", status.Check.Message)
	}

	// Without check=1 the page reuses the last result instead of reaching out
	// to the public platform on every load.
	cached := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/update-status", "", ownerCookie,
	)
	if cached.Code != http.StatusOK {
		t.Fatalf("cached update status status = %d, want 200", cached.Code)
	}
	reused := decodeUpdateStatus(t, cached.Body.Bytes())
	if reused.Check.Status != updateStatusUnavailable {
		t.Fatalf("cached check status = %q, want %q", reused.Check.Status, updateStatusUnavailable)
	}
	if reused.Check.CheckedAt == nil || !reused.Check.CheckedAt.Equal(*status.Check.CheckedAt) {
		t.Fatalf("cached check must reuse the previous timestamp: %v vs %v",
			reused.Check.CheckedAt, status.Check.CheckedAt)
	}
}

func testManifest() serverupdate.Manifest {
	return serverupdate.Manifest{
		SchemaVersion:           1,
		Channel:                 "stable",
		Version:                 "1.0.1",
		PublishedAt:             time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC),
		MinimumSupportedVersion: "1.0.0",
		ReleaseNotes:            []string{"Fixed an update rollback bug", "Improved diagnostics"},
		SigningKeyID:            "release-2026-01",
		Artifacts: []serverupdate.Artifact{
			{
				Kind:      "windows-server",
				Platform:  "windows-amd64",
				URL:       "https://github.com/huangdiyou/Visto/releases/download/v1.0.1/Visto-Server_1.0.1_windows-x64.zip",
				SHA256:    strings.Repeat("a", 64),
				SizeBytes: 2048,
			},
			{
				Kind:      dockerCoreImageKind,
				Platform:  "linux-amd64",
				Image:     "ghcr.io/visto/core@sha256:" + strings.Repeat("b", 64),
				SHA256:    strings.Repeat("c", 64),
				SizeBytes: 4096,
			},
			{
				Kind:      dockerWebImageKind,
				Platform:  "linux-amd64",
				Image:     "ghcr.io/visto/web@sha256:" + strings.Repeat("d", 64),
				SHA256:    strings.Repeat("e", 64),
				SizeBytes: 8192,
			},
			{
				Kind:      linuxServerArtifactKind,
				Platform:  "linux-amd64",
				URL:       "https://github.com/huangdiyou/Visto/releases/download/v1.0.1/Visto-Server_1.0.1_linux-amd64.tar.gz",
				SHA256:    strings.Repeat("f", 64),
				SizeBytes: 16384,
			},
			{
				Kind:      linuxServerArtifactKind,
				Platform:  "linux-arm64",
				URL:       "https://github.com/huangdiyou/Visto/releases/download/v1.0.1/Visto-Server_1.0.1_linux-arm64.tar.gz",
				SHA256:    strings.Repeat("1", 64),
				SizeBytes: 16384,
			},
			{
				Kind:      macOSServerArtifactKind,
				Platform:  "macos-amd64",
				URL:       "https://github.com/huangdiyou/Visto/releases/download/v1.0.1/Visto-Server_1.0.1_macos-amd64.tar.gz",
				SHA256:    strings.Repeat("2", 64),
				SizeBytes: 16384,
			},
			{
				Kind:      macOSServerArtifactKind,
				Platform:  "macos-arm64",
				URL:       "https://github.com/huangdiyou/Visto/releases/download/v1.0.1/Visto-Server_1.0.1_macos-arm64.tar.gz",
				SHA256:    strings.Repeat("3", 64),
				SizeBytes: 16384,
			},
		},
	}
}

// TestUpdateStatusResponseUsesSignedManifestArtifacts verifies the mapping from
// a verified manifest to what the Owner sees. The manifest itself is only
// trusted after serverupdate.Check verifies its signature.
func TestUpdateStatusResponseUsesSignedManifestArtifacts(t *testing.T) {
	h := &handler{
		version:        "1.0.0",
		deploymentKind: serverupdate.DeploymentDocker,
	}
	state := updateCheckState{
		status:    updateStatusAvailable,
		checkedAt: time.Now().UTC(),
		message:   "ok",
		source:    "https://updates.example.com/server/stable",
		manifest:  manifestPointer(testManifest()),
	}
	status := h.toSystemUpdateStatusResponse(state)

	if status.Latest == nil {
		t.Fatal("latest release must be present for a verified manifest")
	}
	if status.Latest.Version != "1.0.1" || status.Latest.UpToDate {
		t.Fatalf("unexpected latest release: %#v", status.Latest)
	}
	if len(status.Latest.Artifacts) != 7 || status.Latest.SigningKeyID != "release-2026-01" {
		t.Fatalf("unexpected artifacts: %#v", status.Latest.Artifacts)
	}

	var update string
	for _, command := range status.Commands {
		if command.ID == "docker-update" {
			update = command.Command
		}
	}
	if update == "" {
		t.Fatalf("docker update command missing: %#v", status.Commands)
	}
	if !strings.Contains(update, "ghcr.io/visto/core@sha256:"+strings.Repeat("b", 64)) ||
		!strings.Contains(update, "ghcr.io/visto/web@sha256:"+strings.Repeat("d", 64)) {
		t.Fatalf("docker update command must carry the signed digests: %s", update)
	}
	if strings.Contains(update, "latest") {
		t.Fatalf("docker update command must not use mutable tags: %s", update)
	}
	if len(status.Sources) != 0 {
		t.Fatalf("sources come from config, not from the caller: %#v", status.Sources)
	}
}

func TestUpdateStatusResponseUsesWindowsPackageURL(t *testing.T) {
	h := &handler{
		version:        "1.0.0",
		deploymentKind: serverupdate.DeploymentWindowsServer,
	}
	status := h.toSystemUpdateStatusResponse(updateCheckState{
		status:    updateStatusAvailable,
		checkedAt: time.Now().UTC(),
		manifest:  manifestPointer(testManifest()),
	})

	var download, apply string
	for _, command := range status.Commands {
		switch command.ID {
		case "windows-download":
			download = command.Command
		case "windows-apply":
			apply = command.Command
		}
	}
	if !strings.Contains(download, "Visto-Server_1.0.1_windows-x64.zip") {
		t.Fatalf("download command must use the signed artifact URL: %s", download)
	}
	if !strings.Contains(apply, "Update-VistoServer.ps1") ||
		!strings.Contains(apply, "latest.json.sig") {
		t.Fatalf("apply command must verify the signed manifest: %s", apply)
	}
	if !strings.Contains(apply, "Visto-Server_1.0.1_windows-x64.zip") {
		t.Fatalf("apply command must target the downloaded package: %s", apply)
	}
	for _, command := range status.Commands {
		if command.Platform != updateCommandPlatformWindowsServer {
			t.Fatalf("unexpected command platform %q for a Windows deployment", command.Platform)
		}
	}
}

// TestUpdateStatusResponseUsesNativeServerPackage covers the Linux and macOS
// native hosts. The page must point at the update script that is already
// installed for the architecture Core runs on, so an administrator never has to
// replace program files by hand.
func TestUpdateStatusResponseUsesNativeServerPackage(t *testing.T) {
	cases := []struct {
		deployment  string
		platform    string
		prefix      string
		packageName string
	}{
		{
			deployment:  serverupdate.DeploymentLinuxServer,
			platform:    updateCommandPlatformLinuxServer,
			prefix:      linuxServerPrefixPath,
			packageName: "Visto-Server_1.0.1_linux-" + runtime.GOARCH + ".tar.gz",
		},
		{
			deployment:  serverupdate.DeploymentMacOSServer,
			platform:    updateCommandPlatformMacOSServer,
			prefix:      macOSServerPrefixPath,
			packageName: "Visto-Server_1.0.1_macos-" + runtime.GOARCH + ".tar.gz",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.deployment, func(t *testing.T) {
			h := &handler{version: "1.0.0", deploymentKind: testCase.deployment}
			status := h.toSystemUpdateStatusResponse(updateCheckState{
				status:    updateStatusAvailable,
				checkedAt: time.Now().UTC(),
				manifest:  manifestPointer(testManifest()),
			})
			if status.Deployment != testCase.deployment {
				t.Fatalf("deployment = %q, want %q", status.Deployment, testCase.deployment)
			}
			if status.Policy.DownloadsPackage || status.Policy.ExecutesHostCommands ||
				status.Policy.WebInstallSupported {
				t.Fatalf("the update page must stay read-only: %#v", status.Policy)
			}

			commands := map[string]string{}
			for _, command := range status.Commands {
				if command.Platform != testCase.platform {
					t.Fatalf("command %q uses platform %q, want %q",
						command.ID, command.Platform, testCase.platform)
				}
				commands[command.ID] = command.Command
			}
			for _, suffix := range []string{"-backup", "-download", "-apply", "-verify", "-rollback"} {
				if commands[testCase.platform+suffix] == "" {
					t.Fatalf("missing %q command: %#v", testCase.platform+suffix, status.Commands)
				}
			}

			download := commands[testCase.platform+"-download"]
			staged := unixUpdateStageDir + "/" + testCase.packageName
			// latest.json and latest.json.sig keep the default names next to the
			// package so the update script can find all three from --package.
			for _, want := range []string{testCase.packageName, staged + ".manifest.json.sig"} {
				if !strings.Contains(download, want) {
					t.Fatalf("download command must contain %q: %s", want, download)
				}
			}

			apply := commands[testCase.platform+"-apply"]
			for _, want := range []string{
				testCase.prefix + "/current/scripts/update-visto-server.sh",
				"VISTO_SERVER_UPDATE_PUBLIC_KEY='" + placeholderPublicKey + "'",
				"--package '" + staged + "'",
			} {
				if !strings.Contains(apply, want) {
					t.Fatalf("apply command must contain %q: %s", want, apply)
				}
			}

			rollback := commands[testCase.platform+"-rollback"]
			for _, want := range []string{
				testCase.prefix + "/current/scripts/rollback-visto-server.sh",
				"--to-version " + placeholderPreviousVersion,
				"--confirm-rollback",
			} {
				if !strings.Contains(rollback, want) {
					t.Fatalf("rollback command must contain %q: %s", want, rollback)
				}
			}

			verify := commands[testCase.platform+"-verify"]
			for _, want := range []string{
				"readlink " + testCase.prefix + "/current",
				testCase.prefix + "/current/bin/visto-server doctor",
			} {
				if !strings.Contains(verify, want) {
					t.Fatalf("verify command must contain %q: %s", want, verify)
				}
			}
		})
	}
}

// TestUpdateStatusResponseFallsBackToNativePlaceholders keeps the native hosts
// usable before a check succeeds: the scripts and their flags are known without
// a manifest, only the package address is not.
func TestUpdateStatusResponseFallsBackToNativePlaceholders(t *testing.T) {
	h := &handler{version: "1.0.0", deploymentKind: serverupdate.DeploymentLinuxServer}
	status := h.toSystemUpdateStatusResponse(updateCheckState{status: updateStatusNotConfigured})
	if status.Latest != nil {
		t.Fatalf("latest must be nil without a manifest: %#v", status.Latest)
	}
	var download, apply string
	for _, command := range status.Commands {
		switch command.ID {
		case updateCommandPlatformLinuxServer + "-download":
			download = command.Command
		case updateCommandPlatformLinuxServer + "-apply":
			apply = command.Command
		}
	}
	if !strings.Contains(download, placeholderLinuxPackageURL) {
		t.Fatalf("download command must expose an explicit placeholder: %s", download)
	}
	wantPackage := unixUpdateStageDir + "/Visto-Server_<版本>_linux-" + runtime.GOARCH + ".tar.gz"
	if !strings.Contains(apply, "--package '"+wantPackage+"'") {
		t.Fatalf("apply command must stay copyable with the placeholder package: %s", apply)
	}
}

// TestUpdateStatusResponseFallsBackToPlaceholders keeps the page usable before
// a check succeeds: commands stay copyable but visibly require the values from
// the public release page.
func TestUpdateStatusResponseFallsBackToPlaceholders(t *testing.T) {
	h := &handler{
		version:        "1.0.0",
		deploymentKind: serverupdate.DeploymentDocker,
	}
	status := h.toSystemUpdateStatusResponse(updateCheckState{
		status:    updateStatusNotConfigured,
		checkedAt: time.Now().UTC(),
	})
	if status.Latest != nil {
		t.Fatalf("latest must be nil without a manifest: %#v", status.Latest)
	}
	for _, command := range status.Commands {
		if command.ID != "docker-update" {
			continue
		}
		if !strings.Contains(command.Command, placeholderCoreImage) ||
			!strings.Contains(command.Command, placeholderWebImage) {
			t.Fatalf("update command must expose explicit placeholders: %s", command.Command)
		}
	}
}

func TestPackageFileNameDerivesNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://example.test/a/Visto-Server_1.0.1_windows-x64.zip": "Visto-Server_1.0.1_windows-x64.zip",
		"https://example.test/a/":                                   "fallback.zip",
		"":                                                          "fallback.zip",
		"<placeholder>":                                             "fallback.zip",
	}
	for input, want := range cases {
		if got := packageFileName(input, "fallback.zip"); got != want {
			t.Fatalf("packageFileName(%q) = %q, want %q", input, got, want)
		}
	}
}

func manifestPointer(manifest serverupdate.Manifest) *serverupdate.Manifest {
	return &manifest
}
