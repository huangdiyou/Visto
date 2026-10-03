package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var windowsDrivePattern = regexp.MustCompile(`^[A-Za-z]:`)

type LocalRoot struct {
	rootPath string
	writable bool
}

type RangeReader struct {
	io.Reader
	file *os.File
}

const fingerprintChunkBytes = 64 * 1024

func (root *LocalRoot) Kind() string {
	return "local"
}

func (root *LocalRoot) Capabilities() map[string]bool {
	return map[string]bool{
		"read":      true,
		"rangeRead": true,
		"list":      true,
		"write":     root.writable,
		"move":      root.writable,
		"copy":      root.writable,
		"delete":    root.writable,
	}
}

func NewLocalRoot(rootPath string) (*LocalRoot, error) {
	return newLocalRoot(rootPath, false)
}

func newLocalRoot(rootPath string, writable bool) (*LocalRoot, error) {
	if strings.TrimSpace(rootPath) == "" {
		return nil, fmt.Errorf("%w: root path is required", ErrPathInvalid)
	}

	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve root path: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRootUnavailable
		}
		return nil, fmt.Errorf("resolve root symlinks: %w", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return nil, fmt.Errorf("stat root path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: root must be a directory", ErrPathInvalid)
	}

	fsRoot, err := os.OpenRoot(realPath)
	if err != nil {
		return nil, fmt.Errorf("open local root: %w", err)
	}
	if err := fsRoot.Close(); err != nil {
		return nil, fmt.Errorf("close local root validation handle: %w", err)
	}

	return &LocalRoot{
		rootPath: filepath.Clean(realPath),
		writable: writable,
	}, nil
}

func NormalizeObjectKey(value string) (string, error) {
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("%w: null byte", ErrPathInvalid)
	}
	if strings.Contains(value, "\\") {
		return "", fmt.Errorf("%w: backslashes are not accepted", ErrPathInvalid)
	}
	if strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "//") ||
		windowsDrivePattern.MatchString(value) ||
		filepath.VolumeName(value) != "" {
		return "", fmt.Errorf("%w: absolute path", ErrPathInvalid)
	}

	cleaned := path.Clean(value)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrPathEscapesRoot
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: invalid segment", ErrPathInvalid)
		}
		for _, character := range segment {
			if character < 0x20 || character == 0x7f {
				return "", fmt.Errorf("%w: control character", ErrPathInvalid)
			}
		}
	}
	return cleaned, nil
}

