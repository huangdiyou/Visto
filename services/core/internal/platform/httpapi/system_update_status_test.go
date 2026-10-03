package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
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

// testManifest is the free tier version announcement: four fields, no artifacts
// and no download location.
func testManifest() serverupdate.Manifest {
	return serverupdate.Manifest{
		SchemaVersion: 1,
		Channel:       "stable",
		Version:       "1.0.1",
		PublishedAt:   time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC),
	}
}

func manifestPointer(manifest serverupdate.Manifest) *serverupdate.Manifest {
	return &manifest
}

func manifestState() updateCheckState {
	return updateCheckState{
		status:    updateStatusAvailable,
		checkedAt: time.Now().UTC(),
		message:   "ok",
		source:    "https://updates.example.com/server/stable",
		manifest:  manifestPointer(testManifest()),
	}
}

// The Owner page receives the announcement only. There is no artifact list and
// no download location, so nothing a source publishes can redirect an operator.
func TestUpdateStatusResponseExposesOnlyTheVersionAnnouncement(t *testing.T) {
	h := &handler{version: "1.0.0", deploymentKind: serverupdate.DeploymentMacOSServer}
	status := h.toSystemUpdateStatusResponse(manifestState())

	if status.Latest == nil {
		t.Fatal("latest release must be present for a parsed manifest")
	}
	if status.Latest.Version != "1.0.1" || status.Latest.State != updateStateUpdateAvailable {
		t.Fatalf("unexpected latest release: %#v", status.Latest)
	}
	if status.Latest.PublishedAt != "2026-09-02T08:00:00Z" {
		t.Fatalf("publishedAt = %q", status.Latest.PublishedAt)
	}

	// Only the announcement object is checked here: command text legitimately
	// mentions SHA-256, but the release object must carry nothing but the
	// version, its publication time, the answering source and the verdict.
	encoded, err := json.Marshal(status.Latest)
	if err != nil {
		t.Fatalf("marshal latest release: %v", err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		"artifacts", `"url"`, `"image"`, "sha256", "signingKeyId",
		"releaseNotes", "minimumSupportedVersion",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("latest release must not carry %q: %s", forbidden, serialized)
		}
	}
}

// A source that still publishes artifact and free-text fields must not be able
// to reach the page: the fields are dropped when the manifest is decoded, and
// nothing derived from them can appear in a command.
func TestUpdateStatusIgnoresManifestFieldsThatCouldSteerADownload(t *testing.T) {
	raw := `{"schemaVersion":1,"channel":"stable","version":"1.0.1",` +
		`"publishedAt":"2026-09-02T00:00:00Z",` +
		`"artifacts":[{"kind":"macos-server","platform":"macos-arm64",` +
		`"url":"https://evil.example/Visto-Server.tar.gz",` +
		`"sha256":"` + strings.Repeat("a", 64) + `","sizeBytes":1}],` +
		`"releaseNotes":["download from evil.example"]}`
	var manifest serverupdate.Manifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	h := &handler{version: "1.0.0", deploymentKind: serverupdate.DeploymentMacOSServer}
	status := h.toSystemUpdateStatusResponse(updateCheckState{
		status:    updateStatusAvailable,
		checkedAt: time.Now().UTC(),
		message:   "ok",
		source:    "https://updates.example.com/server/stable",
		manifest:  manifestPointer(manifest),
	})

	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(encoded), "evil.example") {
		t.Fatalf("manifest content reached the response: %s", encoded)
	}
	for _, command := range status.Commands {
		if strings.Contains(command.Command, "evil.example") {
			t.Fatalf("manifest content reached a command: %#v", command)
		}
	}
}

