package storage

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestValidateRemoteEndpointRejectsPublicPlaintextTransport(t *testing.T) {
	t.Parallel()

	if _, err := validateRemoteEndpoint(context.Background(), "http://1.1.1.1/dav", false); err == nil || !errors.Is(err, ErrEndpointForbidden) {
		t.Fatalf("expected public plaintext endpoint rejection, got %v", err)
	}

	endpoint, err := validateRemoteEndpoint(context.Background(), "https://1.1.1.1/dav", false)
	if err != nil {
		t.Fatalf("expected public HTTPS endpoint to remain valid: %v", err)
	}
	if endpoint.Scheme != "https" {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
}

func TestSafeHTTPClientDoesNotUseEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:7897")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7897")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:7897")
	t.Setenv("NO_PROXY", "")

	endpoint, err := url.Parse("https://webdav.example.test/dav")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	client := newSafeHTTPClient(endpoint, false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected http transport, got %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("safe storage client must not inherit environment proxy")
	}
}

func TestForbiddenRemoteIPBlocksProxyReservedRanges(t *testing.T) {
	blocked := []string{
		"100.64.0.1",
		"198.18.0.41",
		"198.19.255.254",
	}
	for _, value := range blocked {
		if !forbiddenRemoteIP(net.ParseIP(value), false) {
			t.Fatalf("expected %s to be blocked without private network opt-in", value)
		}
	}
	if forbiddenRemoteIP(net.ParseIP("198.18.0.41"), true) {
		t.Fatal("expected private network opt-in to allow proxy reserved range")
	}
}
