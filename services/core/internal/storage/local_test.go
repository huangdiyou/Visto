package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeObjectKeyRejectsEscapesAndAbsolutePaths(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"../secret.mov",
		"media/../../secret.mov",
		"/etc/passwd",
		"C:/Windows/system.ini",
		`C:\Windows\system.ini`,
		`\\server\share\file.mov`,
		"media/\x00clip.mov",
	}
	for _, value := range invalid {
		if _, err := NormalizeObjectKey(value); err == nil {
			t.Errorf("expected %q to be rejected", value)
		}
	}

	key, err := NormalizeObjectKey("client//drafts/../final.mov")
	if err != nil {
		t.Fatalf("normalize valid key: %v", err)
	}
	if key != "client/final.mov" {
		t.Fatalf("unexpected normalized key %q", key)
	}
}

func TestLocalRootListRejectsOversizedDirectory(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	for index := 0; index <= maxDirectoryListingEntries; index++ {
		name := filepath.Join(rootPath, fmt.Sprintf("entry-%06d", index))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatalf("create fixture %d: %v", index, err)
		}
	}

	root, err := NewLocalRoot(rootPath)
	if err != nil {
		t.Fatalf("open local root: %v", err)
	}
	_, err = root.List(context.Background(), "")
	if !errors.Is(err, ErrScanLimitExceeded) {
		t.Fatalf("list error = %v, want ErrScanLimitExceeded", err)
	}
}

func TestLocalRootStatAndRangeRead(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "client"), 0o700); err != nil {
		t.Fatalf("create client directory: %v", err)
	}
	content := []byte("0123456789abcdef")
	if err := os.WriteFile(
		filepath.Join(rootPath, "client", "clip.bin"),
		content,
		0o600,
	); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	root, err := NewLocalRoot(rootPath)
	if err != nil {
		t.Fatalf("open local root: %v", err)
	}

	info, err := root.Stat(context.Background(), "client/clip.bin")
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.ObjectKey != "client/clip.bin" || info.SizeBytes != int64(len(content)) {
		t.Fatalf("unexpected file info: %#v", info)
	}

	reader, rangedInfo, err := root.OpenRange(
		context.Background(),
		"client/clip.bin",
		ByteRange{Offset: 4, Length: 6},
	)
	if err != nil {
		t.Fatalf("open range: %v", err)
	}
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if string(body) != "456789" {
		t.Fatalf("unexpected range body %q", body)
	}
	if rangedInfo.SizeBytes != int64(len(content)) {
		t.Fatalf("unexpected ranged file size %d", rangedInfo.SizeBytes)
	}

	entries, err := root.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list root directory: %v", err)
	}
	if len(entries) != 1 ||
		entries[0].Kind != "directory" ||
		entries[0].ObjectKey != "client" {
		t.Fatalf("unexpected root entries: %#v", entries)
	}

	clientEntries, err := root.List(context.Background(), "client")
	if err != nil {
		t.Fatalf("list client directory: %v", err)
	}
	if len(clientEntries) != 1 ||
		clientEntries[0].Kind != "file" ||
		clientEntries[0].ObjectKey != "client/clip.bin" {
		t.Fatalf("unexpected client entries: %#v", clientEntries)
	}

	if _, _, err := root.OpenRange(
		context.Background(),
		"client/clip.bin",
		ByteRange{Offset: int64(len(content) + 1), Length: 1},
	); !errors.Is(err, ErrRangeInvalid) {
		t.Fatalf("expected invalid range, got %v", err)
	}
}

func TestLocalRootBlocksSymlinkEscape(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows CI")
	}

	rootPath := t.TempDir()
	outsidePath := t.TempDir()
	outsideFile := filepath.Join(outsidePath, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(
		outsideFile,
		filepath.Join(rootPath, "outside-link.txt"),
	); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}

	insideFile := filepath.Join(rootPath, "inside.txt")
	if err := os.WriteFile(insideFile, []byte("inside"), 0o600); err != nil {
		t.Fatalf("write inside file: %v", err)
	}
	if err := os.Symlink(
		"inside.txt",
		filepath.Join(rootPath, "inside-link.txt"),
	); err != nil {
		t.Fatalf("create internal symlink: %v", err)
	}

	root, err := NewLocalRoot(rootPath)
	if err != nil {
		t.Fatalf("open local root: %v", err)
	}

	if _, err := root.Stat(
		context.Background(),
		"outside-link.txt",
	); err == nil {
		t.Fatal("expected absolute symlink escape rejection")
	}
	// os.Root rejects absolute links and returns an OS error rather than the
	// storage key-validation sentinel. Also exercise a relative escaping link.
	relativeOutside, err := filepath.Rel(rootPath, outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relativeOutside, filepath.Join(rootPath, "relative-outside.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(context.Background(), "relative-outside.txt"); err == nil {
		t.Fatal("expected relative symlink escape rejection")
	}
	reader, _, err := root.OpenRange(context.Background(), "inside-link.txt", ByteRange{})
	if err != nil {
		t.Fatalf("expected relative internal symlink to be readable: %v", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil || string(content) != "inside" {
		t.Fatalf("internal symlink content = %q, error = %v", content, err)
	}
}

func TestWritableLocalRootRejectsSymlinkEscapeDuringPut(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows CI")
	}

	rootPath := t.TempDir()
	outsidePath := t.TempDir()
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, "escape")); err != nil {
		t.Fatalf("create escaping directory symlink: %v", err)
	}
	root, err := newLocalRoot(rootPath, true)
	if err != nil {
		t.Fatalf("open writable local root: %v", err)
	}
	if _, err := root.Put(
		context.Background(),
		"escape/blocked.bin",
		strings.NewReader("blocked"),
		int64(len("blocked")),
		"application/octet-stream",
	); err == nil {
		t.Fatal("expected escaping symlink write to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outsidePath, "blocked.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write escaped local root, stat error = %v", err)
	}
}
