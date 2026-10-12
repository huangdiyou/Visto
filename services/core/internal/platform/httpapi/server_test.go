package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/catalog"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/invitation"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectmember"
	"review-studio.local/core/internal/projectstorage"
	"review-studio.local/core/internal/ratelimit"
	reviewdomain "review-studio.local/core/internal/review"
	"review-studio.local/core/internal/reviewtemplate"
	"review-studio.local/core/internal/secretstore"
	sharedomain "review-studio.local/core/internal/share"
	"review-studio.local/core/internal/storage"
	"review-studio.local/core/internal/systemsettings"
	"review-studio.local/core/internal/workspace"
	"review-studio.local/core/internal/workspacesettings"
)

const testHostManagementToken = "test-host-management-capability"

func testPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54,
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
	}
}

func testMP4Bytes() []byte {
	return []byte{
		0x00, 0x00, 0x00, 0x18,
		'f', 't', 'y', 'p',
		'i', 's', 'o', 'm',
		0x00, 0x00, 0x02, 0x00,
		'i', 's', 'o', 'm',
		'm', 'p', '4', '1',
	}
}

type countingReadCloser struct {
	reader io.Reader
	read   int
}

func (reader *countingReadCloser) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	reader.read += n
	return n, err
}

func (reader *countingReadCloser) Close() error {
	return nil
}

func TestLiveEndpoint(t *testing.T) {
	request := newRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	NewHandler(testConfig(t)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}

	if response.Header().Get("X-Request-Id") == "" {
		t.Fatal("expected request id header")
	}

	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("expected security headers")
	}
}

// TestSecureCookieFollowsRealTransport pins the Cookie Secure attribute to
// whether the request actually arrived over HTTPS. There is no Owner-facing
// cookie toggle, and the "remote access requires HTTPS" policy does not change
// this decision.
func TestSecureCookieFollowsRealTransport(t *testing.T) {
	h := &handler{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	plain := httptest.NewRequest(http.MethodGet, "http://visto.test/", nil)
	plain.RemoteAddr = "192.168.21.10:4567"
	if h.secureCookie(plain) {
		t.Fatal("plain HTTP must not receive Secure cookies")
	}

	// Plaintext loopback is allowed transport, but it is still not HTTPS.
	loopback := httptest.NewRequest(http.MethodGet, "http://visto.test/", nil)
	loopback.RemoteAddr = "127.0.0.1:4567"
	if h.secureCookie(loopback) {
		t.Fatal("plaintext loopback must not receive Secure cookies")
	}

	// An allowlisted proxy that terminated TLS and forwarded the scheme counts.
	gateway := httptest.NewRequest(http.MethodGet, "http://visto.test/", nil)
	gateway.RemoteAddr = "10.1.2.3:4567"
	gateway.Header.Set("X-Forwarded-Proto", "https")
	if !h.secureCookie(gateway) {
		t.Fatal("allowlisted HTTPS gateway must receive Secure cookies")
	}

	// The same header from a peer outside the allowlist is untrusted input.
	spoofed := httptest.NewRequest(http.MethodGet, "http://visto.test/", nil)
	spoofed.RemoteAddr = "192.168.21.10:4567"
	spoofed.Header.Set("X-Forwarded-Proto", "https")
	if h.secureCookie(spoofed) {
		t.Fatal("forwarded scheme from an unallowlisted peer must be ignored")
	}

	// Enabling the HTTPS policy must not fabricate a secure transport.
	h.requireRemoteHTTPS.Store(true)
	if h.secureCookie(plain) {
		t.Fatal("HTTPS enforcement must not imply the current request is HTTPS")
	}

	// A gateway that does not forward the scheme can declare it once at deploy.
	t.Setenv("REVIEW_STUDIO_SECURE_COOKIES", "1")
	if !h.secureCookie(plain) {
		t.Fatal("REVIEW_STUDIO_SECURE_COOKIES=1 must force Secure cookies")
	}
}

func TestSystemInfoEndpoint(t *testing.T) {
	request := newRequest(http.MethodGet, "/api/v1/system/info", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set(hostCapabilityHeader, testHostManagementToken)
	response := httptest.NewRecorder()

	NewHandler(testConfig(t)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}

	var body systemInfo
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Name != "Review Studio Core" {
		t.Fatalf("unexpected service name %q", body.Name)
	}

	if body.Version != "test" {
		t.Fatalf("unexpected version %q", body.Version)
	}

	if body.APIVersion != "v1" {
		t.Fatalf("unexpected API version %q", body.APIVersion)
	}

	if body.Access.Surface != "host" || !body.Access.HostManagement {
		t.Fatalf("unexpected access info: %#v", body.Access)
	}

	if body.Media.VideoAccelerationMode != media.VideoAccelerationSoftware ||
		body.Media.VideoEncoder != "libx264" ||
		body.Media.VideoHardwareAcceleration {
		t.Fatalf("unexpected video acceleration info: %#v", body.Media)
	}
}

func TestSystemInfoEndpointDetectsRemoteProxyClient(t *testing.T) {
	request := newRequest(http.MethodGet, "/api/v1/system/info", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set(hostProxyHeader, "desktop")
	response := httptest.NewRecorder()

	NewHandler(testConfig(t)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}

	var body systemInfo
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Access.Surface != "remote" || body.Access.HostManagement {
		t.Fatalf("unexpected access info: %#v", body.Access)
	}
}

func TestSystemInfoEndpointRequiresConfiguredHostCapability(t *testing.T) {
	config := testConfig(t)
	config.HostManagementToken = "trusted-host-capability"
	handler := NewHandler(config)

	spoofed := newRequest(http.MethodGet, "/api/v1/system/info", nil)
	spoofed.RemoteAddr = "127.0.0.1:12345"
	spoofed.Header.Set("X-Forwarded-For", "127.0.0.1")
	spoofed.Header.Set(hostProxyHeader, "desktop")
	spoofed.Header.Set(hostCapabilityHeader, "wrong-capability")
	spoofedResponse := httptest.NewRecorder()
	handler.ServeHTTP(spoofedResponse, spoofed)

	var spoofedBody systemInfo
	if err := json.NewDecoder(spoofedResponse.Body).Decode(&spoofedBody); err != nil {
		t.Fatalf("decode spoofed response: %v", err)
	}
	if spoofedBody.Access.HostManagement || spoofedBody.Access.Surface != "remote" {
		t.Fatalf("spoofed request gained host management: %#v", spoofedBody.Access)
	}

	trusted := newRequest(http.MethodGet, "/api/v1/system/info", nil)
	trusted.RemoteAddr = "127.0.0.1:12345"
	trusted.Header.Set(hostProxyHeader, "desktop")
	trusted.Header.Set(hostCapabilityHeader, config.HostManagementToken)
	trustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(trustedResponse, trusted)

	var trustedBody systemInfo
	if err := json.NewDecoder(trustedResponse.Body).Decode(&trustedBody); err != nil {
		t.Fatalf("decode trusted response: %v", err)
	}
	if !trustedBody.Access.HostManagement || trustedBody.Access.Surface != "host" {
		t.Fatalf("trusted request lacks host management: %#v", trustedBody.Access)
	}
}

func TestSetupRequiresHostCapabilityWhenConfigured(t *testing.T) {
	config := testConfig(t)
	config.HostManagementToken = "trusted-host-capability"
	handler := NewHandler(config)
	body := `{
		"workspaceName": "Studio",
		"ownerName": "Owner",
		"password": "local-password-123",
		"locale": "zh-CN",
		"timezone": "Asia/Shanghai"
	}`

	remote := newRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	remote.RemoteAddr = "127.0.0.1:12345"
	remote.Header.Set("Content-Type", "application/json")
	remote.Header.Set(hostProxyHeader, "desktop")
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("expected remote setup status 403, got %d: %s", remoteResponse.Code, remoteResponse.Body.String())
	}

	trusted := newRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	trusted.RemoteAddr = "127.0.0.1:12345"
	trusted.Header.Set("Content-Type", "application/json")
	trusted.Header.Set(mutationHeaderName, mutationHeaderValue)
	trusted.Header.Set(hostProxyHeader, "desktop")
	trusted.Header.Set(hostCapabilityHeader, config.HostManagementToken)
	trustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(trustedResponse, trusted)
	if trustedResponse.Code != http.StatusCreated {
		t.Fatalf("expected trusted setup status 201, got %d: %s", trustedResponse.Code, trustedResponse.Body.String())
	}
}

func TestRemoteSetupCanClaimOneTimeHostSession(t *testing.T) {
	config := testConfig(t)
	config.HostManagementToken = "bootstrap-token-with-enough-entropy"
	handler := NewHandler(config)

	status := newRequest(http.MethodGet, "/api/v1/setup/status", nil)
	status.RemoteAddr = "203.0.113.9:12345"
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, status)
	var setupStatus setupStatusResponse
	if err := json.NewDecoder(statusResponse.Body).Decode(&setupStatus); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if !setupStatus.SetupRequired || !setupStatus.HostClaimRequired {
		t.Fatalf("expected host claim requirement, got %#v", setupStatus)
	}

	claim := newRequest(http.MethodPost, "/api/v1/setup/claim", strings.NewReader(`{"token":"bootstrap-token-with-enough-entropy"}`))
	claim.RemoteAddr = "203.0.113.9:12345"
	claim.Header.Set("Content-Type", "application/json")
	claim.Header.Set(mutationHeaderName, mutationHeaderValue)
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusNoContent {
		t.Fatalf("expected claim status 204, got %d: %s", claimResponse.Code, claimResponse.Body.String())
	}
	cookie := findCookieNamed(t, claimResponse.Result().Cookies(), hostSessionCookieName)
	if cookie.Value == config.HostManagementToken {
		t.Fatal("host session cookie must not expose the bootstrap token")
	}

	setup := newRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{
		"workspaceName":"Studio",
		"ownerName":"Owner",
		"password":"local-password-123",
		"locale":"zh-CN",
		"timezone":"Asia/Shanghai"
	}`))
	setup.RemoteAddr = "203.0.113.9:12345"
	setup.Header.Set("Content-Type", "application/json")
	setup.Header.Set(mutationHeaderName, mutationHeaderValue)
	setup.AddCookie(cookie)
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("expected claimed setup status 201, got %d: %s", setupResponse.Code, setupResponse.Body.String())
	}

	secondClaim := newRequest(http.MethodPost, "/api/v1/setup/claim", strings.NewReader(`{"token":"bootstrap-token-with-enough-entropy"}`))
	secondClaim.RemoteAddr = "203.0.113.9:12345"
	secondClaim.Header.Set("Content-Type", "application/json")
	secondClaim.Header.Set(mutationHeaderName, mutationHeaderValue)
	secondClaimResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondClaimResponse, secondClaim)
	if secondClaimResponse.Code != http.StatusConflict {
		t.Fatalf("expected completed setup claim status 409, got %d", secondClaimResponse.Code)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	request := newRequest(http.MethodGet, "/api/v1/unknown", nil)
	response := httptest.NewRecorder()

	NewHandler(testConfig(t)).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", response.Code)
	}
}

func TestSetupSessionLogoutAndLogin(t *testing.T) {
	handler := NewHandler(testConfig(t))

	statusRequest := newRequest(
		http.MethodGet,
		"/api/v1/setup/status",
		nil,
	)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)

	var status setupStatusResponse
	if err := json.NewDecoder(statusResponse.Body).Decode(&status); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if !status.SetupRequired {
		t.Fatal("expected setup to be required")
	}

	setupRequest := newRequest(
		http.MethodPost,
		"/api/v1/setup",
		bytes.NewBufferString(`{
			"workspaceName": "我的工作空间",
			"ownerName": "Huang",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`),
	)
	setupRequest.Header.Set("Content-Type", "application/json")
	setupRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	setupRequest.Header.Set(hostCapabilityHeader, testHostManagementToken)
	setupRequest.RemoteAddr = "127.0.0.1:12345"
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setupRequest)

	if setupResponse.Code != http.StatusCreated {
		t.Fatalf(
			"expected setup status 201, got %d: %s",
			setupResponse.Code,
			setupResponse.Body.String(),
		)
	}

	sessionCookie := findSessionCookie(t, setupResponse.Result().Cookies())
	currentRequest := newRequest(
		http.MethodGet,
		"/api/v1/session",
		nil,
	)
	currentRequest.AddCookie(sessionCookie)
	currentResponse := httptest.NewRecorder()
	handler.ServeHTTP(currentResponse, currentRequest)

	if currentResponse.Code != http.StatusOK {
		t.Fatalf(
			"expected current session status 200, got %d: %s",
			currentResponse.Code,
			currentResponse.Body.String(),
		)
	}

	var current sessionResponse
	if err := json.NewDecoder(currentResponse.Body).Decode(&current); err != nil {
		t.Fatalf("decode current session: %v", err)
	}
	if current.Workspace.Name != "我的工作空间" {
		t.Fatalf("unexpected workspace %q", current.Workspace.Name)
	}
	if current.Role != "owner" {
		t.Fatalf("unexpected role %q", current.Role)
	}

	profileResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/profile",
		`{"displayName":"Huang Updated","locale":"en-US"}`,
		sessionCookie,
	)
	if profileResponse.Code != http.StatusOK {
		t.Fatalf(
			"expected profile status 200, got %d: %s",
			profileResponse.Code,
			profileResponse.Body.String(),
		)
	}
	var updatedProfile sessionResponse
	if err := json.NewDecoder(profileResponse.Body).Decode(&updatedProfile); err != nil {
		t.Fatalf("decode updated profile: %v", err)
	}
	if updatedProfile.User.DisplayName != "Huang Updated" ||
		updatedProfile.User.Locale != "en-US" ||
		updatedProfile.User.ID != current.User.ID ||
		updatedProfile.Role != current.Role {
		t.Fatalf("unexpected updated profile: %#v", updatedProfile)
	}

	refreshedRequest := newRequest(http.MethodGet, "/api/v1/session", nil)
	refreshedRequest.AddCookie(sessionCookie)
	refreshedResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshedResponse, refreshedRequest)
	var refreshed sessionResponse
	if err := json.NewDecoder(refreshedResponse.Body).Decode(&refreshed); err != nil {
		t.Fatalf("decode refreshed session: %v", err)
	}
	if refreshed.User.DisplayName != "Huang Updated" {
		t.Fatalf("profile update was not persisted: %#v", refreshed.User)
	}
	if refreshed.User.Locale != "en-US" {
		t.Fatalf("profile locale was not persisted: %#v", refreshed.User)
	}

	logoutRequest := newRequest(
		http.MethodDelete,
		"/api/v1/session",
		nil,
	)
	logoutRequest.AddCookie(sessionCookie)
	logoutRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logoutRequest)

	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("expected logout status 204, got %d", logoutResponse.Code)
	}

	rejectedRequest := newRequest(
		http.MethodGet,
		"/api/v1/session",
		nil,
	)
	rejectedRequest.AddCookie(sessionCookie)
	rejectedResponse := httptest.NewRecorder()
	handler.ServeHTTP(rejectedResponse, rejectedRequest)

	if rejectedResponse.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected revoked session status 401, got %d",
			rejectedResponse.Code,
		)
	}

	loginRequest := newRequest(
		http.MethodPost,
		"/api/v1/session",
		bytes.NewBufferString(`{"password":"local-password-123"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)

	if loginResponse.Code != http.StatusOK {
		t.Fatalf(
			"expected login status 200, got %d: %s",
			loginResponse.Code,
			loginResponse.Body.String(),
		)
	}
	if findSessionCookie(t, loginResponse.Result().Cookies()).Value == sessionCookie.Value {
		t.Fatal("expected login to rotate session token")
	}
}

func TestLoginRateLimitSurvivesHandlerRecreation(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-rate-limit@example.com",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}

	for attempt := 0; attempt < 10; attempt++ {
		failed := performJSONRequest(
			handler,
			http.MethodPost,
			"/api/v1/session",
			`{"email":"owner-rate-limit@example.com","password":"wrong-password"}`,
			nil,
		)
		if failed.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401: %s", attempt, failed.Code, failed.Body.String())
		}
	}

	restartedHandler := NewHandler(config)
	limited := performJSONRequest(
		restartedHandler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"owner-rate-limit@example.com","password":"owner-password-123"}`,
		nil,
	)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("restarted login status = %d, want 429: %s", limited.Code, limited.Body.String())
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited login should include Retry-After")
	}
}

func TestIndependentMemberLoginAndPermissionBoundary(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setupResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setupResponse.Code, setupResponse.Body.String())
	}
	ownerCookie := findSessionCookie(t, setupResponse.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setupResponse.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}
	member, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "reviewer@example.com",
			DisplayName: "Reviewer",
			Password:    "reviewer-password-123",
			Locale:      "zh-CN",
			Role:        "guest",
		},
	)
	if err != nil {
		t.Fatalf("create member account: %v", err)
	}
	loginResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"REVIEWER@example.com","password":"reviewer-password-123"}`,
		nil,
	)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf(
			"member login failed with %d: %s",
			loginResponse.Code,
			loginResponse.Body.String(),
		)
	}
	memberCookie := findSessionCookie(t, loginResponse.Result().Cookies())
	var memberSession sessionResponse
	if err := json.NewDecoder(loginResponse.Body).Decode(&memberSession); err != nil {
		t.Fatalf("decode member session: %v", err)
	}
	if memberSession.User.ID != member.ID ||
		memberSession.User.Email == nil ||
		*memberSession.User.Email != "reviewer@example.com" ||
		memberSession.Role != "guest" {
		t.Fatalf("unexpected member session: %#v", memberSession)
	}
	auditResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs",
		"",
		memberCookie,
	)
	if auditResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"guest audit access status = %d, want 403: %s",
			auditResponse.Code,
			auditResponse.Body.String(),
		)
	}
	notificationsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/notifications",
		"",
		memberCookie,
	)
	if notificationsResponse.Code != http.StatusOK {
		t.Fatalf(
			"guest notification access failed with %d: %s",
			notificationsResponse.Code,
			notificationsResponse.Body.String(),
		)
	}
	createProjectResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		`{"name":"Forbidden project","description":null}`,
		memberCookie,
	)
	if createProjectResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"guest project creation status = %d, want 403: %s",
			createProjectResponse.Code,
			createProjectResponse.Body.String(),
		)
	}
	sourceDownloadResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/missing/objects/content?key=secret.mov",
		"",
		memberCookie,
	)
	if sourceDownloadResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"guest source download status = %d, want 403: %s",
			sourceDownloadResponse.Code,
			sourceDownloadResponse.Body.String(),
		)
	}
	guestMembersResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/members",
		"",
		memberCookie,
	)
	if guestMembersResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"guest member directory status = %d, want 403: %s",
			guestMembersResponse.Code,
			guestMembersResponse.Body.String(),
		)
	}
	membersResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/members",
		"",
		ownerCookie,
	)
	if membersResponse.Code != http.StatusOK {
		t.Fatalf(
			"list members failed with %d: %s",
			membersResponse.Code,
			membersResponse.Body.String(),
		)
	}
	var members membershipListResponse
	if err := json.NewDecoder(membersResponse.Body).Decode(&members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(members.Items) != 2 {
		t.Fatalf("member count = %d, want 2", len(members.Items))
	}
	var ownerMembership membershipResponse
	for _, item := range members.Items {
		if item.Role == "owner" {
			ownerMembership = item
		}
	}
	lastOwnerResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/members/"+ownerMembership.ID,
		fmt.Sprintf(
			`{"role":"admin","status":"active","revision":%d}`,
			ownerMembership.Revision,
		),
		ownerCookie,
	)
	if lastOwnerResponse.Code != http.StatusConflict {
		t.Fatalf(
			"last owner demotion status = %d, want 409: %s",
			lastOwnerResponse.Code,
			lastOwnerResponse.Body.String(),
		)
	}
}

func TestMemberDirectoryRequiresManagementPermission(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setupResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Directory Studio",
			"ownerName":"Owner",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setupResponse.Code, setupResponse.Body.String())
	}
	ownerCookie := findSessionCookie(t, setupResponse.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setupResponse.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	for _, account := range []struct {
		email string
		role  string
	}{
		{email: "member@example.com", role: "member"},
		{email: "admin@example.com", role: "admin"},
	} {
		if _, err := config.Identity.CreateAccount(
			context.Background(),
			identity.CreateAccountInput{
				WorkspaceID: ownerSession.Workspace.ID,
				Email:       account.email,
				DisplayName: account.role,
				Password:    "account-password-123",
				Locale:      "zh-CN",
				Role:        account.role,
			},
		); err != nil {
			t.Fatalf("create %s account: %v", account.role, err)
		}
	}

	memberLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"member@example.com","password":"account-password-123"}`,
		nil,
	)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login failed: %d %s", memberLogin.Code, memberLogin.Body.String())
	}
	memberDirectory := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/members",
		"",
		findSessionCookie(t, memberLogin.Result().Cookies()),
	)
	if memberDirectory.Code != http.StatusForbidden {
		t.Fatalf("member directory status = %d, want 403: %s", memberDirectory.Code, memberDirectory.Body.String())
	}

	adminLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"admin@example.com","password":"account-password-123"}`,
		nil,
	)
	if adminLogin.Code != http.StatusOK {
		t.Fatalf("admin login failed: %d %s", adminLogin.Code, adminLogin.Body.String())
	}
	adminDirectory := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/members",
		"",
		findSessionCookie(t, adminLogin.Result().Cookies()),
	)
	if adminDirectory.Code != http.StatusOK {
		t.Fatalf("admin directory status = %d, want 200: %s", adminDirectory.Code, adminDirectory.Body.String())
	}

	ownerDirectory := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/members",
		"",
		ownerCookie,
	)
	if ownerDirectory.Code != http.StatusOK {
		t.Fatalf("owner directory status = %d, want 200: %s", ownerDirectory.Code, ownerDirectory.Body.String())
	}
}

