package hostctl

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAddress = "127.0.0.1:8787"
	DefaultDataDir = "data"
)

type Config struct {
	Version          string
	Address          string
	DataDir          string
	HTTPClient       *http.Client
	LookupExecutable func(string) (string, error)
}

type Report struct {
	Command string  `json:"command"`
	Status  string  `json:"status"`
	Version string  `json:"version"`
	Address string  `json:"address,omitempty"`
	DataDir string  `json:"dataDir,omitempty"`
	Checks  []Check `json:"checks"`
}

type Check struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Optional bool   `json:"optional,omitempty"`
}

type diagnosticsPayload struct {
	GeneratedAt string            `json:"generatedAt"`
	Report      diagnosticsReport `json:"report"`
}

type diagnosticsReport struct {
	Command string             `json:"command"`
	Status  string             `json:"status"`
	Version string             `json:"version"`
	Port    int                `json:"port,omitempty"`
	Checks  []diagnosticsCheck `json:"checks"`
}

type diagnosticsCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Optional bool   `json:"optional,omitempty"`
}

func ConfigFromEnvironment(version string) Config {
	address := strings.TrimSpace(os.Getenv("REVIEW_STUDIO_ADDR"))
	if address == "" {
		address = DefaultAddress
	}

	dataDir := strings.TrimSpace(os.Getenv("REVIEW_STUDIO_DATA_DIR"))
	if dataDir == "" {
		dataDir = DefaultDataDir
	}

	return Config{
		Version:          version,
		Address:          address,
		DataDir:          dataDir,
		HTTPClient:       &http.Client{Timeout: 3 * time.Second},
		LookupExecutable: exec.LookPath,
	}
}

func (config Config) normalized() Config {
	if strings.TrimSpace(config.Version) == "" {
		config.Version = "dev"
	}
	if strings.TrimSpace(config.Address) == "" {
		config.Address = DefaultAddress
	}
	if strings.TrimSpace(config.DataDir) == "" {
		config.DataDir = DefaultDataDir
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 3 * time.Second}
	}
	if config.LookupExecutable == nil {
		config.LookupExecutable = exec.LookPath
	}

	return config
}

func Validate(config Config) Report {
	config = config.normalized()
	report := baseReport("config validate", config)

	if _, _, err := splitAddress(config.Address); err != nil {
		report.Checks = append(report.Checks, failure("listen_address", err.Error()))
	} else {
		report.Checks = append(report.Checks, success("listen_address", config.Address))
	}

	absDataDir, err := filepath.Abs(config.DataDir)
	if err != nil {
		report.Checks = append(report.Checks, failure("data_directory", err.Error()))
	} else {
		report.DataDir = absDataDir
		info, statErr := os.Stat(absDataDir)
		switch {
		case statErr == nil && !info.IsDir():
			report.Checks = append(report.Checks, failure("data_directory", "path exists but is not a directory"))
		case statErr == nil:
			report.Checks = append(report.Checks, success("data_directory", "existing directory"))
		case errors.Is(statErr, os.ErrNotExist):
			parent := filepath.Dir(absDataDir)
			if parentInfo, parentErr := os.Stat(parent); parentErr != nil || !parentInfo.IsDir() {
				detail := "parent directory is unavailable"
				if parentErr != nil {
					detail = parentErr.Error()
				}
				report.Checks = append(report.Checks, failure("data_directory", detail))
			} else {
				report.Checks = append(report.Checks, success("data_directory", "directory will be created on first start"))
			}
		default:
			report.Checks = append(report.Checks, failure("data_directory", statErr.Error()))
		}
	}

	report.Status = reportStatus(report.Checks)
	return report
}

func Status(ctx context.Context, config Config) Report {
	config = config.normalized()
	report := baseReport("status", config)
	baseURL, err := localURL(config.Address)
	if err != nil {
		report.Checks = append(report.Checks, failure("core_live", err.Error()))
		report.Status = reportStatus(report.Checks)
		return report
	}

	for _, endpoint := range []struct {
		name string
		path string
	}{
		{name: "core_live", path: "/health/live"},
		{name: "core_ready", path: "/health/ready"},
	} {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+endpoint.path, nil)
		if requestErr != nil {
			report.Checks = append(report.Checks, failure(endpoint.name, requestErr.Error()))
			continue
		}

		response, responseErr := config.HTTPClient.Do(request)
		if responseErr != nil {
			report.Checks = append(report.Checks, failure(endpoint.name, responseErr.Error()))
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()

		if response.StatusCode != http.StatusOK {
			report.Checks = append(report.Checks, failure(endpoint.name, fmt.Sprintf("received HTTP %d", response.StatusCode)))
			continue
		}

		report.Checks = append(report.Checks, success(endpoint.name, "HTTP 200"))
	}

	report.Status = reportStatus(report.Checks)
	return report
}

