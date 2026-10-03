package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"

	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/projectaccess"
)

const (
	hostCapabilityHeader = "X-Visto-Host-Capability"
	hostProxyHeader      = "X-Visto-Proxy"
)

type accessInfo struct {
	Surface         string `json:"surface"`
	HostManagement  bool   `json:"hostManagement"`
	RemoteWorkspace bool   `json:"remoteWorkspace"`
}

// hostAccessDefault keeps the host management boundary closed for any instance
// that never recorded a first-run choice (D2, docs/FREE_TIER_BOUNDARY_DESIGN.md).
// A deployment that upgrades from before the setting existed therefore keeps the
// previous behaviour instead of silently relaxing it.
const hostAccessDefault = false

func (h *handler) requireHostManagement(
	response http.ResponseWriter,
	request *http.Request,
) bool {
	if h.requestHasHostManagement(request) {
		return true
	}
	// D2, docs/FREE_TIER_BOUNDARY_DESIGN.md: when the Owner answered the first-run
	// wizard in favour of web host paths, an Owner web session may reach the host
	// management surface. The host token still works for every deployment, and
	// the guard is unchanged when the switch is off.
	if h.webHostPathsAllowed() && h.requestIsOwner(request) {
		return true
	}
	writeError(
		response,
		http.StatusForbidden,
		requestID(response),
		"access.host_required",
		"只能在运行 Core 的主机上执行此操作",
	)
	return false
}

func (h *handler) requirePermission(
	response http.ResponseWriter,
	request *http.Request,
	permission authorization.Permission,
) (identity.Session, bool) {
	session, ok := h.requireSession(response, request)
	if !ok {
		return identity.Session{}, false
	}
	if err := h.authorization.Require(session.Role, permission); err != nil {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.denied",
			"当前角色不能执行此操作",
		)
		return identity.Session{}, false
	}
	return session, true
}

func (h *handler) requireProjectPermission(
	response http.ResponseWriter,
	request *http.Request,
	session identity.Session,
	projectID string,
	permission projectaccess.Permission,
) bool {
	err := h.projectAccess.Require(request.Context(), projectaccess.CheckInput{
		WorkspaceID:   session.Workspace.ID,
		ProjectID:     projectID,
		UserID:        session.User.ID,
		WorkspaceRole: session.Role,
		Permission:    permission,
	})
	if err == nil {
		return true
	}
	if errors.Is(err, projectaccess.ErrDenied) {
		writeError(
			response,
			http.StatusForbidden,
			requestID(response),
			"permission.project_denied",
			"当前账号不能操作这个项目",
		)
		return false
	}
	h.internalError(response, request, err)
	return false
}

func (h *handler) requestAccessInfo(request *http.Request) accessInfo {
	hostManagement := h.requestHasHostManagement(request)
	surface := "remote"
	if hostManagement {
		surface = "host"
	}
	return accessInfo{
		Surface:         surface,
		HostManagement:  hostManagement,
		RemoteWorkspace: true,
	}
}

func (h *handler) requestHasHostManagement(request *http.Request) bool {
	providedToken := strings.TrimSpace(request.Header.Get(hostCapabilityHeader))
	if h.hostManagementToken != "" {
		if secureStringEqual(h.hostManagementToken, providedToken) {
			return true
		}
		cookie, err := request.Cookie(hostSessionCookieName)
		return err == nil && secureStringEqual(hostSessionValue(h.hostManagementToken), cookie.Value)
	}
	// Transport locality is not an authentication capability. A loopback proxy,
	// local process, or DNS-rebound browser must present an explicit token.
	return false
}

// webHostPathsAllowed reports whether an Owner web session may reach the host
// management surface. An explicit deployment override wins over the value the
// first-run wizard recorded (D2, docs/FREE_TIER_BOUNDARY_DESIGN.md).
func (h *handler) webHostPathsAllowed() bool {
	if h.allowWebHostPathsOverride != nil {
		return *h.allowWebHostPathsOverride
	}
	return h.allowWebHostPaths.Load()
}

// requestIsOwner reports whether the request carries an Owner session. It does
// not write a response, because requireHostManagement needs the answer before it
// decides whether to fail.
func (h *handler) requestIsOwner(request *http.Request) bool {
	if h.identity == nil {
		return false
	}
	session, err := h.authenticateRequest(request)
	if err != nil {
		return false
	}
	return strings.EqualFold(session.Role, "owner")
}

// loadHostAccessSetting seeds the runtime value from the database. A read
// failure leaves the restrictive default in place, which is the safe direction.
func (h *handler) loadHostAccessSetting() {
	if h.systemSettings == nil || h.allowWebHostPathsOverride != nil {
		return
	}
	settings, err := h.systemSettings.GetHostAccess(context.Background())
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("system host access settings are unavailable", "error", err)
		}
		return
	}
	h.allowWebHostPaths.Store(settings.AllowWebHostPaths)
}

func hostSessionValue(token string) string {
	digest := sha256.Sum256([]byte("visto-host-session-v1|" + token))
	return hex.EncodeToString(digest[:])
}

// requestClientIP deliberately uses the transport peer only. Forwarded headers
// are untrusted input unless a deployment has an explicit trusted-proxy model.
func requestClientIP(request *http.Request) (net.IP, bool) {
	return parseAddressIP(request.RemoteAddr)
}