func TestInvitationLifecycleAPI(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setupResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Invitation Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner@example.com",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setupResponse.Code, setupResponse.Body.String())
	}
	ownerCookie := findSessionCookie(t, setupResponse.Result().Cookies())

	createResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/invitations",
		`{"email":"member@example.com","role":"member"}`,
		ownerCookie,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create invitation failed with %d: %s",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}
	var created invitationSecretResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode invitation: %v", err)
	}
	firstToken := strings.TrimPrefix(created.URL, "/join/#")
	if firstToken == created.URL || firstToken == "" {
		t.Fatalf("invitation URL did not contain fragment token: %q", created.URL)
	}

	previewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/join-api/v1/invitations/preview",
		fmt.Sprintf(`{"token":%q}`, firstToken),
		nil,
	)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf(
			"preview invitation failed with %d: %s",
			previewResponse.Code,
			previewResponse.Body.String(),
		)
	}
	var preview invitationPreviewResponse
	if err := json.NewDecoder(previewResponse.Body).Decode(&preview); err != nil {
		t.Fatalf("decode invitation preview: %v", err)
	}
	if preview.WorkspaceName != "Invitation Studio" ||
		preview.Email != "member@example.com" ||
		preview.Role != "member" {
		t.Fatalf("unexpected invitation preview: %#v", preview)
	}

	resendResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/invitations/"+created.Invitation.ID+"/resend",
		"",
		ownerCookie,
	)
	if resendResponse.Code != http.StatusOK {
		t.Fatalf(
			"resend invitation failed with %d: %s",
			resendResponse.Code,
			resendResponse.Body.String(),
		)
	}
	var resent invitationSecretResponse
	if err := json.NewDecoder(resendResponse.Body).Decode(&resent); err != nil {
		t.Fatalf("decode resent invitation: %v", err)
	}
	secondToken := strings.TrimPrefix(resent.URL, "/join/#")
	if secondToken == firstToken || resent.Invitation.SendCount != 2 {
		t.Fatalf("invitation token was not rotated: %#v", resent)
	}
	oldPreviewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/join-api/v1/invitations/preview",
		fmt.Sprintf(`{"token":%q}`, firstToken),
		nil,
	)
	if oldPreviewResponse.Code != http.StatusNotFound {
		t.Fatalf(
			"old token preview status = %d, want 404: %s",
			oldPreviewResponse.Code,
			oldPreviewResponse.Body.String(),
		)
	}

	acceptResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/join-api/v1/invitations/accept",
		fmt.Sprintf(
			`{"token":%q,"displayName":"Member","password":"member-password-123","locale":"zh-CN"}`,
			secondToken,
		),
		nil,
	)
	if acceptResponse.Code != http.StatusCreated {
		t.Fatalf(
			"accept invitation failed with %d: %s",
			acceptResponse.Code,
			acceptResponse.Body.String(),
		)
	}
	memberCookie := findSessionCookie(t, acceptResponse.Result().Cookies())
	var memberSession sessionResponse
	if err := json.NewDecoder(acceptResponse.Body).Decode(&memberSession); err != nil {
		t.Fatalf("decode member session: %v", err)
	}
	if memberSession.Role != "member" ||
		memberSession.User.Email == nil ||
		*memberSession.User.Email != "member@example.com" {
		t.Fatalf("unexpected member session: %#v", memberSession)
	}

	reusedResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/join-api/v1/invitations/accept",
		fmt.Sprintf(
			`{"token":%q,"displayName":"Again","password":"another-password-123"}`,
			secondToken,
		),
		nil,
	)
	if reusedResponse.Code != http.StatusNotFound {
		t.Fatalf(
			"reused token status = %d, want 404: %s",
			reusedResponse.Code,
			reusedResponse.Body.String(),
		)
	}

	deniedResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/invitations",
		`{"email":"other@example.com","role":"guest"}`,
		memberCookie,
	)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"member invitation creation status = %d, want 403: %s",
			deniedResponse.Code,
			deniedResponse.Body.String(),
		)
	}
	listResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/invitations",
		"",
		ownerCookie,
	)
	if listResponse.Code != http.StatusOK {
		t.Fatalf(
			"list invitations failed with %d: %s",
			listResponse.Code,
			listResponse.Body.String(),
		)
	}
	var listed invitationListResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode invitations: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Status != "accepted" {
		t.Fatalf("unexpected invitation list: %#v", listed.Items)
	}

	auditResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?resourceType=invitation",
		"",
		ownerCookie,
	)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf(
			"list invitation audit logs failed with %d: %s",
			auditResponse.Code,
			auditResponse.Body.String(),
		)
	}
	var logs auditLogListResponse
	if err := json.NewDecoder(auditResponse.Body).Decode(&logs); err != nil {
		t.Fatalf("decode invitation audit logs: %v", err)
	}
	if len(logs.Items) < 2 {
		t.Fatalf("invitation audit log count = %d, want at least 2", len(logs.Items))
	}
}

func TestOpenRegistrationCreatesIsolatedMember(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Registration Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-registration@example.com",
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

	createProject := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(
			t,
			handler,
			ownerCookie,
			"Hidden Project",
			nil,
		),
		ownerCookie,
	)
	if createProject.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed with %d: %s",
			createProject.Code,
			createProject.Body.String(),
		)
	}

	settingsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/workspace/registration-settings",
		"",
		ownerCookie,
	)
	if settingsResponse.Code != http.StatusOK {
		t.Fatalf(
			"get settings failed with %d: %s",
			settingsResponse.Code,
			settingsResponse.Body.String(),
		)
	}
	var settings registrationSettingsResponse
	if err := json.NewDecoder(settingsResponse.Body).Decode(&settings); err != nil {
		t.Fatalf("decode registration settings: %v", err)
	}
	if settings.RegistrationEnabled {
		t.Fatal("registration should be disabled by default")
	}

	update := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/workspace/registration-settings",
		fmt.Sprintf(`{
			"registrationEnabled":true,
			"emailVerificationRequired":false,
			"revision":%d
		}`, settings.Revision),
		ownerCookie,
	)
	if update.Code != http.StatusOK {
		t.Fatalf("update settings failed with %d: %s", update.Code, update.Body.String())
	}

	publicInfo := performJSONRequest(
		handler,
		http.MethodGet,
		"/join-api/v1/registration",
		"",
		nil,
	)
	if publicInfo.Code != http.StatusOK {
		t.Fatalf(
			"public registration info failed with %d: %s",
			publicInfo.Code,
			publicInfo.Body.String(),
		)
	}
	var info publicRegistrationResponse
	if err := json.NewDecoder(publicInfo.Body).Decode(&info); err != nil {
		t.Fatalf("decode public registration info: %v", err)
	}
	if !info.RegistrationEnabled || info.DefaultWorkspaceRole != "member" {
		t.Fatalf("unexpected public registration info: %#v", info)
	}

	register := performJSONRequest(
		handler,
		http.MethodPost,
		"/join-api/v1/registration",
		`{
			"email":"open-member@example.com",
			"displayName":"Open Member",
			"password":"open-member-password-123",
			"locale":"zh-CN"
		}`,
		nil,
	)
	if register.Code != http.StatusCreated {
		t.Fatalf("register failed with %d: %s", register.Code, register.Body.String())
	}
	memberCookie := findSessionCookie(t, register.Result().Cookies())
	var member sessionResponse
	if err := json.NewDecoder(register.Body).Decode(&member); err != nil {
		t.Fatalf("decode registered session: %v", err)
	}
	if member.Role != "member" || member.User.Email == nil ||
		*member.User.Email != "open-member@example.com" {
		t.Fatalf("unexpected registered session: %#v", member)
	}

	projects := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects",
		"",
		memberCookie,
	)
	if projects.Code != http.StatusOK {
		t.Fatalf(
			"member project list failed with %d: %s",
			projects.Code,
			projects.Body.String(),
		)
	}
	var projectList projectListResponse
	if err := json.NewDecoder(projects.Body).Decode(&projectList); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(projectList.Items) != 0 {
		t.Fatalf(
			"registered member should not see unjoined projects: %#v",
			projectList.Items,
		)
	}
	registrationBody := `{
		"email":"open-member@example.com",
		"displayName":"Open Member",
		"password":"open-member-password-123",
		"locale":"zh-CN"
	}`
	for attempt := 0; attempt < 2; attempt++ {
		duplicate := performJSONRequest(handler, http.MethodPost, "/join-api/v1/registration", registrationBody, nil)
		if duplicate.Code != http.StatusConflict {
			t.Fatalf("duplicate registration attempt %d status = %d, want 409", attempt, duplicate.Code)
		}
	}
	limited := performJSONRequest(handler, http.MethodPost, "/join-api/v1/registration", registrationBody, nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("registration limit status = %d, want 429: %s", limited.Code, limited.Body.String())
	}
}

func TestSystemNetworkSettingsOwnerRoundTripAndRevisionGuard(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Network Settings Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-network@example.com",
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

	getSettings := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/system/network-settings",
		"",
		ownerCookie,
	)
	if getSettings.Code != http.StatusOK {
		t.Fatalf("get network settings failed with %d: %s", getSettings.Code, getSettings.Body.String())
	}
	var settings systemNetworkSettingsResponse
	if err := json.NewDecoder(getSettings.Body).Decode(&settings); err != nil {
		t.Fatalf("decode network settings: %v", err)
	}
	// A fresh install must not require HTTPS for remote access.
	if settings.RequireRemoteHTTPS || settings.EffectiveRequireRemoteHTTPS ||
		settings.EnvironmentForced || !settings.CurrentRequestSecure ||
		settings.Revision != 1 || settings.UpdatedBy != nil {
		t.Fatalf("unexpected default network settings: %#v", settings)
	}

	// Default off: remote plaintext access works out of the box.
	if code := plainRemoteStatus(handler); code != http.StatusOK {
		t.Fatalf("plaintext remote request with default settings status = %d, want 200", code)
	}

	update := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/system/network-settings",
		fmt.Sprintf(`{"requireRemoteHTTPS":true,"revision":%d}`, settings.Revision),
		ownerCookie,
	)
	if update.Code != http.StatusOK {
		t.Fatalf("update network settings failed with %d: %s", update.Code, update.Body.String())
	}
	if err := json.NewDecoder(update.Body).Decode(&settings); err != nil {
		t.Fatalf("decode updated network settings: %v", err)
	}
	if !settings.RequireRemoteHTTPS || !settings.EffectiveRequireRemoteHTTPS ||
		settings.Revision != 2 || settings.UpdatedBy == nil ||
		*settings.UpdatedBy != ownerSession.User.ID {
		t.Fatalf("unexpected updated network settings: %#v", settings)
	}

	// Saving takes effect immediately, with no Core restart.
	if code := plainRemoteStatus(handler); code != http.StatusUpgradeRequired {
		t.Fatalf("plaintext remote request after enabling status = %d, want 426", code)
	}
	// Loopback keeps working so the Owner is never locked out.
	loopbackInfo := performJSONRequest(handler, http.MethodGet, "/api/v1/system/info", "", ownerCookie)
	if loopbackInfo.Code != http.StatusOK {
		t.Fatalf("loopback request after enabling status = %d, want 200", loopbackInfo.Code)
	}

	stale := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/system/network-settings",
		`{"requireRemoteHTTPS":false,"revision":1}`,
		ownerCookie,
	)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale network settings status = %d, want 409: %s", stale.Code, stale.Body.String())
	}
	var staleBody errorEnvelope
	if err := json.NewDecoder(stale.Body).Decode(&staleBody); err != nil {
		t.Fatalf("decode stale network settings error: %v", err)
	}
	if staleBody.Error.Code != "system_settings.revision_conflict" {
		t.Fatalf("stale network settings error code = %q", staleBody.Error.Code)
	}

	// The stored decision survives a Core restart.
	restarted := NewHandler(testConfigFrom(t, config))
	if code := plainRemoteStatus(restarted); code != http.StatusUpgradeRequired {
		t.Fatalf("plaintext remote request after restart status = %d, want 426", code)
	}

	member, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "member-network@example.com",
			DisplayName: "Network Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"member-network@example.com","password":"member-password-123"}`,
		nil,
	)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login failed with %d: %s", memberLogin.Code, memberLogin.Body.String())
	}
	memberCookie := findSessionCookie(t, memberLogin.Result().Cookies())
	memberSettings := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/system/network-settings",
		"",
		memberCookie,
	)
	if memberSettings.Code != http.StatusForbidden {
		t.Fatalf("member network settings status = %d, want 403: %s", memberSettings.Code, memberSettings.Body.String())
	}
	memberUpdate := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/system/network-settings",
		`{"requireRemoteHTTPS":false,"revision":2}`,
		memberCookie,
	)
	if memberUpdate.Code != http.StatusForbidden {
		t.Fatalf("member network settings update status = %d, want 403", memberUpdate.Code)
	}
	if member.ID == "" {
		t.Fatal("member account ID is empty")
	}

	auditResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?action=system.network_settings_updated&resourceType=system_network_settings&resourceId=1",
		"",
		ownerCookie,
	)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("list network settings audit failed with %d: %s", auditResponse.Code, auditResponse.Body.String())
	}
	var auditLogs auditLogListResponse
	if err := json.NewDecoder(auditResponse.Body).Decode(&auditLogs); err != nil {
		t.Fatalf("decode network settings audit: %v", err)
	}
	if len(auditLogs.Items) != 1 ||
		auditLogs.Items[0].Action != "system.network_settings_updated" {
		t.Fatalf("unexpected network settings audit: %#v", auditLogs.Items)
	}

	// Turn it back off from a loopback page, then confirm remote plaintext works.
	reopen := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/system/network-settings",
		`{"requireRemoteHTTPS":false,"revision":2}`,
		ownerCookie,
	)
	if reopen.Code != http.StatusOK {
		t.Fatalf("disable network settings failed with %d: %s", reopen.Code, reopen.Body.String())
	}
	if code := plainRemoteStatus(handler); code != http.StatusOK {
		t.Fatalf("plaintext remote request after disabling status = %d, want 200", code)
	}
}

// TestSystemNetworkSettingsEnvironmentForcedCannotBeDisabled covers the
// deployment override: REVIEW_STUDIO_REQUIRE_HTTPS=1 pins enforcement on, the
// response marks the toggle as forced, and turning it off is rejected.
func TestSystemNetworkSettingsEnvironmentForcedCannotBeDisabled(t *testing.T) {
	t.Setenv("REVIEW_STUDIO_REQUIRE_HTTPS", "1")
	handler := NewHandler(testConfig(t))
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Forced HTTPS Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-forced@example.com",
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

	getSettings := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/system/network-settings",
		"",
		ownerCookie,
	)
	if getSettings.Code != http.StatusOK {
		t.Fatalf("get forced network settings failed with %d: %s", getSettings.Code, getSettings.Body.String())
	}
	var settings systemNetworkSettingsResponse
	if err := json.NewDecoder(getSettings.Body).Decode(&settings); err != nil {
		t.Fatalf("decode forced network settings: %v", err)
	}
	if settings.RequireRemoteHTTPS || !settings.EffectiveRequireRemoteHTTPS ||
		!settings.EnvironmentForced {
		t.Fatalf("unexpected forced network settings: %#v", settings)
	}
	if code := plainRemoteStatus(handler); code != http.StatusUpgradeRequired {
		t.Fatalf("environment-forced plaintext remote status = %d, want 426", code)
	}

	disable := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/system/network-settings",
		fmt.Sprintf(`{"requireRemoteHTTPS":false,"revision":%d}`, settings.Revision),
		ownerCookie,
	)
	if disable.Code != http.StatusConflict {
		t.Fatalf("disable forced network settings status = %d, want 409: %s", disable.Code, disable.Body.String())
	}
	var disableBody errorEnvelope
	if err := json.NewDecoder(disable.Body).Decode(&disableBody); err != nil {
		t.Fatalf("decode forced network settings error: %v", err)
	}
	if disableBody.Error.Code != "system_settings.environment_forced" {
		t.Fatalf("forced network settings error code = %q", disableBody.Error.Code)
	}
}

// TestSystemNetworkSettingsRejectsEnablingFromRemotePlaintext covers the
// self-lockout guard at the API boundary: an Owner on a remote plaintext page
// can read the setting but cannot switch enforcement on.
func TestSystemNetworkSettingsRejectsEnablingFromRemotePlaintext(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Lockout Guard Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-lockout@example.com",
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

	remotePlaintext := func(method string, body string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = bytes.NewBufferString(body)
		}
		request := httptest.NewRequest(
			method,
			"http://visto.test/api/v1/system/network-settings",
			reader,
		)
		request.RemoteAddr = "192.168.21.10:4567"
		request.Header.Set("Accept", "application/json")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet {
			request.Header.Set(mutationHeaderName, mutationHeaderValue)
		}
		request.AddCookie(ownerCookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	read := remotePlaintext(http.MethodGet, "")
	if read.Code != http.StatusOK {
		t.Fatalf("remote plaintext read status = %d, want 200: %s", read.Code, read.Body.String())
	}
	var settings systemNetworkSettingsResponse
	if err := json.NewDecoder(read.Body).Decode(&settings); err != nil {
		t.Fatalf("decode remote plaintext settings: %v", err)
	}
	// The page needs this flag to disable the toggle and explain why.
	if settings.CurrentRequestSecure {
		t.Fatal("currentRequestSecure = true for a remote plaintext request")
	}

	enable := remotePlaintext(
		http.MethodPut,
		fmt.Sprintf(`{"requireRemoteHTTPS":true,"revision":%d}`, settings.Revision),
	)
	if enable.Code != http.StatusConflict {
		t.Fatalf("remote plaintext enable status = %d, want 409: %s", enable.Code, enable.Body.String())
	}
	var enableBody errorEnvelope
	if err := json.NewDecoder(enable.Body).Decode(&enableBody); err != nil {
		t.Fatalf("decode remote plaintext enable error: %v", err)
	}
	if enableBody.Error.Code != "system_settings.insecure_origin" {
		t.Fatalf("remote plaintext enable error code = %q", enableBody.Error.Code)
	}
	// Nothing was persisted, so remote plaintext keeps working.
	if code := plainRemoteStatus(handler); code != http.StatusOK {
		t.Fatalf("plaintext remote request after rejected enable status = %d, want 200", code)
	}
}

// plainRemoteStatus issues a non-loopback plaintext request to a read-only
// endpoint, which is exactly what transportSecurity gates.
func plainRemoteStatus(handler http.Handler) int {
	request := httptest.NewRequest(
		http.MethodGet,
		"http://visto.test/api/v1/system/info",
		nil,
	)
	request.RemoteAddr = "192.168.21.10:4567"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

func TestAuditFiltersAndProjectActivity(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setupResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Activity Studio",
			"ownerName":"Owner",
			"password":"owner-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setupResponse.Code, setupResponse.Body.String())
	}
	cookie := findSessionCookie(t, setupResponse.Result().Cookies())
	var session sessionResponse
	if err := json.NewDecoder(setupResponse.Body).Decode(&session); err != nil {
		t.Fatalf("decode setup session: %v", err)
	}

	projectResponseRecorder := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(
			t,
			handler,
			cookie,
			"Activity Project",
			nil,
		),
		cookie,
	)
	if projectResponseRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed with %d: %s",
			projectResponseRecorder.Code,
			projectResponseRecorder.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(projectResponseRecorder.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	logsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?actorId="+session.User.ID+
			"&action=project.created&resourceType=project&resourceId="+project.ID,
		"",
		cookie,
	)
	if logsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list filtered activity failed with %d: %s",
			logsResponse.Code,
			logsResponse.Body.String(),
		)
	}
	var logs auditLogListResponse
	if err := json.NewDecoder(logsResponse.Body).Decode(&logs); err != nil {
		t.Fatalf("decode activity logs: %v", err)
	}
	if len(logs.Items) != 1 ||
		logs.Items[0].Action != "project.created" ||
		logs.Items[0].ActorID == nil ||
		*logs.Items[0].ActorID != session.User.ID {
		t.Fatalf("unexpected filtered activity: %#v", logs.Items)
	}

	projectActivityResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/activity",
		"",
		cookie,
	)
	if projectActivityResponse.Code != http.StatusOK {
		t.Fatalf(
			"list project activity failed with %d: %s",
			projectActivityResponse.Code,
			projectActivityResponse.Body.String(),
		)
	}
	var projectActivity auditLogListResponse
	if err := json.NewDecoder(projectActivityResponse.Body).Decode(&projectActivity); err != nil {
		t.Fatalf("decode project activity: %v", err)
	}
	if len(projectActivity.Items) != 1 ||
		projectActivity.Items[0].ResourceID != project.ID {
		t.Fatalf("unexpected project activity: %#v", projectActivity.Items)
	}

	emptyResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?action=project.deleted",
		"",
		cookie,
	)
	var empty auditLogListResponse
	if err := json.NewDecoder(emptyResponse.Body).Decode(&empty); err != nil {
		t.Fatalf("decode empty activity logs: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("unexpected logs for unmatched action: %#v", empty.Items)
	}
}

