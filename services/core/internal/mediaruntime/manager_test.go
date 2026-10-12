package mediaruntime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ulikunitz/xz"

	"review-studio.local/core/internal/delivery"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeProbe(ctx context.Context, source, dir string) (Probe, error) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	p := Probe{FFmpeg: filepath.Join(dir, "ffmpeg"+suffix), FFprobe: filepath.Join(dir, "ffprobe"+suffix), Encoder: "libopenh264", VerifiedAt: time.Now().UTC()}
	var e error
	p.FFmpegSHA256, e = hashFile(p.FFmpeg)
	if e != nil {
		return p, e
	}
	p.FFprobeSHA256, e = hashFile(p.FFprobe)
	return p, e
}
func fixture(t *testing.T, version string) ([]byte, []byte, []byte, string) {
	t.Helper()
	p, _ := delivery.Current()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	entries := map[string]string{"bin/ffmpeg" + suffix: version, "bin/ffprobe" + suffix: version, "LICENSE.txt": "LGPL", "SOURCE.txt": "source", "FFmpeg-BUILD.txt": "build", "THIRD_PARTY_NOTICES.md": "notices", "THIRD_PARTY.spdx.json": "{}"}
	var buf bytes.Buffer
	if p.OS == "windows" {
		z := zip.NewWriter(&buf)
		for n, b := range entries {
			w, e := z.Create(n)
			if e != nil {
				t.Fatal(e)
			}
			io.WriteString(w, b)
		}
		if e := z.Close(); e != nil {
			t.Fatal(e)
		}
	} else {
		z, _ := xz.NewWriter(&buf)
		a := tar.NewWriter(z)
		for n, b := range entries {
			a.WriteHeader(&tar.Header{Name: n, Mode: 0755, Size: int64(len(b))})
			io.WriteString(a, b)
		}
		a.Close()
		z.Close()
	}
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	m := Manifest{SchemaVersion: 1, RuntimeVersion: version, Platform: p.ID, URL: "https://github.com/huangdiyou/Visto/releases/download/runtime-test/runtime.zip", Size: int64(len(archive)), SHA256: hex.EncodeToString(sum[:]), License: "LGPL-3.0-or-later", SourceURL: "https://ffmpeg.org/releases/ffmpeg-8.1.tar.xz", BuildRecord: "FFmpeg-BUILD.txt", MinimumServerVersion: "1.0.0"}
	body, _ := json.Marshal(m)
	key, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)))
	return archive, body, sig, base64.StdEncoding.EncodeToString(key)
}
func manager(t *testing.T, key string) Manager {
	t.Helper()
	// macOS temp directories normally pass through /var -> /private/var.
	// Resolve only the test fixture; production still rejects linked roots.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Manager{Root: filepath.Join(base, "runtime"), ServerVersion: "1.0.0", PublicKey: key, inspect: fakeProbe, protect: func(string) error { return nil }, space: func(string) (int64, error) { return 10 << 30, nil }}
}
func offlineFile(t *testing.T, b []byte) string {
	f := filepath.Join(t.TempDir(), "runtime.archive")
	if e := os.WriteFile(f, b, 0600); e != nil {
		t.Fatal(e)
	}
	return f
}
func TestOfflineInstallReuseAndRollback(t *testing.T) {
	archive, body, sig, key := fixture(t, "ffmpeg-1")
	m := manager(t, key)
	ctx := context.Background()
	one, e := m.Install(ctx, body, sig, offlineFile(t, archive), false)
	if e != nil {
		t.Fatal(e)
	}
	if one.Source != "managed" {
		t.Fatal(one)
	}
	// A verified cached archive is enough for reuse, with no network/confirmation.
	m.Transport = roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("cache reuse downloaded"); return nil, nil })
	if _, e = m.Install(ctx, body, sig, "", false); e != nil {
		t.Fatal(e)
	}
	b2, j2, s2, k2 := fixture(t, "ffmpeg-2")
	m.PublicKey = k2
	if _, e = m.Install(ctx, j2, s2, offlineFile(t, b2), false); e != nil {
		t.Fatal(e)
	}
	back, e := m.Rollback(ctx)
	if e != nil || back.RuntimeVersion != "ffmpeg-1" {
		t.Fatalf("rollback: %#v %v", back, e)
	}
	if _, e = m.Resolve(); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(back.Probe.FFmpeg, []byte("tampered"), 0600)
	if _, e = m.Resolve(); e == nil {
		t.Fatal("changed runtime accepted")
	}
}
func TestFailuresKeepPreviousSelection(t *testing.T) {
	for _, kind := range []string{"signature", "hash", "capability", "cancel", "space", "confirmation", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			archive, body, sig, key := fixture(t, "ffmpeg-1")
			m := manager(t, key)
			ctx := context.Background()
			if _, e := m.Install(ctx, body, sig, offlineFile(t, archive), false); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(filepath.Join(m.Root, "state.json"))
			b, j, s, k := fixture(t, "ffmpeg-2")
			m.PublicKey = k
			input := offlineFile(t, b)
			ctx = context.Background()
			switch kind {
			case "signature":
				s = []byte("bad")
			case "hash":
				b[0] ^= 1
				input = offlineFile(t, b)
			case "capability":
				m.inspect = func(context.Context, string, string) (Probe, error) { return Probe{}, runtimeError("missing codec") }
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "space":
				m.space = func(string) (int64, error) { return 1, nil }
			case "confirmation":
				input = ""
			case "truncated":
				input = offlineFile(t, b[:len(b)-1])
			}
			if _, e := m.Install(ctx, j, s, input, false); e == nil {
				t.Fatal("failure was accepted")
			}
			after, _ := os.ReadFile(filepath.Join(m.Root, "state.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("failed install changed active selection")
			}
			entries, _ := os.ReadDir(m.Root)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".staging-") {
					t.Fatal("staging leaked")
				}
			}
		})
	}
}

