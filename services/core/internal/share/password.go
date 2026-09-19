package share

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMemory  = 19 * 1024
	passwordTime    = 2
	passwordThreads = 1
	passwordSaltLen = 16
	passwordKeyLen  = 32
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate share password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		passwordTime,
		passwordMemory,
		passwordThreads,
		passwordKeyLen,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		passwordMemory,
		passwordTime,
		passwordThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(encoded string, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid share password hash")
	}
	versionText, ok := strings.CutPrefix(parts[2], "v=")
	if !ok {
		return false, errors.New("share password version is missing")
	}
	version, err := strconv.Atoi(versionText)
	if err != nil || version != argon2.Version {
		return false, errors.New("unsupported share password version")
	}
	var memory uint32
	var iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&memory,
		&iterations,
		&threads,
	); err != nil {
		return false, fmt.Errorf("parse share password parameters: %w", err)
	}
	if memory < 8*1024 || memory > 64*1024 ||
		iterations < 1 || iterations > 10 ||
		threads < 1 || threads > 8 {
		return false, errors.New("share password parameters are invalid")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode share password salt: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode share password key: %w", err)
	}
	if len(salt) < 8 || len(salt) > 64 || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("share password hash length is invalid")
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		iterations,
		memory,
		threads,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
