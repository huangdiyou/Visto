package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"review-studio.local/core/internal/delivery"
	"testing"
)

// A digest mismatch must still be reported as a verification failure with the
// documented exit code, even though the free tier no longer verifies signatures.
func TestUpdateVerifyPackageReportsAVerificationFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "Visto-Server_1.0.0_linux-amd64.tar.gz")
	if err := os.WriteFile(path, []byte("package bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if got := run([]string{
		"--json", "update", "verify-package",
		"--artifact", path,
		"--kind", "linux-server",
		"--platform", "linux-amd64",
		"--sha256", strings.Repeat("a", 64),
	}, &out, &errOut); got != 8 {
		t.Fatalf("exit=%d want=8: %s", got, errOut.String())
	}
	var envelope delivery.ErrorEnvelope
	if err := json.NewDecoder(&out).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "verification_failed" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
}

func TestJSONFailures(t *testing.T) {
	cases := []struct {
		name string
		args []string
		exit int
		code string
	}{
		{"global flag", []string{"--json", "--bogus"}, 2, "invalid_arguments"},
		{"missing command", []string{"--json"}, 2, "invalid_arguments"},
		{"unknown command", []string{"--json", "bogus"}, 2, "invalid_arguments"},
		{"update flag", []string{"--json", "update", "verify-package", "--bogus"}, 2, "invalid_arguments"},
		{"diagnostic flag", []string{"--json", "diagnostics", "export", "--bogus"}, 2, "invalid_arguments"},
		{"config report", []string{"--json", "--address", "bad-address", "config", "validate"}, 3, "config_or_health"},
		{"platform", []string{"--json", "update", "verify-package", "--kind", "linux-server", "--platform", "macos-arm64"}, 5, "unsupported_platform"},
		// The free tier has no release public key flag any more; a malformed
		// expected digest is the remaining argument-level failure on this path.
		{"package digest", []string{"--json", "update", "verify-package", "--artifact", "/absent/package.tar.gz", "--kind", "linux-server", "--platform", "linux-amd64", "--sha256", "short"}, 2, "invalid_arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(tc.args, &out, &errOut); got != tc.exit {
				t.Fatalf("exit=%d want=%d: %s", got, tc.exit, errOut.String())
			}
			var envelope delivery.ErrorEnvelope
			decoder := json.NewDecoder(&out)
			if err := decoder.Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.OK || envelope.SchemaVersion != 1 || envelope.Error == nil || envelope.Error.Code != tc.code {
				t.Fatalf("unexpected envelope: %#v", envelope)
			}
			if tc.exit == 3 && envelope.Report == nil {
				t.Fatal("failed report was discarded")
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("extra stdout: %v", err)
			}
		})
	}
}

func TestTextFailureAndJSONSuccess(t *testing.T) {
	var out, errOut bytes.Buffer
	if run([]string{"update", "verify-package", "--bogus"}, &out, &errOut) != 2 || out.Len() != 0 || errOut.Len() == 0 {
		t.Fatal("text error must remain on stderr")
	}
	out.Reset()
	errOut.Reset()
	if run([]string{"--json", "version"}, &out, &errOut) != 0 {
		t.Fatal("version failed")
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil || result["name"] != "Visto Server" || result["error"] != nil {
		t.Fatal(out.String())
	}
}

func TestRuntimeCLIRejectsConflictingModes(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "media-runtime", "install", "--media-runtime", "system", "--media-runtime-package", "archive.zip", "--non-interactive"},
		{"--json", "media-runtime", "install", "--media-runtime", "custom", "--non-interactive"},
		{"--json", "media-runtime", "install", "--media-runtime-path", "/path", "--media-runtime-package", "archive.zip"},
		{"--json", "media-runtime", "unknown"},
	} {
		var out, diagnostics bytes.Buffer
		if exit := run(args, &out, &diagnostics); exit != 2 {
			t.Fatalf("got exit %d: %s", exit, out.String())
		}
		var envelope delivery.ErrorEnvelope
		if json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Error == nil || envelope.Error.Code != "invalid_arguments" {
			t.Fatal(out.String())
		}
	}
}
