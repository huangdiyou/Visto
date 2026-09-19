package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestRateLimitClientIdentityHonorsOnlyAllowlistedProxies(t *testing.T) {
	t.Setenv("REVIEW_STUDIO_TRUST_PROXY_HEADERS", "1")
	h := &handler{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "10.1.2.3:4567"
	trusted.Header.Set("X-Real-IP", "203.0.113.9")
	if identity := h.requestRateLimitClientIdentity(trusted); identity != "203.0.113.9" {
		t.Fatalf("expected forwarded identity from allowlisted proxy, got %q", identity)
	}

	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "198.51.100.7:4567"
	untrusted.Header.Set("X-Real-IP", "203.0.113.9")
	if identity := h.requestRateLimitClientIdentity(untrusted); identity != "198.51.100.7" {
		t.Fatalf("expected transport-peer identity from unverified peer, got %q", identity)
	}
}

// TestTransportSecurityAllowsRemotePlaintextByDefault pins the product default:
// a fresh install must not block LAN HTTP before the Owner can configure TLS.
// Turning the policy off means "HTTP or HTTPS is accepted", not "HTTPS off".
func TestTransportSecurityAllowsRemotePlaintextByDefault(t *testing.T) {
	h := &handler{}
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})

	for _, path := range []string{
		"http://visto.test/api/v1/session",
		"http://visto.test/join-api/v1/registration",
		"http://visto.test/share-api/v1/share",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "192.168.21.10:4567"
		response := httptest.NewRecorder()
		h.transportSecurity(next).ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("default remote plaintext %s status = %d, want 204", path, response.Code)
		}
	}
}

func TestTransportSecurityRejectsPlainRemoteManagement(t *testing.T) {
	t.Setenv("REVIEW_STUDIO_TRUST_PROXY_HEADERS", "1")
	h := &handler{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"127.0.0.0/8"}),
	}
	h.requireRemoteHTTPS.Store(true)
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})

	remote := httptest.NewRequest(http.MethodGet, "http://visto.test/api/v1/session", nil)
	remote.RemoteAddr = "127.0.0.1:4567"
	remote.Header.Set("X-Real-IP", "192.168.21.10")
	remoteResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain remote management status = %d, want 426", remoteResponse.Code)
	}

	loopback := httptest.NewRequest(http.MethodGet, "http://visto.test/api/v1/session", nil)
	loopback.RemoteAddr = "127.0.0.1:4567"
	loopback.Header.Set("X-Real-IP", "127.0.0.1")
	loopbackResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(loopbackResponse, loopback)
	if loopbackResponse.Code != http.StatusNoContent {
		t.Fatalf("loopback management status = %d, want 204", loopbackResponse.Code)
	}

	// REVIEW_STUDIO_SECURE_COOKIES only forces the Cookie Secure attribute; it
	// must not authorize plaintext transport by itself.
	t.Setenv("REVIEW_STUDIO_SECURE_COOKIES", "1")
	secureCookieResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(secureCookieResponse, remote)
	if secureCookieResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("secure-cookie override must not authorize plaintext, status = %d, want 426", secureCookieResponse.Code)
	}

	// An allowlisted proxy that terminated TLS and forwarded the scheme is trusted.
	secureRemote := httptest.NewRequest(http.MethodGet, "http://visto.test/api/v1/session", nil)
	secureRemote.RemoteAddr = "127.0.0.1:4567"
	secureRemote.Header.Set("X-Real-IP", "192.168.21.10")
	secureRemote.Header.Set("X-Forwarded-Proto", "https")
	secureRemoteResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(secureRemoteResponse, secureRemote)
	if secureRemoteResponse.Code != http.StatusNoContent {
		t.Fatalf("trusted HTTPS gateway status = %d, want 204", secureRemoteResponse.Code)
	}
}

