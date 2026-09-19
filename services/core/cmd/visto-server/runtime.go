package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"review-studio.local/core/internal/delivery"
	"review-studio.local/core/internal/mediaruntime"
)

func readRuntimeInput(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("runtime metadata exceeds limit")
	}
	return b, nil
}
func runMediaRuntime(args []string, jsonOutput bool, version string, stdout, stderr, progress io.Writer) int {
	failRuntime := func(err error) int {
		return fail(stdout, stderr, jsonOutput, "media-runtime", mediaruntime.Classify(err))
	}
	invalid := func(message string) int {
		return failRuntime(delivery.NewError(delivery.CodeInvalidArguments, message))
	}
	if len(args) == 0 {
		return invalid("choose inspect, status, install or rollback")
	}
	command := args[0]
	flags := flag.NewFlagSet("media-runtime "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("media-runtime", "", "managed, system or custom")
	directory := flags.String("media-runtime-path", "", "absolute custom runtime directory")
	offline := flags.String("media-runtime-package", "", "offline signed archive")
	manifestPath := flags.String("manifest", "", "local signed runtime manifest")
	signaturePath := flags.String("signature", "", "detached signature (default manifest.sig)")
	publicKey := flags.String("public-key", os.Getenv("VISTO_SERVER_UPDATE_PUBLIC_KEY"), "administrator-pinned Server release public key")
	nonInteractive := flags.Bool("non-interactive", false, "never prompt")
	confirmed := flags.Bool("confirm-download", false, "authorize managed runtime download")
	rollback := flags.Bool("confirm-rollback", false, "authorize restoring previous runtime selection")
	if e := flags.Parse(args[1:]); e != nil {
		return invalid(e.Error())
	}
	if flags.NArg() != 0 {
		return invalid("unexpected positional arguments")
	}
	if command != "inspect" && command != "status" && command != "install" && command != "rollback" {
		return invalid("unknown runtime command")
	}
	if *source == "" {
		switch {
		case *directory != "":
			*source = "custom"
		case *offline != "":
			*source = "managed"
		case command == "inspect":
			*source = "system"
		default:
			*source = "managed"
		}
	}
	if *source != "managed" && *source != "system" && *source != "custom" {
		return invalid("unknown runtime source")
	}
	if (*directory != "" && *source != "custom") || (*offline != "" && *source != "managed") || (*directory != "" && *offline != "") || (*source == "custom" && *directory == "") {
		return invalid("conflicting or missing runtime mode parameters")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	manager := mediaruntime.Manager{Root: mediaruntime.DefaultRoot(), ServerVersion: version, PublicKey: *publicKey}
	if command == "status" {
		s, e := manager.ReadState()
		if e != nil {
			return failRuntime(e)
		}
		if s.Active != nil {
			if _, e = manager.Resolve(); e != nil {
				return failRuntime(e)
			}
		}
		return writeResult(stdout, jsonOutput, s, 0)
	}
	if command == "inspect" {
		if *source == "managed" {
			s, e := manager.Resolve()
			if e != nil {
				return failRuntime(e)
			}
			return writeResult(stdout, jsonOutput, s, 0)
		}
		p, e := mediaruntime.Inspect(ctx, *source, *directory)
		if e != nil {
			return failRuntime(e)
		}
		return writeResult(stdout, jsonOutput, p, 0)
	}
	if e := mediaruntime.RequireAdministrator(); e != nil {
		return failRuntime(e)
	}
	confirm := func(message string) bool {
		if *nonInteractive {
			return false
		}
		fmt.Fprintln(progress, message+" [y/N]")
		input := make(chan string, 1)
		go func() { line, _ := bufio.NewReader(os.Stdin).ReadString('\n'); input <- strings.TrimSpace(line) }()
		select {
		case <-ctx.Done():
			return false
		case line := <-input:
			return strings.EqualFold(line, "y")
		}
	}
	if command == "rollback" {
		if !*rollback && !confirm("Restore the previously verified runtime?") {
			return invalid("rollback requires --confirm-rollback")
		}
		s, e := manager.Rollback(ctx)
		if e != nil {
			return failRuntime(e)
		}
		return writeResult(stdout, jsonOutput, s, 0)
	}
	if *source != "managed" {
		s, e := manager.Select(ctx, *source, *directory)
		if e != nil {
			return failRuntime(e)
		}
		return writeResult(stdout, jsonOutput, s, 0)
	}
	if *manifestPath == "" {
		return invalid("managed runtime requires --manifest and its detached signature")
	}
	if *signaturePath == "" {
		*signaturePath = *manifestPath + ".sig"
	}
	body, e := readRuntimeInput(*manifestPath, mediaruntime.MaxManifestBytes)
	if e != nil {
		return invalid("runtime manifest is unavailable or too large")
	}
	sig, e := readRuntimeInput(*signaturePath, 1024)
	if e != nil {
		return invalid("runtime signature is unavailable or too large")
	}
	platform, ok := delivery.Current()
	if !ok {
		return failRuntime(delivery.NewError(delivery.CodeUnsupportedPlatform, "unsupported host"))
	}
	m, e := mediaruntime.VerifyManifest(body, sig, *publicKey, platform.ID, version)
	if e != nil {
		return failRuntime(e)
	}
	if *offline == "" && !*confirmed {
		*confirmed = confirm(fmt.Sprintf("Download FFmpeg %s (%d bytes, %s) from %s?", m.RuntimeVersion, m.Size, m.License, m.URL))
		if !*confirmed {
			return invalid("managed download requires --confirm-download")
		}
	}
	if !jsonOutput {
		var last time.Time
		manager.Progress = func(n, total int64) {
			if time.Since(last) > time.Second || n == total {
				fmt.Fprintf(progress, "FFmpeg download: %d / %d bytes\n", n, total)
				last = time.Now()
			}
		}
	}
	s, e := manager.Install(ctx, body, sig, *offline, *confirmed)
	if e != nil {
		return failRuntime(e)
	}
	return writeResult(stdout, jsonOutput, s, 0)
}
