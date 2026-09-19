package serverupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVerifyArtifactChecksSignatureSizeAndHash(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "server.zip")
	contents := []byte("trusted server package")
	if err := os.WriteFile(artifactPath, contents, 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	hash := sha256.Sum256(contents)
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, Channel: "stable", Version: "1.0.0",
		PublishedAt: time.Now().UTC(), MinimumSupportedVersion: "1.0.0",
		Artifacts: []Artifact{{
			Kind: "windows-server", Platform: "windows-amd64",
			URL: "https://example.test/server.zip", SHA256: fmtHash(hash), SizeBytes: int64(len(contents)),
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestBytes)))
	config := VerifyArtifactConfig{
		ManifestBytes: manifestBytes, SignatureBytes: signature,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey), ArtifactPath: artifactPath,
		Kind: "windows-server", Platform: "windows-amd64",
	}
	if _, err := VerifyArtifact(config); err != nil {
		t.Fatalf("verify trusted artifact: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("tampered server package"), 0o600); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	if _, err := VerifyArtifact(config); err == nil {
		t.Fatal("expected tampered artifact to fail verification")
	}
}

// TestVerifyArtifactRejectsPlatformMismatch is the guard against
// handing one operating system's package to another: a mismatch must fail even
// when the manifest carries a valid signature and a matching hash.
func TestVerifyArtifactRejectsPlatformMismatch(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "server.tar.gz")
	contents := []byte("trusted macos server package")
	if err := os.WriteFile(artifactPath, contents, 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	hash := sha256.Sum256(contents)
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, Channel: "stable", Version: "1.0.0",
		PublishedAt: time.Now().UTC(), MinimumSupportedVersion: "1.0.0",
		Artifacts: []Artifact{
			{
				Kind: "macos-server", Platform: "macos-arm64",
				URL: "https://example.test/server-arm64.tar.gz", SHA256: fmtHash(hash), SizeBytes: int64(len(contents)),
			},
			{
				Kind: "macos-server", Platform: "macos-amd64",
				URL: "https://example.test/server-amd64.tar.gz", SHA256: fmtHash(hash), SizeBytes: int64(len(contents)),
			},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestBytes)))
	base := VerifyArtifactConfig{
		ManifestBytes: manifestBytes, SignatureBytes: signature,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey), ArtifactPath: artifactPath,
	}

	// The Go spelling of macOS is not a manifest platform, and a Linux host
	// must never be served a macOS package even though the sizes match.
	cases := []struct{ kind, platform string }{
		{"macos-server", "darwin-arm64"},
		{"linux-server", "macos-arm64"},
		{"macos-server", "linux-arm64"},
		{"macos-server", "macos-x64"},
		{"windows-server", "windows-arm64"},
		{"macos-server", ""},
	}
	for _, testCase := range cases {
		config := base
		config.Kind = testCase.kind
		config.Platform = testCase.platform
		if _, err := VerifyArtifact(config); err == nil {
			t.Fatalf("VerifyArtifact(%s, %s) was accepted", testCase.kind, testCase.platform)
		}
	}
	config := base
	config.Kind = "macos-server"
	config.Platform = "macos-arm64"
	if _, err := VerifyArtifact(config); err != nil {
		t.Fatalf("verify trusted macOS artifact: %v", err)
	}
}

func TestCheckFallsBackAfterInvalidPrimaryManifest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest, signature := signedManifest(t, privateKey)
	backup := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/latest.json" {
			_, _ = response.Write(manifest)
			return
		}
		if request.URL.Path == "/latest.json.sig" {
			_, _ = response.Write(signature)
			return
		}
		http.NotFound(response, request)
	}))
	defer backup.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("tampered"))
	}))
	defer primary.Close()

	result, err := Check(context.Background(), CheckConfig{
		Sources: []string{primary.URL, backup.URL}, PublicKey: base64.StdEncoding.EncodeToString(publicKey), AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Source != backup.URL || result.Manifest.Version != "1.0.0" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckRejectsUnsignedManifest(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) { _, _ = response.Write([]byte("not signed")) }))
	defer server.Close()
	_, err = Check(context.Background(), CheckConfig{Sources: []string{server.URL}, PublicKey: base64.StdEncoding.EncodeToString(publicKey), AllowInsecure: true})
	if err == nil {
		t.Fatal("Check() error = nil, want signature error")
	}
}

