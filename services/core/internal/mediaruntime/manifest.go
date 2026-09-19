// Package mediaruntime manages host-local, independently signed FFmpeg runtimes.
// No HTTP API exposes its mutation methods.
package mediaruntime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"review-studio.local/core/internal/delivery"
	"review-studio.local/core/internal/serverupdate"
)

const MaxArchiveBytes int64 = 512 << 20
const MaxExpandedBytes int64 = 1536 << 20
const MaxManifestBytes = 64 << 10

var versionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

type Manifest struct {
	SchemaVersion        int    `json:"schemaVersion"`
	RuntimeVersion       string `json:"runtimeVersion"`
	Platform             string `json:"platform"`
	URL                  string `json:"url"`
	Size                 int64  `json:"size"`
	SHA256               string `json:"sha256"`
	License              string `json:"license"`
	SourceURL            string `json:"sourceUrl"`
	BuildRecord          string `json:"buildRecord"`
	MinimumServerVersion string `json:"minimumServerVersion"`
}

func bad(message string) error { return delivery.NewError(delivery.CodeVerificationFailed, message) }
func runtimeError(message string) error {
	return delivery.NewError(delivery.CodeRuntimeUnavailable, message)
}

// VerifyManifest authenticates raw bytes before decoding any executable policy.
func VerifyManifest(body, signature []byte, key, platform, serverVersion string) (Manifest, error) {
	var m Manifest
	if len(body) > MaxManifestBytes || len(signature) > 1024 {
		return m, bad("runtime manifest exceeds limit")
	}
	if err := serverupdate.VerifyDetachedSignature(body, signature, key); err != nil {
		return m, err
	}
	// Decoder rejects unknown fields; token pass also rejects duplicate keys.
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return m, bad("runtime manifest must be an object")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return m, bad("invalid manifest")
		}
		k, ok := token.(string)
		if !ok || seen[k] {
			return m, bad("duplicate manifest field")
		}
		seen[k] = true
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return m, bad("invalid manifest field")
		}
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return m, bad("invalid runtime manifest schema")
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return m, bad("trailing runtime manifest data")
	}
	if m.SchemaVersion != 1 || !versionName.MatchString(m.RuntimeVersion) || strings.Contains(m.RuntimeVersion, "..") {
		return m, bad("invalid runtime version or schema")
	}
	if _, ok := delivery.PlatformByID(m.Platform); !ok || m.Platform != platform {
		return m, delivery.NewError(delivery.CodeUnsupportedPlatform, "runtime platform does not match this host")
	}
	if m.Size <= 0 || m.Size > MaxArchiveBytes || !validHash(m.SHA256) {
		return m, bad("invalid runtime size or SHA-256")
	}
	if m.License != "LGPL-2.1-or-later" && m.License != "LGPL-3.0-or-later" {
		return m, bad("managed runtime must use audited LGPL")
	}
	if m.BuildRecord != "FFmpeg-BUILD.txt" {
		return m, bad("unsupported build record path")
	}
	if err = payloadURL(m.URL, false); err != nil {
		return m, err
	}
	source, err := url.Parse(m.SourceURL)
	if err != nil || source.Scheme != "https" || source.Hostname() == "" || source.User != nil || source.Fragment != "" {
		return m, bad("source URL must be HTTPS without credentials")
	}
	if !supportsVersion(serverVersion, m.MinimumServerVersion) {
		return m, bad("runtime requires a newer or known Server version")
	}
	return m, nil
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func supportsVersion(current, minimum string) bool {
	parse := func(s string) ([3]int, bool) {
		var r [3]int
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return r, false
		}
		for i, p := range parts {
			n, e := strconv.Atoi(p)
			if e != nil || n < 0 {
				return r, false
			}
			r[i] = n
		}
		return r, true
	}
	base, pre, _ := strings.Cut(current, "-")
	a, ok := parse(base)
	b, ok2 := parse(minimum)
	if !ok || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return pre == ""
}
func payloadURL(value string, redirect bool) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" {
		return bad("runtime URL must use trusted HTTPS")
	}
	if redirect && u.Host == "release-assets.githubusercontent.com" {
		return nil
	}
	if u.Host != "github.com" || u.RawQuery != "" {
		return bad("runtime payload host is not trusted")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" || parts[4] == "latest" || parts[4] == "" {
		return bad("runtime payload must use a versioned GitHub Release URL")
	}
	if strings.Contains(strings.ToLower(parts[4]), "latest") {
		return bad("rolling runtime release is forbidden")
	}
	return nil
}
func (m Manifest) archiveFormat() string {
	if strings.HasPrefix(m.Platform, "windows-") {
		return "zip"
	}
	return "tar.xz"
}
func describe(m Manifest) string {
	return fmt.Sprintf("%s (%s, %d bytes, %s)", m.RuntimeVersion, m.Platform, m.Size, m.License)
}
