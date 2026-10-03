package serverupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The free tier channel is a version announcement: no signature, no artifacts
// and no download location. Check therefore validates shape only, and these
// tests pin which shapes are accepted.
func TestCheckAcceptsTheVersionAnnouncement(t *testing.T) {
	body := `{"schemaVersion":1,"channel":"stable","version":"1.0.1","publishedAt":"2026-09-02T00:00:00Z"}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/latest.json" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(body))
	}))
	defer server.Close()

	result, err := Check(context.Background(), CheckConfig{
		Sources:       []string{server.URL},
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Manifest.Version != "1.0.1" || result.Source != server.URL {
		t.Fatalf("result = %#v", result)
	}
	if !result.Manifest.PublishedAt.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("publishedAt = %v", result.Manifest.PublishedAt)
	}
}

// A manifest can no longer name a download location or a signing key, so a
// source that still sends those fields must not change how the manifest is
// interpreted: they are ignored, never surfaced.
func TestCheckIgnoresFieldsTheFreeTierDoesNotUse(t *testing.T) {
	body := `{"schemaVersion":1,"channel":"stable","version":"1.0.1",` +
		`"publishedAt":"2026-09-02T00:00:00Z",` +
		`"artifacts":[{"kind":"docker-core","platform":"linux-amd64",` +
		`"url":"https://evil.example/core","image":"ghcr.io/x@sha256:` + strings.Repeat("a", 64) + `",` +
		`"sha256":"` + strings.Repeat("a", 64) + `","sizeBytes":1}],` +
		`"signingKeyId":"release-2026-09",` +
		`"minimumSupportedVersion":"1.0.0",` +
		`"releaseNotes":["download from evil.example"]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(body))
	}))
	defer server.Close()

	result, err := Check(context.Background(), CheckConfig{
		Sources:       []string{server.URL},
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Manifest.Version != "1.0.1" {
		t.Fatalf("manifest = %#v", result.Manifest)
	}
}

func TestCheckFallsBackToTheNextSource(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"schemaVersion":1,"channel":"stable","version":"1.0.1","publishedAt":"2026-09-02T00:00:00Z"}`))
	}))
	defer good.Close()
	// A source that answers with something unusable must not win.
	bad := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("tampered"))
	}))
	defer bad.Close()

	result, err := Check(context.Background(), CheckConfig{
		Sources:       []string{bad.URL, good.URL},
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Source != good.URL || result.Manifest.Version != "1.0.1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckRequiresAConfiguredSource(t *testing.T) {
	if _, err := Check(context.Background(), CheckConfig{}); err == nil {
		t.Fatal("Check() without sources must fail")
	}
}

func TestCheckRejectsRedirectedManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://untrusted.example/latest.json", http.StatusFound)
	}))
	defer server.Close()

	_, err := Check(context.Background(), CheckConfig{
		Sources:       []string{server.URL},
		AllowInsecure: true,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
}

func TestDetectDeploymentKindPrefersExplicitValue(t *testing.T) {
	cases := map[string]string{
		"Docker":            DeploymentDocker,
		" windows-server  ": DeploymentWindowsServer,
		"linux-server":      DeploymentLinuxServer,
		"macos-server":      DeploymentMacOSServer,
		"SOURCE":            DeploymentSource,
	}
	for explicit, want := range cases {
		if got := DetectDeploymentKind(explicit); got != want {
			t.Fatalf("DetectDeploymentKind(%q) = %q, want %q", explicit, got, want)
		}
	}
	// An unsupported value must fall back to detection rather than reach the
	// Owner page as an unknown deployment kind.
	if got := DetectDeploymentKind("kubernetes"); got == "kubernetes" {
		t.Fatalf("DetectDeploymentKind passed an unsupported value through: %q", got)
	}
}

// TestDetectDeploymentKindUsesHostEvidence pins the precedence the Owner update
// page depends on: a container marker outranks the native install prefixes, and
// a Windows host without either is a native Windows Server rather than a source
// checkout.
func TestDetectDeploymentKindUsesHostEvidence(t *testing.T) {
	got := DetectDeploymentKind("")
	known := map[string]bool{
		DeploymentDocker:        true,
		DeploymentWindowsServer: true,
		DeploymentLinuxServer:   true,
		DeploymentMacOSServer:   true,
		DeploymentSource:        true,
	}
	if !known[got] {
		t.Fatalf("DetectDeploymentKind(\"\") = %q, want a documented deployment kind", got)
	}

	_, containerErr := os.Stat("/.dockerenv")
	_, linuxErr := os.Stat(linuxServerPrefix)
	_, macOSErr := os.Stat(macOSServerPrefix)
	switch {
	case containerErr == nil:
		if got != DeploymentDocker {
			t.Fatalf("container marker must win, got %q", got)
		}
	case linuxErr == nil:
		if got != DeploymentLinuxServer {
			t.Fatalf("linux prefix must win, got %q", got)
		}
	case macOSErr == nil:
		if got != DeploymentMacOSServer {
			t.Fatalf("macOS prefix must win, got %q", got)
		}
	case runtime.GOOS == "windows":
		if got != DeploymentWindowsServer {
			t.Fatalf("a Windows host without native prefixes is a Windows Server, got %q", got)
		}
	}
}
