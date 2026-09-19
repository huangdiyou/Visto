package secretstore

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const keyBytes = 32

func LoadOrCreateKey(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("secret key path is required")
	}
	value, err := os.ReadFile(path)
	if err == nil {
		return decodeKey(value)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}
	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(key))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		value, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read concurrently created secret key: %w", readErr)
		}
		return decodeKey(value)
	}
	if err != nil {
		return nil, fmt.Errorf("create secret key: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write secret key: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close secret key: %w", err)
	}
	return key, nil
}

func decodeKey(value []byte) ([]byte, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(value)))
	if err != nil || len(key) != keyBytes {
		return nil, errors.New("secret key file is invalid")
	}
	return key, nil
}
