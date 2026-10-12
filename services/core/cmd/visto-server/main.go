package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"review-studio.local/core/internal/delivery"
	"review-studio.local/core/internal/hostctl"
	"review-studio.local/core/internal/mediaruntime"
	"review-studio.local/core/internal/serverupdate"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) (exitCode int) {
	originalOut, originalErr := stdout, stderr
	var output, diagnostics bytes.Buffer
	stdout, stderr = &output, &diagnostics
	global := flag.NewFlagSet("visto-server", flag.ContinueOnError)
	global.SetOutput(stderr)
	address := global.String("address", "", "Core listen address")
	dataDir := global.String("data-dir", "", "Core data directory")
	updateSource := global.String("update-source", "", "stable update manifest source")
	jsonOutput := global.Bool("json", false, "print JSON")
	defer func() {
		if *jsonOutput && exitCode != 0 {
			var existing delivery.ErrorEnvelope
			if json.Unmarshal(output.Bytes(), &existing) != nil || existing.Error == nil {
				code := delivery.CodeInternalError
				if exitCode == 2 {
					code = delivery.CodeInvalidArguments
				}
				if exitCode == 3 {
					code = delivery.CodeConfigOrHealth
				}
				message := strings.TrimSpace(diagnostics.String())
				if exitCode == delivery.ExitRestorePrecheckFailed {
					code = delivery.CodeRestorePrecheckFailed
					message = "restore precheck rejected the backup; the full verdict is in report"
				}
				if message == "" {
					message = "command failed; see report for failed checks"
				}
				envelope := delivery.NewErrorEnvelope(commandName(global.Args()), delivery.NewError(code, message))
				// Keep failed health/configuration checks available to local automation.
				var report any
				if json.Unmarshal(output.Bytes(), &report) == nil {
					envelope.Report = report
				}
				output.Reset()
				if err := json.NewEncoder(&output).Encode(envelope); err != nil {
					exitCode = delivery.ExitInternal
				}
			}
		}
		_, _ = originalOut.Write(output.Bytes())
		_, _ = originalErr.Write(diagnostics.Bytes())
	}()

	global.Usage = func() { printUsage(stderr) }
	if err := global.Parse(arguments); err != nil {
		return 2
	}

	remaining := global.Args()
	if len(remaining) == 0 {
		printUsage(stderr)
		return 2
	}

	config := hostctl.ConfigFromEnvironment(version)
	if strings.TrimSpace(*address) != "" {
		config.Address = *address
	}
	if strings.TrimSpace(*dataDir) != "" {
		config.DataDir = *dataDir
	}

	command := remaining[0]
	switch command {
	case "media-runtime":
		return runMediaRuntime(remaining[1:], *jsonOutput, config.Version, stdout, stderr, originalErr)
	case "backup":
		return runBackup(remaining[1:], *jsonOutput, config, stdout, stderr)
	case "version":
		return writeResult(stdout, *jsonOutput, map[string]string{
			"name":    "Visto Server",
			"version": config.Version,
		}, 0)
	case "config":
		if len(remaining) != 2 || remaining[1] != "validate" {
			printUsage(stderr)
			return 2
		}
		report := hostctl.Validate(config)
		return writeReport(stdout, *jsonOutput, report)
	case "status":
		report := hostctl.Status(context.Background(), config)
		return writeReport(stdout, *jsonOutput, report)
	case "doctor":
		selection, err := (mediaruntime.Manager{Root: mediaruntime.DefaultRoot(), ServerVersion: version}).Resolve()
		if err == nil {
			config.LookupExecutable = func(name string) (string, error) {
				switch name {
				case "ffmpeg":
					return selection.Probe.FFmpeg, nil
				case "ffprobe":
					return selection.Probe.FFprobe, nil
				}
				return "", fmt.Errorf("unknown media executable")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(stdout, stderr, *jsonOutput, "doctor", mediaruntime.Classify(err))
		}
		report := hostctl.Doctor(context.Background(), config)
		return writeReport(stdout, *jsonOutput, report)
	case "diagnostics":
		if len(remaining) < 2 || remaining[1] != "export" {
			printUsage(stderr)
			return 2
		}
		diagnosticsFlags := flag.NewFlagSet("visto-server diagnostics export", flag.ContinueOnError)
		diagnosticsFlags.SetOutput(stderr)
		outputPath := diagnosticsFlags.String("output", "", "diagnostics zip output path")
		if err := diagnosticsFlags.Parse(remaining[2:]); err != nil {
			return 2
		}
		report, actualOutputPath, err := hostctl.ExportDiagnostics(context.Background(), config, *outputPath)
		if err != nil {
			fmt.Fprintf(stderr, "diagnostics export failed: %v\n", err)
			return 1
		}
		result := map[string]any{"output": actualOutputPath, "report": report}
		return writeResult(stdout, *jsonOutput, result, reportExitCode(report))
	case "update":
		if len(remaining) >= 2 && remaining[1] == "verify-package" {
			verifyFlags := flag.NewFlagSet("visto-server update verify-package", flag.ContinueOnError)
			verifyFlags.SetOutput(stderr)
			artifactPath := verifyFlags.String("artifact", "", "downloaded package path")
			kind := verifyFlags.String("kind", "", "artifact kind")
			platform := verifyFlags.String("platform", "", "artifact platform")
			expectedSHA256 := verifyFlags.String("sha256", "", "expected SHA-256 from the published .sha256 sidecar")
			expectedSize := verifyFlags.Int64("size", 0, "optional expected size in bytes")
			if err := verifyFlags.Parse(remaining[2:]); err != nil {
				return 2
			}
			verified, err := serverupdate.VerifyPackage(serverupdate.VerifyPackageConfig{
				ArtifactPath:   *artifactPath,
				Kind:           *kind,
				Platform:       *platform,
				ExpectedSHA256: *expectedSHA256,
				ExpectedSize:   *expectedSize,
			})
			if err != nil {
				return fail(stdout, stderr, *jsonOutput, "update verify-package",
					classify(err, delivery.CodeVerificationFailed))
			}
			return writeResult(stdout, *jsonOutput, verified, 0)
		}
		if len(remaining) != 2 || remaining[1] != "check" {
			printUsage(stderr)
			return 2
		}
		sources := sourcesFromEnvironment(*updateSource)
		if len(sources) == 0 {
			return fail(stdout, stderr, *jsonOutput, "update check",
				delivery.NewError(delivery.CodeInvalidArguments, "no HTTPS update source was configured"))
		}
		result, err := serverupdate.Check(context.Background(), serverupdate.CheckConfig{
			Sources: sources,
		})
		if err != nil {
			return fail(stdout, stderr, *jsonOutput, "update check",
				classify(err, delivery.CodeNetworkFailure))
		}
		return writeResult(stdout, *jsonOutput, result, 0)
	default:
		printUsage(stderr)
		return 2
	}
}

func sourcesFromEnvironment(explicit string) []string {
	value := strings.TrimSpace(explicit)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("VISTO_SERVER_UPDATE_SOURCES"))
	}
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ";")
	sources := make([]string, 0, len(parts))
	for _, part := range parts {
		if source := strings.TrimSpace(part); source != "" {
			sources = append(sources, source)
		}
	}
	return sources
}