// TestTransportSecurityRequireRemoteHTTPSSources covers the two inputs of the
// effective policy: the runtime value the Owner saves (applied without a Core
// restart) and REVIEW_STUDIO_REQUIRE_HTTPS=1, which pins enforcement on and
// cannot be turned off from the page.
func TestTransportSecurityRequireRemoteHTTPSSources(t *testing.T) {
	h := &handler{}
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	request := func() *http.Request {
		remote := httptest.NewRequest(http.MethodGet, "http://visto.test/api/v1/session", nil)
		remote.RemoteAddr = "192.168.21.10:4567"
		return remote
	}

	defaultResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(defaultResponse, request())
	if defaultResponse.Code != http.StatusNoContent {
		t.Fatalf("default status = %d, want 204 (enforcement off by default)", defaultResponse.Code)
	}

	// Saving the setting takes effect for the very next request.
	h.requireRemoteHTTPS.Store(true)
	enabledResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(enabledResponse, request())
	if enabledResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("runtime-enabled status = %d, want 426", enabledResponse.Code)
	}

	enabledShare := httptest.NewRequest(http.MethodGet, "http://visto.test/share-api/v1/share", nil)
	enabledShare.RemoteAddr = "192.168.21.10:4567"
	enabledShareResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(enabledShareResponse, enabledShare)
	if enabledShareResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("runtime-enabled share status = %d, want 426", enabledShareResponse.Code)
	}

	h.requireRemoteHTTPS.Store(false)
	disabledResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(disabledResponse, request())
	if disabledResponse.Code != http.StatusNoContent {
		t.Fatalf("runtime-disabled status = %d, want 204", disabledResponse.Code)
	}

	// The environment override wins over a stored "off".
	t.Setenv("REVIEW_STUDIO_REQUIRE_HTTPS", "1")
	if !h.environmentForcesHTTPS() {
		t.Fatal("environmentForcesHTTPS() = false, want true")
	}
	forcedResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(forcedResponse, request())
	if forcedResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("environment-forced status = %d, want 426", forcedResponse.Code)
	}

	// Loopback is never blocked, so the Owner cannot be locked out of the host.
	forcedLoopback := httptest.NewRequest(http.MethodGet, "http://visto.test/api/v1/session", nil)
	forcedLoopback.RemoteAddr = "127.0.0.1:4567"
	forcedLoopbackResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(forcedLoopbackResponse, forcedLoopback)
	if forcedLoopbackResponse.Code != http.StatusNoContent {
		t.Fatalf("environment-forced loopback status = %d, want 204", forcedLoopbackResponse.Code)
	}
}

// TestRequestMayEnableHTTPSEnforcementBlocksRemotePlaintext covers the
// self-lockout guard: an Owner browsing over remote plaintext HTTP must not be
// able to switch enforcement on, because the next request would fail with 426.
func TestRequestMayEnableHTTPSEnforcementBlocksRemotePlaintext(t *testing.T) {
	h := &handler{
		trustedProxyCIDRs:   parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
		hostManagementToken: "host-token",
	}

	remote := httptest.NewRequest(http.MethodPut, "http://visto.test/api/v1/system/network-settings", nil)
	remote.RemoteAddr = "192.168.21.10:4567"
	if h.requestMayEnableHTTPSEnforcement(remote) {
		t.Fatal("remote plaintext request must not be allowed to enable enforcement")
	}

	loopback := httptest.NewRequest(http.MethodPut, "http://visto.test/api/v1/system/network-settings", nil)
	loopback.RemoteAddr = "127.0.0.1:4567"
	if !h.requestMayEnableHTTPSEnforcement(loopback) {
		t.Fatal("loopback request must be allowed to enable enforcement")
	}

	gateway := httptest.NewRequest(http.MethodPut, "http://visto.test/api/v1/system/network-settings", nil)
	gateway.RemoteAddr = "10.1.2.3:4567"
	gateway.Header.Set("X-Real-IP", "192.168.21.10")
	gateway.Header.Set("X-Forwarded-Proto", "https")
	if !h.requestMayEnableHTTPSEnforcement(gateway) {
		t.Fatal("trusted HTTPS gateway request must be allowed to enable enforcement")
	}

	hostSurface := httptest.NewRequest(http.MethodPut, "http://visto.test/api/v1/system/network-settings", nil)
	hostSurface.RemoteAddr = "192.168.21.10:4567"
	hostSurface.Header.Set(hostCapabilityHeader, "host-token")
	if !h.requestMayEnableHTTPSEnforcement(hostSurface) {
		t.Fatal("host control surface request must be allowed to enable enforcement")
	}
}