// The Visto Pages runtime channel is a supported payload shape, so a signed
// manifest pointing at it has to verify rather than be rejected as untrusted.
func TestManifestAcceptsVistoPagesPayload(t *testing.T) {
	_, body, _, _ := fixture(t, "ffmpeg-1")
	p, _ := delivery.Current()
	key, priv, _ := ed25519.GenerateKey(rand.Reader)
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	m["sourceBundleSha256"] = strings.Repeat("a", 64)
	m["url"] = "https://visto-server-updates.pages.dev/media-runtime/" + p.ID + "-v1/runtime.tar.xz"
	encoded, _ := json.Marshal(m)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, encoded)))
	manifest, err := VerifyManifest(encoded, sig, base64.StdEncoding.EncodeToString(key), p.ID, "1.0.0")
	if err != nil {
		t.Fatalf("pages payload rejected: %v", err)
	}
	if manifest.SourceBundleSHA256 != strings.Repeat("a", 64) {
		t.Fatal("source archive digest lost")
	}
	if manifest.URL != m["url"] {
		t.Fatalf("URL = %q, want %q", manifest.URL, m["url"])
	}
}
func TestManifestRejectsUntrustedPolicy(t *testing.T) {
	_, body, _, _ := fixture(t, "ffmpeg-1")
	p, _ := delivery.Current()
	key, priv, _ := ed25519.GenerateKey(rand.Reader)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"invalid source digest", func(m map[string]any) { m["sourceBundleSha256"] = "invalid" }},
		{"unknown field", func(m map[string]any) { m["command"] = "execute" }},
		{"wrong platform", func(m map[string]any) { m["platform"] = "other" }},
		{"gpl", func(m map[string]any) { m["license"] = "GPL-3.0" }},
		{"rolling URL", func(m map[string]any) { m["url"] = "https://github.com/a/b/releases/download/latest/runtime.zip" }},
		{"rolling Pages version", func(m map[string]any) {
			m["url"] = "https://visto-server-updates.pages.dev/media-runtime/latest/runtime.tar.xz"
		}},
		{"Pages traversal", func(m map[string]any) {
			m["url"] = "https://visto-server-updates.pages.dev/media-runtime/../secret/runtime.tar.xz"
		}},
		{"Pages wrong root", func(m map[string]any) {
			m["url"] = "https://visto-server-updates.pages.dev/other/v1/runtime.tar.xz"
		}},
		{"private URL", func(m map[string]any) { m["url"] = "https://127.0.0.1/runtime.zip" }},
		{"large", func(m map[string]any) { m["size"] = MaxArchiveBytes + 1 }},
		{"newer Server", func(m map[string]any) { m["minimumServerVersion"] = "2.0.0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(body, &m)
			tc.mutate(m)
			b, _ := json.Marshal(m)
			sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, b)))
			if _, e := VerifyManifest(b, sig, base64.StdEncoding.EncodeToString(key), p.ID, "1.0.0"); e == nil {
				t.Fatal("unsafe policy accepted")
			}
		})
	}
	duplicate := append([]byte(`{"schemaVersion":1,`), body[1:]...)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, duplicate)))
	if _, e := VerifyManifest(duplicate, sig, base64.StdEncoding.EncodeToString(key), p.ID, "1.0.0"); e == nil {
		t.Fatal("duplicate fields accepted")
	}
}
func TestArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, name := range []string{"../outside", "/outside", `C:\outside`, `\\server\share`, `bin/a:stream`, `bin/CON.txt`, `bin/a.`, `bin/a/../b`, `bin/a\nb`} {
		if memberName(name) == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, format := range []string{"zip", "tar.xz"} {
		for _, kind := range []string{"traversal", "symlink", "duplicate", "case duplicate", "oversize"} {
			t.Run(format+kind, func(t *testing.T) {
				var buf bytes.Buffer
				name := "bin/tool"
				if kind == "traversal" {
					name = "../outside"
				}
				if format == "zip" {
					z := zip.NewWriter(&buf)
					h := &zip.FileHeader{Name: name}
					h.SetMode(0644)
					if kind == "symlink" {
						h.SetMode(os.ModeSymlink | 0777)
					}
					w, _ := z.CreateHeader(h)
					io.WriteString(w, "x")
					if strings.Contains(kind, "duplicate") {
						n := name
						if kind == "case duplicate" {
							n = "BIN/TOOL"
						}
						w, _ = z.Create(n)
						io.WriteString(w, "x")
					}
					if kind == "oversize" {
						large := &zip.FileHeader{Name: "bin/large", Method: zip.Store, UncompressedSize64: uint64(MaxExpandedBytes) + 1, CompressedSize64: 1}
						raw, e := z.CreateRaw(large)
						if e != nil {
							t.Fatal(e)
						}
						raw.Write([]byte("x"))
					}
					z.Close()
				} else {
					z, _ := xz.NewWriter(&buf)
					a := tar.NewWriter(z)
					h := &tar.Header{Name: name, Mode: 0644, Size: 1}
					if kind == "symlink" {
						h.Typeflag = tar.TypeSymlink
						h.Linkname = "/outside"
						h.Size = 0
					}
					if kind == "oversize" {
						h.Size = MaxExpandedBytes + 1
					}
					a.WriteHeader(h)
					a.Write([]byte("x"))
					if strings.Contains(kind, "duplicate") {
						if kind == "case duplicate" {
							h.Name = "BIN/TOOL"
						}
						a.WriteHeader(h)
						a.Write([]byte("x"))
					}
					a.Close()
					z.Close()
				}
				if e := extract(context.Background(), offlineFile(t, buf.Bytes()), format, t.TempDir()); e == nil {
					t.Fatal("unsafe archive accepted")
				}
			})
		}
	}
}
func TestDownloadBoundsAndRedirectPolicy(t *testing.T) {
	archive, body, _, _ := fixture(t, "ffmpeg-1")
	var m Manifest
	json.Unmarshal(body, &m)
	for _, kind := range []string{"success", "oversize", "truncated", "redirect", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			payload := archive
			if kind == "oversize" {
				payload = append(append([]byte{}, archive...), 1)
			}
			if kind == "truncated" {
				payload = archive[:len(archive)-1]
			}
			transport := roundTrip(func(r *http.Request) (*http.Response, error) {
				if kind == "cancel" {
					return nil, context.Canceled
				}
				if kind == "redirect" {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: -1, Body: io.NopCloser(bytes.NewReader(payload)), Request: r}, nil
			})
			e := download(context.Background(), m, filepath.Join(t.TempDir(), "download"), transport, nil)
			if kind == "success" && e != nil {
				t.Fatal(e)
			}
			if kind != "success" && e == nil {
				t.Fatal("bad download accepted")
			}
		})
	}
}
func TestSystemFailureNeverDownloads(t *testing.T) {
	m := manager(t, "")
	m.inspect = func(context.Context, string, string) (Probe, error) { return Probe{}, runtimeError("missing FFmpeg") }
	m.Transport = roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("system failure downloaded"); return nil, nil })
	if _, e := m.Select(context.Background(), "system", ""); e == nil {
		t.Fatal("missing runtime accepted")
	}
}
func TestOperationLock(t *testing.T) {
	m := manager(t, "")
	entered := false
	err := m.withLock(func() error {
		entered = true
		return m.withLock(func() error { t.Fatal("concurrent lock accepted"); return nil })
	})
	if !entered {
		t.Fatalf("outer operation did not acquire lock: %v", err)
	}
	if err == nil {
		t.Fatal("missing lock failure")
	}
}
func TestCancellationClass(t *testing.T) {
	var e *delivery.Error
	if !errors.As(Classify(context.Canceled), &e) || e.ExitCode != 130 {
		t.Fatal("cancellation not classified")
	}
}

