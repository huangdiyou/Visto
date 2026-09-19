package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrForbiddenHost  = errors.New("outbound host is blocked by network policy")
	ErrUnresolvedHost = errors.New("outbound host did not resolve")
	ErrUnexpectedHost = errors.New("outbound request changed host")

	carrierGradeNATCIDR = mustParseCIDR("100.64.0.0/10")
	benchmarkCIDR       = mustParseCIDR("198.18.0.0/15")
	metadataIPv4        = net.ParseIP("169.254.169.254")
	metadataIPv6        = net.ParseIP("fd00:ec2::254")
)

type HTTPPolicy struct {
	AllowPrivate   bool
	AllowRedirects bool
	MaxRedirects   int
	Timeout        time.Duration
}

func mustParseCIDR(value string) *net.IPNet {
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		panic(err)
	}
	return network
}

func ValidateHTTPURL(ctx context.Context, value string, allowPrivate bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return nil, errors.New("outbound URL is invalid")
	}
	if _, err := ResolveAllowed(ctx, parsed.Hostname(), allowPrivate); err != nil {
		return nil, err
	}
	return parsed, nil
}

func ResolveAllowed(ctx context.Context, host string, allowPrivate bool) ([]net.IPAddr, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, strings.TrimSpace(host))
	if err != nil {
		return nil, fmt.Errorf("resolve outbound host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, ErrUnresolvedHost
	}
	for _, address := range addresses {
		if ForbiddenIP(address.IP, allowPrivate) {
			return nil, ErrForbiddenHost
		}
	}
	return addresses, nil
}

func ForbiddenIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip.Equal(metadataIPv4) || ip.Equal(metadataIPv6) {
		return true
	}
	if allowPrivate {
		return false
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		carrierGradeNATCIDR.Contains(ip) ||
		benchmarkCIDR.Contains(ip)
}

func DialContext(allowPrivate bool, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := ResolveAllowed(ctx, host, allowPrivate)
		if err != nil {
			return nil, err
		}
		var failures []error
		for _, resolved := range addresses {
			conn, dialErr := dialer.DialContext(
				ctx,
				network,
				net.JoinHostPort(resolved.String(), port),
			)
			if dialErr == nil {
				return conn, nil
			}
			failures = append(failures, dialErr)
		}
		return nil, errors.Join(failures...)
	}
}

func NewHTTPClient(endpoint *url.URL, policy HTTPPolicy) *http.Client {
	if policy.MaxRedirects < 1 {
		policy.MaxRedirects = 5
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 2 * time.Minute
	}
	dial := DialContext(policy.AllowPrivate, 10*time.Second)
	transport := &http.Transport{
		// Remote credentials must never inherit host proxy environment variables.
		Proxy: nil,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if !policy.AllowRedirects && !strings.EqualFold(host, endpoint.Hostname()) {
				return nil, ErrUnexpectedHost
			}
			return dial(ctx, network, address)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConnsPerHost:   4,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   policy.Timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !policy.AllowRedirects {
				return errors.New("outbound redirects are disabled")
			}
			if len(via) >= policy.MaxRedirects {
				return errors.New("outbound redirect limit exceeded")
			}
			if request.URL == nil ||
				(request.URL.Scheme != "http" && request.URL.Scheme != "https") ||
				request.URL.Hostname() == "" || request.URL.User != nil {
				return ErrForbiddenHost
			}
			if endpoint.Scheme == "https" && request.URL.Scheme != "https" {
				return errors.New("outbound redirect downgraded HTTPS")
			}
			if policy.AllowPrivate && !sameOriginHost(endpoint, request.URL) {
				return ErrUnexpectedHost
			}
			if _, err := ResolveAllowed(request.Context(), request.URL.Hostname(), policy.AllowPrivate); err != nil {
				return err
			}
			if len(via) > 0 && !sameOrigin(via[len(via)-1].URL, request.URL) {
				request.Header.Del("Authorization")
				request.Header.Del("Cookie")
				request.Header.Del("Proxy-Authorization")
			}
			return nil
		},
	}
}

func sameOriginHost(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Hostname(), right.Hostname())
}

func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
