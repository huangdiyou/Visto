package serverupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"review-studio.local/core/internal/delivery"
)

// VerifiedPackage is the result of checking an already-downloaded release
// package. IntegrityOnly is always true for the free tier and exists so a
// machine-readable caller cannot mistake this check for provenance.
type VerifiedPackage struct {
	Kind          string `json:"kind"`
	Platform      string `json:"platform"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"sizeBytes"`
	IntegrityOnly bool   `json:"integrityOnly"`
}

// VerifyPackageConfig describes the integrity check of a downloaded package.
type VerifyPackageConfig struct {
	ArtifactPath   string
	Kind           string
	Platform       string
	ExpectedSHA256 string
	// ExpectedSize is optional. Zero disables the size comparison, which keeps
	// the check usable when only the digest sidecar is available.
	ExpectedSize int64
}

// VerifyPackage confirms that a downloaded package matches the digest published
// beside it.
//
// Free tier boundary, docs/FREE_TIER_BOUNDARY_DESIGN.md §1.9: the signed update
// channel was removed, so this check only proves the download is intact and is
// the file the operator meant to fetch. It cannot prove provenance, because the
// expected digest and the package come from the same place. The returned value
// says so explicitly through IntegrityOnly.
func VerifyPackage(config VerifyPackageConfig) (VerifiedPackage, error) {
	// A native package is still selected by an exact kind/platform pair, so a
	// typo cannot be silently accepted as another platform's package.
	if err := delivery.ValidateKindPlatform(config.Kind, config.Platform); err != nil {
		return VerifiedPackage{}, err
	}
	expected := strings.ToLower(strings.TrimSpace(config.ExpectedSHA256))
	if len(expected) != 64 {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeInvalidArguments,
			"expected SHA-256 must be 64 hexadecimal characters",
		)
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeInvalidArguments,
			"expected SHA-256 must be 64 hexadecimal characters",
		)
	}
	if strings.TrimSpace(config.ArtifactPath) == "" {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeInvalidArguments,
			"artifact path is required",
		)
	}

	file, err := os.Open(config.ArtifactPath)
	if err != nil {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeInvalidArguments,
			fmt.Sprintf("open artifact: %v", err),
		)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeInvalidArguments,
			fmt.Sprintf("stat artifact: %v", err),
		)
	}
	if config.ExpectedSize > 0 && info.Size() != config.ExpectedSize {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeVerificationFailed,
			"artifact size does not match the expected size",
		)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeVerificationFailed,
			fmt.Sprintf("hash artifact: %v", err),
		)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return VerifiedPackage{}, delivery.NewError(
			delivery.CodeVerificationFailed,
			"artifact SHA-256 does not match the expected digest",
		)
	}
	return VerifiedPackage{
		Kind:          config.Kind,
		Platform:      config.Platform,
		Path:          config.ArtifactPath,
		SHA256:        actual,
		SizeBytes:     info.Size(),
		IntegrityOnly: true,
	}, nil
}