func TestManagedCandidateCapabilities(t *testing.T) {
	dir := os.Getenv("VISTO_TEST_MANAGED_RUNTIME")
	if dir == "" {
		t.Skip("set VISTO_TEST_MANAGED_RUNTIME to test a real LGPL candidate")
	}
	p, err := Inspect(context.Background(), "managed", dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Encoder != "libopenh264" {
		t.Fatalf("unexpected managed encoder %s", p.Encoder)
	}
	t.Logf("managed capability smoke passed: %s; ffmpeg SHA256=%s ffprobe SHA256=%s", p.FFmpegVersion, p.FFmpegSHA256, p.FFprobeSHA256)
}
func TestStaleStagingCleanupAndRootBoundary(t *testing.T) {
	m := manager(t, "")
	os.MkdirAll(filepath.Join(m.Root, ".staging-abandoned"), 0700)
	os.WriteFile(filepath.Join(m.Root, ".staging-abandoned", "partial"), []byte("partial"), 0600)
	if err := m.withLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, ".staging-abandoned")); !os.IsNotExist(err) {
		t.Fatal("abandoned staging survived")
	}
	if safeRoot("relative") == nil {
		t.Fatal("relative root accepted")
	}
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(outside, link); err == nil && safeRoot(filepath.Join(link, "runtime")) == nil {
		t.Fatal("symlink root accepted")
	}
}