func TestCheckRejectsRedirectedManifest(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://untrusted.example/latest.json", http.StatusFound)
	}))
	defer server.Close()

	_, err = Check(context.Background(), CheckConfig{
		Sources:       []string{server.URL},
		PublicKey:     base64.StdEncoding.EncodeToString(publicKey),
		AllowInsecure: true,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
}

func TestCheckUsesRootSignedReleaseKey(t *testing.T) {
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, releasePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest, manifestSignature := signedManifest(t, releasePrivate)
	var decoded Manifest
	if err := json.Unmarshal(manifest, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.SigningKeyID = "release-2026"
	manifest, err = json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	manifestSignature = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(releasePrivate, manifest)))
	releasePublic := releasePrivate.Public().(ed25519.PublicKey)
	keySet, err := json.Marshal(KeySet{SchemaVersion: ManifestSchemaVersion, Keys: []ReleaseKey{{ID: "release-2026", PublicKey: base64.StdEncoding.EncodeToString(releasePublic)}}})
	if err != nil {
		t.Fatal(err)
	}
	keySetSignature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(rootPrivate, keySet)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := map[string][]byte{"/latest.json": manifest, "/latest.json.sig": manifestSignature, "/keys.json": keySet, "/keys.json.sig": keySetSignature}
		if value, ok := values[r.URL.Path]; ok {
			_, _ = w.Write(value)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	result, err := Check(context.Background(), CheckConfig{Sources: []string{server.URL}, RootPublicKey: base64.StdEncoding.EncodeToString(rootPublic), AllowInsecure: true})
	if err != nil || result.Manifest.Version != "1.0.0" {
		t.Fatalf("Check() = %#v, %v", result, err)
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

// TestCheckRejectsMutableContainerImage pins the release rule that a container
// artifact is only addressable by digest. A tag can be moved after signing, so
// the digest is what makes the published image immutable.
func TestCheckRejectsMutableContainerImage(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmtHash(sha256.Sum256([]byte("core image")))
	serveManifest := func(image string) *httptest.Server {
		manifest := Manifest{
			SchemaVersion:           ManifestSchemaVersion,
			Channel:                 "stable",
			Version:                 "1.0.0",
			PublishedAt:             time.Now().UTC(),
			MinimumSupportedVersion: "1.0.0",
			ReleaseNotes:            []string{"First release"},
			Artifacts: []Artifact{{
				Kind:      "docker-core",
				Platform:  "linux-amd64",
				Image:     image,
				SHA256:    digest,
				SizeBytes: 42,
			}},
		}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, encoded))
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/latest.json":
				_, _ = w.Write(encoded)
			case "/latest.json.sig":
				_, _ = w.Write([]byte(signature))
			default:
				http.NotFound(w, r)
			}
		}))
	}
	config := func(source string) CheckConfig {
		return CheckConfig{
			Sources:       []string{source},
			PublicKey:     base64.StdEncoding.EncodeToString(publicKey),
			AllowInsecure: true,
		}
	}

	tagged := serveManifest("ghcr.io/visto/core:latest")
	defer tagged.Close()
	_, err = Check(context.Background(), config(tagged.URL))
	if err == nil || !strings.Contains(err.Error(), "immutable digest") {
		t.Fatalf("Check() error = %v, want rejection of a mutable image tag", err)
	}

	pinned := serveManifest("ghcr.io/visto/core@sha256:" + digest)
	defer pinned.Close()
	result, err := Check(context.Background(), config(pinned.URL))
	if err != nil {
		t.Fatalf("Check() error = %v, want the digest-pinned image to be accepted", err)
	}
	if len(result.Manifest.Artifacts) != 1 || result.Manifest.Artifacts[0].Image == "" {
		t.Fatalf("digest-pinned image was dropped: %#v", result.Manifest.Artifacts)
	}
	if result.Manifest.Artifacts[0].URL != "" {
		t.Fatalf("a container artifact must not carry an HTTPS URL: %#v", result.Manifest.Artifacts[0])
	}
}

func signedManifest(t *testing.T, privateKey ed25519.PrivateKey) ([]byte, []byte) {
	t.Helper()
	hash := sha256.Sum256([]byte("server package"))
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Channel: "stable", Version: "1.0.0", PublishedAt: time.Now().UTC(), MinimumSupportedVersion: "1.0.0", ReleaseNotes: []string{"First release"}, Artifacts: []Artifact{{Kind: "windows-server", Platform: "windows-amd64", URL: "https://example.test/server.zip", SHA256: fmtHash(hash), SizeBytes: 42}}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return encoded, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, encoded)))
}

func fmtHash(hash [32]byte) string { return fmt.Sprintf("%x", hash) }