func writeReport(writer io.Writer, jsonOutput bool, report hostctl.Report) int {
	return writeResult(writer, jsonOutput, report, reportExitCode(report))
}

func writeResult(writer io.Writer, jsonOutput bool, value any, exitCode int) int {
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return 1
		}
		return exitCode
	}

	switch typed := value.(type) {
	case hostctl.Report:
		fmt.Fprintf(writer, "%s: %s\n", typed.Command, typed.Status)
		fmt.Fprintf(writer, "version: %s\n", typed.Version)
		if typed.Address != "" {
			fmt.Fprintf(writer, "address: %s\n", typed.Address)
		}
		if typed.DataDir != "" {
			fmt.Fprintf(writer, "data directory: %s\n", typed.DataDir)
		}
		for _, check := range typed.Checks {
			fmt.Fprintf(writer, "- [%s] %s: %s\n", check.Status, check.Name, check.Detail)
		}
	default:
		if encoded, err := json.Marshal(value); err == nil {
			fmt.Fprintln(writer, string(encoded))
		} else {
			fmt.Fprintln(writer, value)
		}
	}

	return exitCode
}

// classify keeps an already classified failure and wraps anything else in the
// fallback code. Commands must never turn an unclassified failure into a
// success, and never report a failure class that hides the real cause.
func classify(err error, fallbackCode string) *delivery.Error {
	var classified *delivery.Error
	if errors.As(err, &classified) {
		return classified
	}
	return delivery.NewError(fallbackCode, err.Error())
}

