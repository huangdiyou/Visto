package serverupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"review-studio.local/core/internal/delivery"
)

func writeTestPackage(t *testing.T, contents []byte) (string, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "Visto-Server_1.0.0_macos-arm64.tar.gz")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	return path, hex.EncodeToString(digest[:])
}

func TestVerifyPackageAcceptsAMatchingDigest(t *testing.T) {
	contents := []byte("release package bytes")
	path, digest := writeTestPackage(t, contents)

	verified, err := VerifyPackage(VerifyPackageConfig{
		ArtifactPath:   path,
		Kind:           "macos-server",
		Platform:       "macos-arm64",
		ExpectedSHA256: digest,
	})
	if err != nil {
		t.Fatalf("VerifyPackage() error = %v", err)
	}
	if verified.SHA256 != digest || verified.SizeBytes != int64(len(contents)) {
		t.Fatalf("verified = %#v", verified)
	}
	// The free tier no longer verifies signatures, so the result must say that
	// it only proves integrity. A caller must not be able to read this as
	// provenance.
	if !verified.IntegrityOnly {
		t.Fatal("VerifyPackage must report integrityOnly=true for the free tier")
	}
}

func TestVerifyPackageAcceptsAnUppercaseDigest(t *testing.T) {
	path, digest := writeTestPackage(t, []byte("bytes"))
	if _, err := VerifyPackage(VerifyPackageConfig{
		ArtifactPath:   path,
		Kind:           "macos-server",
		Platform:       "macos-arm64",
		ExpectedSHA256: strings.ToUpper(digest),
	}); err != nil {
		t.Fatalf("VerifyPackage() error = %v", err)
	}
}

func TestVerifyPackageRejectsAMismatchedDigest(t *testing.T) {
	path, _ := writeTestPackage(t, []byte("bytes"))
	_, err := VerifyPackage(VerifyPackageConfig{
		ArtifactPath:   path,
		Kind:           "macos-server",
		Platform:       "macos-arm64",
		ExpectedSHA256: strings.Repeat("a", 64),
	})
	var classified *delivery.Error
	if !errors.As(err, &classified) || classified.Code != delivery.CodeVerificationFailed {
		t.Fatalf("got %v, want %s", err, delivery.CodeVerificationFailed)
	}
}

func TestVerifyPackageRejectsAMismatchedSize(t *testing.T) {
	path, digest := writeTestPackage(t, []byte("bytes"))
	_, err := VerifyPackage(VerifyPackageConfig{
		ArtifactPath:   path,
		Kind:           "macos-server",
		Platform:       "macos-arm64",
		ExpectedSHA256: digest,
		ExpectedSize:   4096,
	})
	var classified *delivery.Error
	if !errors.As(err, &classified) || classified.Code != delivery.CodeVerificationFailed {
		t.Fatalf("got %v, want %s", err, delivery.CodeVerificationFailed)
	}
}

func TestVerifyPackageRejectsBadInput(t *testing.T) {
	path, digest := writeTestPackage(t, []byte("bytes"))
	for _, testCase := range []struct {
		name   string
		config VerifyPackageConfig
	}{
		{
			name:   "missing digest",
			config: VerifyPackageConfig{ArtifactPath: path, Kind: "macos-server", Platform: "macos-arm64"},
		},
		{
			name: "digest is not hexadecimal",
			config: VerifyPackageConfig{
				ArtifactPath: path, Kind: "macos-server", Platform: "macos-arm64",
				ExpectedSHA256: strings.Repeat("z", 64),
			},
		},
		{
			name: "missing artifact path",
			config: VerifyPackageConfig{
				Kind: "macos-server", Platform: "macos-arm64", ExpectedSHA256: digest,
			},
		},
		{
			name: "unreadable artifact",
			config: VerifyPackageConfig{
				ArtifactPath: filepath.Join(t.TempDir(), "absent.tar.gz"),
				Kind:         "macos-server", Platform: "macos-arm64", ExpectedSHA256: digest,
			},
		},
		{
			name: "platform does not belong to the kind",
			config: VerifyPackageConfig{
				ArtifactPath: path, Kind: "windows-server", Platform: "macos-arm64",
				ExpectedSHA256: digest,
			},
		},
		{
			name: "unknown platform",
			config: VerifyPackageConfig{
				ArtifactPath: path, Kind: "macos-server", Platform: "freebsd-amd64",
				ExpectedSHA256: digest,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := VerifyPackage(testCase.config); err == nil {
				t.Fatal("VerifyPackage() must fail for this input")
			}
		})
	}
}
