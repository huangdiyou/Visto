package mediaruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"review-studio.local/core/internal/delivery"
	"review-studio.local/core/internal/storage"
)

type Selection struct {
	MinimumServerVersion string    `json:"minimumServerVersion,omitempty"`
	Source               string    `json:"source"`
	RuntimeVersion       string    `json:"runtimeVersion,omitempty"`
	Platform             string    `json:"platform"`
	ManifestSHA256       string    `json:"manifestSha256,omitempty"`
	InstalledAt          time.Time `json:"installedAt"`
	Probe                Probe     `json:"probe"`
}
type State struct {
	SchemaVersion int        `json:"schemaVersion"`
	Active        *Selection `json:"active"`
	Previous      *Selection `json:"previous,omitempty"`
}
type Manager struct {
	Root          string
	ServerVersion string
	PublicKey     string
	Transport     http.RoundTripper
	Progress      func(int64, int64)
	// Dependencies remain internal, so production entrypoints cannot bypass checks.
	protect func(string) error
	inspect func(context.Context, string, string) (Probe, error)
	space   func(string) (int64, error)
}

func DefaultRoot() string {
	if root := os.Getenv("VISTO_MEDIA_RUNTIME_ROOT"); root != "" {
		return root
	}
	p, ok := delivery.Current()
	if !ok {
		return ""
	}
	root := delivery.DefaultLayout(p).RuntimeDir
	if runtime.GOOS == "windows" {
		root = strings.Replace(root, "%ProgramData%", os.Getenv("ProgramData"), 1)
	}
	return root
}
func (m Manager) probe(ctx context.Context, source, dir string) (Probe, error) {
	if m.inspect != nil {
		return m.inspect(ctx, source, dir)
	}
	return Inspect(ctx, source, dir)
}
func safeRoot(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) == filepath.VolumeName(name)+string(os.PathSeparator) {
		return bad("runtime root must be an absolute non-volume directory")
	}
	for p := filepath.Clean(name); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return bad("runtime root must not contain symlinks or junctions")
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func (m Manager) withLock(action func() error) error {
	if err := safeRoot(m.Root); err != nil {
		return err
	}
	if err := os.MkdirAll(m.Root, 0755); err != nil {
		return err
	}
	protect := m.protect
	if protect == nil {
		protect = secureRoot
	}
	if err := protect(m.Root); err != nil {
		return err
	}
	lockPath := filepath.Join(m.Root, "manager.lock")
	if info, e := os.Lstat(lockPath); e == nil && !info.Mode().IsRegular() {
		return bad("invalid runtime lock file")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = lockFile(f); err != nil {
		return delivery.NewError(delivery.CodeInternalError, "another runtime operation is in progress")
	}
	defer unlockFile(f)
	// Only abandoned manager staging directories are removed, under the locked root.
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") || strings.HasPrefix(entry.Name(), ".state-") {
			if entry.Type()&os.ModeSymlink != 0 {
				return bad("invalid runtime staging link")
			}
			if err = os.RemoveAll(filepath.Join(m.Root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return action()
}
func (m Manager) ReadState() (State, error) {
	var s State
	if err := safeRoot(m.Root); err != nil {
		return s, err
	}
	statePath := filepath.Join(m.Root, "state.json")
	if info, e := os.Lstat(statePath); e == nil && !info.Mode().IsRegular() {
		return s, bad("runtime state must be a regular file")
	}
	f, err := os.Open(statePath)
	if os.IsNotExist(err) {
		return State{SchemaVersion: 1}, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	if err = json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&s); err != nil || s.SchemaVersion != 1 {
		return s, bad("invalid runtime state")
	}
	return s, nil
}
func (m Manager) save(s State) error {
	s.SchemaVersion = 1
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(m.Root, ".state-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Chmod(name, 0644); e != nil {
		return e
	}
	return replaceFile(name, filepath.Join(m.Root, "state.json"))
}
func (m Manager) Select(ctx context.Context, source, directory string) (Selection, error) {
	var selection Selection
	if source != "system" && source != "custom" {
		return selection, delivery.NewError(delivery.CodeInvalidArguments, "select requires system or custom source")
	}
	err := m.withLock(func() error {
		probe, err := m.probe(ctx, source, directory)
		if err != nil {
			return err
		}
		state, err := m.ReadState()
		if err != nil {
			return err
		}
		p, ok := delivery.Current()
		if !ok {
			return delivery.NewError(delivery.CodeUnsupportedPlatform, "unsupported host")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		selection = Selection{Source: source, Platform: p.ID, InstalledAt: time.Now().UTC(), Probe: probe}
		state.Previous = state.Active
		state.Active = &selection
		return m.save(state)
	})
	return selection, err
}
func (m Manager) Install(ctx context.Context, body, sig []byte, offline string, confirmDownload bool) (Selection, error) {
	var selection Selection
	p, ok := delivery.Current()
	if !ok {
		return selection, delivery.NewError(delivery.CodeUnsupportedPlatform, "unsupported host")
	}
	manifest, err := VerifyManifest(body, sig, m.PublicKey, p.ID, m.ServerVersion)
	if err != nil {
		return selection, err
	}
	err = m.withLock(func() error {
		state, err := m.ReadState()
		if err != nil {
			return err
		}
		stage, err := os.MkdirTemp(m.Root, ".staging-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		free := m.space
		if free == nil {
			free = storage.AvailableDiskBytes
		}
		available, err := free(m.Root)
		if err != nil {
			return err
		}
		if available < manifest.Size+MaxExpandedBytes+(128<<20) {
			return delivery.NewError(delivery.CodeInsufficientStorage, "insufficient space for runtime download and extraction")
		}
		// Cache is on the runtime volume. Never move an unchecked input into it.
		cache := filepath.Join(m.Root, "cache")
		if err = os.MkdirAll(cache, 0755); err != nil {
			return err
		}
		if err = safeRoot(cache); err != nil {
			return err
		}
		entries, err := os.ReadDir(cache)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".download-") {
				if !entry.Type().IsRegular() {
					return bad("invalid cache staging member")
				}
				if err = os.Remove(filepath.Join(cache, entry.Name())); err != nil {
					return err
				}
			}
		}
		cached := filepath.Join(cache, manifest.SHA256+".archive")
		check := func(name string) bool {
			info, e := os.Lstat(name)
			if e != nil || !info.Mode().IsRegular() || info.Size() != manifest.Size {
				return false
			}
			h, e := hashFile(name)
			return e == nil && h == manifest.SHA256
		}
		archive := filepath.Join(stage, "input.archive")
		if check(cached) {
			if err = copyBounded(ctx, cached, archive, manifest.Size); err != nil {
				return err
			}
		} else {
			if offline != "" {
				if err = copyBounded(ctx, offline, archive, manifest.Size); err != nil {
					return err
				}
			} else {
				if !confirmDownload {
					return delivery.NewError(delivery.CodeInvalidArguments, "managed download requires --confirm-download")
				}
				if err = download(ctx, manifest, archive, m.Transport, m.Progress); err != nil {
					return err
				}
			}
			if !check(archive) {
				return bad("runtime archive size or SHA-256 mismatch")
			}
			// Copy via a private temporary file and replace only after verification.
			tmp, err := os.CreateTemp(cache, ".download-")
			if err != nil {
				return err
			}
			tempName := tmp.Name()
			tmp.Close()
			os.Remove(tempName)
			defer os.Remove(tempName)
			if err = copyBounded(ctx, archive, tempName, manifest.Size); err != nil {
				return err
			}
			if err = replaceFile(tempName, cached); err != nil {
				return err
			}
		}
		if !check(archive) {
			return bad("cached runtime archive changed during copy")
		}
		expanded := filepath.Join(stage, "expanded")
		if err = os.Mkdir(expanded, 0755); err != nil {
			return err
		}
		if err = extract(ctx, archive, manifest.archiveFormat(), expanded); err != nil {
			return err
		}
		probe, err := m.probe(ctx, "managed", filepath.Join(expanded, "bin"))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		releaseName := manifest.RuntimeVersion + "-" + manifest.SHA256[:16]
		releases := filepath.Join(m.Root, "releases")
		if err = os.MkdirAll(releases, 0755); err != nil {
			return err
		}
		if err = safeRoot(releases); err != nil {
			return err
		}
		target := filepath.Join(releases, releaseName)
		if err = safeRoot(target); err != nil {
			return err
		}
		if _, err = os.Lstat(target); err == nil {
			// Never overwrite an existing release, including one used by a running Core.
			current, e := m.probe(ctx, "managed", filepath.Join(target, "bin"))
			if e != nil {
				return e
			}
			if current.FFmpegSHA256 != probe.FFmpegSHA256 || current.FFprobeSHA256 != probe.FFprobeSHA256 {
				return bad("existing runtime release differs from signed package")
			}
			probe = current
		} else if !os.IsNotExist(err) {
			return err
		} else {
			if err = os.WriteFile(filepath.Join(expanded, "manifest.json"), body, 0644); err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(expanded, "manifest.json.sig"), sig, 0644); err != nil {
				return err
			}
			if err = os.Rename(expanded, target); err != nil {
				return err
			}
			probe.FFmpeg = filepath.Join(target, "bin", filepath.Base(probe.FFmpeg))
			probe.FFprobe = filepath.Join(target, "bin", filepath.Base(probe.FFprobe))
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		selection = Selection{MinimumServerVersion: manifest.MinimumServerVersion, Source: "managed", RuntimeVersion: manifest.RuntimeVersion, Platform: manifest.Platform, ManifestSHA256: digest, InstalledAt: time.Now().UTC(), Probe: probe}
		if state.Active != nil && state.Active.ManifestSHA256 == digest {
			selection.InstalledAt = state.Active.InstalledAt
			state.Active = &selection
			return m.save(state)
		}
		state.Previous = state.Active
		state.Active = &selection
		return m.save(state)
	})
	return selection, err
}
func copyBounded(ctx context.Context, source, destination string, size int64) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return bad("offline runtime must be a regular file")
	}
	if info.Size() != size {
		return bad("runtime input size mismatch")
	}
	out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	n, e := io.Copy(out, io.LimitReader(contextReader{ctx, in}, size+1))
	if e != nil {
		return e
	}
	if n != size {
		return bad("runtime input changed size")
	}
	return out.Sync()
}
func (m Manager) Rollback(ctx context.Context) (Selection, error) {
	var selected Selection
	err := m.withLock(func() error {
		s, e := m.ReadState()
		if e != nil {
			return e
		}
		if s.Previous == nil {
			return runtimeError("no previous runtime is available")
		}
		selected = *s.Previous
		if e = m.validateSelection(selected); e != nil {
			return e
		}
		p, e := m.probe(ctx, selected.Source, filepath.Dir(selected.Probe.FFmpeg))
		if e != nil {
			return e
		}
		if p.FFmpegSHA256 != selected.Probe.FFmpegSHA256 || p.FFprobeSHA256 != selected.Probe.FFprobeSHA256 {
			return bad("previous runtime changed since verification")
		}
		selected.Probe = p
		s.Previous = s.Active
		s.Active = &selected
		return m.save(s)
	})
	return selected, err
}

// Resolve is read-only and is used at Core startup. A selected runtime never
// silently falls back to PATH when missing or changed.
func (m Manager) Resolve() (Selection, error) {
	s, e := m.ReadState()
	if e != nil {
		return Selection{}, e
	}
	if s.Active == nil {
		return Selection{}, os.ErrNotExist
	}
	if e = m.validateSelection(*s.Active); e != nil {
		return Selection{}, e
	}
	p := s.Active.Probe
	for name, expected := range map[string]string{p.FFmpeg: p.FFmpegSHA256, p.FFprobe: p.FFprobeSHA256} {
		if !filepath.IsAbs(name) {
			return Selection{}, bad("runtime state contains a relative executable")
		}
		actual, e := hashFile(name)
		if e != nil || actual != expected {
			return Selection{}, runtimeError("selected runtime changed; run media-runtime install again")
		}
	}
	return *s.Active, nil
}
func Classify(err error) error {
	if errors.Is(err, context.Canceled) {
		return delivery.NewError(delivery.CodeCancelled, "runtime operation cancelled; previous selection retained")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return delivery.NewError(delivery.CodeNetworkFailure, "runtime operation timed out; previous selection retained")
	}
	var known *delivery.Error
	if errors.As(err, &known) {
		return known
	}
	return delivery.NewError(delivery.CodeInternalError, fmt.Sprintf("runtime operation failed: %v", err))
}

func (m Manager) validateSelection(s Selection) error {
	if s.Source == "managed" && !supportsVersion(m.ServerVersion, s.MinimumServerVersion) {
		return bad("selected runtime is incompatible with this Server version")
	}
	p, ok := delivery.Current()
	if !ok || s.Platform != p.ID {
		return delivery.NewError(delivery.CodeUnsupportedPlatform, "selected runtime belongs to another platform")
	}
	if s.Source != "system" && s.Source != "custom" && s.Source != "managed" {
		return bad("unknown runtime state source")
	}
	if s.Probe.Encoder != "libx264" &&
		s.Probe.Encoder != "libopenh264" &&
		s.Probe.Encoder != "h264_videotoolbox" {
		return bad("unsupported runtime encoder in state")
	}
	if !validHash(s.Probe.FFmpegSHA256) || !validHash(s.Probe.FFprobeSHA256) {
		return bad("runtime state is missing executable hashes")
	}
	for _, name := range []string{s.Probe.FFmpeg, s.Probe.FFprobe} {
		if !filepath.IsAbs(name) {
			return bad("runtime state contains relative executable")
		}
		info, e := os.Lstat(name)
		if e != nil || !info.Mode().IsRegular() {
			return runtimeError("selected runtime executable is missing or not regular")
		}
		if s.Source == "managed" {
			rel, e := filepath.Rel(filepath.Join(m.Root, "releases"), name)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return bad("managed runtime escaped release root")
			}
			if e = safeRoot(filepath.Dir(name)); e != nil {
				return e
			}
		}
	}
	return nil
}
