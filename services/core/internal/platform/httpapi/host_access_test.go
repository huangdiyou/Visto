package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"review-studio.local/core/internal/delivery"
	"review-studio.local/core/internal/identity"
)

// D2 coverage, docs/FREE_TIER_BOUNDARY_DESIGN.md §2. The host management guard
// must open only for an Owner web session, and only while the switch is on.

func boolSetting(value bool) *bool { return &value }

// setupInstance completes the first-run wizard and returns the Owner session
// plus the workspace it created.
func setupInstance(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Host Access Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-host-access@example.com",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}
	var session sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&session); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	return findSessionCookie(t, setup.Result().Cookies()), session.Workspace.ID
}

func memberCookie(t *testing.T, handler http.Handler, config Config, workspaceID string) *http.Cookie {
	t.Helper()
	if _, err := config.Identity.CreateAccount(context.Background(), identity.CreateAccountInput{
		WorkspaceID: workspaceID,
		Email:       "member-host-access@example.com",
		DisplayName: "Host Access Member",
		Password:    "member-password-123",
		Locale:      "zh-CN",
		Role:        "member",
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
	login := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"member-host-access@example.com","password":"member-password-123"}`,
		nil,
	)
	if login.Code != http.StatusOK {
		t.Fatalf("member login failed with %d: %s", login.Code, login.Body.String())
	}
	return findSessionCookie(t, login.Result().Cookies())
}

func errorCodeOf(t *testing.T, body []byte) string {
	t.Helper()
	var envelope delivery.ErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	if envelope.Error == nil {
		return ""
	}
	return envelope.Error.Code
}

func TestHostManagementOpensForOwnerWebSessionsOnlyWhenAllowed(t *testing.T) {
	// The assertion is about the guard, not about what the handler does next:
	// once the guard opens, the request reaches storage and fails there for
	// unrelated reasons in this fixture.
	const (
		blockedByHost     = "blocked_by_host_management"
		deniedAsNonOwner  = "denied_as_non_owner"
		reachedTheHandler = "reached_the_handler"
	)
	cases := []struct {
		name      string
		allow     *bool
		as        string
		hostToken bool
		want      string
	}{
		{
			name:  "switch off keeps the boundary closed",
			allow: boolSetting(false), as: "owner",
			want: blockedByHost,
		},
		{
			// The wizard answer travels with setup; an omitted field keeps the
			// documented default, so the guard is open afterwards.
			name:  "unset follows the wizard default",
			allow: nil, as: "owner",
			want: reachedTheHandler,
		},
		{
			name:  "switch on admits an owner web session",
			allow: boolSetting(true), as: "owner",
			want: reachedTheHandler,
		},
		{
			// This handler checks the role before it reaches the guard, so the
			// refusal comes from that check. The guard's own owner-only rule is
			// covered by TestHostManagementGuardRefusesNonOwnerSessions, which
			// uses an endpoint that has no role check in front of it.
			name:  "switch on does not admit a member",
			allow: boolSetting(true), as: "member",
			want: deniedAsNonOwner,
		},
		{
			name:  "the host token keeps working with the switch off",
			allow: boolSetting(false), as: "owner", hostToken: true,
			want: reachedTheHandler,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := testConfig(t)
			config.AllowWebHostPaths = testCase.allow
			handler := NewHandler(config)
			ownerCookie, workspaceID := setupInstance(t, handler)

			cookie := ownerCookie
			if testCase.as == "member" {
				cookie = memberCookie(t, handler, config, workspaceID)
			}

			// Built by hand on purpose: performJSONRequest always attaches the host
			// capability token, which would pass the guard without exercising the
			// switch at all.
			request := newRequest(
				http.MethodPost,
				"/api/v1/authorized-roots",
				strings.NewReader(`{"displayName":"Hosts","localPath":"/tmp/visto-host-access"}`),
			)
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set("Accept", "application/json")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(mutationHeaderName, mutationHeaderValue)
			request.AddCookie(cookie)
			if testCase.hostToken {
				request.Header.Set(hostCapabilityHeader, config.HostManagementToken)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			code := errorCodeOf(t, response.Body.Bytes())
			switch testCase.want {
			case blockedByHost:
				if response.Code != http.StatusForbidden || code != "access.host_required" {
					t.Fatalf("want a host management rejection, got %d %q: %s",
						response.Code, code, response.Body.String())
				}
			case deniedAsNonOwner:
				if response.Code != http.StatusForbidden || code != "permission.denied" {
					t.Fatalf("want the owner check to reject, got %d %q: %s",
						response.Code, code, response.Body.String())
				}
			default:
				if code == "access.host_required" || response.Code == http.StatusUnauthorized {
					t.Fatalf("the guard must open, got %d %q: %s",
						response.Code, code, response.Body.String())
				}
			}
		})
	}
}

// The switch admits an Owner web session and nothing else. A member never
// reaches the guard at all: the permission middleware refuses first, which is
// why the endpoint test above sees permission.denied. requireHostManagement is
// therefore exercised on a bare handler below, so the answer is the guard's own.
func TestHostManagementGuardStillRefusesWithoutACredential(t *testing.T) {
	h := &handler{}
	h.allowWebHostPaths.Store(true)

	request := newRequest(http.MethodGet, "/api/v1/authorized-roots/root_missing/objects", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	if h.requireHostManagement(response, request) {
		t.Fatal("the switch must not stand in for a credential")
	}
	if response.Code != http.StatusForbidden ||
		errorCodeOf(t, response.Body.Bytes()) != "access.host_required" {
		t.Fatalf("got %d %q: %s",
			response.Code, errorCodeOf(t, response.Body.Bytes()), response.Body.String())
	}
}

// A bare handler with no identity service cannot report an Owner session, so the
// owner half of the guard is closed even with the switch on. This is the shape a
// misconfigured instance would have, and it must fail closed.
func TestHostManagementGuardRequiresAnOwnerSessionNotJustTheSwitch(t *testing.T) {
	h := &handler{}
	h.allowWebHostPaths.Store(true)

	request := newRequest(http.MethodGet, "/api/v1/authorized-roots/root_missing/objects", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "not-a-session"})
	response := httptest.NewRecorder()
	if h.requireHostManagement(response, request) {
		t.Fatal("an unauthenticated request must not pass the guard")
	}
	if code := errorCodeOf(t, response.Body.Bytes()); code != "access.host_required" {
		t.Fatalf("error code = %q, want access.host_required", code)
	}
}

// The deployment override wins over the value the wizard recorded, in both
// directions: it can open the switch the wizard left closed and close the switch
// the wizard opened.
func TestWebHostPathsAllowedPrefersTheEnvironmentOverride(t *testing.T) {
	cases := []struct {
		name     string
		stored   bool
		override *bool
		want     bool
	}{
		{name: "no override uses the recorded value", stored: true, want: true},
		{name: "no override keeps a recorded refusal", stored: false, want: false},
		{name: "an override can close an open switch", stored: true, override: boolSetting(false), want: false},
		{name: "an override can open a closed switch", stored: false, override: boolSetting(true), want: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := &handler{allowWebHostPathsOverride: testCase.override}
			h.allowWebHostPaths.Store(testCase.stored)
			if got := h.webHostPathsAllowed(); got != testCase.want {
				t.Fatalf("webHostPathsAllowed() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The point of the first-run switch (D2 §2.6): with it on, the Owner can create
// the first storage location from the web without a host credential, which is
// what unblocks "create the first project" on a default installation. The request
// is built by hand so it carries no host capability token.
func TestOwnerWebSessionMayRegisterAStorageLocationWhenTheWizardAllowedIt(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	localPath := t.TempDir()
	body, err := json.Marshal(map[string]any{
		"displayName": "网页添加的目录",
		"localPath":   localPath,
		"mode":        "referenced",
		"scanEnabled": false,
	})
	if err != nil {
		t.Fatalf("encode root request: %v", err)
	}

	request := newRequest(http.MethodPost, "/api/v1/authorized-roots", bytes.NewReader(body))
	request.RemoteAddr = "203.0.113.9:4567"
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(mutationHeaderName, mutationHeaderValue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("an Owner web session must be able to add a storage location: %d %s",
			response.Code, response.Body.String())
	}
	// The response must stay as quiet about the host as it was for the token path.
	if strings.Contains(response.Body.String(), "localPath") ||
		strings.Contains(response.Body.String(), localPath) {
		t.Fatalf("the response leaked the local path: %s", response.Body.String())
	}

	// The same request shape must be refused once the deployment pins the switch
	// closed, which is how an operator locks this down after the fact. The database
	// is the one the wizard already wrote, so this is the restart path rather than
	// a fresh install.
	config.AllowWebHostPaths = boolSetting(false)
	lockedHandler := NewHandler(config)
	lockedRequest := newRequest(http.MethodPost, "/api/v1/authorized-roots", bytes.NewReader(body))
	lockedRequest.RemoteAddr = "203.0.113.9:4567"
	lockedRequest.Header.Set("Accept", "application/json")
	lockedRequest.Header.Set("Content-Type", "application/json")
	lockedRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	lockedRequest.AddCookie(cookie)
	lockedResponse := httptest.NewRecorder()
	lockedHandler.ServeHTTP(lockedResponse, lockedRequest)

	if lockedResponse.Code != http.StatusForbidden ||
		errorCodeOf(t, lockedResponse.Body.Bytes()) != "access.host_required" {
		t.Fatalf("the override must close the switch: %d %s",
			lockedResponse.Code, lockedResponse.Body.String())
	}
}

// D2 §2.3: an Owner web session may read the switch but must never be able to
// change it. If a write route ever appears, this test fails.
func TestHostAccessSettingHasNoWebWritePath(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			response := performJSONRequest(
				handler,
				method,
				"/api/v1/system/host-access",
				`{"allowWebHostPaths":true}`,
				cookie,
			)
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s must not be routable, got %d: %s",
					method, response.Code, response.Body.String())
			}
		})
	}

	// The read path reports the wizard's answer and what the guard uses.
	read := performJSONRequest(handler, http.MethodGet, "/api/v1/system/host-access", "", cookie)
	if read.Code != http.StatusOK {
		t.Fatalf("read host access settings failed: %d %s", read.Code, read.Body.String())
	}
	var view struct {
		AllowWebHostPaths          bool    `json:"allowWebHostPaths"`
		EffectiveAllowWebHostPaths bool    `json:"effectiveAllowWebHostPaths"`
		EnvironmentForced          bool    `json:"environmentForced"`
		Revision                   int     `json:"revision"`
		UpdatedBy                  *string `json:"updatedBy"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode host access settings: %v", err)
	}
	if !view.AllowWebHostPaths || !view.EffectiveAllowWebHostPaths {
		t.Fatalf("the wizard default must be reported as on: %s", read.Body.String())
	}
	if view.EnvironmentForced {
		t.Fatal("nothing pinned the value in this fixture")
	}
	if view.UpdatedBy == nil || *view.UpdatedBy == "" {
		t.Fatal("the recorded choice must name the Owner account")
	}

	// A pinned deployment reports the stored answer and the enforced one
	// separately, so the page can explain why the wizard's answer has no effect.
	pinned := testConfig(t)
	pinned.AllowWebHostPaths = boolSetting(false)
	pinnedHandler := NewHandler(pinned)
	pinnedCookie, _ := setupInstance(t, pinnedHandler)
	pinnedRead := performJSONRequest(
		pinnedHandler, http.MethodGet, "/api/v1/system/host-access", "", pinnedCookie,
	)
	if pinnedRead.Code != http.StatusOK {
		t.Fatalf("read pinned settings failed: %d %s", pinnedRead.Code, pinnedRead.Body.String())
	}
	if err := json.Unmarshal(pinnedRead.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode pinned settings: %v", err)
	}
	if !view.EnvironmentForced || view.EffectiveAllowWebHostPaths {
		t.Fatalf("a pinned deployment must report the override: %s", pinnedRead.Body.String())
	}
}