// TestTransportSecurityCoversPublicShareAPI closes the gap where share
// sessions, bearer tokens, and shared content under /share-api/ were exempt
// from the HTTPS transport enforcement applied to /api/ and /join-api/.
func TestTransportSecurityCoversPublicShareAPI(t *testing.T) {
	h := &handler{}
	h.requireRemoteHTTPS.Store(true)
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})

	remoteShare := httptest.NewRequest(http.MethodGet, "http://visto.test/share-api/v1/share", nil)
	remoteShare.RemoteAddr = "192.168.21.10:4567"
	remoteResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(remoteResponse, remoteShare)
	if remoteResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain remote share status = %d, want 426", remoteResponse.Code)
	}

	remoteContent := httptest.NewRequest(http.MethodGet, "http://visto.test/share-api/v1/items/item-1/content", nil)
	remoteContent.RemoteAddr = "192.168.21.10:4567"
	contentResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(contentResponse, remoteContent)
	if contentResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain remote share content status = %d, want 426", contentResponse.Code)
	}

	loopbackShare := httptest.NewRequest(http.MethodGet, "http://visto.test/share-api/v1/share", nil)
	loopbackShare.RemoteAddr = "127.0.0.1:4567"
	loopbackResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(loopbackResponse, loopbackShare)
	if loopbackResponse.Code != http.StatusNoContent {
		t.Fatalf("loopback share status = %d, want 204", loopbackResponse.Code)
	}
}

// TestTransportSecurityLoopbackAllowanceUsesSocketPeerOnly closes the bypass
// where an allowlisted proxy could claim "X-Real-IP: 127.0.0.1" and turn a
// plaintext remote request into a loopback one without proving HTTPS. The
// proxy peer here is a remote allowlisted address, exactly as in the reported
// reproduction.
func TestTransportSecurityLoopbackAllowanceUsesSocketPeerOnly(t *testing.T) {
	t.Setenv("REVIEW_STUDIO_TRUST_PROXY_HEADERS", "1")
	h := &handler{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}
	h.requireRemoteHTTPS.Store(true)
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})

	probe := func(remoteAddr string, headers map[string]string) int {
		request := httptest.NewRequest(
			http.MethodGet, "http://visto.test/api/v1/session", nil,
		)
		request.RemoteAddr = remoteAddr
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		h.transportSecurity(next).ServeHTTP(response, request)
		return response.Code
	}

	const proxyPeer = "10.1.2.3:4567"
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			name:    "allowlisted peer claims loopback without any proto",
			headers: map[string]string{"X-Real-IP": "127.0.0.1"},
			want:    http.StatusUpgradeRequired,
		},
		{
			name: "allowlisted peer claims loopback and forwards http",
			headers: map[string]string{
				"X-Real-IP":         "127.0.0.1",
				"X-Forwarded-Proto": "http",
			},
			want: http.StatusUpgradeRequired,
		},
		{
			name:    "allowlisted peer claims an IPv6 loopback client",
			headers: map[string]string{"X-Real-IP": "::1"},
			want:    http.StatusUpgradeRequired,
		},
		{
			name:    "allowlisted peer forwards a remote client but no proto",
			headers: map[string]string{"X-Real-IP": "203.0.113.9"},
			want:    http.StatusUpgradeRequired,
		},
		{
			name: "allowlisted peer forwards a remote client over plaintext http",
			headers: map[string]string{
				"X-Real-IP":         "203.0.113.9",
				"X-Forwarded-Proto": "http",
			},
			want: http.StatusUpgradeRequired,
		},
		{
			name: "allowlisted peer that proves HTTPS is still trusted",
			headers: map[string]string{
				"X-Real-IP":         "203.0.113.9",
				"X-Forwarded-Proto": "https",
			},
			want: http.StatusNoContent,
		},
		{
			// nginx always forwards both, but a gateway that only proves the
			// scheme must keep working.
			name:    "allowlisted peer proves HTTPS without a forwarded client",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    http.StatusNoContent,
		},
		{
			name: "allowlisted peer proves HTTPS for a local client",
			headers: map[string]string{
				"X-Real-IP":         "127.0.0.1",
				"X-Forwarded-Proto": "https",
			},
			want: http.StatusNoContent,
		},
	}
	for _, testCase := range cases {
		if code := probe(proxyPeer, testCase.headers); code != testCase.want {
			t.Fatalf("%s: status = %d, want %d", testCase.name, code, testCase.want)
		}
	}

	// A proxy that is not allowlisted cannot authorize anything, even with a
	// perfectly formed HTTPS forwarding header.
	const unknownPeer = "198.51.100.7:4567"
	if code := probe(unknownPeer, map[string]string{
		"X-Real-IP":         "203.0.113.9",
		"X-Forwarded-Proto": "https",
	}); code != http.StatusUpgradeRequired {
		t.Fatalf("unallowlisted peer: status = %d, want 426", code)
	}

	// Listing a peer in the allowlist is itself the operator's declaration that
	// the peer terminates TLS, so the scheme assertion does not additionally
	// require REVIEW_STUDIO_TRUST_PROXY_HEADERS (which only governs trusting a
	// forwarded client identity).
	t.Setenv("REVIEW_STUDIO_TRUST_PROXY_HEADERS", "0")
	if code := probe(proxyPeer, map[string]string{
		"X-Real-IP":         "203.0.113.9",
		"X-Forwarded-Proto": "https",
	}); code != http.StatusNoContent {
		t.Fatalf("allowlisted peer without trust-proxy headers: status = %d, want 204", code)
	}
	if code := probe(unknownPeer, map[string]string{
		"X-Real-IP":         "203.0.113.9",
		"X-Forwarded-Proto": "https",
	}); code != http.StatusUpgradeRequired {
		t.Fatalf("unallowlisted peer without trust-proxy headers: status = %d, want 426", code)
	}
}