func TestMutationGuardRejectsRequestsWithoutLocalHeader(t *testing.T) {
	request := newRequest(
		http.MethodPost,
		"/api/v1/session",
		bytes.NewBufferString(`{"password":"local-password-123"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	NewHandler(testConfig(t)).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", response.Code)
	}

	var body errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "request.cross_site_rejected" {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestProjectAndCollectionAPI(t *testing.T) {
	handler := NewHandler(testConfig(t))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		newRequest(http.MethodGet, "/api/v1/projects", nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated status 401, got %d", unauthorized.Code)
	}

	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	missingStorage := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		`{"name":"缺少存储"}`,
		cookie,
	)
	if missingStorage.Code != http.StatusBadRequest ||
		!strings.Contains(missingStorage.Body.String(), "project.storage_required") {
		t.Fatalf(
			"missing project storage status = %d, want 400: %s",
			missingStorage.Code,
			missingStorage.Body.String(),
		)
	}

	projectDescription := "首轮交付"
	created := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(
			t,
			handler,
			cookie,
			"夏季广告",
			&projectDescription,
		),
		cookie,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create project failed with %d: %s", created.Code, created.Body.String())
	}
	var project projectResponse
	if err := json.NewDecoder(created.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if project.Name != "夏季广告" || project.Revision != 1 {
		t.Fatalf("unexpected project: %#v", project)
	}
	if project.PrimaryOwnerID == nil {
		t.Fatal("expected project primary owner in response")
	}

	listed := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects",
		"",
		cookie,
	)
	var projects projectListResponse
	if err := json.NewDecoder(listed.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(projects.Items) != 1 || projects.Items[0].ID != project.ID {
		t.Fatalf("unexpected project list: %#v", projects.Items)
	}

	updated := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/projects/"+project.ID,
		`{"name":"夏季广告 2026","description":null,"revision":1}`,
		cookie,
	)
	if updated.Code != http.StatusOK {
		t.Fatalf("update project failed with %d: %s", updated.Code, updated.Body.String())
	}
	if err := json.NewDecoder(updated.Body).Decode(&project); err != nil {
		t.Fatalf("decode updated project: %v", err)
	}

	stale := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/projects/"+project.ID,
		`{"name":"过期写入","description":null,"revision":1}`,
		cookie,
	)
	if stale.Code != http.StatusConflict {
		t.Fatalf("expected revision conflict 409, got %d", stale.Code)
	}

	createdCollection := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/collections",
		`{"name":"客户交付","description":"按顺序审阅"}`,
		cookie,
	)
	if createdCollection.Code != http.StatusCreated {
		t.Fatalf(
			"create collection failed with %d: %s",
			createdCollection.Code,
			createdCollection.Body.String(),
		)
	}
	var collection collectionResponse
	if err := json.NewDecoder(createdCollection.Body).Decode(&collection); err != nil {
		t.Fatalf("decode collection: %v", err)
	}

	collectionsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/collections",
		"",
		cookie,
	)
	var collections collectionListResponse
	if err := json.NewDecoder(collectionsResponse.Body).Decode(&collections); err != nil {
		t.Fatalf("decode collections: %v", err)
	}
	if len(collections.Items) != 1 || collections.Items[0].ID != collection.ID {
		t.Fatalf("unexpected collections: %#v", collections.Items)
	}

	archived := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/archive",
		`{"revision":2}`,
		cookie,
	)
	if archived.Code != http.StatusOK {
		t.Fatalf("archive project failed with %d: %s", archived.Code, archived.Body.String())
	}

	blockedCollection := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/collections",
		`{"name":"不可创建","description":null}`,
		cookie,
	)
	if blockedCollection.Code != http.StatusConflict {
		t.Fatalf(
			"expected archived project conflict, got %d",
			blockedCollection.Code,
		)
	}
}

func TestProjectAPIHidesUnjoinedProjectsFromMember(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)

	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "owner-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
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

	createdProjectResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(
			t,
			handler,
			ownerCookie,
			"Owner only",
			nil,
		),
		ownerCookie,
	)
	if createdProjectResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed with %d: %s",
			createdProjectResponse.Code,
			createdProjectResponse.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(createdProjectResponse.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	member, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "member-boundary@example.com",
			DisplayName: "Boundary Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	loginResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"member-boundary@example.com","password":"member-password-123"}`,
		nil,
	)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf(
			"member login failed with %d: %s",
			loginResponse.Code,
			loginResponse.Body.String(),
		)
	}
	memberCookie := findSessionCookie(t, loginResponse.Result().Cookies())

	memberList := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects",
		"",
		memberCookie,
	)
	if memberList.Code != http.StatusOK {
		t.Fatalf(
			"member project list failed with %d: %s",
			memberList.Code,
			memberList.Body.String(),
		)
	}
	var projects projectListResponse
	if err := json.NewDecoder(memberList.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(projects.Items) != 0 {
		t.Fatalf("member should not see unjoined projects: %#v", projects.Items)
	}

	memberGet := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID,
		"",
		memberCookie,
	)
	if memberGet.Code != http.StatusForbidden {
		t.Fatalf(
			"member direct project access status = %d, want 403: %s",
			memberGet.Code,
			memberGet.Body.String(),
		)
	}

	memberCreate := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		`{"name":"Should not exist","description":null}`,
		memberCookie,
	)
	if memberCreate.Code != http.StatusForbidden {
		t.Fatalf(
			"member project creation status = %d, want 403: %s",
			memberCreate.Code,
			memberCreate.Body.String(),
		)
	}

	membersResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/members",
		"",
		ownerCookie,
	)
	if membersResponse.Code != http.StatusOK {
		t.Fatalf(
			"list project members failed with %d: %s",
			membersResponse.Code,
			membersResponse.Body.String(),
		)
	}
	var members projectMemberListResponse
	if err := json.NewDecoder(membersResponse.Body).Decode(&members); err != nil {
		t.Fatalf("decode project members: %v", err)
	}
	if len(members.Items) != 1 || members.Items[0].RoleKey != "primary_owner" {
		t.Fatalf("unexpected initial project members: %#v", members.Items)
	}
	addedResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/members",
		fmt.Sprintf(
			`{"userId":%q,"roleKey":"member","permissions":{"assets.upload":true},"expiresAt":null}`,
			member.ID,
		),
		ownerCookie,
	)
	if addedResponse.Code != http.StatusCreated {
		t.Fatalf(
			"add project member failed with %d: %s",
			addedResponse.Code,
			addedResponse.Body.String(),
		)
	}
	var addedMember projectMemberResponse
	if err := json.NewDecoder(addedResponse.Body).Decode(&addedMember); err != nil {
		t.Fatalf("decode added project member: %v", err)
	}
	delegatedResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/projects/"+project.ID+"/members/"+addedMember.ID,
		fmt.Sprintf(
			`{"roleKey":"member","status":"active","permissions":{"project.members.manage":true},"expiresAt":null,"revision":%d}`,
			addedMember.Revision,
		),
		ownerCookie,
	)
	if delegatedResponse.Code != http.StatusOK {
		t.Fatalf("delegate member management failed with %d: %s", delegatedResponse.Code, delegatedResponse.Body.String())
	}
	if err := json.NewDecoder(delegatedResponse.Body).Decode(&addedMember); err != nil {
		t.Fatalf("decode delegated project member: %v", err)
	}
	escalationResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/projects/"+project.ID+"/members/"+addedMember.ID,
		fmt.Sprintf(
			`{"roleKey":"supervisor","status":"active","permissions":{},"expiresAt":null,"revision":%d}`,
			addedMember.Revision,
		),
		memberCookie,
	)
	if escalationResponse.Code != http.StatusForbidden {
		t.Fatalf("role-default escalation status = %d, want 403: %s", escalationResponse.Code, escalationResponse.Body.String())
	}
	guestExpiry := time.Now().UTC().Add(24 * time.Hour)
	guestResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/guests",
		fmt.Sprintf(
			`{"email":"project-guest@example.com","displayName":"Project Guest","permissions":{"reviews.comment":true},"expiresAt":%q}`,
			guestExpiry.Format(time.RFC3339Nano),
		),
		ownerCookie,
	)
	if guestResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create project guest failed with %d: %s",
			guestResponse.Code,
			guestResponse.Body.String(),
		)
	}
	var guest projectMemberResponse
	if err := json.NewDecoder(guestResponse.Body).Decode(&guest); err != nil {
		t.Fatalf("decode project guest: %v", err)
	}
	if guest.RoleKey != "guest" || guest.Email == nil ||
		*guest.Email != "project-guest@example.com" ||
		!guest.Permissions["reviews.comment"] {
		t.Fatalf("unexpected project guest: %#v", guest)
	}
	guestLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"project-guest@example.com","password":"guest-password-123"}`,
		nil,
	)
	if guestLogin.Code != http.StatusUnauthorized {
		t.Fatalf(
			"project guest login status = %d, want 401: %s",
			guestLogin.Code,
			guestLogin.Body.String(),
		)
	}
	memberListAfterJoin := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects",
		"",
		memberCookie,
	)
	var joinedProjects projectListResponse
	if err := json.NewDecoder(memberListAfterJoin.Body).Decode(&joinedProjects); err != nil {
		t.Fatalf("decode joined member projects: %v", err)
	}
	if len(joinedProjects.Items) != 1 || joinedProjects.Items[0].ID != project.ID {
		t.Fatalf("member should see joined project: %#v", joinedProjects.Items)
	}
	removePrimary := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/projects/"+project.ID+"/members/"+members.Items[0].ID,
		fmt.Sprintf(`{"revision":%d}`, members.Items[0].Revision),
		ownerCookie,
	)
	if removePrimary.Code != http.StatusForbidden {
		t.Fatalf(
			"remove primary owner status = %d, want 403: %s",
			removePrimary.Code,
			removePrimary.Body.String(),
		)
	}
	transferResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/transfer",
		fmt.Sprintf(
			`{"newPrimaryUserId":%q,"formerOwnerAction":"demote_member"}`,
			member.ID,
		),
		ownerCookie,
	)
	if transferResponse.Code != http.StatusOK {
		t.Fatalf(
			"transfer project failed with %d: %s",
			transferResponse.Code,
			transferResponse.Body.String(),
		)
	}
	var newPrimary projectMemberResponse
	if err := json.NewDecoder(transferResponse.Body).Decode(&newPrimary); err != nil {
		t.Fatalf("decode transfer response: %v", err)
	}
	if newPrimary.UserID != member.ID || newPrimary.RoleKey != "primary_owner" {
		t.Fatalf("unexpected transfer response: %#v", newPrimary)
	}
}

func TestAuthorizedRootMetadataAndRangeAPI(t *testing.T) {
	config := testConfig(t)
	// D2, docs/FREE_TIER_BOUNDARY_DESIGN.md §2: pin the locked-down deployment so
	// the "remote raw-object access" assertions below keep testing what they were
	// written for. Without the pin the wizard's default (host paths allowed for an
	// Owner web session) applies, which is a separate, deliberate behaviour covered
	// by TestOwnerWebSessionMayRegisterAStorageLocationWhenTheWizardAllowedIt.
	config.AllowWebHostPaths = boolSetting(false)
	handler := NewHandler(config)

	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	var session sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&session); err != nil {
		t.Fatalf("decode setup session: %v", err)
	}

	localPath := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(localPath, "clip.txt"),
		[]byte("0123456789abcdef"),
		0o600,
	); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	createBody, err := json.Marshal(authorizedRootRequest{
		DisplayName: "工作素材",
		LocalPath:   localPath,
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("encode root request: %v", err)
	}
	createResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/authorized-roots",
		string(createBody),
		cookie,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create root failed with %d: %s",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}
	if bytes.Contains(createResponse.Body.Bytes(), []byte(localPath)) {
		t.Fatal("create root response exposed the raw local path")
	}
	var root authorizedRootResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&root); err != nil {
		t.Fatalf("decode created root: %v", err)
	}

	scanResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/authorized-roots/"+root.ID+"/scan",
		"",
		cookie,
	)
	if scanResponse.Code != http.StatusOK {
		t.Fatalf(
			"scan root failed with %d: %s",
			scanResponse.Code,
			scanResponse.Body.String(),
		)
	}
	var scan scanResultResponse
	if err := json.NewDecoder(scanResponse.Body).Decode(&scan); err != nil {
		t.Fatalf("decode scan: %v", err)
	}
	if scan.Summary.Discovered != 1 || scan.Summary.New != 1 {
		t.Fatalf("unexpected initial scan summary: %#v", scan.Summary)
	}

	objectsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+"/objects",
		"",
		cookie,
	)
	if objectsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list objects failed with %d: %s",
			objectsResponse.Code,
			objectsResponse.Body.String(),
		)
	}
	var objects storageObjectListResponse
	if err := json.NewDecoder(objectsResponse.Body).Decode(&objects); err != nil {
		t.Fatalf("decode objects: %v", err)
	}
	if len(objects.Items) != 1 ||
		objects.Items[0].ObjectKey != "clip.txt" ||
		objects.Items[0].Status != "available" {
		t.Fatalf("unexpected storage objects: %#v", objects.Items)
	}

	libraryResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+
			"/library?search=clip&mediaType=other&state=waiting&page=1&pageSize=24",
		"",
		cookie,
	)
	if libraryResponse.Code != http.StatusOK {
		t.Fatalf(
			"query media library failed with %d: %s",
			libraryResponse.Code,
			libraryResponse.Body.String(),
		)
	}
	var library mediaLibraryPageResponse
	if err := json.NewDecoder(libraryResponse.Body).Decode(&library); err != nil {
		t.Fatalf("decode media library: %v", err)
	}
	if library.Total != 1 || len(library.Items) != 1 ||
		library.Items[0].Object.ObjectKey != "clip.txt" ||
		library.Items[0].Asset == nil ||
		library.Items[0].Asset.VersionNumber != 1 ||
		library.Page != 1 || library.PageSize != 24 {
		t.Fatalf("unexpected media library page: %#v", library)
	}

	projectResponseRecorder := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(t, handler, cookie, "客户项目", nil),
		cookie,
	)
	if projectResponseRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed with %d: %s",
			projectResponseRecorder.Code,
			projectResponseRecorder.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(projectResponseRecorder.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	assignBody, err := json.Marshal(mediaAssetProjectRequest{ProjectID: &project.ID})
	if err != nil {
		t.Fatalf("encode asset project: %v", err)
	}
	assignResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/storage-objects/"+objects.Items[0].ID+"/project",
		string(assignBody),
		cookie,
	)
	if assignResponse.Code != http.StatusOK {
		t.Fatalf(
			"assign asset project failed with %d: %s",
			assignResponse.Code,
			assignResponse.Body.String(),
		)
	}
	var assigned mediaLibraryAssetResponse
	if err := json.NewDecoder(assignResponse.Body).Decode(&assigned); err != nil {
		t.Fatalf("decode assigned asset: %v", err)
	}
	if assigned.ProjectID == nil || *assigned.ProjectID != project.ID {
		t.Fatalf("unexpected assigned asset: %#v", assigned)
	}
	filteredResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+"/library?projectId="+project.ID,
		"",
		cookie,
	)
	var filtered mediaLibraryPageResponse
	if err := json.NewDecoder(filteredResponse.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode project-filtered library: %v", err)
	}
	if filteredResponse.Code != http.StatusOK ||
		filtered.Total != 1 ||
		len(filtered.Items) != 1 {
		t.Fatalf("unexpected project-filtered library: %#v", filtered)
	}

	dueAt := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	reviewBody, err := json.Marshal(reviewSessionRequest{
		ProjectID:     project.ID,
		Name:          "First client review",
		DueAt:         &dueAt,
		AllowDownload: true,
		DecisionRule:  "any_reviewer",
		Participants: []reviewParticipantRequest{
			{DisplayName: "Client", Role: "reviewer"},
		},
		Items: []reviewItemRequest{
			{
				AssetID:        library.Items[0].Asset.ID,
				AssetVersionID: library.Items[0].Asset.VersionID,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode review session: %v", err)
	}
	reviewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions",
		string(reviewBody),
		cookie,
	)
	if reviewResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create review session failed with %d: %s",
			reviewResponse.Code,
			reviewResponse.Body.String(),
		)
	}
	var createdReview reviewSessionResponse
	if err := json.NewDecoder(reviewResponse.Body).Decode(&createdReview); err != nil {
		t.Fatalf("decode review session: %v", err)
	}
	if createdReview.Status != "draft" ||
		len(createdReview.Items) != 1 ||
		createdReview.Items[0].AssetVersionID != library.Items[0].Asset.VersionID ||
		len(createdReview.Participants) != 1 ||
		!createdReview.AllowDownload ||
		createdReview.DecisionRule != "any_reviewer" {
		t.Fatalf("unexpected review session: %#v", createdReview)
	}

	duplicateReviewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions",
		string(reviewBody),
		cookie,
	)
	if duplicateReviewResponse.Code != http.StatusConflict {
		t.Fatalf(
			"duplicate review session status = %d, want 409: %s",
			duplicateReviewResponse.Code,
			duplicateReviewResponse.Body.String(),
		)
	}

	reviewListResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/review-sessions?assetVersionId="+
			library.Items[0].Asset.VersionID,
		"",
		cookie,
	)
	if reviewListResponse.Code != http.StatusOK {
		t.Fatalf(
			"list review sessions failed with %d: %s",
			reviewListResponse.Code,
			reviewListResponse.Body.String(),
		)
	}
	var reviewList reviewSessionListResponse
	if err := json.NewDecoder(reviewListResponse.Body).Decode(&reviewList); err != nil {
		t.Fatalf("decode review session list: %v", err)
	}
	if len(reviewList.Items) != 1 || reviewList.Items[0].ID != createdReview.ID {
		t.Fatalf("unexpected review session list: %#v", reviewList.Items)
	}

	openBody, err := json.Marshal(revisionRequest{Revision: createdReview.Revision})
	if err != nil {
		t.Fatalf("encode review open request: %v", err)
	}
	openReviewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions/"+createdReview.ID+"/open",
		string(openBody),
		cookie,
	)
	if openReviewResponse.Code != http.StatusOK {
		t.Fatalf(
			"open review session failed with %d: %s",
			openReviewResponse.Code,
			openReviewResponse.Body.String(),
		)
	}
	var openedReview reviewSessionResponse
	if err := json.NewDecoder(openReviewResponse.Body).Decode(&openedReview); err != nil {
		t.Fatalf("decode opened review session: %v", err)
	}
	if openedReview.Status != "open" ||
		openedReview.Revision != createdReview.Revision+1 {
		t.Fatalf("unexpected opened review session: %#v", openedReview)
	}

	sharePassword := "4827"
	shareBody, err := json.Marshal(shareRequest{
		ReviewSessionID: openedReview.ID,
		Name:            "Client delivery",
		AllowComment:    true,
		AllowDownload:   false,
		RequireNickname: true,
		Password:        &sharePassword,
		NotifyUserIDs:   []string{session.User.ID},
	})
	if err != nil {
		t.Fatalf("encode share: %v", err)
	}
	shareResponseRecorder := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/shares",
		string(shareBody),
		cookie,
	)
	if shareResponseRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"create share failed with %d: %s",
			shareResponseRecorder.Code,
			shareResponseRecorder.Body.String(),
		)
	}
	var shareSecret shareSecretResponse
	if err := json.NewDecoder(shareResponseRecorder.Body).Decode(&shareSecret); err != nil {
		t.Fatalf("decode share secret: %v", err)
	}
	if shareSecret.Share == nil ||
		shareSecret.Share.Status != "active" ||
		shareSecret.Share.AllowDownload ||
		!shareSecret.Share.PasswordProtected ||
		shareSecret.Password == nil ||
		*shareSecret.Password != sharePassword ||
		!strings.Contains(shareSecret.URL, "/s/#") {
		t.Fatalf("unexpected share secret: %#v", shareSecret)
	}
	shareCredentialsRecorder := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/shares/"+shareSecret.Share.ID+"/credentials",
		"",
		cookie,
	)
	if shareCredentialsRecorder.Code != http.StatusOK {
		t.Fatalf(
			"read share credentials failed with %d: %s",
			shareCredentialsRecorder.Code,
			shareCredentialsRecorder.Body.String(),
		)
	}
	var shareCredentials shareCredentialsResponse
	if err := json.NewDecoder(shareCredentialsRecorder.Body).Decode(
		&shareCredentials,
	); err != nil {
		t.Fatalf("decode share credentials: %v", err)
	}
	if shareCredentials.Password == nil ||
		*shareCredentials.Password != sharePassword ||
		len(shareCredentials.Links) != 1 ||
		shareCredentials.Links[0].URL != shareSecret.URL {
		t.Fatalf("unexpected share credentials: %#v", shareCredentials)
	}
	shareNotificationResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/notifications",
		"",
		cookie,
	)
	if shareNotificationResponse.Code != http.StatusOK {
		t.Fatalf(
			"list share notifications failed with %d: %s",
			shareNotificationResponse.Code,
			shareNotificationResponse.Body.String(),
		)
	}
	var shareNotifications notificationListResponse
	if err := json.NewDecoder(shareNotificationResponse.Body).Decode(
		&shareNotifications,
	); err != nil {
		t.Fatalf("decode share notifications: %v", err)
	}
	shareCreatedNotification := false
	for _, item := range shareNotifications.Items {
		if item.Type == "share.created" &&
			item.ResourceType == "share" &&
			item.ResourceID == shareSecret.Share.ID {
			shareCreatedNotification = true
			break
		}
	}
	if !shareCreatedNotification {
		t.Fatalf(
			"expected share notification for selected member: %#v",
			shareNotifications.Items,
		)
	}
	if _, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: session.Workspace.ID,
			Email:       "outside-reviewer@example.com",
			DisplayName: "Outside Reviewer",
			Password:    "outside-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	); err != nil {
		t.Fatalf("create outside member: %v", err)
	}
	outsideLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"outside-reviewer@example.com","password":"outside-password-123"}`,
		nil,
	)
	if outsideLogin.Code != http.StatusOK {
		t.Fatalf(
			"outside member login failed with %d: %s",
			outsideLogin.Code,
			outsideLogin.Body.String(),
		)
	}
	outsideCookie := findSessionCookie(t, outsideLogin.Result().Cookies())
	outsideReviews := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/review-sessions?assetVersionId="+
			library.Items[0].Asset.VersionID,
		"",
		outsideCookie,
	)
	if outsideReviews.Code != http.StatusOK {
		t.Fatalf(
			"outside review list failed with %d: %s",
			outsideReviews.Code,
			outsideReviews.Body.String(),
		)
	}
	var outsideReviewList reviewSessionListResponse
	if err := json.NewDecoder(outsideReviews.Body).Decode(&outsideReviewList); err != nil {
		t.Fatalf("decode outside review list: %v", err)
	}
	if len(outsideReviewList.Items) != 0 {
		t.Fatalf("outside member should not see review sessions: %#v", outsideReviewList.Items)
	}
	outsideLibrary := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+"/library",
		"",
		outsideCookie,
	)
	if outsideLibrary.Code != http.StatusOK {
		t.Fatalf(
			"outside media library failed with %d: %s",
			outsideLibrary.Code,
			outsideLibrary.Body.String(),
		)
	}
	var outsideLibraryPage mediaLibraryPageResponse
	if err := json.NewDecoder(outsideLibrary.Body).Decode(&outsideLibraryPage); err != nil {
		t.Fatalf("decode outside media library: %v", err)
	}
	if len(outsideLibraryPage.Items) != 0 || outsideLibraryPage.Total != 0 {
		t.Fatalf("outside member should not see media: %#v", outsideLibraryPage)
	}
	outsideReviewGet := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/review-sessions/"+openedReview.ID,
		"",
		outsideCookie,
	)
	if outsideReviewGet.Code != http.StatusForbidden {
		t.Fatalf(
			"outside review get status = %d, want 403: %s",
			outsideReviewGet.Code,
			outsideReviewGet.Body.String(),
		)
	}
	outsideShares := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/shares",
		"",
		outsideCookie,
	)
	if outsideShares.Code != http.StatusOK {
		t.Fatalf(
			"outside share list failed with %d: %s",
			outsideShares.Code,
			outsideShares.Body.String(),
		)
	}
	var outsideShareList shareListResponse
	if err := json.NewDecoder(outsideShares.Body).Decode(&outsideShareList); err != nil {
		t.Fatalf("decode outside share list: %v", err)
	}
	if len(outsideShareList.Items) != 0 {
		t.Fatalf("outside member should not see shares: %#v", outsideShareList.Items)
	}
	outsideShareGet := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/shares/"+shareSecret.Share.ID,
		"",
		outsideCookie,
	)
	if outsideShareGet.Code != http.StatusForbidden {
		t.Fatalf(
			"outside share get status = %d, want 403: %s",
			outsideShareGet.Code,
			outsideShareGet.Body.String(),
		)
	}
	readOnlyShareBody, err := json.Marshal(shareRequest{
		ReviewSessionID: openedReview.ID,
		Name:            "Read only delivery",
		AllowComment:    false,
		AllowDownload:   false,
		RequireNickname: false,
	})
	if err != nil {
		t.Fatalf("encode read-only share: %v", err)
	}
	readOnlyShareResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/shares",
		string(readOnlyShareBody),
		cookie,
	)
	if readOnlyShareResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create read-only share failed with %d: %s",
			readOnlyShareResponse.Code,
			readOnlyShareResponse.Body.String(),
		)
	}
	var readOnlyShareSecret shareSecretResponse
	if err := json.NewDecoder(readOnlyShareResponse.Body).Decode(
		&readOnlyShareSecret,
	); err != nil {
		t.Fatalf("decode read-only share secret: %v", err)
	}
	_, readOnlyEntryToken, found := strings.Cut(readOnlyShareSecret.URL, "#")
	if !found || readOnlyEntryToken == "" {
		t.Fatalf(
			"read-only share URL did not contain an entry token: %q",
			readOnlyShareSecret.URL,
		)
	}
	readOnlyOpenResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/entries/"+readOnlyEntryToken+"/open",
		"",
		nil,
	)
	if readOnlyOpenResponse.Code != http.StatusOK {
		t.Fatalf(
			"open read-only public share failed with %d: %s",
			readOnlyOpenResponse.Code,
			readOnlyOpenResponse.Body.String(),
		)
	}
	var readOnlyEntry publicEntryResponse
	if err := json.NewDecoder(readOnlyOpenResponse.Body).Decode(
		&readOnlyEntry,
	); err != nil {
		t.Fatalf("decode read-only public share: %v", err)
	}
	if readOnlyEntry.Share == nil ||
		readOnlyEntry.Share.AllowComment ||
		len(readOnlyEntry.Share.Items) != 1 {
		t.Fatalf("unexpected read-only public share: %#v", readOnlyEntry)
	}
	readOnlyCookie := findShareSessionCookie(
		t,
		readOnlyOpenResponse.Result().Cookies(),
	)
	readOnlyThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+readOnlyEntry.Share.Items[0].ID+"/threads",
		`{
			"body":"不应允许提交",
			"annotation":{
				"kind":"time_point",
				"timeStartUs":1200000,
				"geometryVersion":1
			}
		}`,
		readOnlyCookie,
	)
	if readOnlyThreadResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"read-only comment status = %d, want 403: %s",
			readOnlyThreadResponse.Code,
			readOnlyThreadResponse.Body.String(),
		)
	}
	readOnlyDecisionResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/decisions",
		fmt.Sprintf(
			`{"reviewItemId":%q,"decision":"approved","note":""}`,
			readOnlyEntry.Share.Items[0].ID,
		),
		readOnlyCookie,
	)
	if readOnlyDecisionResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"read-only decision status = %d, want 403: %s",
			readOnlyDecisionResponse.Code,
			readOnlyDecisionResponse.Body.String(),
		)
	}
	_, entryToken, found := strings.Cut(shareSecret.URL, "#")
	if !found || entryToken == "" {
		t.Fatalf("share URL did not contain an entry token: %q", shareSecret.URL)
	}
	visitorCodeResponseRecorder := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/shares/"+shareSecret.Share.ID+"/visitor-codes",
		`{"displayName":"Client A","expiresAt":null}`,
		cookie,
	)
	if visitorCodeResponseRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"create visitor code failed with %d: %s",
			visitorCodeResponseRecorder.Code,
			visitorCodeResponseRecorder.Body.String(),
		)
	}
	var visitorCode visitorCodeResponse
	if err := json.NewDecoder(visitorCodeResponseRecorder.Body).Decode(&visitorCode); err != nil {
		t.Fatalf("decode visitor code: %v", err)
	}
	if visitorCode.Code == "" ||
		visitorCode.DisplayName != "Client A" ||
		visitorCode.CodePrefix == "" {
		t.Fatalf("unexpected visitor code: %#v", visitorCode)
	}

	openShareResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/entries/"+entryToken+"/open",
		"",
		nil,
	)
	if openShareResponse.Code != http.StatusOK {
		t.Fatalf(
			"open public share failed with %d: %s",
			openShareResponse.Code,
			openShareResponse.Body.String(),
		)
	}
	var publicEntry publicEntryResponse
	if err := json.NewDecoder(openShareResponse.Body).Decode(&publicEntry); err != nil {
		t.Fatalf("decode public share entry: %v", err)
	}
	if publicEntry.Status != "password_required" || publicEntry.Share != nil {
		t.Fatalf("protected share exposed content before verification: %#v", publicEntry)
	}
	shareCookie := findShareSessionCookie(
		t,
		openShareResponse.Result().Cookies(),
	)
	wrongPasswordResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/session/verify",
		`{"password":"wrong-password"}`,
		shareCookie,
	)
	if wrongPasswordResponse.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected wrong share password status 401, got %d: %s",
			wrongPasswordResponse.Code,
			wrongPasswordResponse.Body.String(),
		)
	}
	verifyResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/session/verify",
		`{"password":"4827"}`,
		shareCookie,
	)
	if verifyResponse.Code != http.StatusOK {
		t.Fatalf(
			"verify share failed with %d: %s",
			verifyResponse.Code,
			verifyResponse.Body.String(),
		)
	}
	if err := json.NewDecoder(verifyResponse.Body).Decode(&publicEntry); err != nil {
		t.Fatalf("decode verified share: %v", err)
	}
	if publicEntry.Share == nil ||
		len(publicEntry.Share.Items) != 1 ||
		publicEntry.Share.Items[0].VersionNumber != 1 ||
		publicEntry.Share.Items[0].SizeBytes != 16 ||
		publicEntry.Share.Items[0].DownloadURL != nil ||
		publicEntry.Share.Visitor.Identified ||
		publicEntry.Share.Visitor.IdentityMethod != "anonymous" {
		t.Fatalf("unexpected verified public share: %#v", publicEntry)
	}
	identifyBody, err := json.Marshal(identifyShareRequest{
		Method: "verification_code",
		Code:   visitorCode.Code,
	})
	if err != nil {
		t.Fatalf("encode public identity: %v", err)
	}
	identifyResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/session/identity",
		string(identifyBody),
		shareCookie,
	)
	if identifyResponse.Code != http.StatusOK {
		t.Fatalf(
			"identify public visitor failed with %d: %s",
			identifyResponse.Code,
			identifyResponse.Body.String(),
		)
	}
	var identifiedShare publicShareResponse
	if err := json.NewDecoder(identifyResponse.Body).Decode(&identifiedShare); err != nil {
		t.Fatalf("decode identified public share: %v", err)
	}
	if identifiedShare.Visitor.DisplayName == nil ||
		*identifiedShare.Visitor.DisplayName != "Client A" ||
		identifiedShare.Visitor.IdentityMethod != "verification_code" ||
		!identifiedShare.Visitor.Identified ||
		!identifiedShare.Visitor.Verified {
		t.Fatalf("unexpected identified visitor: %#v", identifiedShare.Visitor)
	}
	threadBody := `{
		"body":"这里需要缩短",
		"annotation":{
			"kind":"time_range",
			"timeStartUs":1200000,
			"timeEndUs":2600000,
			"geometryVersion":1
		}
	}`
	createThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		threadBody,
		shareCookie,
	)
	if createThreadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create public comment thread failed with %d: %s",
			createThreadResponse.Code,
			createThreadResponse.Body.String(),
		)
	}
	var createdThread commentThreadResponse
	if err := json.NewDecoder(createThreadResponse.Body).Decode(&createdThread); err != nil {
		t.Fatalf("decode public comment thread: %v", err)
	}
	if createdThread.Annotation.Kind != "time_range" ||
		createdThread.Annotation.TimeStartUs == nil ||
		*createdThread.Annotation.TimeStartUs != 1_200_000 ||
		createdThread.Annotation.TimeEndUs == nil ||
		*createdThread.Annotation.TimeEndUs != 2_600_000 ||
		createdThread.Author.DisplayName != "Client A" ||
		len(createdThread.Comments) != 1 {
		t.Fatalf("unexpected public comment thread: %#v", createdThread)
	}
	listThreadsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		"",
		shareCookie,
	)
	if listThreadsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list public comment threads failed with %d: %s",
			listThreadsResponse.Code,
			listThreadsResponse.Body.String(),
		)
	}
	var publicThreads commentThreadListResponse
	if err := json.NewDecoder(listThreadsResponse.Body).Decode(&publicThreads); err != nil {
		t.Fatalf("decode public comment threads: %v", err)
	}
	if len(publicThreads.Items) != 1 ||
		publicThreads.Items[0].ID != createdThread.ID {
		t.Fatalf("unexpected public comment list: %#v", publicThreads.Items)
	}
	replyResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+
			"/threads/"+createdThread.ID+"/comments",
		`{"body":"这个区间确实可以更紧凑"}`,
		shareCookie,
	)
	if replyResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create public comment reply failed with %d: %s",
			replyResponse.Code,
			replyResponse.Body.String(),
		)
	}
	var repliedThread commentThreadResponse
	if err := json.NewDecoder(replyResponse.Body).Decode(&repliedThread); err != nil {
		t.Fatalf("decode public comment reply: %v", err)
	}
	if len(repliedThread.Comments) != 2 ||
		!repliedThread.Comments[1].CanEdit ||
		!repliedThread.Comments[1].CanDelete ||
		!repliedThread.CanReply {
		t.Fatalf("unexpected replied public thread: %#v", repliedThread)
	}
	editReplyResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+
			"/threads/"+createdThread.ID+"/comments/"+
			repliedThread.Comments[1].ID,
		`{"body":"这个区间可以再紧凑一点"}`,
		shareCookie,
	)
	if editReplyResponse.Code != http.StatusOK {
		t.Fatalf(
			"edit public comment reply failed with %d: %s",
			editReplyResponse.Code,
			editReplyResponse.Body.String(),
		)
	}
	var editedReplyThread commentThreadResponse
	if err := json.NewDecoder(editReplyResponse.Body).Decode(
		&editedReplyThread,
	); err != nil {
		t.Fatalf("decode edited public comment reply: %v", err)
	}
	if editedReplyThread.Comments[1].Body != "这个区间可以再紧凑一点" ||
		editedReplyThread.Comments[1].EditedAt == nil {
		t.Fatalf("unexpected edited public reply: %#v", editedReplyThread)
	}
	deleteReplyResponse := performJSONRequest(
		handler,
		http.MethodDelete,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+
			"/threads/"+createdThread.ID+"/comments/"+
			editedReplyThread.Comments[1].ID,
		"",
		shareCookie,
	)
	if deleteReplyResponse.Code != http.StatusNoContent {
		t.Fatalf(
			"delete public comment reply failed with %d: %s",
			deleteReplyResponse.Code,
			deleteReplyResponse.Body.String(),
		)
	}
	listAfterDeleteResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		"",
		shareCookie,
	)
	if listAfterDeleteResponse.Code != http.StatusOK {
		t.Fatalf(
			"list public threads after delete failed with %d: %s",
			listAfterDeleteResponse.Code,
			listAfterDeleteResponse.Body.String(),
		)
	}
	var publicThreadsAfterDelete commentThreadListResponse
	if err := json.NewDecoder(listAfterDeleteResponse.Body).Decode(
		&publicThreadsAfterDelete,
	); err != nil {
		t.Fatalf("decode public threads after delete: %v", err)
	}
	if len(publicThreadsAfterDelete.Items) != 1 ||
		len(publicThreadsAfterDelete.Items[0].Comments) != 1 {
		t.Fatalf(
			"unexpected public threads after delete: %#v",
			publicThreadsAfterDelete.Items,
		)
	}
	resolveThreadBody, err := json.Marshal(revisionRequest{
		Revision: publicThreadsAfterDelete.Items[0].Revision,
	})
	if err != nil {
		t.Fatalf("encode thread resolution: %v", err)
	}
	resolveThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions/"+openedReview.ID+
			"/threads/"+createdThread.ID+"/resolve",
		string(resolveThreadBody),
		cookie,
	)
	if resolveThreadResponse.Code != http.StatusOK {
		t.Fatalf(
			"resolve comment thread failed with %d: %s",
			resolveThreadResponse.Code,
			resolveThreadResponse.Body.String(),
		)
	}
	var resolvedThread commentThreadResponse
	if err := json.NewDecoder(resolveThreadResponse.Body).Decode(
		&resolvedThread,
	); err != nil {
		t.Fatalf("decode resolved thread: %v", err)
	}
	if resolvedThread.Status != "resolved" ||
		resolvedThread.ResolvedBy == nil ||
		resolvedThread.ResolvedAt == nil ||
		!resolvedThread.CanReopen ||
		resolvedThread.CanResolve {
		t.Fatalf("unexpected resolved thread: %#v", resolvedThread)
	}
	reopenThreadBody, err := json.Marshal(revisionRequest{
		Revision: resolvedThread.Revision,
	})
	if err != nil {
		t.Fatalf("encode thread reopen: %v", err)
	}
	reopenThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions/"+openedReview.ID+
			"/threads/"+createdThread.ID+"/reopen",
		string(reopenThreadBody),
		cookie,
	)
	if reopenThreadResponse.Code != http.StatusOK {
		t.Fatalf(
			"reopen comment thread failed with %d: %s",
			reopenThreadResponse.Code,
			reopenThreadResponse.Body.String(),
		)
	}
	var reopenedThread commentThreadResponse
	if err := json.NewDecoder(reopenThreadResponse.Body).Decode(
		&reopenedThread,
	); err != nil {
		t.Fatalf("decode reopened thread: %v", err)
	}
	if reopenedThread.Status != "open" ||
		reopenedThread.ResolvedBy != nil ||
		reopenedThread.ResolvedAt != nil ||
		!reopenedThread.CanResolve {
		t.Fatalf("unexpected reopened thread: %#v", reopenedThread)
	}
	invalidThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		`{
			"body":"无效区间",
			"annotation":{
				"kind":"time_range",
				"timeStartUs":3000000,
				"timeEndUs":2000000,
				"geometryVersion":1
			}
		}`,
		shareCookie,
	)
	if invalidThreadResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected invalid annotation status 400, got %d: %s",
			invalidThreadResponse.Code,
			invalidThreadResponse.Body.String(),
		)
	}
	publicDecisionResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/decisions",
		fmt.Sprintf(
			`{"reviewItemId":%q,"decision":"rejected","note":"方向不符合交付要求"}`,
			publicEntry.Share.Items[0].ID,
		),
		shareCookie,
	)
	if publicDecisionResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create public decision failed with %d: %s",
			publicDecisionResponse.Code,
			publicDecisionResponse.Body.String(),
		)
	}
	var publicDecision reviewDecisionResponse
	if err := json.NewDecoder(publicDecisionResponse.Body).Decode(
		&publicDecision,
	); err != nil {
		t.Fatalf("decode public decision: %v", err)
	}
	if publicDecision.Decision != "rejected" ||
		publicDecision.ReviewItemID == nil ||
		*publicDecision.ReviewItemID != publicEntry.Share.Items[0].ID ||
		publicDecision.Actor.DisplayName != "Client A" {
		t.Fatalf("unexpected public decision: %#v", publicDecision)
	}
	crossItemDecisionResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/decisions",
		`{"reviewItemId":"other-item","decision":"approved","note":""}`,
		shareCookie,
	)
	if crossItemDecisionResponse.Code != http.StatusNotFound {
		t.Fatalf(
			"expected cross-item decision status 404, got %d: %s",
			crossItemDecisionResponse.Code,
			crossItemDecisionResponse.Body.String(),
		)
	}
	managerDecisionResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions/"+openedReview.ID+"/decisions",
		`{"reviewItemId":"","decision":"approved","note":"已确认最终版本"}`,
		cookie,
	)
	if managerDecisionResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create manager decision failed with %d: %s",
			managerDecisionResponse.Code,
			managerDecisionResponse.Body.String(),
		)
	}
	decisionHistoryResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/review-sessions/"+openedReview.ID+"/decisions",
		"",
		cookie,
	)
	if decisionHistoryResponse.Code != http.StatusOK {
		t.Fatalf(
			"list review decisions failed with %d: %s",
			decisionHistoryResponse.Code,
			decisionHistoryResponse.Body.String(),
		)
	}
	var decisionHistory reviewDecisionListResponse
	if err := json.NewDecoder(decisionHistoryResponse.Body).Decode(
		&decisionHistory,
	); err != nil {
		t.Fatalf("decode review decision history: %v", err)
	}
	if len(decisionHistory.Items) != 2 ||
		decisionHistory.Items[0].Decision != "approved" ||
		decisionHistory.Items[1].Decision != "rejected" {
		t.Fatalf("unexpected review decision history: %#v", decisionHistory.Items)
	}
	decisionReviewResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/review-sessions/"+openedReview.ID,
		"",
		cookie,
	)
	if decisionReviewResponse.Code != http.StatusOK {
		t.Fatalf(
			"read review after decisions failed with %d: %s",
			decisionReviewResponse.Code,
			decisionReviewResponse.Body.String(),
		)
	}
	var decisionReview reviewSessionResponse
	if err := json.NewDecoder(decisionReviewResponse.Body).Decode(
		&decisionReview,
	); err != nil {
		t.Fatalf("decode review after decisions: %v", err)
	}
	if decisionReview.Status != "approved" ||
		len(decisionReview.Items) != 1 ||
		decisionReview.Items[0].Status != "approved" {
		t.Fatalf("unexpected review after decisions: %#v", decisionReview)
	}
	closeReviewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions/"+openedReview.ID+"/close",
		fmt.Sprintf(`{"revision":%d}`, decisionReview.Revision),
		cookie,
	)
	if closeReviewResponse.Code != http.StatusOK {
		t.Fatalf(
			"close review failed with %d: %s",
			closeReviewResponse.Code,
			closeReviewResponse.Body.String(),
		)
	}
	var closedReview reviewSessionResponse
	if err := json.NewDecoder(closeReviewResponse.Body).Decode(&closedReview); err != nil {
		t.Fatalf("decode closed review: %v", err)
	}
	closedPublicResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/share-api/v1/share",
		"",
		shareCookie,
	)
	if closedPublicResponse.Code != http.StatusOK {
		t.Fatalf(
			"read closed public review failed with %d: %s",
			closedPublicResponse.Code,
			closedPublicResponse.Body.String(),
		)
	}
	var closedPublicShare publicShareResponse
	if err := json.NewDecoder(closedPublicResponse.Body).Decode(
		&closedPublicShare,
	); err != nil {
		t.Fatalf("decode closed public review: %v", err)
	}
	if closedPublicShare.ReviewStatus != "closed" ||
		closedPublicShare.AllowComment {
		t.Fatalf(
			"closed review exposed comment permission: %#v",
			closedPublicShare,
		)
	}
	deniedThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		threadBody,
		shareCookie,
	)
	if deniedThreadResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"expected closed review comment status 403, got %d: %s",
			deniedThreadResponse.Code,
			deniedThreadResponse.Body.String(),
		)
	}
	reopenReviewResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/review-sessions/"+closedReview.ID+"/open",
		fmt.Sprintf(`{"revision":%d}`, closedReview.Revision),
		cookie,
	)
	if reopenReviewResponse.Code != http.StatusOK {
		t.Fatalf(
			"reopen review failed with %d: %s",
			reopenReviewResponse.Code,
			reopenReviewResponse.Body.String(),
		)
	}
	var reopenedReview reviewSessionResponse
	if err := json.NewDecoder(reopenReviewResponse.Body).Decode(
		&reopenedReview,
	); err != nil {
		t.Fatalf("decode reopened review: %v", err)
	}
	if reopenedReview.Status != "open" || reopenedReview.ClosedAt != nil {
		t.Fatalf("unexpected reopened review: %#v", reopenedReview)
	}
	pointThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		`{
			"body":"Move this element",
			"annotation":{
				"kind":"point",
				"timeStartUs":null,
				"timeEndUs":null,
				"geometry":{"shape":"point","x":0.25,"y":0.4},
				"geometryVersion":1
			}
		}`,
		shareCookie,
	)
	if pointThreadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create public point comment failed with %d: %s",
			pointThreadResponse.Code,
			pointThreadResponse.Body.String(),
		)
	}
	var publicPointThread commentThreadResponse
	if err := json.NewDecoder(pointThreadResponse.Body).Decode(
		&publicPointThread,
	); err != nil {
		t.Fatalf("decode public point comment: %v", err)
	}
	if publicPointThread.Annotation.Kind != "point" ||
		publicPointThread.Annotation.Geometry == nil ||
		publicPointThread.Annotation.Geometry.Shape != "point" ||
		publicPointThread.Annotation.Geometry.X != 0.25 ||
		publicPointThread.Annotation.Geometry.Y != 0.4 {
		t.Fatalf(
			"unexpected public point annotation: %#v",
			publicPointThread.Annotation,
		)
	}
	drawingThreadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/threads",
		`{
			"body":"Draw this correction",
			"annotation":{
				"kind":"drawing",
				"timeStartUs":null,
				"timeEndUs":null,
				"geometry":{
					"shape":"drawing",
					"elements":[
						{
							"tool":"brush",
							"color":"#ff5c7a",
							"strokeWidth":4,
							"points":[{"x":0.1,"y":0.1},{"x":0.2,"y":0.2}]
						},
						{
							"tool":"rect",
							"color":"#64d081",
							"strokeWidth":4,
							"x":0.3,
							"y":0.25,
							"width":0.4,
							"height":0.2
						}
					]
				},
				"geometryVersion":1
			}
		}`,
		shareCookie,
	)
	if drawingThreadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create public drawing comment failed with %d: %s",
			drawingThreadResponse.Code,
			drawingThreadResponse.Body.String(),
		)
	}
	var publicDrawingThread commentThreadResponse
	if err := json.NewDecoder(drawingThreadResponse.Body).Decode(
		&publicDrawingThread,
	); err != nil {
		t.Fatalf("decode public drawing comment: %v", err)
	}
	if publicDrawingThread.Annotation.Kind != "drawing" ||
		publicDrawingThread.Annotation.Geometry == nil ||
		publicDrawingThread.Annotation.Geometry.Shape != "drawing" ||
		len(publicDrawingThread.Annotation.Geometry.Elements) != 2 {
		t.Fatalf(
			"unexpected public drawing annotation: %#v",
			publicDrawingThread.Annotation,
		)
	}
	publicJSON, err := json.Marshal(publicEntry)
	if err != nil {
		t.Fatalf("encode public share for leak check: %v", err)
	}
	publicBody := string(publicJSON)
	for _, forbidden := range []string{
		"workspaceId",
		"storageObject",
		"authorizedRoot",
		localPath,
	} {
		if strings.Contains(publicBody, forbidden) {
			t.Fatalf("public share leaked %q: %s", forbidden, publicBody)
		}
	}
	downloadResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/share-api/v1/items/"+publicEntry.Share.Items[0].ID+"/download",
		"",
		shareCookie,
	)
	if downloadResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"expected disabled download status 403, got %d: %s",
			downloadResponse.Code,
			downloadResponse.Body.String(),
		)
	}
	accessEventsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/shares/"+shareSecret.Share.ID+"/access-events",
		"",
		cookie,
	)
	if accessEventsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list share access events failed with %d: %s",
			accessEventsResponse.Code,
			accessEventsResponse.Body.String(),
		)
	}
	var accessEvents accessEventListResponse
	if err := json.NewDecoder(accessEventsResponse.Body).Decode(&accessEvents); err != nil {
		t.Fatalf("decode share access events: %v", err)
	}
	expectedAccessEvents := map[string]bool{
		audit.AccessEventOpened:            false,
		audit.AccessEventVerified:          false,
		audit.AccessEventIdentified:        false,
		audit.AccessEventCommented:         false,
		audit.AccessEventDecisionSubmitted: false,
	}
	identifiedVisitor := false
	for _, item := range accessEvents.Items {
		if _, exists := expectedAccessEvents[item.EventType]; exists {
			expectedAccessEvents[item.EventType] = true
		}
		if item.VisitorName == "Client A" {
			identifiedVisitor = true
		}
	}
	for eventType, found := range expectedAccessEvents {
		if !found {
			t.Fatalf(
				"expected access event %q, got %#v",
				eventType,
				accessEvents.Items,
			)
		}
	}
	if !identifiedVisitor {
		t.Fatalf(
			"expected identified visitor in access events, got %#v",
			accessEvents.Items,
		)
	}

	notificationsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/notifications",
		"",
		cookie,
	)
	if notificationsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list notifications failed with %d: %s",
			notificationsResponse.Code,
			notificationsResponse.Body.String(),
		)
	}
	var notifications notificationListResponse
	if err := json.NewDecoder(notificationsResponse.Body).Decode(
		&notifications,
	); err != nil {
		t.Fatalf("decode notifications: %v", err)
	}
	if notifications.UnreadCount < 3 || len(notifications.Items) < 3 {
		t.Fatalf("expected visitor activity notifications: %#v", notifications)
	}
	if notifications.Items[0].ReadAt != nil {
		t.Fatalf("expected newest notification to be unread: %#v", notifications.Items[0])
	}
	markReadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/notifications/"+notifications.Items[0].ID+"/read",
		"",
		cookie,
	)
	if markReadResponse.Code != http.StatusOK {
		t.Fatalf(
			"mark notification read failed with %d: %s",
			markReadResponse.Code,
			markReadResponse.Body.String(),
		)
	}
	var readNotification notificationResponse
	if err := json.NewDecoder(markReadResponse.Body).Decode(
		&readNotification,
	); err != nil {
		t.Fatalf("decode read notification: %v", err)
	}
	if readNotification.ReadAt == nil {
		t.Fatalf("notification remained unread: %#v", readNotification)
	}
	markAllReadResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/notifications/read-all",
		"",
		cookie,
	)
	if markAllReadResponse.Code != http.StatusNoContent {
		t.Fatalf(
			"mark all notifications read failed with %d: %s",
			markAllReadResponse.Code,
			markAllReadResponse.Body.String(),
		)
	}
	notificationsAfterReadResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/notifications",
		"",
		cookie,
	)
	var notificationsAfterRead notificationListResponse
	if err := json.NewDecoder(notificationsAfterReadResponse.Body).Decode(
		&notificationsAfterRead,
	); err != nil {
		t.Fatalf("decode notifications after read: %v", err)
	}
	if notificationsAfterRead.UnreadCount != 0 {
		t.Fatalf(
			"expected all notifications read, got %#v",
			notificationsAfterRead,
		)
	}

	auditLogsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?limit=200",
		"",
		cookie,
	)
	if auditLogsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list audit logs failed with %d: %s",
			auditLogsResponse.Code,
			auditLogsResponse.Body.String(),
		)
	}
	rawAuditLogs := auditLogsResponse.Body.Bytes()
	var auditLogs auditLogListResponse
	if err := json.Unmarshal(rawAuditLogs, &auditLogs); err != nil {
		t.Fatalf("decode audit logs: %v", err)
	}
	expectedAuditActions := map[string]bool{
		"share.created":              false,
		"share.visitor_code_created": false,
		"review.thread_created":      false,
		"review.decision_submitted":  false,
	}
	for _, item := range auditLogs.Items {
		if _, exists := expectedAuditActions[item.Action]; exists {
			expectedAuditActions[item.Action] = true
		}
	}
	for action, found := range expectedAuditActions {
		if !found {
			t.Fatalf("expected audit action %q, got %#v", action, auditLogs.Items)
		}
	}
	if bytes.Contains(rawAuditLogs, []byte("Move this element")) ||
		bytes.Contains(rawAuditLogs, []byte("Draw this correction")) {
		t.Fatal("audit log response leaked visitor comment content")
	}
	revokeShareBody, err := json.Marshal(revisionRequest{
		Revision: shareSecret.Share.Revision,
	})
	if err != nil {
		t.Fatalf("encode share revocation: %v", err)
	}
	revokeShareResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/shares/"+shareSecret.Share.ID+"/revoke",
		string(revokeShareBody),
		cookie,
	)
	if revokeShareResponse.Code != http.StatusOK {
		t.Fatalf(
			"revoke share failed with %d: %s",
			revokeShareResponse.Code,
			revokeShareResponse.Body.String(),
		)
	}
	expiredPublicResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/share-api/v1/share",
		"",
		shareCookie,
	)
	if expiredPublicResponse.Code != http.StatusNotFound {
		t.Fatalf(
			"revoked share remained accessible with %d: %s",
			expiredPublicResponse.Code,
			expiredPublicResponse.Body.String(),
		)
	}

	probeResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/authorized-roots/"+root.ID+"/media-probes",
		"",
		cookie,
	)
	if probeResponse.Code != http.StatusAccepted {
		t.Fatalf(
			"probe root failed with %d: %s",
			probeResponse.Code,
			probeResponse.Body.String(),
		)
	}
	var queued jobEnvelope
	if err := json.NewDecoder(probeResponse.Body).Decode(&queued); err != nil {
		t.Fatalf("decode queued probe job: %v", err)
	}
	if queued.Job.Status != "queued" ||
		queued.Job.Type != media.ProbeRootJobType ||
		queued.Job.Subject.ID != root.ID {
		t.Fatalf("unexpected queued probe job: %#v", queued.Job)
	}

	duplicateProbe := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/authorized-roots/"+root.ID+"/media-probes",
		"",
		cookie,
	)
	var duplicate jobEnvelope
	if err := json.NewDecoder(duplicateProbe.Body).Decode(&duplicate); err != nil {
		t.Fatalf("decode duplicate probe job: %v", err)
	}
	if duplicateProbe.Code != http.StatusAccepted ||
		duplicate.Job.ID != queued.Job.ID {
		t.Fatalf("probe job was not idempotent: %#v", duplicate.Job)
	}

	getJobResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/jobs/"+queued.Job.ID,
		"",
		cookie,
	)
	if getJobResponse.Code != http.StatusOK {
		t.Fatalf(
			"get probe job failed with %d: %s",
			getJobResponse.Code,
			getJobResponse.Body.String(),
		)
	}

	cancelResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/jobs/"+queued.Job.ID+"/cancel",
		"",
		cookie,
	)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf(
			"cancel probe job failed with %d: %s",
			cancelResponse.Code,
			cancelResponse.Body.String(),
		)
	}
	var cancelled jobResponse
	if err := json.NewDecoder(cancelResponse.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancelled probe job: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("queued job was not cancelled: %#v", cancelled)
	}

	rootsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots",
		"",
		cookie,
	)
	if rootsResponse.Code != http.StatusOK {
		t.Fatalf("list roots failed with %d", rootsResponse.Code)
	}
	if bytes.Contains(rootsResponse.Body.Bytes(), []byte(localPath)) {
		t.Fatal("authorized root response exposed the raw local path")
	}
	var roots authorizedRootListResponse
	if err := json.NewDecoder(rootsResponse.Body).Decode(&roots); err != nil {
		t.Fatalf("decode roots: %v", err)
	}
	var scannedRoot *authorizedRootResponse
	for index := range roots.Items {
		if roots.Items[index].ID == root.ID {
			scannedRoot = &roots.Items[index]
			break
		}
	}
	if scannedRoot == nil || scannedRoot.LastScanStatus == nil ||
		*scannedRoot.LastScanStatus != "succeeded" ||
		scannedRoot.LastScanSummary == nil {
		t.Fatalf("unexpected root scan state: %#v", roots.Items)
	}

	metadataResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+
			"/objects/metadata?key=clip.txt",
		"",
		cookie,
	)
	if metadataResponse.Code != http.StatusOK {
		t.Fatalf(
			"metadata failed with %d: %s",
			metadataResponse.Code,
			metadataResponse.Body.String(),
		)
	}
	var metadata localObjectMetadataResponse
	if err := json.NewDecoder(metadataResponse.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.ObjectKey != "clip.txt" || metadata.SizeBytes != 16 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}

	remoteMetadataRequest := newRequest(
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+"/objects/metadata?key=clip.txt",
		nil,
	)
	remoteMetadataRequest.RemoteAddr = "203.0.113.9:4567"
	remoteMetadataRequest.AddCookie(cookie)
	remoteMetadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteMetadataResponse, remoteMetadataRequest)
	if remoteMetadataResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"expected remote raw-object metadata status 403, got %d: %s",
			remoteMetadataResponse.Code,
			remoteMetadataResponse.Body.String(),
		)
	}

	rangeRequest := newRequest(
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+
			"/objects/content?key=clip.txt",
		nil,
	)
	rangeRequest.AddCookie(cookie)
	rangeRequest.RemoteAddr = "127.0.0.1:12345"
	rangeRequest.Header.Set("Range", "bytes=4-9")
	rangeRequest.Header.Set(hostCapabilityHeader, testHostManagementToken)
	rangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(rangeResponse, rangeRequest)

	if rangeResponse.Code != http.StatusPartialContent {
		t.Fatalf(
			"expected range status 206, got %d: %s",
			rangeResponse.Code,
			rangeResponse.Body.String(),
		)
	}
	if rangeResponse.Body.String() != "456789" {
		t.Fatalf("unexpected range body %q", rangeResponse.Body.String())
	}
	if rangeResponse.Header().Get("Content-Range") != "bytes 4-9/16" {
		t.Fatalf(
			"unexpected content range %q",
			rangeResponse.Header().Get("Content-Range"),
		)
	}

	escapeResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+root.ID+
			"/objects/metadata?key=..%2Fsecret.txt",
		"",
		cookie,
	)
	if escapeResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected traversal status 400, got %d: %s",
			escapeResponse.Code,
			escapeResponse.Body.String(),
		)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		newRequest(
			http.MethodGet,
			"/api/v1/authorized-roots/"+root.ID+
				"/objects/metadata?key=clip.txt",
			nil,
		),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status 401, got %d", unauthorized.Code)
	}

	updateResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/authorized-roots/"+root.ID,
		`{"displayName":"成片素材","scanEnabled":false,"revision":1}`,
		cookie,
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf(
			"update root failed with %d: %s",
			updateResponse.Code,
			updateResponse.Body.String(),
		)
	}
	var updated authorizedRootResponse
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated root: %v", err)
	}
	if updated.DisplayName != "成片素材" ||
		updated.ScanEnabled ||
		updated.Revision != 2 {
		t.Fatalf("unexpected updated root: %#v", updated)
	}
}