func TestApplicationRollbackCompatibility(t *testing.T) {
	archive, body, sig, key := fixture(t, "ffmpeg-1")
	m := manager(t, key)
	if _, err := m.Install(context.Background(), body, sig, offlineFile(t, archive), false); err != nil {
		t.Fatal(err)
	}
	m.ServerVersion = "0.9.0"
	if _, err := m.Resolve(); err == nil {
		t.Fatal("older Server accepted incompatible runtime")
	}
}
func TestValidTarXZLayout(t *testing.T) {
	var buf bytes.Buffer
	compressed, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(compressed)
	for _, name := range []string{"bin/ffmpeg", "bin/ffprobe", "LICENSE.txt", "FFmpeg-BUILD.txt", "SOURCE.txt", "THIRD_PARTY_NOTICES.md", "THIRD_PARTY.spdx.json"} {
		if err = writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 2}); err != nil {
			t.Fatal(err)
		}
		writer.Write([]byte("ok"))
	}
	writer.Close()
	compressed.Close()
	target := t.TempDir()
	if err = extract(context.Background(), offlineFile(t, buf.Bytes()), "tar.xz", target); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(target, "bin", "ffmpeg"))
	if err != nil || string(body) != "ok" {
		t.Fatal("runtime not extracted")
	}
}