func TestSetupRecordsTheHostAccessChoice(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Host Access Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-host-access@example.com",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai",
			"allowWebHostPaths":true
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}

	settings, err := config.SystemSettings.GetHostAccess(context.Background())
	if err != nil {
		t.Fatalf("read host access settings: %v", err)
	}
	if !settings.AllowWebHostPaths {
		t.Fatal("the wizard answer must be recorded")
	}
	if settings.UpdatedBy == nil || *settings.UpdatedBy == "" {
		t.Fatal("the recorded choice must name the Owner account")
	}
}

// A wizard that omits the field keeps the documented default rather than being
// read as a refusal, and an instance whose row was never written stays closed.
func TestSetupDefaultsToAllowingWebHostPaths(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setupInstance(t, handler)

	settings, err := config.SystemSettings.GetHostAccess(context.Background())
	if err != nil {
		t.Fatalf("read host access settings: %v", err)
	}
	if !settings.AllowWebHostPaths {
		t.Fatal("an omitted field must keep the documented default (enabled)")
	}
}

func TestHostAccessDefaultsToClosedBeforeSetup(t *testing.T) {
	config := testConfig(t)
	NewHandler(config)

	settings, err := config.SystemSettings.GetHostAccess(context.Background())
	if err != nil {
		t.Fatalf("read host access settings: %v", err)
	}
	if settings.AllowWebHostPaths {
		t.Fatal("an instance that recorded no choice must stay restrictive")
	}

	// The guard reads this value through loadHostAccessSetting at construction
	// time, so a second instance built on the same database must reach the same
	// conclusion. This is the restart path an upgraded deployment takes.
	NewHandler(config)
	restarted, err := config.SystemSettings.GetHostAccess(context.Background())
	if err != nil {
		t.Fatalf("read host access settings after restart: %v", err)
	}
	if restarted.AllowWebHostPaths {
		t.Fatal("the default must survive a restart")
	}
}

