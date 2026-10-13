// Package serverupdate reads the public update manifest used by a self-hosted
// Visto Server. It deliberately has no dependency on Desktop.
//
// The free tier channel is unsigned by decision (docs/FREE_TIER_BOUNDARY_DESIGN.md):
// the manifest is a version announcement only. It carries no artifact
// descriptors, no download location and no free text, and this package performs
// no release signature verification. An unsigned channel must not be able to
// name a download location, otherwise whoever can rewrite the source also
// chooses what gets installed.
//
// Media runtime manifests are a separate trust chain with their own pinned key;
// VerifyDetachedSignature stays for that caller.
package serverupdate

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"review-studio.local/core/internal/delivery"
)

const ManifestSchemaVersion = 1

// Manifest is the free tier version announcement: four fields, no artifact
// descriptors, no download location and no free text. The absence of a download
// field is deliberate — see the package comment.
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	Channel       string    `json:"channel"`
	Version       string    `json:"version"`
	PublishedAt   time.Time `json:"publishedAt"`
}

// Deployment kinds reported to the Owner update page. The page only explains
// what an administrator must run; the value never selects executable behaviour.
const (
	DeploymentDocker        = "docker"
	DeploymentWindowsServer = "windows-server"
	DeploymentLinuxServer   = "linux-server"
	DeploymentMacOSServer   = "macos-server"
	DeploymentSource        = "source"
)

// Deployment prefixes from the native package contract. Their presence is what
// distinguishes a managed native install from a source checkout on Linux and
// macOS, where Core itself cannot tell the two apart.
const (
	linuxServerPrefix = "/opt/visto/current"
	macOSServerPrefix = "/Library/Visto/current"
)

// DetectDeploymentKind reports how this Core instance is deployed. Operators
// can pin the answer with VISTO_SERVER_DEPLOYMENT_KIND; otherwise a container is
// detected from /.dockerenv, a native Windows install from the runtime and a
// native Linux or macOS install from its release prefix. Anything else is a
// source deployment.
func DetectDeploymentKind(explicit string) string {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case DeploymentDocker:
		return DeploymentDocker
	case DeploymentWindowsServer:
		return DeploymentWindowsServer
	case DeploymentLinuxServer:
		return DeploymentLinuxServer
	case DeploymentMacOSServer:
		return DeploymentMacOSServer
	case DeploymentSource:
		return DeploymentSource
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return DeploymentDocker
	}
	if _, err := os.Stat(linuxServerPrefix); err == nil {
		return DeploymentLinuxServer
	}
	if _, err := os.Stat(macOSServerPrefix); err == nil {
		return DeploymentMacOSServer
	}
	if runtime.GOOS == "windows" {
		return DeploymentWindowsServer
	}
	return DeploymentSource
}

type CheckConfig struct {
	Sources       []string
	HTTPClient    *http.Client
	AllowInsecure bool
}

type Result struct {
	Source   string   `json:"source"`
	Manifest Manifest `json:"manifest"`
}

// Check returns the first usable manifest among the configured sources. It
// checks shape only: the free tier channel is unsigned, so a manifest that
// parses and validates is reported, and the version guard downstream decides
// whether it is worth acting on.
func Check(ctx context.Context, config CheckConfig) (Result, error) {
	client := updateHTTPClient(config.HTTPClient)
	if len(config.Sources) == 0 {
		return Result{}, delivery.NewError(delivery.CodeInvalidArguments, "no update sources configured")
	}

	var failures []string
	failureCode := delivery.CodeNetworkFailure
	for _, source := range config.Sources {
		base, normalizeErr := normalizeSource(source, config.AllowInsecure)
		if normalizeErr != nil {
			failureCode = delivery.CodeVerificationFailed
			failures = append(failures, normalizeErr.Error())
			continue
		}
		manifestBytes, fetchErr := fetch(ctx, client, base+"/latest.json")
		if fetchErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", base, fetchErr))
			continue
		}
		var manifest Manifest
		if decodeErr := json.Unmarshal(manifestBytes, &manifest); decodeErr != nil {
			failureCode = delivery.CodeVerificationFailed
			failures = append(failures, fmt.Sprintf("%s: decode manifest: %v", base, decodeErr))
			continue
		}
		if validateErr := validateManifest(manifest); validateErr != nil {
			failureCode = delivery.CodeVerificationFailed
			failures = append(failures, fmt.Sprintf("%s: %v", base, validateErr))
			continue
		}
		return Result{Source: base, Manifest: manifest}, nil
	}

	return Result{}, delivery.NewError(
		failureCode,
		fmt.Sprintf("no usable update manifest was available: %s", strings.Join(failures, "; ")),
	)
}

func updateHTTPClient(source *http.Client) *http.Client {
	if source == nil {
		source = &http.Client{Timeout: 10 * time.Second}
	}
	client := *source
	// Update redirects could silently switch to another host. Each configured
	// source is an explicit trust boundary, so a redirect is treated as failure.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("update public key must be a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

func normalizeSource(value string, allowInsecure bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("invalid update source %q", value)
	}
	if parsed.Scheme != "https" && !(allowInsecure && parsed.Scheme == "http") {
		return "", fmt.Errorf("update source must use HTTPS: %q", value)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func fetch(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 512*1024))
}

// validateManifest rejects a manifest the free tier cannot act on. There is no
// artifact or signature validation left: the channel carries neither, and the
// version guard is applied where the product decides whether to prompt.
func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported manifest schema %d", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.Channel) != "stable" || strings.TrimSpace(manifest.Version) == "" {
		return errors.New("manifest must describe a stable version")
	}
	if manifest.PublishedAt.IsZero() {
		return errors.New("manifest is missing its publication time")
	}
	return nil
}

// VerifyDetachedSignature verifies a bounded caller-owned document against an
// administrator-pinned Ed25519 public key.
//
// This is not used by the free tier Server update channel, which is unsigned.
// It exists for the media runtime trust chain (internal/mediaruntime), which
// keeps its own pinned key and is unaffected by the free tier boundary change.
func VerifyDetachedSignature(body, signature []byte, publicKey string) error {
	key, err := decodePublicKey(publicKey)
	if err != nil {
		return delivery.NewError(delivery.CodeVerificationFailed, err.Error())
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || !ed25519.Verify(key, body, sig) {
		return delivery.NewError(delivery.CodeVerificationFailed, "document signature verification failed")
	}
	return nil
}
