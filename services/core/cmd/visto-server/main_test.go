package main

import (
	"bytes"
	"encoding/json"
	"io"
	"review-studio.local/core/internal/delivery"
	"testing"
)

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
		{"public key", []string{"--json", "--update-source", "https://example.test", "--update-public-key", "invalid", "update", "check"}, 8, "verification_failed"},
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
