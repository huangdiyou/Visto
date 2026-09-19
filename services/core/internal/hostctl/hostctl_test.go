package hostctl

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsInvalidAddress(t *testing.T) {
	report := Validate(Config{Address: "not-an-address", DataDir: t.TempDir()})
	if report.Status != "failed" {
		t.Fatalf("status = %q, want failed", report.Status)
	}
	if report.Checks[0].Name != "listen_address" || report.Checks[0].Status != "failed" {
		t.Fatalf("listen address check = %#v", report.Checks[0])
	}
}

func TestStatusUsesLocalHealthEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health/live" && request.URL.Path != "/health/ready" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	report := Status(context.Background(), Config{Address: server.Listener.Addr().String(), DataDir: t.TempDir()})
	if report.Status != "ok" {
		t.Fatalf("status = %q, checks = %#v", report.Status, report.Checks)
	}
	if len(report.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(report.Checks))
	}
}

func TestDoctorReportsMissingMediaToolsAsWarnings(t *testing.T) {
	report := Doctor(context.Background(), Config{
		Address: "127.0.0.1:1",
		DataDir: t.TempDir(),
		LookupExecutable: func(string) (string, error) {
			return "", errors.New("not found")
		},
	})

	if report.Status != "warning" {
		t.Fatalf("status = %q, checks = %#v", report.Status, report.Checks)
	}
	if !containsCheck(report.Checks, "ffmpeg", "warning") {
		t.Fatalf("missing ffmpeg warning: %#v", report.Checks)
	}
}

func TestExportDiagnosticsCreatesSanitizedArchive(t *testing.T) {
	dataDir := `Z:\Customer Media\Private Workspace`
	secretFragments := []string{
		dataDir,
		`\\nas01\finance\visto.db`,
		"webhook-token-should-not-leak",
		"https://user:password@example.com/dav?token=secret",
	}
	outputPath := filepath.Join(t.TempDir(), "diagnostics.zip")
	_, actualOutputPath, err := ExportDiagnostics(context.Background(), Config{
		Address: "0.0.0.0:8787",
		DataDir: dataDir,
		LookupExecutable: func(string) (string, error) {
			return secretFragments[1] + " " + secretFragments[2] + " " + secretFragments[3], nil
		},
	}, outputPath)
	if err != nil {
		t.Fatalf("ExportDiagnostics() error = %v", err)
	}
	info, err := os.Stat(actualOutputPath)
	if err != nil {
		t.Fatalf("diagnostics archive stat error = %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("diagnostics archive is empty")
	}
	archive, err := zip.OpenReader(actualOutputPath)
	if err != nil {
		t.Fatalf("open diagnostics archive: %v", err)
	}
	defer archive.Close()
	if len(archive.File) != 1 || archive.File[0].Name != "diagnostics.json" {
		t.Fatalf("archive entries = %#v", archive.File)
	}
	entry, err := archive.File[0].Open()
	if err != nil {
		t.Fatalf("open diagnostics entry: %v", err)
	}
	defer entry.Close()
	var payload diagnosticsPayload
	if err := json.NewDecoder(entry).Decode(&payload); err != nil {
		t.Fatalf("decode diagnostics payload: %v", err)
	}
	if payload.Report.Port != 8787 {
		t.Fatalf("diagnostics port = %d, want 8787", payload.Report.Port)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode diagnostics payload: %v", err)
	}
	for _, fragment := range secretFragments {
		if strings.Contains(string(encoded), fragment) {
			t.Fatalf("diagnostics payload leaks %q: %s", fragment, encoded)
		}
	}
	if strings.Contains(string(encoded), "0.0.0.0") {
		t.Fatalf("diagnostics payload leaks listen address: %s", encoded)
	}
}

func containsCheck(checks []Check, name, status string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}

	return false
}
