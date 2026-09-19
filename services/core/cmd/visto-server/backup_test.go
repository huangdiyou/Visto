package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupMetadataFailureProducesNoRecoveryPoint(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		dir := t.TempDir()
		if corrupt {
			if err := os.WriteFile(filepath.Join(dir, "review-studio.db"), []byte("corrupt database"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		output := filepath.Join(dir, "backup.json")
		var stdout, stderr bytes.Buffer
		code := run([]string{"--json", "backup", "metadata", "--data-dir", dir, "--archive", "backup.tar.gz", "--sha256", strings.Repeat("a", 64), "--output", output}, &stdout, &stderr)
		if code == 0 {
			t.Fatalf("unreadable database reported success: %s", stdout.String())
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("failed backup wrote metadata: %v", err)
		}
	}
}
