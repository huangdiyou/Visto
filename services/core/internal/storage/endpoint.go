package storage

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"review-studio.local/core/internal/outbound"
)

func validateRemoteEndpoint(
	ctx context.Context,
	value string,
	allowPrivate bool,
) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: endpoint is invalid", ErrInvalidRootInput)
	}
	// Credentials for an Internet-reachable storage provider must not travel in
	// clear text. HTTP remains available only for an explicitly opted-in private
	// network endpoint, such as a local NAS or development MinIO service.
	if parsed.Scheme == "http" && !allowPrivate {
		return nil, fmt.Errorf("%w: plaintext endpoint requires private network opt-in", ErrEndpointForbidden)
	}
	if err := validateRemoteHost(ctx, parsed.Hostname(), allowPrivate); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateRemoteHost(
	ctx context.Context,
	host string,
	allowPrivate bool,
) error {
	if _, err := outbound.ResolveAllowed(ctx, host, allowPrivate); err != nil {
		return fmt.Errorf("%w: %v", ErrEndpointForbidden, err)
	}
	return nil
}

func forbiddenRemoteIP(ip net.IP, allowPrivate bool) bool {
	return outbound.ForbiddenIP(ip, allowPrivate)
}

func newSafeHTTPClient(
	endpoint *url.URL,
	allowPrivate bool,
) *http.Client {
	return newSafeHTTPClientWithRedirects(endpoint, allowPrivate, false)
}

func newSafeReadHTTPClient(
	endpoint *url.URL,
	allowPrivate bool,
) *http.Client {
	return newSafeHTTPClientWithRedirects(endpoint, allowPrivate, true)
}

func newSafeHTTPClientWithRedirects(
	endpoint *url.URL,
	allowPrivate bool,
	allowRedirects bool,
) *http.Client {
	return outbound.NewHTTPClient(endpoint, outbound.HTTPPolicy{
		AllowPrivate:   allowPrivate,
		AllowRedirects: allowRedirects,
		MaxRedirects:   5,
		Timeout:        2 * time.Minute,
	})
}
