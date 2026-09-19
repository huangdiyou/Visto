package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"review-studio.local/core/internal/storage"
)

var ErrLibraryUploadTypeRejected = errors.New("media library upload type rejected")

const uploadSignatureBytes = 512

type UploadTypeError struct {
	Reason string
}

func (err *UploadTypeError) Error() string {
	if err == nil || err.Reason == "" {
		return ErrLibraryUploadTypeRejected.Error()
	}
	return ErrLibraryUploadTypeRejected.Error() + ": " + err.Reason
}

func (err *UploadTypeError) Unwrap() error {
	return ErrLibraryUploadTypeRejected
}

func (err *UploadTypeError) UserMessage() string {
	if err == nil {
		return "文件类型不允许上传到媒体库"
	}
	switch err.Reason {
	case "extension_magic_mismatch", "declared_mime_mismatch":
		return "文件内容与文件类型不一致"
	case "forbidden_extension", "forbidden_mime":
		return "文件类型不允许上传到媒体库"
	case "unsupported_extension", "unsupported_content":
		return "当前不支持这种文件类型"
	case "inspection_failed":
		return "无法完成文件类型检查"
	default:
		return "文件类型不允许上传到媒体库"
	}
}

type uploadTypeInspection struct {
	Filename     string
	DeclaredMIME string
	ObservedMIME string
	Source       ManagedSource
	Target       *UploadTarget
}

func (service *LibraryService) validateUploadSourceType(
	ctx context.Context,
	input uploadTypeInspection,
) (string, error) {
	extension := strings.ToLower(filepath.Ext(input.Filename))
	if forbiddenUploadExtension(extension) {
		return "", uploadTypeRejected("forbidden_extension")
	}
	extensionMIME := uploadMIMEForExtension(extension)
	if extension != "" && extensionMIME == "" {
		return "", uploadTypeRejected("unsupported_extension")
	}

	signature, err := service.readUploadSignature(ctx, input.Source, input.Target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", uploadTypeRejected("inspection_failed"), err)
	}
	signatureMIME := sniffUploadMIME(signature, extension)
	signatureFamily := uploadMIMEFamily(signatureMIME)
	if signatureFamily == "" {
		if extensionMIME != "" {
			return "", uploadTypeRejected("extension_magic_mismatch")
		}
		return "", uploadTypeRejected("unsupported_content")
	}
	if extensionMIME != "" &&
		uploadMIMEFamily(extensionMIME) != signatureFamily {
		return "", uploadTypeRejected("extension_magic_mismatch")
	}

	declaredMIME := normalizeUploadMIME(input.DeclaredMIME)
	if declaredMIME != "" && declaredMIME != "application/octet-stream" {
		if forbiddenUploadMIME(declaredMIME) {
			return "", uploadTypeRejected("forbidden_mime")
		}
		if uploadMIMEFamily(declaredMIME) != signatureFamily {
			return "", uploadTypeRejected("declared_mime_mismatch")
		}
	}

	observedMIME := normalizeUploadMIME(input.ObservedMIME)
	if observedMIME != "" && observedMIME != "application/octet-stream" {
		if forbiddenUploadMIME(observedMIME) {
			return "", uploadTypeRejected("forbidden_mime")
		}
		if family := uploadMIMEFamily(observedMIME); family != "" &&
			family != signatureFamily {
			return "", uploadTypeRejected("extension_magic_mismatch")
		}
	}

	return canonicalUploadMIME(extension, extensionMIME, signatureMIME), nil
}

func uploadTypeRejected(reason string) *UploadTypeError {
	return &UploadTypeError{Reason: reason}
}

func (service *LibraryService) readUploadSignature(
	ctx context.Context,
	source ManagedSource,
	target *UploadTarget,
) ([]byte, error) {
	if source.Observed.SizeBytes <= 0 {
		return nil, ErrLibraryUploadInvalid
	}
	length := source.Observed.SizeBytes
	if length > uploadSignatureBytes {
		length = uploadSignatureBytes
	}
	if len(source.Signature) > 0 {
		if int64(len(source.Signature)) > length {
			return source.Signature[:length], nil
		}
		return source.Signature, nil
	}

	var adapter storage.Adapter
	var err error
	if source.RootPath != "" {
		adapter, err = storage.NewLocalRoot(source.RootPath)
	} else {
		if target == nil || service.storage == nil {
			return nil, ErrLibraryUploadUnavailable
		}
		adapter, err = service.storage.Adapter(
			ctx,
			strings.TrimSpace(source.WorkspaceID),
			strings.TrimSpace(target.AuthorizedRootID),
		)
	}
	if err != nil {
		return nil, err
	}
	reader, _, err := adapter.OpenRange(
		ctx,
		source.ObjectKey,
		storage.ByteRange{Offset: 0, Length: length},
	)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	signature, err := io.ReadAll(io.LimitReader(reader, length))
	if err != nil {
		return nil, err
	}
	if len(signature) == 0 {
		return nil, ErrLibraryUploadInvalid
	}
	return signature, nil
}

