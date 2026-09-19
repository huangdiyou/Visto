package media

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ManagedStore struct {
	root string
}

func NewManagedStore(root string) (*ManagedStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("managed rendition root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve rendition root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create rendition root: %w", err)
	}
	return &ManagedStore{root: filepath.Clean(absolute)}, nil
}

func (store *ManagedStore) Write(objectKey string, data []byte) error {
	target, err := store.resolve(objectKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create rendition directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".rendition-*")
	if err != nil {
		return fmt.Errorf("create temporary rendition: %w", err)
	}
	tempName := file.Name()
	defer os.Remove(tempName)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("protect temporary rendition: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write temporary rendition: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync temporary rendition: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary rendition: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("publish rendition: %w", err)
	}
	return nil
}

func (store *ManagedStore) Import(objectKey string, sourcePath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open rendition artifact: %w", err)
	}
	defer source.Close()
	target, err := store.resolve(objectKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create rendition directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".rendition-*")
	if err != nil {
		return fmt.Errorf("create temporary rendition: %w", err)
	}
	tempName := file.Name()
	defer os.Remove(tempName)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("protect temporary rendition: %w", err)
	}
	if _, err := io.Copy(file, source); err != nil {
		file.Close()
		return fmt.Errorf("copy rendition artifact: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync rendition artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close rendition artifact: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("publish rendition artifact: %w", err)
	}
	return nil
}

func (store *ManagedStore) Open(objectKey string) (*os.File, os.FileInfo, error) {
	target, err := store.resolve(objectKey)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, errors.New("rendition is not a regular file")
	}
	return file, info, nil
}

func (store *ManagedStore) CopyTo(objectKey string, writer io.Writer) error {
	file, _, err := store.Open(objectKey)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	return err
}

func (store *ManagedStore) resolve(objectKey string) (string, error) {
	if objectKey == "" || filepath.IsAbs(objectKey) ||
		strings.HasPrefix(objectKey, "/") ||
		strings.Contains(objectKey, "\\") {
		return "", errors.New("invalid rendition object key")
	}
	clean := filepath.Clean(filepath.FromSlash(objectKey))
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("rendition object key escapes root")
	}
	target := filepath.Join(store.root, clean)
	relative, err := filepath.Rel(store.root, target)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("rendition object key escapes root")
	}
	return target, nil
}