// requestRateLimitClientIdentity accepts a gateway-provided client address only
// when the operator explicitly opts into a trusted-proxy model and the transport
// peer is an allowlisted proxy. Core's default remains the direct transport peer,
// so a forwarded header cannot turn a remote request into a different rate-limit
// identity by itself.
func (h *handler) requestRateLimitClientIdentity(request *http.Request) string {
	if os.Getenv("REVIEW_STUDIO_TRUST_PROXY_HEADERS") == "1" {
		if peer, ok := requestClientIP(request); ok && h.trustedProxyContains(peer) {
			if address, ok := parseAddressIP(request.Header.Get("X-Real-IP")); ok {
				return address.String()
			}
		}
	}
	if address, ok := requestClientIP(request); ok {
		return address.String()
	}
	return ""
}

// environmentForcesHTTPS reports the positive deployment override. Setting
// REVIEW_STUDIO_REQUIRE_HTTPS=1 pins enforcement on and disables the Owner
// toggle; changing it requires a Core restart. The old negative variable
// REVIEW_STUDIO_ALLOW_PLAINTEXT_HTTP is gone: a negative flag whose default
// silently enforced HTTPS was both hard to reason about and wrong for a
// local-first deployment.
func (h *handler) environmentForcesHTTPS() bool {
	return os.Getenv("REVIEW_STUDIO_REQUIRE_HTTPS") == "1"
}

// requireRemoteHTTPSEffective is what transportSecurity enforces right now. The
// atomic value is refreshed the moment the Owner saves, so a page-level setting
// never needs a Core restart.
func (h *handler) requireRemoteHTTPSEffective() bool {
	return h.environmentForcesHTTPS() || h.requireRemoteHTTPS.Load()
}

// requestMayEnableHTTPSEnforcement guards the Owner against locking itself out.
// Turning enforcement on from a remote plaintext page would make the next
// request fail with 426 and leave no way back in, so only loopback, the host
// control surface, or a provably-HTTPS request may enable it.
func (h *handler) requestMayEnableHTTPSEnforcement(request *http.Request) bool {
	return h.requestIsSecureTransport(request) || h.requestHasHostManagement(request)
}

func (h *handler) transportSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/api/") &&
			!strings.HasPrefix(request.URL.Path, "/join-api/") &&
			!strings.HasPrefix(request.URL.Path, "/share-api/") {
			next.ServeHTTP(response, request)
			return
		}
		// Default is off: a fresh install must not block LAN HTTP before the
		// Owner has any chance to configure TLS. Off means "HTTP or HTTPS is
		// accepted", never "HTTPS is disabled".
		if !h.requireRemoteHTTPSEffective() {
			next.ServeHTTP(response, request)
			return
		}
		// Loopback is always allowed, including when enforcement is on, so the
		// Owner can never be locked out of the machine running Core.
		if h.requestIsSecureTransport(request) {
			next.ServeHTTP(response, request)
			return
		}
		writeError(
			response,
			http.StatusUpgradeRequired,
			requestID(response),
			"security.https_required",
			"远程管理与分享必须通过 HTTPS 访问",
		)
	})
}

// requestIsSecureTransport reports whether a request arrived over a channel Core
// can trust as end-to-end encrypted. It deliberately ignores
// REVIEW_STUDIO_SECURE_COOKIES, which only governs the Cookie Secure attribute
// and must never authorize plaintext transport.
//
// Plaintext is only acceptable when the path is loopback at every hop Core can
// observe: the socket peer is loopback, and an allowlisted proxy did not
// introduce a client address that is not loopback either. In every other case an
// allowlisted proxy must prove the outer connection was HTTPS.
//
// Listing a peer in REVIEW_STUDIO_TRUSTED_PROXIES is itself the operator's
// declaration that this peer terminates TLS, so the scheme assertion is
// accepted without REVIEW_STUDIO_TRUST_PROXY_HEADERS (which only governs
// trusting a forwarded client identity).
//
// This closes the bypass where an allowlisted peer sent "X-Real-IP: 127.0.0.1"
// to be treated as a loopback client over plaintext HTTP.
func (h *handler) requestIsSecureTransport(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	peer, ok := requestClientIP(request)
	if !ok {
		return false
	}
	trustedProxy := h.trustedProxyContains(peer)
	// The forwarded address is only authoritative when the direct peer is an
	// allowlisted proxy; otherwise the socket peer is the client.
	client := peer
	if trustedProxy {
		if forwarded, forwardedOK := parseAddressIP(request.Header.Get("X-Real-IP")); forwardedOK {
			client = forwarded
		}
	}
	if client.IsLoopback() && peer.IsLoopback() {
		return true
	}
	if !trustedProxy {
		return false
	}
	return strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https")
}

func (h *handler) trustedProxyContains(address net.IP) bool {
	for _, network := range h.trustedProxyCIDRs {
		if network.Contains(address) {
			return true
		}
	}
	return false
}

func parseTrustedProxyCIDRs(values []string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(value); err == nil {
			networks = append(networks, network)
		}
	}
	return networks
}

func secureStringEqual(expected string, actual string) bool {
	if len(expected) != len(actual) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func parseAddressIP(value string) (net.IP, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	address := net.ParseIP(strings.Trim(value, "[]"))
	return address, address != nil
}