// TestTransportSecurityAllowsFullyLocalProxyChain keeps the local reverse-proxy
// deployment working: nginx on the same host forwarding for a local client is
// still a loopback-only path and must not be forced onto HTTPS. No
// REVIEW_STUDIO_TRUST_PROXY_HEADERS is set, proving the decision depends only
// on the socket peer and the allowlist.
func TestTransportSecurityAllowsFullyLocalProxyChain(t *testing.T) {
	h := &handler{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"127.0.0.0/8"}),
	}
	h.requireRemoteHTTPS.Store(true)
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})

	localProxyLocalClient := httptest.NewRequest(
		http.MethodGet, "http://visto.test/api/v1/session", nil,
	)
	localProxyLocalClient.RemoteAddr = "127.0.0.1:4567"
	localProxyLocalClient.Header.Set("X-Real-IP", "127.0.0.1")
	response := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(response, localProxyLocalClient)
	if response.Code != http.StatusNoContent {
		t.Fatalf("local proxy for local client status = %d, want 204", response.Code)
	}

	// Local development connects straight to 127.0.0.1 with no proxy headers
	// at all, even though the loopback network is in the allowlist.
	directLoopback := httptest.NewRequest(
		http.MethodGet, "http://visto.test/api/v1/session", nil,
	)
	directLoopback.RemoteAddr = "127.0.0.1:4567"
	directResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(directResponse, directLoopback)
	if directResponse.Code != http.StatusNoContent {
		t.Fatalf("direct loopback status = %d, want 204", directResponse.Code)
	}

	// The same local proxy carrying a remote client must prove HTTPS.
	localProxyRemoteClient := httptest.NewRequest(
		http.MethodGet, "http://visto.test/api/v1/session", nil,
	)
	localProxyRemoteClient.RemoteAddr = "127.0.0.1:4567"
	localProxyRemoteClient.Header.Set("X-Real-IP", "192.168.21.10")
	remoteResponse := httptest.NewRecorder()
	h.transportSecurity(next).ServeHTTP(remoteResponse, localProxyRemoteClient)
	if remoteResponse.Code != http.StatusUpgradeRequired {
		t.Fatalf("local proxy for remote client status = %d, want 426", remoteResponse.Code)
	}
}