func sniffUploadMIME(signature []byte, extension string) string {
	if len(signature) >= 12 && bytes.Equal(signature[:4], []byte("RIFF")) {
		switch string(signature[8:12]) {
		case "WEBP":
			return "image/webp"
		case "WAVE":
			return "audio/wav"
		case "AVI ":
			return "video/x-msvideo"
		}
	}
	if len(signature) >= 4 && bytes.Equal(signature[:4], []byte("OggS")) {
		if extension == ".ogv" {
			return "video/ogg"
		}
		return "audio/ogg"
	}
	if len(signature) >= 4 && bytes.Equal(signature[:4], []byte("fLaC")) {
		return "audio/flac"
	}
	if len(signature) >= 3 && bytes.Equal(signature[:3], []byte("ID3")) {
		return "audio/mpeg"
	}
	if len(signature) >= 2 && signature[0] == 0xff && signature[1]&0xe0 == 0xe0 {
		return "audio/mpeg"
	}
	if len(signature) >= 4 &&
		signature[0] == 0x1a &&
		signature[1] == 0x45 &&
		signature[2] == 0xdf &&
		signature[3] == 0xa3 {
		if extension == ".webm" {
			return "video/webm"
		}
		return "video/x-matroska"
	}
	if len(signature) >= 12 && string(signature[4:8]) == "ftyp" {
		brandText := string(signature[8:min(len(signature), 64)])
		switch {
		case strings.Contains(brandText, "avif") ||
			strings.Contains(brandText, "avis"):
			return "image/avif"
		case strings.Contains(brandText, "heic"):
			return "image/heic"
		case strings.Contains(brandText, "heif") ||
			strings.Contains(brandText, "mif1") ||
			strings.Contains(brandText, "msf1"):
			return "image/heif"
		case strings.Contains(brandText, "qt  "):
			return "video/quicktime"
		case extension == ".m4a":
			return "audio/mp4"
		default:
			return "video/mp4"
		}
	}
	return normalizeUploadMIME(http.DetectContentType(signature))
}

func normalizeUploadMIME(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	parsed, _, err := mime.ParseMediaType(value)
	if err == nil {
		return parsed
	}
	if before, _, ok := strings.Cut(value, ";"); ok {
		return strings.TrimSpace(before)
	}
	return value
}

func uploadMIMEForExtension(extension string) string {
	switch strings.ToLower(extension) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".avif":
		return "image/avif"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mpg", ".mpeg":
		return "video/mpeg"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".ogv":
		return "video/ogg"
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}

func uploadMIMEFamily(value string) string {
	value = normalizeUploadMIME(value)
	if forbiddenUploadMIME(value) {
		return ""
	}
	switch {
	case strings.HasPrefix(value, "image/"):
		return "image"
	case strings.HasPrefix(value, "video/"):
		return "video"
	case strings.HasPrefix(value, "audio/"):
		return "audio"
	case value == "application/pdf":
		return "pdf"
	default:
		return ""
	}
}

func forbiddenUploadExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".js", ".mjs",
		".jsx", ".ts", ".tsx", ".css", ".xml", ".json", ".exe",
		".dll", ".bat", ".cmd", ".ps1", ".vbs", ".msi", ".scr",
		".com", ".sh", ".app", ".jar", ".zip", ".rar", ".7z",
		".tar", ".gz", ".bz2", ".xz", ".iso", ".dmg":
		return true
	default:
		return false
	}
}

func forbiddenUploadMIME(value string) bool {
	value = normalizeUploadMIME(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "text/") {
		return true
	}
	switch value {
	case "image/svg+xml",
		"application/xhtml+xml",
		"application/javascript",
		"application/x-javascript",
		"application/ecmascript",
		"application/json",
		"application/xml",
		"application/x-msdownload",
		"application/x-msdos-program",
		"application/x-ms-installer",
		"application/java-archive",
		"application/zip",
		"application/x-7z-compressed",
		"application/vnd.rar",
		"application/x-rar-compressed",
		"application/x-tar",
		"application/gzip",
		"application/x-bzip2",
		"application/x-xz":
		return true
	default:
		return false
	}
}

func canonicalUploadMIME(
	extension string,
	extensionMIME string,
	signatureMIME string,
) string {
	signatureMIME = normalizeUploadMIME(signatureMIME)
	if extension == ".m4a" && uploadMIMEFamily(signatureMIME) == "video" {
		return "audio/mp4"
	}
	if extensionMIME != "" && uploadMIMEFamily(extensionMIME) == uploadMIMEFamily(signatureMIME) {
		switch extension {
		case ".mov":
			return "video/quicktime"
		case ".heic":
			return "image/heic"
		case ".heif":
			return "image/heif"
		case ".mkv":
			return "video/x-matroska"
		case ".ogv":
			return "video/ogg"
		case ".ogg", ".oga", ".opus":
			return "audio/ogg"
		}
	}
	return signatureMIME
}