func Doctor(ctx context.Context, config Config) Report {
	config = config.normalized()
	report := Validate(config)
	report.Command = "doctor"

	absDataDir, err := filepath.Abs(config.DataDir)
	if err == nil {
		if writable, detail := directoryWritable(absDataDir); writable {
			report.Checks = append(report.Checks, success("data_directory_writable", detail))
		} else {
			report.Checks = append(report.Checks, failure("data_directory_writable", detail))
		}
	}

	for _, executable := range []string{"ffmpeg", "ffprobe"} {
		if path, lookupErr := config.LookupExecutable(executable); lookupErr == nil {
			report.Checks = append(report.Checks, success(executable, path))
		} else {
			report.Checks = append(report.Checks, Check{
				Name:     executable,
				Status:   "warning",
				Detail:   "not found; media preview processing is unavailable",
				Optional: true,
			})
		}
	}

	status := Status(ctx, config)
	for _, check := range status.Checks {
		if check.Status == "failed" {
			check.Optional = true
			check.Status = "warning"
			check.Detail = "core is not currently reachable: " + check.Detail
		}
		report.Checks = append(report.Checks, check)
	}

	report.Status = reportStatus(report.Checks)
	return report
}

func ExportDiagnostics(ctx context.Context, config Config, outputPath string) (Report, string, error) {
	config = config.normalized()
	report := Doctor(ctx, config)
	if strings.TrimSpace(outputPath) == "" {
		outputPath = fmt.Sprintf("visto-diagnostics-%s.zip", time.Now().Format("20060102-150405"))
	}

	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return report, "", fmt.Errorf("resolve diagnostics output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absOutputPath), 0o700); err != nil {
		return report, "", fmt.Errorf("create diagnostics directory: %w", err)
	}

	file, err := os.Create(absOutputPath)
	if err != nil {
		return report, "", fmt.Errorf("create diagnostics archive: %w", err)
	}

	archive := zip.NewWriter(file)
	payload, marshalErr := json.MarshalIndent(diagnosticsPayload{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Report:      sanitizeForDiagnostics(report),
	}, "", "  ")
	if marshalErr == nil {
		entry, entryErr := archive.Create("diagnostics.json")
		if entryErr == nil {
			_, marshalErr = entry.Write(payload)
		} else {
			marshalErr = entryErr
		}
	}
	closeErr := archive.Close()
	fileCloseErr := file.Close()
	if marshalErr != nil {
		_ = os.Remove(absOutputPath)
		return report, "", fmt.Errorf("write diagnostics archive: %w", marshalErr)
	}
	if closeErr != nil {
		_ = os.Remove(absOutputPath)
		return report, "", fmt.Errorf("close diagnostics archive: %w", closeErr)
	}
	if fileCloseErr != nil {
		return report, "", fmt.Errorf("close diagnostics file: %w", fileCloseErr)
	}

	return report, absOutputPath, nil
}

func baseReport(command string, config Config) Report {
	return Report{
		Command: command,
		Status:  "ok",
		Version: config.Version,
		Address: config.Address,
		DataDir: config.DataDir,
		Checks:  make([]Check, 0),
	}
}

func splitAddress(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if strings.TrimSpace(host) == "" {
		return "", 0, errors.New("listen host is empty")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid listen port %q", portText)
	}

	return host, port, nil
}

func localURL(address string) (string, error) {
	host, port, err := splitAddress(address)
	if err != nil {
		return "", err
	}
	switch host {
	case "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}

	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func directoryWritable(path string) (bool, string) {
	directory := path
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		directory = filepath.Dir(directory)
	}

	temporaryFile, err := os.CreateTemp(directory, ".visto-write-check-*")
	if err != nil {
		return false, err.Error()
	}
	name := temporaryFile.Name()
	if closeErr := temporaryFile.Close(); closeErr != nil {
		_ = os.Remove(name)
		return false, closeErr.Error()
	}
	if removeErr := os.Remove(name); removeErr != nil {
		return false, removeErr.Error()
	}

	return true, "write check passed"
}

func success(name, detail string) Check {
	return Check{Name: name, Status: "ok", Detail: detail}
}

func failure(name, detail string) Check {
	return Check{Name: name, Status: "failed", Detail: detail}
}

func reportStatus(checks []Check) string {
	for _, check := range checks {
		if check.Status == "failed" {
			return "failed"
		}
	}
	for _, check := range checks {
		if check.Status == "warning" {
			return "warning"
		}
	}

	return "ok"
}

func sanitizeForDiagnostics(report Report) diagnosticsReport {
	_, port, _ := splitAddress(report.Address)
	checks := make([]diagnosticsCheck, 0, len(report.Checks))
	for _, check := range report.Checks {
		checks = append(checks, diagnosticsCheck{
			Name:     check.Name,
			Status:   check.Status,
			Optional: check.Optional,
		})
	}

	return diagnosticsReport{
		Command: report.Command,
		Status:  report.Status,
		Version: report.Version,
		Port:    port,
		Checks:  checks,
	}
}
