package media

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"review-studio.local/core/internal/storage"
)

var safeSourceID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var safeSourceExtension = regexp.MustCompile(`^\.[A-Za-z0-9]{1,12}$`)

type ManagedSource struct {
	WorkspaceID string
	RootPath    string
	ObjectKey   string
	Observed    storage.ObservedFile
	Signature   []byte
	Cleanup     func()
	Release     func()
}

type ManagedSourceStore struct {
	rootPath string
}

func (store *ManagedSourceStore) stagingDirectory() (string, error) {
	if store == nil || strings.TrimSpace(store.rootPath) == "" {
		return "", fmt.Errorf("managed source store is unavailable")
	}
	directory := filepath.Join(store.rootPath, ".staging")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create managed source staging directory: %w", err)
	}
	return directory, nil
}

func (store *ManagedSourceStore) StagingDiskAvailableBytes() (*int64, error) {
	directory, err := store.stagingDirectory()
	if err != nil {
		return nil, err
	}
	available, err := storage.AvailableDiskBytes(directory)
	if err != nil {
		return nil, err
	}
	return &available, nil
}

func (store *ManagedSourceStore) StagingDiskReservation() (string, int64, error) {
	directory, err := store.stagingDirectory()
	if err != nil {
		return "", 0, err
	}
	return storage.StagingDiskReservation(directory)
}

func NewManagedSourceStore(rootPath string) (*ManagedSourceStore, error) {
	if strings.TrimSpace(rootPath) == "" {
		return nil, fmt.Errorf("managed source root is required")
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve managed source root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create managed source root: %w", err)
	}
	return &ManagedSourceStore{rootPath: absolute}, nil
}

func (store *ManagedSourceStore) Import(
	ctx context.Context,
	workspaceID string,
	assetID string,
	versionID string,
	filename string,
	reader io.Reader,
) (ManagedSource, error) {
	for _, value := range []string{workspaceID, assetID, versionID} {
		if !safeSourceID.MatchString(value) {
			return ManagedSource{}, fmt.Errorf("managed source identifier is invalid")
		}
	}

	extension := strings.ToLower(filepath.Ext(filename))
	if !safeSourceExtension.MatchString(extension) {
		extension = ""
	}
	rootPath := filepath.Join(store.rootPath, workspaceID)
	objectKey := path.Join(assetID, versionID, "source"+extension)
	directory := filepath.Join(rootPath, filepath.FromSlash(path.Dir(objectKey)))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ManagedSource{}, fmt.Errorf("create managed source directory: %w", err)
	}
	destination := filepath.Join(rootPath, filepath.FromSlash(objectKey))
	temporary, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return ManagedSource{}, fmt.Errorf("create managed source upload: %w", err)
	}
	temporaryName := temporary.Name()
	cleanupTemporary := func() { _ = os.Remove(temporaryName) }
	if err := copyWithContext(ctx, temporary, reader); err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("write managed source: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("sync managed source: %w", err)
	}
	if err := temporary.Close(); err != nil {
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("close managed source: %w", err)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("activate managed source: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(filepath.Join(rootPath, assetID, versionID))
	}
	localRoot, err := storage.NewLocalRoot(rootPath)
	if err != nil {
		cleanup()
		return ManagedSource{}, err
	}
	observed, err := localRoot.Observe(ctx, objectKey)
	if err != nil {
		cleanup()
		return ManagedSource{}, err
	}
	return ManagedSource{
		WorkspaceID: workspaceID,
		RootPath:    rootPath,
		ObjectKey:   objectKey,
		Observed:    observed,
		Cleanup:     cleanup,
	}, nil
}

func ImportSourceToAdapter(
	ctx context.Context,
	adapter storage.Adapter,
	stagingDirectory string,
	projectID string,
	assetID string,
	versionID string,
	filename string,
	mimeType string,
	reader io.Reader,
) (ManagedSource, error) {
	if adapter == nil {
		return ManagedSource{}, fmt.Errorf("managed source adapter is required")
	}
	for _, value := range []string{projectID, assetID, versionID} {
		if !safeSourceID.MatchString(value) {
			return ManagedSource{}, fmt.Errorf("managed source identifier is invalid")
		}
	}

	extension := strings.ToLower(filepath.Ext(filename))
	if !safeSourceExtension.MatchString(extension) {
		extension = ""
	}
	objectKey := path.Join("projects", projectID, assetID, versionID, "source"+extension)
	if strings.TrimSpace(stagingDirectory) == "" {
		return ManagedSource{}, fmt.Errorf("managed source staging directory is required")
	}
	if err := os.MkdirAll(stagingDirectory, 0o700); err != nil {
		return ManagedSource{}, fmt.Errorf("create managed source staging directory: %w", err)
	}
	temporary, err := os.CreateTemp(stagingDirectory, ".review-studio-upload-*")
	if err != nil {
		return ManagedSource{}, fmt.Errorf("create managed source staging file: %w", err)
	}
	temporaryName := temporary.Name()
	cleanupTemporary := func() { _ = os.Remove(temporaryName) }
	if err := copyWithContext(ctx, temporary, reader); err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("stage managed source: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("sync managed source staging file: %w", err)
	}
	signature, err := readSignatureFromReaderAt(ctx, temporary)
	if err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("read managed source signature: %w", err)
	}
	size, err := temporary.Seek(0, io.SeekEnd)
	if err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("measure managed source staging file: %w", err)
	}
	fingerprint, err := storage.QuickFingerprintReaderAt(
		ctx,
		temporary,
		size,
	)
	if err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("fingerprint managed source staging file: %w", err)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("rewind managed source staging file: %w", err)
	}
	info, err := adapter.Put(ctx, objectKey, temporary, size, mimeType)
	if err != nil {
		temporary.Close()
		cleanupTemporary()
		return ManagedSource{}, fmt.Errorf("write managed source to upload bucket: %w", err)
	}
	if err := temporary.Close(); err != nil {
		cleanupTemporary()
		_ = adapter.Delete(ctx, objectKey)
		return ManagedSource{}, fmt.Errorf("close managed source staging file: %w", err)
	}
	cleanupTemporary()

	if info.ObjectKey == "" {
		info.ObjectKey = objectKey
	}
	if info.SizeBytes == 0 {
		info.SizeBytes = size
	}
	if info.MIMEType == "" {
		info.MIMEType = mimeType
	}
	observed := storage.ObservedFile{
		ObjectKey:        info.ObjectKey,
		SizeBytes:        info.SizeBytes,
		ModifiedAt:       info.ModifiedAt,
		MIMEType:         info.MIMEType,
		QuickFingerprint: fingerprint,
	}
	return ManagedSource{
		ObjectKey: objectKey,
		Observed:  observed,
		Signature: signature,
		Cleanup: func() {
			_ = adapter.Delete(context.Background(), objectKey)
		},
	}, nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func readSignatureFromReaderAt(
	ctx context.Context,
	reader io.ReaderAt,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buffer := make([]byte, 512)
	read, err := reader.ReadAt(buffer, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if read == 0 {
		return nil, nil
	}
	return buffer[:read], nil
}