func TestStorageProviderManagementAPIDoesNotExposeCredentials(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Storage API",
			"ownerName":"Owner",
			"password":"local-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	remote := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()

	created := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/storage-providers",
		fmt.Sprintf(`{
			"kind":"webdav",
			"name":"团队 WebDAV",
			"endpoint":%q,
			"allowPrivateNetwork":true,
			"username":"review-user",
			"password":"top-secret"
		}`, remote.URL+"/dav"),
		cookie,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf(
			"create provider failed: %d %s",
			created.Code,
			created.Body.String(),
		)
	}
	if strings.Contains(created.Body.String(), "top-secret") ||
		strings.Contains(created.Body.String(), "review-user") {
		t.Fatalf("provider response exposed credentials: %s", created.Body.String())
	}
	var provider storageProviderResponse
	if err := json.NewDecoder(created.Body).Decode(&provider); err != nil {
		t.Fatalf("decode provider: %v", err)
	}
	if provider.Kind != "webdav" || provider.Status != "offline" {
		t.Fatalf("unexpected provider: %#v", provider)
	}

	listed := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/storage-providers",
		"",
		cookie,
	)
	if listed.Code != http.StatusOK ||
		strings.Contains(listed.Body.String(), "top-secret") ||
		strings.Contains(listed.Body.String(), "review-user") {
		t.Fatalf("unsafe provider list: %d %s", listed.Code, listed.Body.String())
	}
	deleted := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/storage-providers/"+provider.ID,
		fmt.Sprintf(`{"revision":%d}`, provider.Revision),
		cookie,
	)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf(
			"delete provider failed: %d %s",
			deleted.Code,
			deleted.Body.String(),
		)
	}
}

