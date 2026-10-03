package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestDialContextRejectsPrivateAddressAtConnectionTime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := DialContext(false, time.Second)(ctx, "tcp", listener.Addr().String()); err == nil {
		t.Fatal("expected final dial to reject loopback address")
	}
}

func TestDialContextAllowsExplicitPrivateAddressAndConnects(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			acceptErr = conn.Close()
		}
		accepted <- acceptErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := DialContext(true, time.Second)(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial explicitly allowed private endpoint: %v", err)
	}
	_ = conn.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept connection: %v", err)
	}
}

func TestDialContextEnforcesIPv6PrivateAndMetadataPolicyAtConnectionTime(t *testing.T) {
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable on this host: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := DialContext(false, time.Second)(ctx, "tcp", listener.Addr().String()); err == nil {
		t.Fatal("expected IPv6 loopback to be rejected without private network opt-in")
	}

	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			acceptErr = connection.Close()
		}
		accepted <- acceptErr
	}()
	connection, err := DialContext(true, time.Second)(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial explicitly allowed IPv6 loopback: %v", err)
	}
	_ = connection.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept IPv6 connection: %v", err)
	}

	if _, err := DialContext(true, time.Second)(ctx, "tcp", "[fd00:ec2::254]:80"); !errors.Is(err, ErrForbiddenHost) {
		t.Fatalf("expected IPv6 metadata endpoint rejection at final dial, got %v", err)
	}
}

func TestForbiddenIPAlwaysBlocksMetadataEndpoint(t *testing.T) {
	if !ForbiddenIP(net.ParseIP("169.254.169.254"), true) {
		t.Fatal("metadata endpoint must remain blocked with private network opt-in")
	}
	if !ForbiddenIP(net.ParseIP("fd00:ec2::254"), true) {
		t.Fatal("IPv6 metadata endpoint must remain blocked with private network opt-in")
	}
}

func TestHTTPRedirectPolicyPreservesHTTPSAndPrivateOrigin(t *testing.T) {
	httpsEndpoint, err := url.Parse("https://example.test/dav")
	if err != nil {
		t.Fatalf("parse HTTPS endpoint: %v", err)
	}
	downgrade, err := url.Parse("http://example.test/object")
	if err != nil {
		t.Fatalf("parse downgrade URL: %v", err)
	}
	httpsClient := NewHTTPClient(httpsEndpoint, HTTPPolicy{AllowRedirects: true})
	if err := httpsClient.CheckRedirect(
		&http.Request{URL: downgrade},
		[]*http.Request{{URL: httpsEndpoint}},
	); err == nil {
		t.Fatal("HTTPS redirect downgrade must be rejected")
	}

	privateEndpoint, err := url.Parse("http://127.0.0.1:8787/dav")
	if err != nil {
		t.Fatalf("parse private endpoint: %v", err)
	}
	crossHost, err := url.Parse("http://127.0.0.2:8787/object")
	if err != nil {
		t.Fatalf("parse cross-host URL: %v", err)
	}
	privateClient := NewHTTPClient(privateEndpoint, HTTPPolicy{
		AllowPrivate:   true,
		AllowRedirects: true,
	})
	if err := privateClient.CheckRedirect(
		&http.Request{URL: crossHost},
		[]*http.Request{{URL: privateEndpoint}},
	); !errors.Is(err, ErrUnexpectedHost) {
		t.Fatalf("private redirect changed host without rejection: %v", err)
	}
}