func (root *LocalRoot) Stat(
	ctx context.Context,
	objectKey string,
) (FileInfo, error) {
	file, canonicalKey, err := root.open(ctx, objectKey)
	if err != nil {
		return FileInfo{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return FileInfo{}, fmt.Errorf("stat opened file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return FileInfo{}, ErrNotRegularFile
	}

	mimeType, err := detectMIME(file, canonicalKey)
	if err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		ObjectKey:  canonicalKey,
		Name:       path.Base(canonicalKey),
		Kind:       "file",
		SizeBytes:  info.Size(),
		ModifiedAt: info.ModTime().UTC(),
		MIMEType:   mimeType,
	}, nil
}

func (root *LocalRoot) List(
	ctx context.Context,
	objectKey string,
) ([]DirectoryEntry, error) {
	directory, canonicalKey, err := root.openDirectory(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	defer directory.Close()

	// Read at most one entry beyond the public limit. ReadDir(-1) materialises
	// the entire directory before the limit check and therefore does not bound
	// memory for a hostile or accidentally enormous authorized directory.
	entries, err := directory.ReadDir(maxDirectoryListingEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("list local directory: %w", err)
	}
	if len(entries) > maxDirectoryListingEntries {
		return nil, ErrScanLimitExceeded
	}

	result := make([]DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("read directory entry %q: %w", entry.Name(), err)
		}

		kind := "file"
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			kind = "symlink"
		case entry.IsDir():
			kind = "directory"
		case !info.Mode().IsRegular():
			kind = "other"
		}

		entryKey := entry.Name()
		if canonicalKey != "" {
			entryKey = path.Join(canonicalKey, entry.Name())
		}
		result = append(result, DirectoryEntry{
			ObjectKey:  entryKey,
			Name:       entry.Name(),
			Kind:       kind,
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime().UTC(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return result[i].Name < result[j].Name
		}
		if result[i].Kind == "directory" {
			return true
		}
		if result[j].Kind == "directory" {
			return false
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (root *LocalRoot) OpenRange(
	ctx context.Context,
	objectKey string,
	byteRange ByteRange,
) (ObjectReader, FileInfo, error) {
	if byteRange.Offset < 0 || byteRange.Length < 0 {
		return nil, FileInfo{}, ErrRangeInvalid
	}

	file, canonicalKey, err := root.open(ctx, objectKey)
	if err != nil {
		return nil, FileInfo{}, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, FileInfo{}, fmt.Errorf("stat opened file: %w", err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, FileInfo{}, ErrNotRegularFile
	}
	if byteRange.Offset > info.Size() {
		file.Close()
		return nil, FileInfo{}, ErrRangeInvalid
	}

	length := byteRange.Length
	if length == 0 || length > info.Size()-byteRange.Offset {
		length = info.Size() - byteRange.Offset
	}

	mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(canonicalKey)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return &RangeReader{
			Reader: io.NewSectionReader(file, byteRange.Offset, length),
			file:   file,
		}, FileInfo{
			ObjectKey:  canonicalKey,
			Name:       path.Base(canonicalKey),
			Kind:       "file",
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime().UTC(),
			MIMEType:   mimeType,
		}, nil
}

func (root *LocalRoot) Observe(
	ctx context.Context,
	objectKey string,
) (ObservedFile, error) {
	file, canonicalKey, err := root.open(ctx, objectKey)
	if err != nil {
		return ObservedFile{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ObservedFile{}, fmt.Errorf("stat observed file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ObservedFile{}, ErrNotRegularFile
	}
	mimeType, err := detectMIME(file, canonicalKey)
	if err != nil {
		return ObservedFile{}, err
	}
	fingerprint, err := fingerprintFile(ctx, file, info.Size())
	if err != nil {
		return ObservedFile{}, err
	}

	return ObservedFile{
		ObjectKey:        canonicalKey,
		SizeBytes:        info.Size(),
		ModifiedAt:       info.ModTime().UTC(),
		MIMEType:         mimeType,
		QuickFingerprint: fingerprint,
	}, nil
}

func (root *LocalRoot) Put(
	ctx context.Context,
	objectKey string,
	reader io.Reader,
	size int64,
	_ string,
) (FileInfo, error) {
	if !root.writable {
		return FileInfo{}, ErrCapabilityUnsupported
	}
	if size < 0 {
		return FileInfo{}, ErrInvalidRootInput
	}
	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return FileInfo{}, err
	}
	if canonicalKey == "" {
		return FileInfo{}, ErrPathInvalid
	}
	fsRoot, err := root.openFSRoot()
	if err != nil {
		return FileInfo{}, err
	}
	defer fsRoot.Close()
	if err := fsRoot.MkdirAll(filepath.FromSlash(path.Dir(canonicalKey)), 0o700); err != nil {
		return FileInfo{}, fmt.Errorf("create local object directory: %w", err)
	}
	tempKey, err := localTemporaryObjectKey(canonicalKey)
	if err != nil {
		return FileInfo{}, err
	}
	file, err := fsRoot.OpenFile(
		filepath.FromSlash(tempKey),
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return FileInfo{}, fmt.Errorf("create local upload: %w", err)
	}
	defer fsRoot.Remove(filepath.FromSlash(tempKey))
	written, copyErr := io.Copy(file, io.LimitReader(reader, size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return FileInfo{}, fmt.Errorf("write local upload: %w", copyErr)
	}
	if closeErr != nil {
		return FileInfo{}, fmt.Errorf("close local upload: %w", closeErr)
	}
	if written != size {
		return FileInfo{}, fmt.Errorf(
			"%w: expected %d bytes, received %d",
			ErrInvalidRootInput,
			size,
			written,
		)
	}
	if err := fsRoot.Rename(
		filepath.FromSlash(tempKey),
		filepath.FromSlash(canonicalKey),
	); err != nil {
		return FileInfo{}, fmt.Errorf("commit local upload: %w", err)
	}
	return root.Stat(ctx, canonicalKey)
}

func (root *LocalRoot) Move(
	_ context.Context,
	sourceKey string,
	destinationKey string,
) error {
	if !root.writable {
		return ErrCapabilityUnsupported
	}
	source, err := NormalizeObjectKey(sourceKey)
	if err != nil {
		return err
	}
	destination, err := NormalizeObjectKey(destinationKey)
	if err != nil {
		return err
	}
	if source == "" || destination == "" {
		return ErrPathInvalid
	}
	fsRoot, err := root.openFSRoot()
	if err != nil {
		return err
	}
	defer fsRoot.Close()
	if err := fsRoot.MkdirAll(filepath.FromSlash(path.Dir(destination)), 0o700); err != nil {
		return fmt.Errorf("create local object directory: %w", err)
	}
	if err := fsRoot.Rename(
		filepath.FromSlash(source),
		filepath.FromSlash(destination),
	); err != nil {
		return fmt.Errorf("move local object: %w", err)
	}
	return nil
}

func (root *LocalRoot) Copy(
	ctx context.Context,
	sourceKey string,
	destinationKey string,
) error {
	if !root.writable {
		return ErrCapabilityUnsupported
	}
	source, _, err := root.open(ctx, sourceKey)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat copy source: %w", err)
	}
	_, err = root.Put(
		ctx,
		destinationKey,
		source,
		info.Size(),
		"application/octet-stream",
	)
	return err
}

func (root *LocalRoot) Delete(
	_ context.Context,
	objectKey string,
) error {
	if !root.writable {
		return ErrCapabilityUnsupported
	}
	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return err
	}
	if canonicalKey == "" {
		return ErrNotRegularFile
	}
	fsRoot, err := root.openFSRoot()
	if err != nil {
		return err
	}
	defer fsRoot.Close()
	if err := fsRoot.Remove(filepath.FromSlash(canonicalKey)); err != nil {
		return fmt.Errorf("delete local object: %w", err)
	}
	return nil
}

func (reader *RangeReader) Close() error {
	return reader.file.Close()
}

func (reader *RangeReader) File() *os.File {
	return reader.file
}

func (root *LocalRoot) open(
	ctx context.Context,
	objectKey string,
) (*os.File, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return nil, "", err
	}
	if canonicalKey == "" {
		return nil, "", ErrNotRegularFile
	}

	fsRoot, err := root.openFSRoot()
	if err != nil {
		return nil, "", err
	}
	file, err := fsRoot.Open(filepath.FromSlash(canonicalKey))
	if err != nil {
		fsRoot.Close()
		return nil, "", err
	}
	if err := fsRoot.Close(); err != nil {
		file.Close()
		return nil, "", fmt.Errorf("close local root handle: %w", err)
	}
	return file, canonicalKey, nil
}

func (root *LocalRoot) openDirectory(
	ctx context.Context,
	objectKey string,
) (*os.File, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return nil, "", err
	}

	fsRoot, err := root.openFSRoot()
	if err != nil {
		return nil, "", err
	}
	entry := "."
	if canonicalKey != "" {
		entry = filepath.FromSlash(canonicalKey)
	}
	directory, err := fsRoot.Open(entry)
	if err != nil {
		fsRoot.Close()
		return nil, "", err
	}
	if err := fsRoot.Close(); err != nil {
		directory.Close()
		return nil, "", fmt.Errorf("close local root handle: %w", err)
	}
	info, err := directory.Stat()
	if err != nil {
		directory.Close()
		return nil, "", fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		directory.Close()
		return nil, "", fmt.Errorf("%w: path is not a directory", ErrPathInvalid)
	}
	return directory, canonicalKey, nil
}

func localTemporaryObjectKey(canonicalKey string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate local upload name: %w", err)
	}
	return path.Join(path.Dir(canonicalKey), ".review-studio-"+hex.EncodeToString(random[:])), nil
}

func (root *LocalRoot) openFSRoot() (*os.Root, error) {
	fsRoot, err := os.OpenRoot(root.rootPath)
	if err != nil {
		return nil, fmt.Errorf("open local root: %w", err)
	}
	return fsRoot, nil
}

func pathWithin(rootPath, candidate string) bool {
	relative, err := filepath.Rel(rootPath, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func detectMIME(file *os.File, objectKey string) (string, error) {
	mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(objectKey)))
	if mimeType != "" {
		return mimeType, nil
	}

	buffer := make([]byte, 512)
	read, err := file.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read file signature: %w", err)
	}
	if read == 0 {
		return "application/octet-stream", nil
	}
	return http.DetectContentType(buffer[:read]), nil
}

func fingerprintFile(
	ctx context.Context,
	file *os.File,
	size int64,
) (string, error) {
	return QuickFingerprintReaderAt(ctx, file, size)
}

func QuickFingerprintReaderAt(
	ctx context.Context,
	reader io.ReaderAt,
	size int64,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if size < 0 {
		return "", ErrInvalidRootInput
	}

	hash := sha256.New()
	var encodedSize [8]byte
	binary.BigEndian.PutUint64(encodedSize[:], uint64(size))
	if _, err := hash.Write(encodedSize[:]); err != nil {
		return "", err
	}

	firstLength := min64(size, fingerprintChunkBytes)
	if err := hashReaderAtRange(ctx, hash, reader, 0, firstLength); err != nil {
		return "", err
	}
	if size > firstLength {
		lastLength := min64(size-firstLength, fingerprintChunkBytes)
		if err := hashReaderAtRange(
			ctx,
			hash,
			reader,
			size-lastLength,
			lastLength,
		); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashReaderAtRange(
	ctx context.Context,
	hash io.Writer,
	readerAt io.ReaderAt,
	offset int64,
	length int64,
) error {
	buffer := make([]byte, 32*1024)
	reader := io.NewSectionReader(readerAt, offset, length)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, err := reader.Read(buffer)
		if read > 0 {
			if _, writeErr := hash.Write(buffer[:read]); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read fingerprint range: %w", err)
		}
	}
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