// D2, docs/FREE_TIER_BOUNDARY_DESIGN.md §2: the first-run wizard may allow an
// Owner web session to reach host management, so "loopback only" is no longer
// the whole rule. This test pins the locked-down deployment — the switch closed
// by the environment override — which is the configuration that must keep the
// previous behaviour. The default is covered by
// TestOwnerWebSessionMayRegisterAStorageLocationWhenTheWizardAllowedIt.
func TestAuthorizedRootRegistrationRefusedWhenWebHostPathsAreClosed(t *testing.T) {
	config := testConfig(t)
	config.AllowWebHostPaths = boolSetting(false)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	cookie := findSessionCookie(t, setup.Result().Cookies())
	body, err := json.Marshal(authorizedRootRequest{
		DisplayName: "远程目录",
		LocalPath:   t.TempDir(),
		Mode:        "referenced",
		ScanEnabled: true,
	})
	if err != nil {
		t.Fatalf("encode root request: %v", err)
	}

	request := newRequest(
		http.MethodPost,
		"/api/v1/authorized-roots",
		bytes.NewReader(body),
	)
	request.RemoteAddr = "203.0.113.9:4567"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(mutationHeaderName, mutationHeaderValue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"expected remote registration status 403, got %d: %s",
			response.Code,
			response.Body.String(),
		)
	}

	proxiedRequest := newRequest(
		http.MethodPost,
		"/api/v1/authorized-roots",
		bytes.NewReader(body),
	)
	proxiedRequest.RemoteAddr = "127.0.0.1:4567"
	proxiedRequest.Header.Set(hostProxyHeader, "desktop")
	proxiedRequest.Header.Set("Content-Type", "application/json")
	proxiedRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	proxiedRequest.AddCookie(cookie)
	proxiedResponse := httptest.NewRecorder()
	handler.ServeHTTP(proxiedResponse, proxiedRequest)

	if proxiedResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"expected proxied remote registration status 403, got %d: %s",
			proxiedResponse.Code,
			proxiedResponse.Body.String(),
		)
	}
}