// fail reports a classified failure. With --json the single JSON object on
// stdout is the failure envelope; without it the message goes to stderr and
// stdout keeps carrying command output only.
func fail(stdout, stderr io.Writer, jsonOutput bool, command string, err error) int {
	classified := classify(err, delivery.CodeInternalError)
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(delivery.NewErrorEnvelope(command, classified)); encodeErr != nil {
			fmt.Fprintf(stderr, "%s failed: %s\n", command, classified.Message)
			return delivery.ExitInternal
		}
		return classified.ExitCode
	}
	fmt.Fprintf(stderr, "%s failed: %s\n", command, classified.Message)
	return classified.ExitCode
}

func reportExitCode(report hostctl.Report) int {
	if report.Status == "failed" {
		return 3
	}

	return 0
}

func printUsage(writer io.Writer) {
	fmt.Fprint(writer, `Visto Server host management

Usage:
  visto-server [--json] media-runtime inspect|status|install|rollback [--media-runtime managed|system|custom] [--media-runtime-path DIR] [--media-runtime-package FILE] [--manifest FILE] [--signature FILE] [--non-interactive] [--confirm-download] [--confirm-rollback]
  visto-server [--address ADDRESS] [--data-dir PATH] [--json] version
  visto-server [--address ADDRESS] [--data-dir PATH] [--json] config validate
  visto-server [--address ADDRESS] [--data-dir PATH] [--json] status
  visto-server [--address ADDRESS] [--data-dir PATH] [--json] doctor
  visto-server [--address ADDRESS] [--data-dir PATH] [--json] diagnostics export [--output PATH]
  visto-server [--update-source HTTPS_URL] [--json] update check
  visto-server [--json] update verify-package --artifact PATH --kind KIND --platform PLATFORM --sha256 HEX [--size BYTES]
  visto-server [--json] backup metadata --data-dir PATH --archive NAME --sha256 HEX [--output PATH] [--archive-root NAME | --no-archive-root] [--compose-project NAME] [--data-volume NAME]
  visto-server [--json] backup precheck --metadata PATH --staged-data-dir PATH --target-data-dir PATH

Exit codes:
  0    command completed; doctor warnings are included in the report
  1    execution failed without a more specific class
  2    invalid command, flag, missing confirmation or no update source
  3    configuration or running Core health check failed
  4    the action requires an administrator
  5    the host or the requested platform/architecture is not supported
  6    no usable media runtime
  7    network, DNS, TLS, proxy or offline source failure
  8    signature, SHA-256, size or archive layout verification failed
  9    not enough disk space or an archive exceeded an expansion limit
  10   service install, start, stop or restart failed
  11   restore precheck rejected the backup; current data was not changed
  130  the administrator cancelled the operation

With --json a failing command writes exactly one JSON error object to stdout:
{"schemaVersion":1,"ok":false,"command":"...","error":{"code":"...","message":"..."}}
`)
}

func commandName(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "version", "status", "doctor":
		return args[0]
	case "config", "diagnostics", "update", "media-runtime", "backup":
		if len(args) > 1 {
			switch args[1] {
			case "validate", "export", "check", "verify-package", "install", "inspect", "status", "rollback", "metadata", "precheck":
				return args[0] + " " + args[1]
			}
		}
		return args[0]
	}
	return ""
}
