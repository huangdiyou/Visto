package identity

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
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key := argon2.IDKey(
		[]byte(password),
		salt,
		argonTime,
		argonMemory,
		argonThreads,
		argonKeyLen,
	)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid password hash format")
	}

	versionText, ok := strings.CutPrefix(parts[2], "v=")
	if !ok {
		return false, errors.New("password hash version is missing")
	}
	version, err := strconv.Atoi(versionText)
	if err != nil || version != argon2.Version {
		return false, errors.New("unsupported password hash version")
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
		return false, fmt.Errorf("parse password hash parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode password salt: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode password key: %w", err)
	}

	if memory == 0 || iterations == 0 || threads == 0 || len(expected) == 0 {
		return false, errors.New("invalid password hash parameters")
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