func TestLocalManagedBucketRequiresHostAndHidesPath(t *testing.T) {
	config := testConfig(t)
	// D2, docs/FREE_TIER_BOUNDARY_DESIGN.md §2: with the wizard's default in force
	// an Owner web session may create a storage location, which is the point of the
	// first-run switch. Pin it closed here so this test keeps proving that a member
	// and a host-proxied non-owner request are still refused, and that the response
	// never leaks the local path.
	config.AllowWebHostPaths = boolSetting(false)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	cookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	bucketPath := filepath.Join(t.TempDir(), "owner-bucket")
	quota := int64(10 * 1024 * 1024)
	body, err := json.Marshal(createLocalManagedBucketRequest{
		DisplayName:          "Owner upload bucket",
		LocalPath:            bucketPath,
		Purpose:              "upload",
		QuotaBytes:           &quota,
		UploadSecurityPolicy: "standard",
		ProjectAvailable:     boolPointer(true),
	})
	if err != nil {
		t.Fatalf("encode bucket request: %v", err)
	}

	if _, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "member-bucket@example.com",
			DisplayName: "Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	); err != nil {
		t.Fatalf("create member account: %v", err)
	}
	memberLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"member-bucket@example.com","password":"member-password-123"}`,
		nil,
	)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login failed: %d %s", memberLogin.Code, memberLogin.Body.String())
	}
	memberCookie := findSessionCookie(t, memberLogin.Result().Cookies())
	memberResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/local-managed-buckets",
		string(body),
		memberCookie,
	)
	if memberResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"expected member bucket creation status 403, got %d: %s",
			memberResponse.Code,
			memberResponse.Body.String(),
		)
	}

	remoteRequest := newRequest(
		http.MethodPost,
		"/api/v1/local-managed-buckets",
		bytes.NewReader(body),
	)
	remoteRequest.RemoteAddr = "127.0.0.1:4567"
	remoteRequest.Header.Set(hostProxyHeader, "desktop")
	remoteRequest.Header.Set("Content-Type", "application/json")
	remoteRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	remoteRequest.AddCookie(cookie)
	remoteResponse := httptest.NewRecorder()
	handler.ServeHTTP(remoteResponse, remoteRequest)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"expected remote bucket creation status 403, got %d: %s",
			remoteResponse.Code,
			remoteResponse.Body.String(),
		)
	}

	createResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/local-managed-buckets",
		string(body),
		cookie,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create bucket failed: %d %s",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}
	rawCreated := createResponse.Body.String()
	if strings.Contains(rawCreated, "localPath") ||
		strings.Contains(rawCreated, bucketPath) ||
		strings.Contains(rawCreated, filepath.ToSlash(bucketPath)) {
		t.Fatalf("bucket response leaked local path: %s", rawCreated)
	}
	var created localManagedBucketResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode bucket: %v", err)
	}
	if created.AuthorizedRootID == "" ||
		created.StorageProviderID == "" ||
		created.DisplayPath == "" ||
		created.Status != "active" ||
		created.QuotaBytes == nil ||
		*created.QuotaBytes != quota {
		t.Fatalf("unexpected bucket response: %#v", created)
	}
	if _, err := os.Stat(bucketPath); err != nil {
		t.Fatalf("bucket directory was not created: %v", err)
	}

	updateBody, err := json.Marshal(updateLocalManagedBucketRequest{
		DisplayName:          created.DisplayName,
		Purpose:              created.Purpose,
		QuotaBytes:           created.QuotaBytes,
		UploadSecurityPolicy: "enhanced",
		ProjectAvailable:     false,
		Status:               "disabled",
		Revision:             created.Revision,
	})
	if err != nil {
		t.Fatalf("encode bucket update: %v", err)
	}
	updateResponse := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/local-managed-buckets/"+created.ID,
		string(updateBody),
		cookie,
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf(
			"update bucket failed: %d %s",
			updateResponse.Code,
			updateResponse.Body.String(),
		)
	}
	var updated localManagedBucketResponse
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated bucket: %v", err)
	}
	if updated.Status != "disabled" || updated.ProjectAvailable ||
		updated.UploadSecurityPolicy != "enhanced" {
		t.Fatalf("unexpected updated bucket: %#v", updated)
	}
	auditResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?action=storage.local_bucket_updated&resourceType=local_managed_bucket&resourceId="+created.ID,
		"",
		cookie,
	)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf(
			"list bucket audit failed: %d %s",
			auditResponse.Code,
			auditResponse.Body.String(),
		)
	}
	var auditLogs auditLogListResponse
	if err := json.NewDecoder(auditResponse.Body).Decode(&auditLogs); err != nil {
		t.Fatalf("decode bucket audit: %v", err)
	}
	if len(auditLogs.Items) != 1 ||
		auditLogs.Items[0].Action != "storage.local_bucket_updated" ||
		auditLogs.Items[0].ResourceID != created.ID {
		t.Fatalf("unexpected bucket audit logs: %#v", auditLogs.Items)
	}

	listResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/local-managed-buckets",
		"",
		cookie,
	)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list buckets failed: %d %s", listResponse.Code, listResponse.Body.String())
	}
	rawList := listResponse.Body.String()
	if strings.Contains(rawList, "localPath") ||
		strings.Contains(rawList, bucketPath) ||
		strings.Contains(rawList, filepath.ToSlash(bucketPath)) {
		t.Fatalf("bucket list leaked local path: %s", rawList)
	}
	var listed localManagedBucketListResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode bucket list: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("unexpected bucket list: %#v", listed.Items)
	}
}

func TestRemoteWorkspaceCanUploadManagedAsset(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	cookie := findSessionCookie(t, setup.Result().Cookies())

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "remote-upload.png")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(testPNGBytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := newRequest(
		http.MethodPost,
		"/api/v1/assets",
		&body,
	)
	request.RemoteAddr = "127.0.0.1:4567"
	request.Header.Set(hostProxyHeader, "desktop")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(mutationHeaderName, mutationHeaderValue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf(
			"expected managed upload status 201, got %d: %s",
			response.Code,
			response.Body.String(),
		)
	}
	var version assetVersionResponse
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		t.Fatalf("decode uploaded asset: %v", err)
	}
	if version.VersionNumber != 1 ||
		version.StorageObjectID == "" ||
		version.AuthorizedRootID == "" {
		t.Fatalf("unexpected uploaded asset version: %#v", version)
	}
	if version.UploadCheck == nil ||
		version.UploadCheck.Status != "ready" ||
		version.UploadCheck.UploadSecurityPolicy != "standard" ||
		version.UploadCheck.StorageObjectID != version.StorageObjectID {
		t.Fatalf("unexpected upload check: %#v", version.UploadCheck)
	}

	rootsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots",
		"",
		cookie,
	)
	if rootsResponse.Code != http.StatusOK {
		t.Fatalf("list roots failed: %d", rootsResponse.Code)
	}
	var roots authorizedRootListResponse
	if err := json.NewDecoder(rootsResponse.Body).Decode(&roots); err != nil {
		t.Fatalf("decode roots: %v", err)
	}
	if len(roots.Items) != 1 || roots.Items[0].Mode != "managed" {
		t.Fatalf("expected managed upload source, got %#v", roots.Items)
	}
	auditResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?action=asset.version_uploaded&resourceType=asset_version",
		"",
		cookie,
	)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("list version activity failed: %d", auditResponse.Code)
	}
	var logs auditLogListResponse
	if err := json.NewDecoder(auditResponse.Body).Decode(&logs); err != nil {
		t.Fatalf("decode version activity: %v", err)
	}
	if len(logs.Items) != 1 || logs.Items[0].ResourceID != version.ID {
		t.Fatalf("unexpected version activity: %#v", logs.Items)
	}
	checkAuditResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/audit-logs?action=upload_check.ready&resourceType=upload_check",
		"",
		cookie,
	)
	if checkAuditResponse.Code != http.StatusOK {
		t.Fatalf("list upload check activity failed: %d", checkAuditResponse.Code)
	}
	var checkLogs auditLogListResponse
	if err := json.NewDecoder(checkAuditResponse.Body).Decode(&checkLogs); err != nil {
		t.Fatalf("decode upload check activity: %v", err)
	}
	if len(checkLogs.Items) != 1 ||
		version.UploadCheck == nil ||
		checkLogs.Items[0].ResourceID != version.UploadCheck.ID {
		t.Fatalf("unexpected upload check activity: %#v", checkLogs.Items)
	}

	var rejectedBody bytes.Buffer
	rejectedWriter := multipart.NewWriter(&rejectedBody)
	rejectedPart, err := rejectedWriter.CreateFormFile(
		"file",
		"looks-like-image.png",
	)
	if err != nil {
		t.Fatalf("create rejected multipart file: %v", err)
	}
	if _, err := rejectedPart.Write([]byte("<html><script>bad()</script></html>")); err != nil {
		t.Fatalf("write rejected multipart file: %v", err)
	}
	if err := rejectedWriter.Close(); err != nil {
		t.Fatalf("close rejected multipart writer: %v", err)
	}
	rejectedRequest := newRequest(
		http.MethodPost,
		"/api/v1/assets",
		&rejectedBody,
	)
	rejectedRequest.RemoteAddr = "127.0.0.1:4567"
	rejectedRequest.Header.Set(hostProxyHeader, "desktop")
	rejectedRequest.Header.Set("Content-Type", rejectedWriter.FormDataContentType())
	rejectedRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	rejectedRequest.AddCookie(cookie)
	rejectedResponse := httptest.NewRecorder()
	handler.ServeHTTP(rejectedResponse, rejectedRequest)

	if rejectedResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected rejected upload status 400, got %d: %s",
			rejectedResponse.Code,
			rejectedResponse.Body.String(),
		)
	}
	rawRejected := rejectedResponse.Body.String()
	if strings.Contains(rawRejected, `:\`) ||
		strings.Contains(rawRejected, "review-studio-upload") {
		t.Fatalf("rejected upload response leaked local detail: %s", rawRejected)
	}
	var rejectedError errorEnvelope
	if err := json.NewDecoder(strings.NewReader(rawRejected)).Decode(&rejectedError); err != nil {
		t.Fatalf("decode rejected upload error: %v", err)
	}
	if rejectedError.Error.Code != "media.upload_type_rejected" {
		t.Fatalf("unexpected rejected upload error: %#v", rejectedError)
	}
}

func TestProjectUploadTargetUsesBucketSecurityPolicy(t *testing.T) {
	config := testConfig(t)
	httpHandler := NewHandler(config)
	setup := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	createProject := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(t, httpHandler, cookie, "Policy Project", nil),
		cookie,
	)
	if createProject.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed: %d %s",
			createProject.Code,
			createProject.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(createProject.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	bucket := createProjectUploadBucketAndSelectWithPolicy(
		t,
		httpHandler,
		cookie,
		project.ID,
		"enhanced",
	)

	resolver := &handler{
		storage:        config.Storage,
		projectStorage: config.ProjectStorage,
	}
	target, err := resolver.projectUploadTarget(
		context.Background(),
		ownerSession.Workspace.ID,
		&project.ID,
	)
	if err != nil {
		t.Fatalf("resolve project upload target: %v", err)
	}
	if target == nil ||
		target.AuthorizedRootID != bucket.AuthorizedRootID ||
		target.StorageProviderID != bucket.StorageProviderID ||
		target.UploadSecurityPolicy != "enhanced" {
		t.Fatalf("unexpected upload target: %#v", target)
	}

	preflight := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/upload-target",
		"",
		cookie,
	)
	if preflight.Code != http.StatusOK {
		t.Fatalf(
			"upload target preflight failed: %d %s",
			preflight.Code,
			preflight.Body.String(),
		)
	}
	var response projectUploadTargetResponse
	if err := json.NewDecoder(preflight.Body).Decode(&response); err != nil {
		t.Fatalf("decode upload target preflight: %v", err)
	}
	if response.ProjectID != project.ID ||
		response.UploadTarget != "local_managed_bucket" ||
		response.UploadSecurityPolicy != "enhanced" ||
		response.StorageProviderID != bucket.StorageProviderID ||
		response.AuthorizedRootID != bucket.AuthorizedRootID {
		t.Fatalf("unexpected upload target preflight: %#v", response)
	}
}

func TestProjectUploadTargetAllowsRemoteRootWithQuickPolicy(t *testing.T) {
	httpHandler := NewHandler(testConfig(t))
	cookie, _, project := setupUploadSecurityProject(
		t,
		httpHandler,
		"Remote Upload Project",
	)
	remote := newProjectUploadWebDAVProbeServer(t)
	defer remote.Close()

	createProvider := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/storage-providers",
		fmt.Sprintf(`{
			"kind":"webdav",
			"name":"Remote Uploads",
			"endpoint":%q,
			"allowPrivateNetwork":true,
			"username":"review",
			"password":"secret"
		}`, remote.URL+"/dav"),
		cookie,
	)
	if createProvider.Code != http.StatusCreated {
		t.Fatalf(
			"create remote provider failed: %d %s",
			createProvider.Code,
			createProvider.Body.String(),
		)
	}
	var provider storageProviderResponse
	if err := json.NewDecoder(createProvider.Body).Decode(&provider); err != nil {
		t.Fatalf("decode remote provider: %v", err)
	}

	testProvider := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/storage-providers/"+provider.ID+"/test",
		"",
		cookie,
	)
	if testProvider.Code != http.StatusOK {
		t.Fatalf(
			"test remote provider failed: %d %s",
			testProvider.Code,
			testProvider.Body.String(),
		)
	}

	rootlessGrant := performJSONRequest(
		httpHandler,
		http.MethodPut,
		"/api/v1/project-storage-grants",
		fmt.Sprintf(
			`{"storageProviderId":%q,"authorizedRootId":null,"status":"active"}`,
			provider.ID,
		),
		cookie,
	)
	if rootlessGrant.Code != http.StatusOK {
		t.Fatalf(
			"create rootless grant failed: %d %s",
			rootlessGrant.Code,
			rootlessGrant.Body.String(),
		)
	}
	var rootless projectStorageGrantResponse
	if err := json.NewDecoder(rootlessGrant.Body).Decode(&rootless); err != nil {
		t.Fatalf("decode rootless grant: %v", err)
	}
	rejectedSelection := performJSONRequest(
		httpHandler,
		http.MethodPut,
		"/api/v1/projects/"+project.ID+"/storage-selections/upload",
		fmt.Sprintf(`{"grantId":%q}`, rootless.ID),
		cookie,
	)
	if rejectedSelection.Code != http.StatusConflict {
		t.Fatalf(
			"rootless remote grant should not be selectable for upload, got %d: %s",
			rejectedSelection.Code,
			rejectedSelection.Body.String(),
		)
	}

	createRoot := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/storage-providers/"+provider.ID+"/roots",
		`{
			"displayName":"Remote upload root",
			"basePath":"uploads",
			"mode":"managed",
			"scanEnabled":true
		}`,
		cookie,
	)
	if createRoot.Code != http.StatusCreated {
		t.Fatalf(
			"create remote root failed: %d %s",
			createRoot.Code,
			createRoot.Body.String(),
		)
	}
	var root authorizedRootResponse
	if err := json.NewDecoder(createRoot.Body).Decode(&root); err != nil {
		t.Fatalf("decode remote root: %v", err)
	}
	rootGrantResponse := performJSONRequest(
		httpHandler,
		http.MethodPut,
		"/api/v1/project-storage-grants",
		fmt.Sprintf(
			`{"storageProviderId":%q,"authorizedRootId":%q,"status":"active"}`,
			provider.ID,
			root.ID,
		),
		cookie,
	)
	if rootGrantResponse.Code != http.StatusOK {
		t.Fatalf(
			"create remote root grant failed: %d %s",
			rootGrantResponse.Code,
			rootGrantResponse.Body.String(),
		)
	}
	var rootGrant projectStorageGrantResponse
	if err := json.NewDecoder(rootGrantResponse.Body).Decode(&rootGrant); err != nil {
		t.Fatalf("decode remote root grant: %v", err)
	}
	selectResponse := performJSONRequest(
		httpHandler,
		http.MethodPut,
		"/api/v1/projects/"+project.ID+"/storage-selections/upload",
		fmt.Sprintf(`{"grantId":%q}`, rootGrant.ID),
		cookie,
	)
	if selectResponse.Code != http.StatusOK {
		t.Fatalf(
			"select remote root upload target failed: %d %s",
			selectResponse.Code,
			selectResponse.Body.String(),
		)
	}

	preflight := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/upload-target?sizeBytes=1024",
		"",
		cookie,
	)
	if preflight.Code != http.StatusOK {
		t.Fatalf(
			"remote upload target preflight failed: %d %s",
			preflight.Code,
			preflight.Body.String(),
		)
	}
	var target projectUploadTargetResponse
	if err := json.NewDecoder(preflight.Body).Decode(&target); err != nil {
		t.Fatalf("decode remote upload target: %v", err)
	}
	if target.UploadTarget != "remote_storage" ||
		target.UploadSecurityPolicy != "quick" ||
		target.StorageProviderID != provider.ID ||
		target.AuthorizedRootID != root.ID ||
		target.LocalManagedBucketID != nil ||
		target.QuotaBytes != nil ||
		target.QuotaAvailableBytes != nil ||
		target.MaxSingleFileBytes <= 0 {
		t.Fatalf("unexpected remote upload target: %#v", target)
	}

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	if err := writer.WriteField("projectId", project.ID); err != nil {
		t.Fatalf("write project id field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "remote-poster.png")
	if err != nil {
		t.Fatalf("create remote upload file: %v", err)
	}
	if _, err := part.Write(testPNGBytes()); err != nil {
		t.Fatalf("write remote upload file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close remote upload writer: %v", err)
	}
	uploadRequest := newRequest(http.MethodPost, "/api/v1/assets", &uploadBody)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	uploadRequest.AddCookie(cookie)
	uploadResponse := httptest.NewRecorder()
	httpHandler.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"upload remote project asset failed: %d %s",
			uploadResponse.Code,
			uploadResponse.Body.String(),
		)
	}
	var version assetVersionResponse
	if err := json.NewDecoder(uploadResponse.Body).Decode(&version); err != nil {
		t.Fatalf("decode remote uploaded asset: %v", err)
	}
	if version.AuthorizedRootID != root.ID ||
		version.UploadCheck == nil ||
		version.UploadCheck.UploadSecurityPolicy != "quick" ||
		version.UploadCheck.Status != "ready" {
		t.Fatalf("unexpected remote uploaded asset version: %#v", version)
	}
}

func TestProjectUploadQuickPolicyRecordsNotScanned(t *testing.T) {
	httpHandler := NewHandler(testConfig(t))
	cookie, _, project := setupUploadSecurityProject(
		t,
		httpHandler,
		"Quick Scan Project",
	)
	createProjectUploadBucketAndSelectWithPolicy(
		t,
		httpHandler,
		cookie,
		project.ID,
		"quick",
	)

	version := uploadProjectMP4ForTest(
		t,
		httpHandler,
		cookie,
		project.ID,
		"quick.mp4",
	)
	if version.UploadCheck == nil ||
		version.UploadCheck.Status != "ready" ||
		version.UploadCheck.UploadSecurityPolicy != "quick" ||
		version.UploadCheck.ResultCode == nil ||
		*version.UploadCheck.ResultCode != "malware_not_scanned" ||
		version.UploadCheck.CompletedAt == nil ||
		version.UploadCheck.QuarantinedAt != nil {
		t.Fatalf("unexpected quick upload check: %#v", version.UploadCheck)
	}

	checkAuditResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/audit-logs?action=upload_check.ready&resourceType=upload_check",
		"",
		cookie,
	)
	if checkAuditResponse.Code != http.StatusOK {
		t.Fatalf(
			"list quick upload check activity failed: %d %s",
			checkAuditResponse.Code,
			checkAuditResponse.Body.String(),
		)
	}
	var checkLogs auditLogListResponse
	if err := json.NewDecoder(checkAuditResponse.Body).Decode(&checkLogs); err != nil {
		t.Fatalf("decode quick upload check activity: %v", err)
	}
	if len(checkLogs.Items) != 1 ||
		checkLogs.Items[0].ResourceID != version.UploadCheck.ID {
		t.Fatalf("unexpected quick upload check activity: %#v", checkLogs.Items)
	}
}

func TestProjectUploadStandardPolicyUsesCleanMalwareVerdict(t *testing.T) {
	config := testConfig(t)
	config.Library.SetMalwareScanner(media.StaticMalwareScanner{
		Scanner: "test-clean",
		Verdict: media.MalwareVerdictClean,
	})
	httpHandler := NewHandler(config)
	cookie, _, project := setupUploadSecurityProject(
		t,
		httpHandler,
		"Standard Scan Project",
	)
	createProjectUploadBucketAndSelectWithPolicy(
		t,
		httpHandler,
		cookie,
		project.ID,
		"standard",
	)

	version := uploadProjectMP4ForTest(
		t,
		httpHandler,
		cookie,
		project.ID,
		"standard-clean.mp4",
	)
	if version.UploadCheck == nil ||
		version.UploadCheck.Status != "ready" ||
		version.UploadCheck.UploadSecurityPolicy != "standard" ||
		version.UploadCheck.ResultCode == nil ||
		*version.UploadCheck.ResultCode != "malware_clean" ||
		version.UploadCheck.CompletedAt == nil ||
		version.UploadCheck.QuarantinedAt != nil {
		t.Fatalf("unexpected standard upload check: %#v", version.UploadCheck)
	}
}

func TestEnhancedUploadQuarantinesWhenScannerUnavailable(t *testing.T) {
	httpHandler := NewHandler(testConfig(t))
	cookie, _, project := setupUploadSecurityProject(
		t,
		httpHandler,
		"Enhanced Scan Project",
	)
	createProjectUploadBucketAndSelectWithPolicy(
		t,
		httpHandler,
		cookie,
		project.ID,
		"enhanced",
	)

	version := uploadProjectMP4ForTest(
		t,
		httpHandler,
		cookie,
		project.ID,
		"enhanced-unavailable.mp4",
	)
	if version.UploadCheck == nil ||
		version.UploadCheck.Status != "quarantined" ||
		version.UploadCheck.UploadSecurityPolicy != "enhanced" ||
		version.UploadCheck.ResultCode == nil ||
		*version.UploadCheck.ResultCode != "malware_scan_unavailable" ||
		version.UploadCheck.CompletedAt == nil ||
		version.UploadCheck.QuarantinedAt == nil {
		t.Fatalf("unexpected enhanced upload check: %#v", version.UploadCheck)
	}

	libraryResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/library",
		"",
		cookie,
	)
	if libraryResponse.Code != http.StatusOK {
		t.Fatalf(
			"list enhanced project library failed: %d %s",
			libraryResponse.Code,
			libraryResponse.Body.String(),
		)
	}
	var library mediaLibraryPageResponse
	if err := json.NewDecoder(libraryResponse.Body).Decode(&library); err != nil {
		t.Fatalf("decode enhanced project library: %v", err)
	}
	if library.Total != 0 || len(library.Items) != 0 {
		t.Fatalf("quarantined upload entered project library: %#v", library)
	}

	checkAuditResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/audit-logs?action=upload_check.quarantined&resourceType=upload_check",
		"",
		cookie,
	)
	if checkAuditResponse.Code != http.StatusOK {
		t.Fatalf(
			"list quarantined upload check activity failed: %d %s",
			checkAuditResponse.Code,
			checkAuditResponse.Body.String(),
		)
	}
	var checkLogs auditLogListResponse
	if err := json.NewDecoder(checkAuditResponse.Body).Decode(&checkLogs); err != nil {
		t.Fatalf("decode quarantined upload check activity: %v", err)
	}
	if len(checkLogs.Items) != 1 ||
		checkLogs.Items[0].ResourceID != version.UploadCheck.ID {
		t.Fatalf("unexpected quarantined upload check activity: %#v", checkLogs.Items)
	}

	reviewBody, err := json.Marshal(reviewSessionRequest{
		ProjectID:     project.ID,
		Name:          "Blocked Review",
		AllowDownload: true,
		DecisionRule:  "any_reviewer",
		Participants: []reviewParticipantRequest{
			{DisplayName: "Client", Role: "reviewer"},
		},
		Items: []reviewItemRequest{
			{
				AssetID:        version.AssetID,
				AssetVersionID: version.ID,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode blocked review: %v", err)
	}
	createReview := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/review-sessions",
		string(reviewBody),
		cookie,
	)
	if createReview.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected quarantined review creation status 400, got %d: %s",
			createReview.Code,
			createReview.Body.String(),
		)
	}
	var reviewError errorEnvelope
	if err := json.NewDecoder(createReview.Body).Decode(&reviewError); err != nil {
		t.Fatalf("decode quarantined review error: %v", err)
	}
	if reviewError.Error.Code != "review.invalid_item" {
		t.Fatalf("unexpected quarantined review error: %#v", reviewError)
	}
}

func TestOwnerCanResolveAndRejectQuarantinedUploadChecks(t *testing.T) {
	httpHandler := NewHandler(testConfig(t))
	cookie, _, project := setupUploadSecurityProject(
		t,
		httpHandler,
		"Manual Upload Check Project",
	)
	bucket := createProjectUploadBucketAndSelectWithPolicy(
		t,
		httpHandler,
		cookie,
		project.ID,
		"enhanced",
	)

	version := uploadProjectMP4ForTest(
		t,
		httpHandler,
		cookie,
		project.ID,
		"manual-release.mp4",
	)
	if version.UploadCheck == nil || version.UploadCheck.Status != "quarantined" {
		t.Fatalf("expected quarantined upload, got %#v", version.UploadCheck)
	}

	listResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/upload-checks?status=quarantined",
		"",
		cookie,
	)
	if listResponse.Code != http.StatusOK {
		t.Fatalf(
			"list quarantined checks failed: %d %s",
			listResponse.Code,
			listResponse.Body.String(),
		)
	}
	rawList := listResponse.Body.String()
	if strings.Contains(rawList, bucket.DisplayPath) ||
		strings.Contains(rawList, "objectKey") {
		t.Fatalf("upload check list leaked storage detail: %s", rawList)
	}
	var listed uploadCheckListResponse
	if err := json.NewDecoder(strings.NewReader(rawList)).Decode(&listed); err != nil {
		t.Fatalf("decode upload check list: %v", err)
	}
	if listed.Total != 1 ||
		len(listed.Items) != 1 ||
		listed.Items[0].UploadCheck.ID != version.UploadCheck.ID ||
		listed.Items[0].SourceFilename != "manual-release.mp4" ||
		listed.Items[0].ProjectName == nil ||
		*listed.Items[0].ProjectName != project.Name {
		t.Fatalf("unexpected upload check list: %#v", listed)
	}

	releaseResponse := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/upload-checks/"+version.UploadCheck.ID+"/release",
		`{"message":"Owner manually accepted this sample"}`,
		cookie,
	)
	if releaseResponse.Code != http.StatusOK {
		t.Fatalf(
			"release upload check failed: %d %s",
			releaseResponse.Code,
			releaseResponse.Body.String(),
		)
	}
	var released uploadCheckResponse
	if err := json.NewDecoder(releaseResponse.Body).Decode(&released); err != nil {
		t.Fatalf("decode released upload check: %v", err)
	}
	if released.Status != "ready" ||
		released.ResultCode == nil ||
		*released.ResultCode != "manual_released" {
		t.Fatalf("unexpected released upload check: %#v", released)
	}

	libraryResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/library",
		"",
		cookie,
	)
	if libraryResponse.Code != http.StatusOK {
		t.Fatalf(
			"list project library after release failed: %d %s",
			libraryResponse.Code,
			libraryResponse.Body.String(),
		)
	}
	var library mediaLibraryPageResponse
	if err := json.NewDecoder(libraryResponse.Body).Decode(&library); err != nil {
		t.Fatalf("decode project library after release: %v", err)
	}
	if library.Total != 1 || len(library.Items) != 1 {
		t.Fatalf("released upload should enter library: %#v", library)
	}

	readyAuditResponse := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/audit-logs?action=upload_check.ready&resourceType=upload_check&resourceId="+version.UploadCheck.ID,
		"",
		cookie,
	)
	if readyAuditResponse.Code != http.StatusOK {
		t.Fatalf(
			"list release audit failed: %d %s",
			readyAuditResponse.Code,
			readyAuditResponse.Body.String(),
		)
	}
	var readyAudit auditLogListResponse
	if err := json.NewDecoder(readyAuditResponse.Body).Decode(&readyAudit); err != nil {
		t.Fatalf("decode release audit: %v", err)
	}
	if len(readyAudit.Items) != 1 ||
		readyAudit.Items[0].Details["resultCode"] != "manual_released" {
		t.Fatalf("unexpected release audit: %#v", readyAudit.Items)
	}

	rejectedVersion := uploadProjectMP4ForTest(
		t,
		httpHandler,
		cookie,
		project.ID,
		"manual-reject.mp4",
	)
	if rejectedVersion.UploadCheck == nil ||
		rejectedVersion.UploadCheck.Status != "quarantined" {
		t.Fatalf("expected second quarantined upload, got %#v", rejectedVersion.UploadCheck)
	}
	rejectResponse := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/upload-checks/"+rejectedVersion.UploadCheck.ID+"/reject",
		`{"message":"Rejected after manual review"}`,
		cookie,
	)
	if rejectResponse.Code != http.StatusOK {
		t.Fatalf(
			"reject upload check failed: %d %s",
			rejectResponse.Code,
			rejectResponse.Body.String(),
		)
	}
	var rejected uploadCheckResponse
	if err := json.NewDecoder(rejectResponse.Body).Decode(&rejected); err != nil {
		t.Fatalf("decode rejected upload check: %v", err)
	}
	if rejected.Status != "rejected" ||
		rejected.ResultCode == nil ||
		*rejected.ResultCode != "manual_rejected" ||
		rejected.RejectedAt == nil {
		t.Fatalf("unexpected rejected upload check: %#v", rejected)
	}

	releaseRejected := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/upload-checks/"+rejectedVersion.UploadCheck.ID+"/release",
		`{}`,
		cookie,
	)
	if releaseRejected.Code != http.StatusConflict {
		t.Fatalf(
			"expected release rejected status 409, got %d: %s",
			releaseRejected.Code,
			releaseRejected.Body.String(),
		)
	}
}

func TestProjectUploadTargetRejectsQuotaBeforeUpload(t *testing.T) {
	config := testConfig(t)
	httpHandler := NewHandler(config)
	setup := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	createProject := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(t, httpHandler, cookie, "Quota Project", nil),
		cookie,
	)
	if createProject.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed: %d %s",
			createProject.Code,
			createProject.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(createProject.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	quota := int64(len(testMP4Bytes()) - 1)
	bucket := createProjectUploadBucketAndSelectWithPolicyAndQuota(
		t,
		httpHandler,
		cookie,
		project.ID,
		"standard",
		&quota,
	)

	preflight := performJSONRequest(
		httpHandler,
		http.MethodGet,
		fmt.Sprintf(
			"/api/v1/projects/%s/upload-target?sizeBytes=%d",
			project.ID,
			len(testMP4Bytes()),
		),
		"",
		cookie,
	)
	if preflight.Code != http.StatusConflict {
		t.Fatalf(
			"expected quota preflight status 409, got %d: %s",
			preflight.Code,
			preflight.Body.String(),
		)
	}
	rawPreflight := preflight.Body.String()
	if strings.Contains(rawPreflight, `:\`) ||
		strings.Contains(rawPreflight, bucket.DisplayPath) {
		t.Fatalf("quota preflight leaked local detail: %s", rawPreflight)
	}
	var preflightError errorEnvelope
	if err := json.NewDecoder(strings.NewReader(rawPreflight)).Decode(&preflightError); err != nil {
		t.Fatalf("decode quota preflight error: %v", err)
	}
	if preflightError.Error.Code != "project_storage.upload_quota_exceeded" {
		t.Fatalf("unexpected quota preflight error: %#v", preflightError)
	}
}

func TestProjectUploadQuotaCannotBeBypassedByDirectUpload(t *testing.T) {
	httpHandler := NewHandler(testConfig(t))
	setup := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	createProject := performJSONRequest(
		httpHandler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(
			t,
			httpHandler,
			cookie,
			"Direct Quota Project",
			nil,
		),
		cookie,
	)
	if createProject.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed: %d %s",
			createProject.Code,
			createProject.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(createProject.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	quota := int64(len(testMP4Bytes()) - 1)
	createProjectUploadBucketAndSelectWithPolicyAndQuota(
		t,
		httpHandler,
		cookie,
		project.ID,
		"standard",
		&quota,
	)

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	if err := writer.WriteField("projectId", project.ID); err != nil {
		t.Fatalf("write project id: %v", err)
	}
	part, err := writer.CreateFormFile("file", "too-large.mp4")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(testMP4Bytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := newRequest(http.MethodPost, "/api/v1/assets", &uploadBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(mutationHeaderName, mutationHeaderValue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	httpHandler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf(
			"expected direct quota upload status 409, got %d: %s",
			response.Code,
			response.Body.String(),
		)
	}
	var quotaError errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&quotaError); err != nil {
		t.Fatalf("decode direct quota error: %v", err)
	}
	if quotaError.Error.Code != "project_storage.upload_quota_exceeded" {
		t.Fatalf("unexpected direct quota error: %#v", quotaError)
	}

	library := performJSONRequest(
		httpHandler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/library",
		"",
		cookie,
	)
	if library.Code != http.StatusOK {
		t.Fatalf("query project library failed: %d %s", library.Code, library.Body.String())
	}
	var page mediaLibraryPageResponse
	if err := json.NewDecoder(library.Body).Decode(&page); err != nil {
		t.Fatalf("decode project library: %v", err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("quota rejected upload still created media: %#v", page)
	}
}

func TestProjectAssetCopyMoveAndTrashScope(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())

	createProject := func(name string) projectResponse {
		response := performJSONRequest(
			handler,
			http.MethodPost,
			"/api/v1/projects",
			projectCreateBodyWithStorage(t, handler, cookie, name, nil),
			cookie,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("create project %q failed: %d %s", name, response.Code, response.Body.String())
		}
		var project projectResponse
		if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
			t.Fatalf("decode project %q: %v", name, err)
		}
		return project
	}

	projectA := createProject("Project A")
	projectB := createProject("Project B")
	projectC := createProject("Project C")
	uploadBucket := createProjectUploadBucketAndSelect(
		t,
		handler,
		cookie,
		projectA.ID,
	)

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	if err := writer.WriteField("projectId", projectA.ID); err != nil {
		t.Fatalf("write project id field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "project-cut.mp4")
	if err != nil {
		t.Fatalf("create upload file: %v", err)
	}
	if _, err := part.Write(testMP4Bytes()); err != nil {
		t.Fatalf("write upload file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close upload writer: %v", err)
	}
	uploadRequest := newRequest(http.MethodPost, "/api/v1/assets", &uploadBody)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	uploadRequest.AddCookie(cookie)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"upload project asset failed: %d %s",
			uploadResponse.Code,
			uploadResponse.Body.String(),
		)
	}
	var version assetVersionResponse
	if err := json.NewDecoder(uploadResponse.Body).Decode(&version); err != nil {
		t.Fatalf("decode uploaded asset: %v", err)
	}
	if version.AuthorizedRootID != uploadBucket.AuthorizedRootID {
		t.Fatalf(
			"uploaded version root = %s, want bucket root %s",
			version.AuthorizedRootID,
			uploadBucket.AuthorizedRootID,
		)
	}
	if version.UploadCheck == nil ||
		version.UploadCheck.ProjectID == nil ||
		*version.UploadCheck.ProjectID != projectA.ID ||
		version.UploadCheck.UploadSecurityPolicy != "standard" ||
		version.UploadCheck.Status != "ready" {
		t.Fatalf("unexpected project upload check: %#v", version.UploadCheck)
	}

	var versionBody bytes.Buffer
	versionWriter := multipart.NewWriter(&versionBody)
	if err := versionWriter.WriteField("revision", "1"); err != nil {
		t.Fatalf("write version revision: %v", err)
	}
	versionPart, err := versionWriter.CreateFormFile("file", "project-cut-v2.mp4")
	if err != nil {
		t.Fatalf("create version upload file: %v", err)
	}
	if _, err := versionPart.Write(testMP4Bytes()); err != nil {
		t.Fatalf("write version upload file: %v", err)
	}
	if err := versionWriter.Close(); err != nil {
		t.Fatalf("close version upload writer: %v", err)
	}
	versionRequest := newRequest(
		http.MethodPost,
		"/api/v1/assets/"+version.AssetID+"/versions",
		&versionBody,
	)
	versionRequest.Header.Set("Content-Type", versionWriter.FormDataContentType())
	versionRequest.Header.Set(mutationHeaderName, mutationHeaderValue)
	versionRequest.AddCookie(cookie)
	versionResponse := httptest.NewRecorder()
	handler.ServeHTTP(versionResponse, versionRequest)
	if versionResponse.Code != http.StatusCreated {
		t.Fatalf(
			"upload project asset version failed: %d %s",
			versionResponse.Code,
			versionResponse.Body.String(),
		)
	}
	var versionTwo assetVersionResponse
	if err := json.NewDecoder(versionResponse.Body).Decode(&versionTwo); err != nil {
		t.Fatalf("decode uploaded asset version: %v", err)
	}
	if versionTwo.VersionNumber != 2 ||
		versionTwo.AuthorizedRootID != uploadBucket.AuthorizedRootID {
		t.Fatalf("unexpected uploaded asset version: %#v", versionTwo)
	}
	if versionTwo.UploadCheck == nil ||
		versionTwo.UploadCheck.ProjectID == nil ||
		*versionTwo.UploadCheck.ProjectID != projectA.ID ||
		versionTwo.UploadCheck.Status != "ready" {
		t.Fatalf("unexpected project version upload check: %#v", versionTwo.UploadCheck)
	}
	rootID := uploadBucket.AuthorizedRootID

	projectLibraryTotal := func(projectID string) int {
		response := performJSONRequest(
			handler,
			http.MethodGet,
			"/api/v1/authorized-roots/"+rootID+"/library?projectId="+projectID,
			"",
			cookie,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("query project library failed: %d %s", response.Code, response.Body.String())
		}
		var page mediaLibraryPageResponse
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			t.Fatalf("decode project library: %v", err)
		}
		return page.Total
	}

	if projectLibraryTotal(projectA.ID) != 1 {
		t.Fatal("uploaded asset should be visible in project A")
	}
	copyResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectB.ID+"/assets",
		fmt.Sprintf(`{"assetId":%q}`, version.AssetID),
		cookie,
	)
	if copyResponse.Code != http.StatusCreated {
		t.Fatalf("copy asset failed: %d %s", copyResponse.Code, copyResponse.Body.String())
	}
	if projectLibraryTotal(projectA.ID) != 1 || projectLibraryTotal(projectB.ID) != 1 {
		t.Fatal("copy should leave the asset visible in both projects")
	}

	trashResponse := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/projects/"+projectA.ID+"/assets/"+version.AssetID,
		"",
		cookie,
	)
	if trashResponse.Code != http.StatusOK {
		t.Fatalf("trash project asset failed: %d %s", trashResponse.Code, trashResponse.Body.String())
	}
	if projectLibraryTotal(projectA.ID) != 0 || projectLibraryTotal(projectB.ID) != 1 {
		t.Fatal("trashing from project A should not remove the asset from project B")
	}
	trashList := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+projectA.ID+"/asset-trash",
		"",
		cookie,
	)
	if trashList.Code != http.StatusOK {
		t.Fatalf("list project trash failed: %d %s", trashList.Code, trashList.Body.String())
	}
	var trashed projectAssetListResponse
	if err := json.NewDecoder(trashList.Body).Decode(&trashed); err != nil {
		t.Fatalf("decode project trash: %v", err)
	}
	if len(trashed.Items) != 1 ||
		trashed.Items[0].AssetID != version.AssetID ||
		trashed.Items[0].TrashExpiresAt == nil {
		t.Fatalf("unexpected project trash: %#v", trashed.Items)
	}

	restoreResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/asset-trash/"+version.AssetID+"/restore",
		"",
		cookie,
	)
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore project asset failed: %d %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	if projectLibraryTotal(projectA.ID) != 1 || projectLibraryTotal(projectB.ID) != 1 {
		t.Fatal("restore should bring the asset back only to project A")
	}

	moveResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/assets/"+version.AssetID+"/move",
		fmt.Sprintf(`{"targetProjectId":%q}`, projectC.ID),
		cookie,
	)
	if moveResponse.Code != http.StatusOK {
		t.Fatalf("move project asset failed: %d %s", moveResponse.Code, moveResponse.Body.String())
	}
	if projectLibraryTotal(projectA.ID) != 0 ||
		projectLibraryTotal(projectB.ID) != 1 ||
		projectLibraryTotal(projectC.ID) != 1 {
		t.Fatal("move should remove only the source project relation")
	}
}

func TestProjectStorageGrantAndSelectionBoundary(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"ownerEmail": "owner-storage@example.com",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	memberAccount, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "plain-member@example.com",
			DisplayName: "Plain Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"plain-member@example.com","password":"member-password-123"}`,
		nil,
	)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login failed: %d %s", memberLogin.Code, memberLogin.Body.String())
	}
	memberCookie := findSessionCookie(t, memberLogin.Result().Cookies())

	localPath := t.TempDir()
	createRootBody, err := json.Marshal(authorizedRootRequest{
		DisplayName: "Shared Source",
		LocalPath:   localPath,
		Mode:        "referenced",
		ScanEnabled: false,
	})
	if err != nil {
		t.Fatalf("encode root request: %v", err)
	}
	rootResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/authorized-roots",
		string(createRootBody),
		ownerCookie,
	)
	if rootResponse.Code != http.StatusCreated {
		t.Fatalf("create root failed: %d %s", rootResponse.Code, rootResponse.Body.String())
	}
	var root authorizedRootResponse
	if err := json.NewDecoder(rootResponse.Body).Decode(&root); err != nil {
		t.Fatalf("decode root: %v", err)
	}

	memberGrant := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/project-storage-grants",
		fmt.Sprintf(
			`{"storageProviderId":%q,"authorizedRootId":%q,"status":"active"}`,
			root.StorageProviderID,
			root.ID,
		),
		memberCookie,
	)
	if memberGrant.Code != http.StatusForbidden {
		t.Fatalf(
			"member grant status = %d, want 403: %s",
			memberGrant.Code,
			memberGrant.Body.String(),
		)
	}

	grantResponse := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/project-storage-grants",
		fmt.Sprintf(
			`{"storageProviderId":%q,"authorizedRootId":%q,"status":"active"}`,
			root.StorageProviderID,
			root.ID,
		),
		ownerCookie,
	)
	if grantResponse.Code != http.StatusOK {
		t.Fatalf("set grant failed: %d %s", grantResponse.Code, grantResponse.Body.String())
	}
	var grant projectStorageGrantResponse
	if err := json.NewDecoder(grantResponse.Body).Decode(&grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if grant.Status != "active" || grant.AuthorizedRootID == nil ||
		*grant.AuthorizedRootID != root.ID {
		t.Fatalf("unexpected grant: %#v", grant)
	}
	invalidProjectCreate := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		fmt.Sprintf(
			`{"name":"Invalid Storage Project","storageGrantId":%q}`,
			grant.ID,
		),
		ownerCookie,
	)
	if invalidProjectCreate.Code != http.StatusConflict {
		t.Fatalf(
			"invalid project storage status = %d, want 409: %s",
			invalidProjectCreate.Code,
			invalidProjectCreate.Body.String(),
		)
	}

	projectCreate := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(
			t,
			handler,
			ownerCookie,
			"Storage Project",
			nil,
		),
		ownerCookie,
	)
	if projectCreate.Code != http.StatusCreated {
		t.Fatalf("create project failed: %d %s", projectCreate.Code, projectCreate.Body.String())
	}
	var project projectResponse
	if err := json.NewDecoder(projectCreate.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	uploadBucket := createProjectUploadBucketAndSelect(
		t,
		handler,
		ownerCookie,
		project.ID,
	)

	addProjectMember := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/members",
		fmt.Sprintf(
			`{"userId":%q,"roleKey":"member","permissions":{"project.read":true},"expiresAt":null}`,
			memberAccount.ID,
		),
		ownerCookie,
	)
	if addProjectMember.Code != http.StatusCreated {
		t.Fatalf(
			"add project member failed: %d %s",
			addProjectMember.Code,
			addProjectMember.Body.String(),
		)
	}
	var memberProjectMembership projectMemberResponse
	if err := json.NewDecoder(addProjectMember.Body).Decode(&memberProjectMembership); err != nil {
		t.Fatalf("decode project member: %v", err)
	}

	memberGrantList := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/storage-grants",
		"",
		memberCookie,
	)
	if memberGrantList.Code != http.StatusForbidden {
		t.Fatalf(
			"read-only member list project grants status = %d, want 403: %s",
			memberGrantList.Code,
			memberGrantList.Body.String(),
		)
	}

	promoteMember := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/projects/"+project.ID+"/members/"+memberProjectMembership.ID,
		fmt.Sprintf(
			`{"roleKey":"member","status":"active","permissions":{"project.read":true,"project.manage":true},"expiresAt":null,"revision":%d}`,
			memberProjectMembership.Revision,
		),
		ownerCookie,
	)
	if promoteMember.Code != http.StatusOK {
		t.Fatalf(
			"grant project manage permission failed: %d %s",
			promoteMember.Code,
			promoteMember.Body.String(),
		)
	}

	memberGrantList = performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/storage-grants",
		"",
		memberCookie,
	)
	if memberGrantList.Code != http.StatusOK {
		t.Fatalf(
			"project manager list project grants failed: %d %s",
			memberGrantList.Code,
			memberGrantList.Body.String(),
		)
	}
	var memberGrants projectStorageGrantListResponse
	if err := json.NewDecoder(memberGrantList.Body).Decode(&memberGrants); err != nil {
		t.Fatalf("decode member grants: %v", err)
	}
	var sawLegacyGrant bool
	var bucketGrantID string
	for _, item := range memberGrants.Items {
		if item.ID == grant.ID {
			sawLegacyGrant = true
		}
		if item.AuthorizedRootID != nil &&
			*item.AuthorizedRootID == uploadBucket.AuthorizedRootID {
			bucketGrantID = item.ID
		}
	}
	if !sawLegacyGrant || bucketGrantID == "" {
		t.Fatalf("unexpected member grants: %#v", memberGrants.Items)
	}

	// A workspace-only bucket, such as the review-upload bucket, must stay
	// invisible to project members even though its provider is active.
	workspaceBucket, err := config.Storage.CreateLocalManagedBucket(
		context.Background(),
		storage.CreateLocalManagedBucketInput{
			WorkspaceID:          ownerSession.Workspace.ID,
			DisplayName:          "Workspace review uploads",
			LocalPath:            filepath.Join(t.TempDir(), "workspace-review-uploads"),
			Purpose:              "review_upload",
			UploadSecurityPolicy: "standard",
			ProjectAvailable:     false,
			CreatedBy:            ownerSession.User.ID,
		},
	)
	if err != nil {
		t.Fatalf("create workspace-only bucket: %v", err)
	}
	workspaceGrant := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/project-storage-grants",
		fmt.Sprintf(
			`{"storageProviderId":%q,"authorizedRootId":%q,"status":"active"}`,
			root.StorageProviderID,
			workspaceBucket.AuthorizedRootID,
		),
		ownerCookie,
	)
	if workspaceGrant.Code != http.StatusOK {
		t.Fatalf(
			"set workspace-only grant failed: %d %s",
			workspaceGrant.Code,
			workspaceGrant.Body.String(),
		)
	}
	memberGrantList = performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/storage-grants",
		"",
		memberCookie,
	)
	if memberGrantList.Code != http.StatusOK {
		t.Fatalf(
			"project manager relist project grants failed: %d %s",
			memberGrantList.Code,
			memberGrantList.Body.String(),
		)
	}
	var relistedGrants projectStorageGrantListResponse
	if err := json.NewDecoder(memberGrantList.Body).Decode(&relistedGrants); err != nil {
		t.Fatalf("decode relisted member grants: %v", err)
	}
	for _, item := range relistedGrants.Items {
		if item.AuthorizedRootID != nil &&
			*item.AuthorizedRootID == workspaceBucket.AuthorizedRootID {
			t.Fatal("project member must not see a workspace-only storage bucket")
		}
	}

	selectResponse := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/projects/"+project.ID+"/storage-selections/upload",
		fmt.Sprintf(`{"grantId":%q}`, grant.ID),
		ownerCookie,
	)
	if selectResponse.Code != http.StatusConflict {
		t.Fatalf(
			"select legacy upload root status = %d, want 409: %s",
			selectResponse.Code,
			selectResponse.Body.String(),
		)
	}

	selectResponse = performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/projects/"+project.ID+"/storage-selections/upload",
		fmt.Sprintf(`{"grantId":%q}`, bucketGrantID),
		ownerCookie,
	)
	if selectResponse.Code != http.StatusOK {
		t.Fatalf("select storage failed: %d %s", selectResponse.Code, selectResponse.Body.String())
	}
	var selection projectStorageSelectionResponse
	if err := json.NewDecoder(selectResponse.Body).Decode(&selection); err != nil {
		t.Fatalf("decode selection: %v", err)
	}
	if selection.Purpose != "upload" || selection.GrantID != bucketGrantID ||
		selection.Grant.StorageProviderID != uploadBucket.StorageProviderID {
		t.Fatalf("unexpected selection: %#v", selection)
	}

	listSelections := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/storage-selections",
		"",
		ownerCookie,
	)
	if listSelections.Code != http.StatusOK {
		t.Fatalf("list selections failed: %d %s", listSelections.Code, listSelections.Body.String())
	}
	var selections projectStorageSelectionListResponse
	if err := json.NewDecoder(listSelections.Body).Decode(&selections); err != nil {
		t.Fatalf("decode selections: %v", err)
	}
	purposes := map[string]string{}
	for _, item := range selections.Items {
		purposes[item.Purpose] = item.GrantID
	}
	if len(selections.Items) != 3 ||
		purposes["upload"] != selection.GrantID ||
		purposes["default_rendition"] == "" ||
		purposes["archive"] == "" {
		t.Fatalf("unexpected selections: %#v", selections.Items)
	}
}