// Every generated command must point at the compiled-in release page and never
// at a location taken from a manifest.
func TestUpdateStatusCommandsPointOnlyAtTheOfficialReleasePage(t *testing.T) {
	for _, deployment := range []string{
		serverupdate.DeploymentDocker,
		serverupdate.DeploymentWindowsServer,
		serverupdate.DeploymentLinuxServer,
		serverupdate.DeploymentMacOSServer,
		serverupdate.DeploymentSource,
	} {
		t.Run(deployment, func(t *testing.T) {
			h := &handler{version: "1.0.0", deploymentKind: deployment}
			status := h.toSystemUpdateStatusResponse(manifestState())
			if len(status.Commands) == 0 {
				t.Fatalf("%s must keep its update guidance", deployment)
			}
			namesOfficialPage := false
			for _, command := range status.Commands {
				if strings.Contains(command.Command, officialReleasePage) {
					namesOfficialPage = true
				}
				if strings.Contains(command.Command, "http://") {
					t.Fatalf("command %q uses plain HTTP: %#v", command.ID, command)
				}
				// Any HTTPS location must be the official release page; a
				// manifest-derived or invented host is a failure.
				if strings.Contains(command.Command, "https://") &&
					!strings.Contains(command.Command, officialReleasePage) {
					t.Fatalf("command %q carries a foreign location: %#v", command.ID, command)
				}
			}
			if !namesOfficialPage {
				t.Fatalf("%s must name the official release page", deployment)
			}
		})
	}
}

// Downgrade guard coverage, docs/FREE_TIER_BOUNDARY_DESIGN.md D1.
func TestUpdateStatusWithholdsDowngradePrompts(t *testing.T) {
	cases := []struct {
		name             string
		currentVersion   string
		publishedVersion string
		wantState        string
	}{
		{"published version is newer", "1.0.0", "1.0.1", updateStateUpdateAvailable},
		{"published version matches", "1.0.1", "1.0.1", updateStateUpToDate},
		{"published version is older", "1.0.2", "1.0.1", updateStateUpToDate},
		{"installed build is ahead of the published release", "1.0.3-integration.3", "1.0.0", updateStateUpToDate},
		{"published pre-release is behind a stable install", "1.0.0", "1.0.0-rc.1", updateStateUpToDate},
		{"published version is malformed", "1.0.0", "not-a-version", updateStateUnknown},
		// A development build has no comparable version, so the page must not
		// claim the instance is current either.
		{"running version is malformed", "development", "1.0.1", updateStateUnknown},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := &handler{
				version:        testCase.currentVersion,
				deploymentKind: serverupdate.DeploymentDocker,
			}
			manifest := testManifest()
			manifest.Version = testCase.publishedVersion
			status := h.toSystemUpdateStatusResponse(updateCheckState{
				status:    updateStatusAvailable,
				checkedAt: time.Now().UTC(),
				message:   "ok",
				source:    "https://updates.example.com/server/stable",
				manifest:  manifestPointer(manifest),
			})

			if status.Latest == nil {
				t.Fatal("latest release must be present for a parsed manifest")
			}
			if status.Latest.State != testCase.wantState {
				t.Fatalf("State = %q, want %q", status.Latest.State, testCase.wantState)
			}
			if status.Latest.State == updateStateUnknown && status.Latest.Version == "" {
				t.Fatal("an unknown verdict must still report the published version verbatim")
			}
			// An instance that is not strictly behind must not be handed an
			// actionable update command: the page would otherwise claim
			// "已是最新" while still prompting an update to an older version.
			if testCase.wantState != updateStateUpdateAvailable && len(status.Commands) != 0 {
				t.Fatalf("downgrade prompt was surfaced: %#v", status.Commands)
			}
			if testCase.wantState == updateStateUpdateAvailable && len(status.Commands) == 0 {
				t.Fatal("a strictly newer release must keep its update commands")
			}
		})
	}
}

// A deployment that never obtained a manifest keeps its generic guidance: no
// version claim was made, so there is nothing to withhold.
func TestUpdateStatusKeepsGuidanceWithoutAManifest(t *testing.T) {
	h := &handler{version: "1.0.0", deploymentKind: serverupdate.DeploymentMacOSServer}
	status := h.toSystemUpdateStatusResponse(updateCheckState{
		status:  updateStatusNotConfigured,
		message: "no source",
	})
	if status.Latest != nil {
		t.Fatalf("latest must be nil: %#v", status.Latest)
	}
	if len(status.Commands) == 0 {
		t.Fatal("offline guidance must stay available without a manifest")
	}
}