func TestAllowWebHostPathsFromEnvironment(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    *bool
		wantErr bool
	}{
		{name: "unset leaves the choice to the wizard", value: "", want: nil},
		{name: "enabled", value: "1", want: boolSetting(true)},
		{name: "disabled", value: "0", want: boolSetting(false)},
		{name: "true is accepted", value: "TRUE", want: boolSetting(true)},
		{name: "off is accepted", value: " off ", want: boolSetting(false)},
		{name: "a typo is rejected", value: "maybe", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("VISTO_ALLOW_WEB_HOST_PATHS", testCase.value)
			got, err := AllowWebHostPathsFromEnvironment()
			if testCase.wantErr {
				if err == nil {
					t.Fatal("an unparsable override must fail instead of being ignored")
				}
				return
			}
			if err != nil {
				t.Fatalf("AllowWebHostPathsFromEnvironment() error = %v", err)
			}
			switch {
			case testCase.want == nil && got != nil:
				t.Fatalf("got %v, want nil so the wizard value applies", *got)
			case testCase.want != nil && got == nil:
				t.Fatalf("got nil, want %v", *testCase.want)
			case testCase.want != nil && *got != *testCase.want:
				t.Fatalf("got %v, want %v", *got, *testCase.want)
			}
		})
	}
}
