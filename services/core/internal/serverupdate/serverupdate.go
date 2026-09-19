// Package serverupdate verifies the signed public update manifest used by a
// self-hosted Visto Server. It deliberately has no dependency on Desktop.
package serverupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

type Manifest struct {
	SchemaVersion           int        `json:"schemaVersion"`
	Channel                 string     `json:"channel"`
	Version                 string     `json:"version"`
	PublishedAt             time.Time  `json:"publishedAt"`
	MinimumSupportedVersion string     `json:"minimumSupportedVersion"`
	ReleaseNotes            []string   `json:"releaseNotes"`
	Artifacts               []Artifact `json:"artifacts"`
	SigningKeyID            string     `json:"signingKeyId,omitempty"`
}

type KeySet struct {
	SchemaVersion int          `json:"schemaVersion"`
	Keys          []ReleaseKey `json:"keys"`
}

type ReleaseKey struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
}

type Artifact struct {
	Kind     string `json:"kind"`
	Platform string `json:"platform"`
	URL      string `json:"url"`
	// Image carries the immutable OCI reference for container artifacts, for
	// example "ghcr.io/visto/core@sha256:<digest>". Container images are not
	// addressable over HTTPS, so they cannot reuse URL.
	Image     string `json:"image,omitempty"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
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
	PublicKey     string
	RootPublicKey string
	HTTPClient    *http.Client
	AllowInsecure bool
}

type Result struct {
	Source   string   `json:"source"`
	Manifest Manifest `json:"manifest"`
}

type VerifyArtifactConfig struct {
	ManifestBytes  []byte
	SignatureBytes []byte
	PublicKey      string
	ArtifactPath   string
	Kind           string
	Platform       string
}

func VerifyArtifact(config VerifyArtifactConfig) (Artifact, error) {
	// a native package must be selected by an exact kind/platform
	// pair. An unknown platform or a kind from another operating system is a
	// hard failure; selecting another platform's package is never a fallback.
	if err := delivery.ValidateKindPlatform(config.Kind, config.Platform); err != nil {
		return Artifact{}, err
	}
	publicKey, err := decodePublicKey(config.PublicKey)
	if err != nil {
		return Artifact{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(config.SignatureBytes)))
	if err != nil || !ed25519.Verify(publicKey, config.ManifestBytes, signature) {
		return Artifact{}, errors.New("manifest signature verification failed")
	}
	var manifest Manifest
	if err := json.Unmarshal(config.ManifestBytes, &manifest); err != nil {
		return Artifact{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Artifact{}, err
	}
	var selected *Artifact
	for index := range manifest.Artifacts {
		artifact := &manifest.Artifacts[index]
		if artifact.Kind == config.Kind && artifact.Platform == config.Platform {
			if selected != nil {
				return Artifact{}, errors.New("manifest contains duplicate matching artifacts")
			}
			selected = artifact
		}
	}
	if selected == nil {
		return Artifact{}, errors.New("manifest does not contain the required artifact")
	}
	file, err := os.Open(config.ArtifactPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Artifact{}, fmt.Errorf("stat artifact: %w", err)
	}
	if info.Size() != selected.SizeBytes {
		return Artifact{}, errors.New("artifact size does not match signed manifest")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Artifact{}, fmt.Errorf("hash artifact: %w", err)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), selected.SHA256) {
		return Artifact{}, errors.New("artifact SHA-256 does not match signed manifest")
	}
	return *selected, nil
}

func Check(ctx context.Context, config CheckConfig) (Result, error) {
	publicKey, err := decodePublicKey(config.PublicKey)
	if err != nil && strings.TrimSpace(config.RootPublicKey) == "" {
		return Result{}, delivery.NewError(delivery.CodeVerificationFailed, err.Error())
	}
	var rootPublicKey ed25519.PublicKey
	if strings.TrimSpace(config.RootPublicKey) != "" {
		rootPublicKey, err = decodePublicKey(config.RootPublicKey)
		if err != nil {
			return Result{}, delivery.NewError(delivery.CodeVerificationFailed, fmt.Sprintf("update root public key: %v", err))
		}
	}
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
		signature, fetchErr := fetch(ctx, client, base+"/latest.json.sig")
		if fetchErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", base, fetchErr))
			continue
		}
		if rootPublicKey != nil {
			keySet, keySetErr := fetchKeySet(ctx, client, base, rootPublicKey)
			if keySetErr != nil {
				var classified *delivery.Error
				if !errors.As(keySetErr, &classified) || classified.Code != delivery.CodeNetworkFailure {
					failureCode = delivery.CodeVerificationFailed
				}
				failures = append(failures, fmt.Sprintf("%s: %v", base, keySetErr))
				continue
			}
			var candidate Manifest
			if decodeErr := json.Unmarshal(manifestBytes, &candidate); decodeErr != nil {
				failureCode = delivery.CodeVerificationFailed
				failures = append(failures, fmt.Sprintf("%s: decode manifest: %v", base, decodeErr))
				continue
			}
			publicKey, keySetErr = keySet.key(candidate.SigningKeyID)
			if keySetErr != nil {
				var classified *delivery.Error
				if !errors.As(keySetErr, &classified) || classified.Code != delivery.CodeNetworkFailure {
					failureCode = delivery.CodeVerificationFailed
				}
				failures = append(failures, fmt.Sprintf("%s: %v", base, keySetErr))
				continue
			}
		}
		decodedSignature, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
		if decodeErr != nil || !ed25519.Verify(publicKey, manifestBytes, decodedSignature) {
			failureCode = delivery.CodeVerificationFailed
			failures = append(failures, fmt.Sprintf("%s: manifest signature verification failed", base))
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

	return Result{}, delivery.NewError(failureCode, fmt.Sprintf("no trusted update manifest was available: %s", strings.Join(failures, "; ")))
}

func fetchKeySet(ctx context.Context, client *http.Client, base string, rootPublicKey ed25519.PublicKey) (KeySet, error) {
	body, err := fetch(ctx, client, base+"/keys.json")
	if err != nil {
		return KeySet{}, delivery.NewError(delivery.CodeNetworkFailure, fmt.Sprintf("fetch trusted key set: %v", err))
	}
	signature, err := fetch(ctx, client, base+"/keys.json.sig")
	if err != nil {
		return KeySet{}, delivery.NewError(delivery.CodeNetworkFailure, fmt.Sprintf("fetch trusted key set signature: %v", err))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || !ed25519.Verify(rootPublicKey, body, decoded) {
		return KeySet{}, errors.New("trusted key set signature verification failed")
	}
	var keySet KeySet
	if err := json.Unmarshal(body, &keySet); err != nil {
		return KeySet{}, fmt.Errorf("decode trusted key set: %w", err)
	}
	if keySet.SchemaVersion != ManifestSchemaVersion || len(keySet.Keys) == 0 {
		return KeySet{}, errors.New("trusted key set is invalid")
	}
	return keySet, nil
}

func (keySet KeySet) key(id string) (ed25519.PublicKey, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("manifest does not declare a signing key ID")
	}
	for _, key := range keySet.Keys {
		if key.ID == id {
			return decodePublicKey(key.PublicKey)
		}
	}
	return nil, fmt.Errorf("manifest signing key %q is not trusted", id)
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

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported manifest schema %d", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.Channel) != "stable" || strings.TrimSpace(manifest.Version) == "" {
		return errors.New("manifest must describe a stable version")
	}
	if manifest.PublishedAt.IsZero() || len(manifest.Artifacts) == 0 {
		return errors.New("manifest is missing release metadata or artifacts")
	}
	for _, artifact := range manifest.Artifacts {
		if strings.TrimSpace(artifact.Kind) == "" || strings.TrimSpace(artifact.Platform) == "" || artifact.SizeBytes <= 0 {
			return errors.New("manifest contains an incomplete artifact")
		}
		if len(artifact.SHA256) != 64 {
			return errors.New("manifest contains an invalid SHA-256")
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return errors.New("manifest contains an invalid SHA-256")
		}
		image := strings.TrimSpace(artifact.Image)
		if image != "" {
			// Container artifacts are addressed by immutable image reference,
			// not by HTTPS URL. Mutable tags would let a release silently move.
			if !strings.Contains(image, "@sha256:") {
				return errors.New("manifest contains a container image without an immutable digest")
			}
			continue
		}
		artifactURL, err := url.Parse(strings.TrimSpace(artifact.URL))
		if err != nil || !artifactURL.IsAbs() || artifactURL.Host == "" || artifactURL.Scheme != "https" || artifactURL.User != nil {
			return errors.New("manifest contains an invalid artifact URL")
		}
	}
	return nil
}

// VerifyDetachedSignature verifies a bounded caller-owned document using the
// same administrator-pinned Ed25519 release key as Server update packages.
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