func TestProjectMediaIsolationRejectsCrossProjectCollectionAndAssignment(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-isolation@example.com",
			"password":"local-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	createProject := func(name string) projectResponse {
		t.Helper()
		response := performJSONRequest(
			handler,
			http.MethodPost,
			"/api/v1/projects",
			projectCreateBodyWithStorage(t, handler, ownerCookie, name, nil),
			ownerCookie,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("create project %s failed: %d %s", name, response.Code, response.Body.String())
		}
		var project projectResponse
		if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
			t.Fatalf("decode project %s: %v", name, err)
		}
		createProjectUploadBucketAndSelect(t, handler, ownerCookie, project.ID)
		return project
	}
	projectA := createProject("Project A")
	projectB := createProject("Project B")
	versionB := uploadProjectMP4ForTest(
		t,
		handler,
		ownerCookie,
		projectB.ID,
		"private-b.mp4",
	)

	collectionResponseRecorder := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/collections",
		`{"name":"A review queue","description":null}`,
		ownerCookie,
	)
	if collectionResponseRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"create collection failed: %d %s",
			collectionResponseRecorder.Code,
			collectionResponseRecorder.Body.String(),
		)
	}
	var collection collectionResponse
	if err := json.NewDecoder(collectionResponseRecorder.Body).Decode(&collection); err != nil {
		t.Fatalf("decode collection: %v", err)
	}

	member, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "project-a-member@example.com",
			DisplayName: "Project A Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	addMember := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/members",
		fmt.Sprintf(
			`{"userId":%q,"roleKey":"member","permissions":{},"expiresAt":null}`,
			member.ID,
		),
		ownerCookie,
	)
	if addMember.Code != http.StatusCreated {
		t.Fatalf("add project A member failed: %d %s", addMember.Code, addMember.Body.String())
	}
	login := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"project-a-member@example.com","password":"member-password-123"}`,
		nil,
	)
	if login.Code != http.StatusOK {
		t.Fatalf("member login failed: %d %s", login.Code, login.Body.String())
	}
	memberCookie := findSessionCookie(t, login.Result().Cookies())

	addCrossProjectItem := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/collections/"+collection.ID+"/items",
		fmt.Sprintf(`{"assetId":%q}`, versionB.AssetID),
		memberCookie,
	)
	if addCrossProjectItem.Code != http.StatusForbidden {
		t.Fatalf(
			"cross-project collection status = %d, want 403: %s",
			addCrossProjectItem.Code,
			addCrossProjectItem.Body.String(),
		)
	}

	assignCrossProject := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/storage-objects/"+versionB.StorageObjectID+"/project",
		fmt.Sprintf(`{"projectId":%q}`, projectA.ID),
		memberCookie,
	)
	if assignCrossProject.Code != http.StatusForbidden {
		t.Fatalf(
			"cross-project assignment status = %d, want 403: %s",
			assignCrossProject.Code,
			assignCrossProject.Body.String(),
		)
	}

	addToProjectA := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/assets",
		fmt.Sprintf(`{"assetId":%q}`, versionB.AssetID),
		ownerCookie,
	)
	if addToProjectA.Code != http.StatusCreated {
		t.Fatalf("owner add asset to project A failed: %d %s", addToProjectA.Code, addToProjectA.Body.String())
	}
	sharedAssetRename := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/assets/"+versionB.AssetID,
		`{"name":"member-renamed.mp4","revision":1}`,
		memberCookie,
	)
	if sharedAssetRename.Code != http.StatusForbidden {
		t.Fatalf(
			"shared asset rename status = %d, want 403: %s",
			sharedAssetRename.Code,
			sharedAssetRename.Body.String(),
		)
	}
	addVisibleItem := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/collections/"+collection.ID+"/items",
		fmt.Sprintf(`{"assetId":%q}`, versionB.AssetID),
		memberCookie,
	)
	if addVisibleItem.Code != http.StatusCreated {
		t.Fatalf(
			"visible collection item status = %d, want 201: %s",
			addVisibleItem.Code,
			addVisibleItem.Body.String(),
		)
	}
}

func TestUnassignSharedAssetRequiresAllProjectPermission(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-unassign@example.com",
			"password":"local-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	createProject := func(name string) projectResponse {
		t.Helper()
		response := performJSONRequest(
			handler,
			http.MethodPost,
			"/api/v1/projects",
			projectCreateBodyWithStorage(t, handler, ownerCookie, name, nil),
			ownerCookie,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("create project %s failed: %d %s", name, response.Code, response.Body.String())
		}
		var project projectResponse
		if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
			t.Fatalf("decode project %s: %v", name, err)
		}
		createProjectUploadBucketAndSelect(t, handler, ownerCookie, project.ID)
		return project
	}
	projectA := createProject("Project A")
	projectB := createProject("Project B")
	versionB := uploadProjectMP4ForTest(
		t,
		handler,
		ownerCookie,
		projectB.ID,
		"shared-unassign.mp4",
	)

	addToProjectA := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/assets",
		fmt.Sprintf(`{"assetId":%q}`, versionB.AssetID),
		ownerCookie,
	)
	if addToProjectA.Code != http.StatusCreated {
		t.Fatalf("add asset to project A failed: %d %s", addToProjectA.Code, addToProjectA.Body.String())
	}

	member, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "project-a-member-unassign@example.com",
			DisplayName: "Project A Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	addMember := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+projectA.ID+"/members",
		fmt.Sprintf(
			`{"userId":%q,"roleKey":"member","permissions":{},"expiresAt":null}`,
			member.ID,
		),
		ownerCookie,
	)
	if addMember.Code != http.StatusCreated {
		t.Fatalf("add project A member failed: %d %s", addMember.Code, addMember.Body.String())
	}
	login := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"project-a-member-unassign@example.com","password":"member-password-123"}`,
		nil,
	)
	if login.Code != http.StatusOK {
		t.Fatalf("member login failed: %d %s", login.Code, login.Body.String())
	}
	memberCookie := findSessionCookie(t, login.Result().Cookies())

	unassignShared := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/storage-objects/"+versionB.StorageObjectID+"/project",
		`{"projectId":null}`,
		memberCookie,
	)
	if unassignShared.Code != http.StatusForbidden {
		t.Fatalf(
			"unassign shared asset status = %d, want 403: %s",
			unassignShared.Code,
			unassignShared.Body.String(),
		)
	}
}

