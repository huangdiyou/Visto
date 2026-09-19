package media

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInputForFastSeekProcessUsesLocalFileDirectlyOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only optimization")
	}
	sourcePath := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open source fixture: %v", err)
	}
	defer source.Close()

	input, err := inputForFastSeekProcess(source, t.TempDir())
	if err != nil {
		t.Fatalf("create fast seek input: %v", err)
	}
	if input.name != sourcePath {
		t.Fatalf("input name = %q, want source path %q", input.name, sourcePath)
	}
	if input.cleanup != nil {
		t.Fatal("local file input should not create a temporary copy")
	}
	if len(input.sensitivePaths) != 1 || input.sensitivePaths[0] != sourcePath {
		t.Fatalf("unexpected sensitive paths: %#v", input.sensitivePaths)
	}
}

func TestInputForSeekableProcessCopiesLocalFileOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only behavior")
	}
	sourcePath := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open source fixture: %v", err)
	}
	defer source.Close()

	workDir := t.TempDir()
	input, err := inputForSeekableProcess(source, workDir)
	if err != nil {
		t.Fatalf("create seekable input: %v", err)
	}
	if input.name == sourcePath {
		t.Fatal("seekable input should keep using a stable temporary copy")
	}
	if input.cleanup == nil {
		t.Fatal("seekable input should clean up the temporary copy")
	}
	input.cleanup()
}