func TestStorageCopyRejectsSourceProjectWithoutReadAccess(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-copy@example.com",
			"password":"local-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	createProject := func(name string) projectResponse {
		t.Helper()
		response := performJSONRequest(
			handler,
			http.MethodPost,
			"/api/v1/projects",
			projectCreateBodyWithStorage(t, handler, ownerCookie, name, nil),
			ownerCookie,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("create project %s failed: %d %s", name, response.Code, response.Body.String())
		}
		var project projectResponse
		if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
			t.Fatalf("decode project %s: %v", name, err)
		}
		createProjectUploadBucketAndSelect(t, handler, ownerCookie, project.ID)
		return project
	}
	projectB := createProject("Project B")
	versionB := uploadProjectMP4ForTest(
		t,
		handler,
		ownerCookie,
		projectB.ID,
		"confidential.mp4",
	)

	if _, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "storage-admin@example.com",
			DisplayName: "Storage Admin",
			Password:    "admin-password-123",
			Locale:      "zh-CN",
			Role:        "admin",
		},
	); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminLogin := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"storage-admin@example.com","password":"admin-password-123"}`,
		nil,
	)
	if adminLogin.Code != http.StatusOK {
		t.Fatalf("admin login failed: %d %s", adminLogin.Code, adminLogin.Body.String())
	}
	adminCookie := findSessionCookie(t, adminLogin.Result().Cookies())

	copyBody := fmt.Sprintf(
		`{"sourceStorageObjectId":%q,"targetRootId":%q}`,
		versionB.StorageObjectID,
		versionB.AuthorizedRootID,
	)
	copyResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/storage-copy-tasks",
		copyBody,
		adminCookie,
	)
	if copyResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"cross-project copy status = %d, want 403: %s",
			copyResponse.Code,
			copyResponse.Body.String(),
		)
	}
}

func TestRawObjectEndpointsRequireStorageAdministration(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName":"Studio",
			"ownerName":"Owner",
			"ownerEmail":"owner-raw@example.com",
			"password":"local-password-123",
			"locale":"zh-CN",
			"timezone":"Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	ownerCookie := findSessionCookie(t, setup.Result().Cookies())
	var ownerSession sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&ownerSession); err != nil {
		t.Fatalf("decode owner session: %v", err)
	}

	createProjectResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(t, handler, ownerCookie, "Project A", nil),
		ownerCookie,
	)
	if createProjectResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed: %d %s",
			createProjectResponse.Code,
			createProjectResponse.Body.String(),
		)
	}
	var projectA projectResponse
	if err := json.NewDecoder(createProjectResponse.Body).Decode(&projectA); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	bucket := createProjectUploadBucketAndSelect(t, handler, ownerCookie, projectA.ID)

	if _, err := config.Identity.CreateAccount(
		context.Background(),
		identity.CreateAccountInput{
			WorkspaceID: ownerSession.Workspace.ID,
			Email:       "raw-member@example.com",
			DisplayName: "Raw Member",
			Password:    "member-password-123",
			Locale:      "zh-CN",
			Role:        "member",
		},
	); err != nil {
		t.Fatalf("create member: %v", err)
	}
	login := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/session",
		`{"email":"raw-member@example.com","password":"member-password-123"}`,
		nil,
	)
	if login.Code != http.StatusOK {
		t.Fatalf("member login failed: %d %s", login.Code, login.Body.String())
	}
	memberCookie := findSessionCookie(t, login.Result().Cookies())

	listObjects := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/authorized-roots/"+bucket.AuthorizedRootID+"/objects",
		"",
		memberCookie,
	)
	if listObjects.Code != http.StatusForbidden {
		t.Fatalf(
			"member raw object list status = %d, want 403: %s",
			listObjects.Code,
			listObjects.Body.String(),
		)
	}
}

func TestParseRangeHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		size    int64
		offset  int64
		length  int64
		partial bool
		wantErr bool
	}{
		{name: "full", value: "", size: 16, offset: 0, length: 0},
		{
			name: "bounded", value: "bytes=4-9", size: 16,
			offset: 4, length: 6, partial: true,
		},
		{
			name: "open ended", value: "bytes=10-", size: 16,
			offset: 10, length: 6, partial: true,
		},
		{
			name: "suffix", value: "bytes=-4", size: 16,
			offset: 12, length: 4, partial: true,
		},
		{name: "multiple", value: "bytes=0-1,4-5", size: 16, wantErr: true},
		{name: "past end", value: "bytes=20-", size: 16, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, partial, err := parseRangeHeader(test.value, test.size)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected range error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse range: %v", err)
			}
			if result.Offset != test.offset ||
				result.Length != test.length ||
				partial != test.partial {
				t.Fatalf(
					"unexpected range: %#v partial=%v",
					result,
					partial,
				)
			}
		})
	}
}

func performJSONRequest(
	handler http.Handler,
	method string,
	path string,
	body string,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request := newRequest(method, path, reader)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Accept", "application/json")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set(mutationHeaderName, mutationHeaderValue)
	}
	request.Header.Set(hostCapabilityHeader, testHostManagementToken)
	if cookie != nil {
		request.AddCookie(cookie)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func setupUploadSecurityProject(
	t *testing.T,
	handler http.Handler,
	projectName string,
) (*http.Cookie, sessionResponse, projectResponse) {
	t.Helper()

	setup := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", setup.Code, setup.Body.String())
	}
	cookie := findSessionCookie(t, setup.Result().Cookies())
	var session sessionResponse
	if err := json.NewDecoder(setup.Body).Decode(&session); err != nil {
		t.Fatalf("decode setup session: %v", err)
	}

	createProject := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects",
		projectCreateBodyWithStorage(t, handler, cookie, projectName, nil),
		cookie,
	)
	if createProject.Code != http.StatusCreated {
		t.Fatalf(
			"create project failed: %d %s",
			createProject.Code,
			createProject.Body.String(),
		)
	}
	var project projectResponse
	if err := json.NewDecoder(createProject.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return cookie, session, project
}

func TestProjectArchiveQueuesCopiesForSeparateArchiveStorage(t *testing.T) {
	handler := NewHandler(testConfig(t))
	cookie, _, project := setupUploadSecurityProject(
		t,
		handler,
		"Archive routing",
	)
	version := uploadProjectMP4ForTest(
		t,
		handler,
		cookie,
		project.ID,
		"archive-source.mp4",
	)

	bucketBody, err := json.Marshal(createLocalManagedBucketRequest{
		DisplayName:          "Archive destination",
		LocalPath:            filepath.Join(t.TempDir(), "archive-destination"),
		Purpose:              "source_archive",
		UploadSecurityPolicy: "standard",
		ProjectAvailable:     boolPointer(true),
	})
	if err != nil {
		t.Fatalf("encode archive bucket: %v", err)
	}
	createBucket := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/local-managed-buckets",
		string(bucketBody),
		cookie,
	)
	if createBucket.Code != http.StatusCreated {
		t.Fatalf("create archive bucket failed: %d %s", createBucket.Code, createBucket.Body.String())
	}
	var bucket localManagedBucketResponse
	if err := json.NewDecoder(createBucket.Body).Decode(&bucket); err != nil {
		t.Fatalf("decode archive bucket: %v", err)
	}

	listGrants := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+project.ID+"/storage-grants",
		"",
		cookie,
	)
	if listGrants.Code != http.StatusOK {
		t.Fatalf("list project grants failed: %d %s", listGrants.Code, listGrants.Body.String())
	}
	var grants projectStorageGrantListResponse
	if err := json.NewDecoder(listGrants.Body).Decode(&grants); err != nil {
		t.Fatalf("decode project grants: %v", err)
	}
	archiveGrantID := ""
	for _, grant := range grants.Items {
		if grant.AuthorizedRootID != nil &&
			*grant.AuthorizedRootID == bucket.AuthorizedRootID {
			archiveGrantID = grant.ID
			break
		}
	}
	if archiveGrantID == "" {
		t.Fatalf("archive grant missing: %#v", grants.Items)
	}
	selectArchive := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/projects/"+project.ID+"/storage-selections/archive",
		fmt.Sprintf(`{"grantId":%q}`, archiveGrantID),
		cookie,
	)
	if selectArchive.Code != http.StatusOK {
		t.Fatalf("select archive storage failed: %d %s", selectArchive.Code, selectArchive.Body.String())
	}

	archive := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/projects/"+project.ID+"/archive",
		`{"revision":1}`,
		cookie,
	)
	if archive.Code != http.StatusOK {
		t.Fatalf("archive project failed: %d %s", archive.Code, archive.Body.String())
	}

	jobsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/jobs?limit=100",
		"",
		cookie,
	)
	if jobsResponse.Code != http.StatusOK {
		t.Fatalf("list archive jobs failed: %d %s", jobsResponse.Code, jobsResponse.Body.String())
	}
	var jobs jobListResponse
	if err := json.NewDecoder(jobsResponse.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode archive jobs: %v", err)
	}
	copyTaskID := ""
	for _, item := range jobs.Items {
		if item.Type == storage.CopyObjectJobType {
			copyTaskID = item.Subject.ID
			break
		}
	}
	if copyTaskID == "" {
		t.Fatalf("archive copy job missing: %#v", jobs.Items)
	}
	copyResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/storage-copy-tasks/"+copyTaskID,
		"",
		cookie,
	)
	if copyResponse.Code != http.StatusOK {
		t.Fatalf("read archive copy task failed: %d %s", copyResponse.Code, copyResponse.Body.String())
	}
	var task storageCopyTaskResponse
	if err := json.NewDecoder(copyResponse.Body).Decode(&task); err != nil {
		t.Fatalf("decode archive copy task: %v", err)
	}
	if task.SourceStorageObjectID != version.StorageObjectID ||
		task.TargetRootID != bucket.AuthorizedRootID ||
		!strings.Contains(task.TargetObjectKey, "/archive/") {
		t.Fatalf("unexpected archive copy task: %#v", task)
	}
}

func projectCreateBodyWithStorage(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	name string,
	description *string,
) string {
	t.Helper()
	bucketBody, err := json.Marshal(createLocalManagedBucketRequest{
		DisplayName:          "Project default storage",
		LocalPath:            filepath.Join(t.TempDir(), "project-default-storage"),
		Purpose:              "upload",
		UploadSecurityPolicy: "standard",
		ProjectAvailable:     boolPointer(true),
	})
	if err != nil {
		t.Fatalf("encode project default storage: %v", err)
	}
	createBucket := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/local-managed-buckets",
		string(bucketBody),
		cookie,
	)
	if createBucket.Code != http.StatusCreated {
		t.Fatalf(
			"create project default storage failed: %d %s",
			createBucket.Code,
			createBucket.Body.String(),
		)
	}
	var bucket localManagedBucketResponse
	if err := json.NewDecoder(createBucket.Body).Decode(&bucket); err != nil {
		t.Fatalf("decode project default storage: %v", err)
	}
	listGrants := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/project-storage-grants/available",
		"",
		cookie,
	)
	if listGrants.Code != http.StatusOK {
		t.Fatalf(
			"list project creation storage failed: %d %s",
			listGrants.Code,
			listGrants.Body.String(),
		)
	}
	var grants projectStorageGrantListResponse
	if err := json.NewDecoder(listGrants.Body).Decode(&grants); err != nil {
		t.Fatalf("decode project creation storage: %v", err)
	}
	var grantID string
	for _, grant := range grants.Items {
		if grant.AuthorizedRootID != nil &&
			*grant.AuthorizedRootID == bucket.AuthorizedRootID {
			grantID = grant.ID
			break
		}
	}
	if grantID == "" {
		t.Fatalf("project default storage grant missing: %#v", grants.Items)
	}
	projectBody, err := json.Marshal(projectRequest{
		Name:           name,
		Description:    description,
		StorageGrantID: grantID,
	})
	if err != nil {
		t.Fatalf("encode project with default storage: %v", err)
	}
	return string(projectBody)
}

func uploadProjectMP4ForTest(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	projectID string,
	filename string,
) assetVersionResponse {
	t.Helper()

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	if err := writer.WriteField("projectId", projectID); err != nil {
		t.Fatalf("write project id: %v", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(testMP4Bytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := newRequest(http.MethodPost, "/api/v1/assets", &uploadBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(mutationHeaderName, mutationHeaderValue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf(
			"upload project asset failed: %d %s",
			response.Code,
			response.Body.String(),
		)
	}
	var version assetVersionResponse
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		t.Fatalf("decode uploaded asset: %v", err)
	}
	return version
}

func TestUploadAssetRejectsUnknownMultipartFieldBeforeDrainingIt(t *testing.T) {
	handler := NewHandler(testConfig(t))
	cookie, _, project := setupUploadSecurityProject(t, handler, "Upload field boundary")

	var uploadBody bytes.Buffer
	writer := multipart.NewWriter(&uploadBody)
	unknown, err := writer.CreateFormField("untrusted")
	if err != nil {
		t.Fatalf("create unknown field: %v", err)
	}
	const oversizedFieldBytes = 2 * 1024 * 1024
	if _, err := unknown.Write(bytes.Repeat([]byte("x"), oversizedFieldBytes)); err != nil {
		t.Fatalf("write unknown field: %v", err)
	}
	if err := writer.WriteField("projectId", project.ID); err != nil {
		t.Fatalf("write project id: %v", err)
	}
	part, err := writer.CreateFormFile("file", "should-not-be-read.mp4")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(testMP4Bytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	body := &countingReadCloser{reader: bytes.NewReader(uploadBody.Bytes())}
	request := newRequest(http.MethodPost, "/api/v1/assets", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(mutationHeaderName, mutationHeaderValue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "media.invalid_asset_upload") ||
		!strings.Contains(response.Body.String(), "unsupported multipart field") {
		t.Fatalf("expected unsupported field error, got %s", response.Body.String())
	}
	if body.read >= 128*1024 {
		t.Fatalf("unknown field was drained before rejection: read %d bytes", body.read)
	}
}

func createProjectUploadBucketAndSelect(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	projectID string,
) localManagedBucketResponse {
	t.Helper()
	return createProjectUploadBucketAndSelectWithPolicy(
		t,
		handler,
		cookie,
		projectID,
		"standard",
	)
}

func createProjectUploadBucketAndSelectWithPolicy(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	projectID string,
	policy string,
) localManagedBucketResponse {
	t.Helper()
	return createProjectUploadBucketAndSelectWithPolicyAndQuota(
		t,
		handler,
		cookie,
		projectID,
		policy,
		nil,
	)
}

func createProjectUploadBucketAndSelectWithPolicyAndQuota(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	projectID string,
	policy string,
	quotaBytes *int64,
) localManagedBucketResponse {
	t.Helper()

	bucketBody, err := json.Marshal(createLocalManagedBucketRequest{
		DisplayName:          "Project uploads",
		LocalPath:            filepath.Join(t.TempDir(), "project-uploads"),
		Purpose:              "upload",
		QuotaBytes:           quotaBytes,
		UploadSecurityPolicy: policy,
		ProjectAvailable:     boolPointer(true),
	})
	if err != nil {
		t.Fatalf("encode upload bucket: %v", err)
	}
	createBucket := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/local-managed-buckets",
		string(bucketBody),
		cookie,
	)
	if createBucket.Code != http.StatusCreated {
		t.Fatalf(
			"create upload bucket failed: %d %s",
			createBucket.Code,
			createBucket.Body.String(),
		)
	}
	var bucket localManagedBucketResponse
	if err := json.NewDecoder(createBucket.Body).Decode(&bucket); err != nil {
		t.Fatalf("decode upload bucket: %v", err)
	}

	grantsResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/projects/"+projectID+"/storage-grants",
		"",
		cookie,
	)
	if grantsResponse.Code != http.StatusOK {
		t.Fatalf(
			"list upload bucket grants failed: %d %s",
			grantsResponse.Code,
			grantsResponse.Body.String(),
		)
	}
	var grants projectStorageGrantListResponse
	if err := json.NewDecoder(grantsResponse.Body).Decode(&grants); err != nil {
		t.Fatalf("decode upload bucket grants: %v", err)
	}
	var grantID string
	for _, grant := range grants.Items {
		if grant.AuthorizedRootID != nil &&
			*grant.AuthorizedRootID == bucket.AuthorizedRootID &&
			grant.Status == "active" {
			grantID = grant.ID
			break
		}
	}
	if grantID == "" {
		t.Fatalf("upload bucket grant was not synchronized: %#v", grants.Items)
	}

	selectResponse := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/projects/"+projectID+"/storage-selections/upload",
		fmt.Sprintf(`{"grantId":%q}`, grantID),
		cookie,
	)
	if selectResponse.Code != http.StatusOK {
		t.Fatalf(
			"select upload bucket failed: %d %s",
			selectResponse.Code,
			selectResponse.Body.String(),
		)
	}
	return bucket
}

func newProjectUploadWebDAVProbeServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	collections := map[string]bool{"/dav": true}
	return httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "review" || password != "secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/dav") {
			http.NotFound(response, request)
			return
		}
		requestPath := strings.TrimSuffix(request.URL.Path, "/")
		if requestPath == "" {
			requestPath = "/dav"
		}
		switch request.Method {
		case http.MethodOptions:
			response.Header().Set(
				"Allow",
				"OPTIONS, PROPFIND, GET, PUT, COPY, MOVE, DELETE, MKCOL",
			)
			response.Header().Set("DAV", "1, 2")
			response.WriteHeader(http.StatusNoContent)
		case "PROPFIND":
			mu.Lock()
			body, isFile := objects[requestPath]
			isCollection := collections[requestPath]
			mu.Unlock()
			if !isFile && !isCollection {
				http.NotFound(response, request)
				return
			}
			href := requestPath
			resourceType := ""
			contentType := "image/png"
			contentLength := fmt.Sprintf("%d", len(body))
			if isCollection {
				href += "/"
				resourceType = `<d:collection/>`
				contentType = ""
				contentLength = "0"
			}
			displayName := href
			if index := strings.LastIndex(strings.TrimSuffix(href, "/"), "/"); index >= 0 {
				displayName = strings.TrimSuffix(href, "/")[index+1:]
			}
			response.Header().Set("Content-Type", "application/xml")
			response.WriteHeader(http.StatusMultiStatus)
			_, _ = io.WriteString(response,
				`<?xml version="1.0" encoding="utf-8"?>`+
					`<d:multistatus xmlns:d="DAV:">`+
					fmt.Sprintf(`<d:response><d:href>%s</d:href>`, href)+
					`<d:propstat><d:prop>`+
					fmt.Sprintf(`<d:resourcetype>%s</d:resourcetype>`, resourceType)+
					fmt.Sprintf(`<d:getcontentlength>%s</d:getcontentlength>`, contentLength)+
					fmt.Sprintf(`<d:getcontenttype>%s</d:getcontenttype>`, contentType)+
					fmt.Sprintf(`<d:displayname>%s</d:displayname>`, displayName)+
					`</d:prop><d:status>HTTP/1.1 200 OK</d:status>`+
					`</d:propstat></d:response></d:multistatus>`,
			)
		case "MKCOL":
			mu.Lock()
			alreadyExists := collections[requestPath]
			collections[requestPath] = true
			mu.Unlock()
			if alreadyExists {
				response.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			response.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			objects[requestPath] = body
			mu.Unlock()
			response.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			mu.Lock()
			_, ok := objects[requestPath]
			mu.Unlock()
			if !ok {
				http.NotFound(response, request)
				return
			}
			http.Redirect(response, request, "https://cdn.example.test/object", http.StatusFound)
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func testConfig(t *testing.T) Config {
	t.Helper()
	// Handler integration tests exercise application authorization rather than
	// plaintext transport rejection. Requests are modeled as arriving through
	// the trusted HTTPS gateway (see newRequest), and cookies are marked Secure.
	t.Setenv("REVIEW_STUDIO_SECURE_COOKIES", "1")

	db, err := database.Open(context.Background(), database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	secretStore, err := secretstore.NewEncryptedSQLiteStore(
		db,
		bytes.Repeat([]byte{0x51}, 32),
	)
	if err != nil {
		t.Fatalf("create test secret store: %v", err)
	}
	storageService := storage.NewServiceWithDependencies(
		storage.NewSQLiteRepository(db),
		storage.NewDefaultProviderRegistry(),
		secretStore,
	)
	mediaRepository := media.NewSQLiteRepository(db)
	mediaProber := media.NewFFProber("ffprobe")
	renditionStore, err := media.NewManagedStore(
		filepath.Join(t.TempDir(), "renditions"),
	)
	if err != nil {
		t.Fatalf("create rendition store: %v", err)
	}
	sourceStore, err := media.NewManagedSourceStore(
		filepath.Join(t.TempDir(), "sources"),
	)
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	return Config{
		Version:             "test",
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		HostManagementToken: testHostManagementToken,
		// Handler integration tests model requests as arriving via the trusted
		// HTTPS gateway; treat the httptest client networks as trusted proxies.
		TrustedProxyCIDRs: []string{"192.0.2.0/24", "203.0.113.0/24"},
		Identity:          identityService,
		Catalog:           catalog.NewService(catalog.NewSQLiteRepository(db)),
		Storage:           storageService,
		Media: media.NewService(
			mediaRepository,
			storageService,
			mediaProber,
		),
		Library: media.NewLibraryServiceWithSourceStoreAndStorage(
			media.NewSQLiteLibraryRepository(db),
			sourceStore,
			storageService,
		),
		Renditions: media.NewRenditionService(
			media.NewSQLiteRenditionRepository(db),
			mediaRepository,
			storageService,
			renditionStore,
			media.NewFFmpegImageProcessor("ffmpeg"),
			media.NewFFmpegVideoProcessor("ffmpeg", t.TempDir()),
			mediaProber,
		),
		Jobs: job.NewService(job.NewSQLiteRepository(db)),
		Reviews: reviewdomain.NewService(
			reviewdomain.NewSQLiteRepository(db),
		),
		ReviewTemplates: reviewtemplate.NewService(
			reviewtemplate.NewSQLiteRepository(db),
		),
		Shares: sharedomain.NewServiceWithSecretsAndRateLimits(
			sharedomain.NewSQLiteRepository(db),
			secretStore,
			ratelimit.New(db),
		),
		Audit: audit.NewService(audit.NewSQLiteRepository(db)),
		Notifications: notification.NewServiceWithDependencies(
			notification.NewSQLiteRepository(db),
			secretStore,
			notification.NoopSender{},
		),
		Authorization: authorization.NewService(),
		ProjectAccess: projectaccess.NewService(
			projectaccess.NewSQLiteRepository(db),
		),
		ProjectMembers: projectmember.NewService(
			projectmember.NewSQLiteRepository(db),
		),
		ProjectStorage: projectstorage.NewService(
			projectstorage.NewSQLiteRepository(db),
		),
		Members: workspace.NewService(
			workspace.NewSQLiteRepository(db),
		),
		Invitations: invitation.NewService(
			invitation.NewSQLiteRepository(db),
			identityService,
		),
		WorkspaceSettings: workspacesettings.NewService(
			workspacesettings.NewSQLiteRepository(db),
		),
		SystemSettings: systemsettings.NewService(
			systemsettings.NewSQLiteRepository(db),
		),
		RateLimits: ratelimit.New(db),
	}
}

// testConfigFrom rebuilds a Config from an existing one to model a Core restart:
// the stored network decision is re-read into the handler runtime value exactly
// as cmd/server does at startup.
func testConfigFrom(t *testing.T, previous Config) Config {
	t.Helper()
	settings, err := previous.SystemSettings.GetNetwork(context.Background())
	if err != nil {
		t.Fatalf("read stored network settings: %v", err)
	}
	next := previous
	next.RequireRemoteHTTPS = settings.RequireRemoteHTTPS
	return next
}

func findSessionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	return findCookieNamed(t, cookies, sessionCookieName)
}

// newRequest builds a handler integration request as if it arrived through the
// trusted HTTPS gateway, so transport security is satisfied while tests exercise
// application authorization rather than plaintext rejection.
func newRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("X-Forwarded-Proto", "https")
	return request
}

func findCookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()

	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}

	t.Fatalf("cookie %q was not set", name)
	return nil
}

func findShareSessionCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()

	for _, cookie := range cookies {
		if cookie.Name == shareSessionCookieName {
			return cookie
		}
	}

	t.Fatal("share session cookie was not set")
	return nil
}

func boolPointer(value bool) *bool {
	return &value
}

// TestSecurityHeadersIsolateShareResponses locks down the SAR-F61 fix:
// share responses are authorized per visitor, so they must not be reusable by
// shared or misconfigured caches.
func TestSecurityHeadersIsolateShareResponses(t *testing.T) {
	h := &handler{}
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"ok": "true"})
	})

	share := httptest.NewRequest(http.MethodGet, "http://visto.test/share-api/v1/share", nil)
	shareResponse := httptest.NewRecorder()
	h.securityHeaders(next).ServeHTTP(shareResponse, share)
	if got := shareResponse.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("share cache-control = %q, want %q", got, "private, no-store")
	}

	api := httptest.NewRequest(http.MethodGet, "http://visto.test/api/v1/session", nil)
	apiResponse := httptest.NewRecorder()
	h.securityHeaders(next).ServeHTTP(apiResponse, api)
	if got := apiResponse.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("management cache-control = %q, want empty default", got)
	}
}
