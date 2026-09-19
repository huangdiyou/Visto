package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type webDAVCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type WebDAVAdapter struct {
	endpoint    *url.URL
	basePath    string
	credentials webDAVCredentials
	client      *http.Client
	readClient  *http.Client
}

type webDAVMultiStatus struct {
	Responses []webDAVResponse `xml:"response"`
}

type webDAVResponse struct {
	Href      string           `xml:"href"`
	PropStats []webDAVPropStat `xml:"propstat"`
}

type webDAVPropStat struct {
	Status string     `xml:"status"`
	Prop   webDAVProp `xml:"prop"`
}

type webDAVProp struct {
	ResourceType  webDAVResourceType `xml:"resourcetype"`
	ContentLength string             `xml:"getcontentlength"`
	LastModified  string             `xml:"getlastmodified"`
	ContentType   string             `xml:"getcontenttype"`
	DisplayName   string             `xml:"displayname"`
}

type webDAVResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func NewWebDAVAdapter(
	ctx context.Context,
	config AdapterConfig,
) (*WebDAVAdapter, error) {
	endpointText := config.Config["endpoint"]
	allowPrivate := config.Config["allowPrivateNetwork"] == "true"
	endpoint, err := validateRemoteEndpoint(ctx, endpointText, allowPrivate)
	if err != nil {
		return nil, err
	}
	var credentials webDAVCredentials
	if len(config.Secret) > 0 {
		if err := json.Unmarshal(config.Secret, &credentials); err != nil {
			return nil, errors.New("webdav credentials are invalid")
		}
	}
	basePath, err := NormalizeObjectKey(config.Config["basePath"])
	if err != nil {
		return nil, err
	}
	if config.LocalPath != "" {
		rootPath, pathErr := NormalizeObjectKey(config.LocalPath)
		if pathErr != nil {
			return nil, pathErr
		}
		basePath = path.Join(basePath, rootPath)
		if basePath == "." {
			basePath = ""
		}
	}
	return &WebDAVAdapter{
		endpoint:    endpoint,
		basePath:    basePath,
		credentials: credentials,
		client:      newSafeHTTPClient(endpoint, allowPrivate),
		readClient:  newSafeReadHTTPClient(endpoint, allowPrivate),
	}, nil
}

func (adapter *WebDAVAdapter) Kind() string {
	return "webdav"
}

func (adapter *WebDAVAdapter) Capabilities() map[string]bool {
	return map[string]bool{
		"read":      true,
		"rangeRead": true,
		"list":      true,
		"write":     true,
		"move":      true,
		"copy":      true,
		"delete":    true,
	}
}

func (adapter *WebDAVAdapter) Stat(
	ctx context.Context,
	objectKey string,
) (FileInfo, error) {
	items, err := adapter.propFind(ctx, objectKey, "0")
	if err != nil {
		return FileInfo{}, err
	}
	if len(items) == 0 {
		return FileInfo{}, os.ErrNotExist
	}
	return items[0], nil
}

func (adapter *WebDAVAdapter) List(
	ctx context.Context,
	objectKey string,
) ([]DirectoryEntry, error) {
	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return nil, err
	}
	items, err := adapter.propFind(ctx, canonicalKey, "1")
	if err != nil {
		return nil, err
	}
	result := make([]DirectoryEntry, 0, len(items))
	for _, item := range items {
		if item.ObjectKey == canonicalKey {
			continue
		}
		parent := path.Dir(item.ObjectKey)
		if parent == "." {
			parent = ""
		}
		if parent != canonicalKey {
			continue
		}
		result = append(result, DirectoryEntry{
			ObjectKey:  item.ObjectKey,
			Name:       item.Name,
			Kind:       item.Kind,
			SizeBytes:  item.SizeBytes,
			ModifiedAt: item.ModifiedAt,
		})
	}
	return result, nil
}

func (adapter *WebDAVAdapter) OpenRange(
	ctx context.Context,
	objectKey string,
	byteRange ByteRange,
) (ObjectReader, FileInfo, error) {
	if byteRange.Offset < 0 || byteRange.Length < 0 {
		return nil, FileInfo{}, ErrRangeInvalid
	}
	info, err := adapter.Stat(ctx, objectKey)
	if err != nil {
		return nil, FileInfo{}, err
	}
	if info.Kind != "file" || byteRange.Offset > info.SizeBytes {
		return nil, FileInfo{}, ErrRangeInvalid
	}
	request, err := adapter.request(ctx, http.MethodGet, objectKey, nil)
	if err != nil {
		return nil, FileInfo{}, err
	}
	if byteRange.Offset > 0 || byteRange.Length > 0 {
		end := ""
		if byteRange.Length > 0 {
			end = strconv.FormatInt(
				byteRange.Offset+byteRange.Length-1,
				10,
			)
		}
		request.Header.Set(
			"Range",
			fmt.Sprintf("bytes=%d-%s", byteRange.Offset, end),
		)
	}
	response, err := adapter.readClient.Do(request)
	if err != nil {
		return nil, FileInfo{}, fmt.Errorf("read webdav object: %w", err)
	}
	if err := remoteStatusError(response); err != nil {
		response.Body.Close()
		return nil, FileInfo{}, err
	}
	if byteRange.Offset > 0 && response.StatusCode != http.StatusPartialContent {
		response.Body.Close()
		return nil, FileInfo{}, ErrCapabilityUnsupported
	}
	return response.Body, info, nil
}

func (adapter *WebDAVAdapter) Observe(
	ctx context.Context,
	objectKey string,
) (ObservedFile, error) {
	info, err := adapter.Stat(ctx, objectKey)
	if err != nil {
		return ObservedFile{}, err
	}
	fingerprint, err := fingerprintRemoteObject(ctx, adapter, info)
	if err != nil {
		return ObservedFile{}, err
	}
	return ObservedFile{
		ObjectKey:        info.ObjectKey,
		SizeBytes:        info.SizeBytes,
		ModifiedAt:       info.ModifiedAt,
		MIMEType:         info.MIMEType,
		QuickFingerprint: fingerprint,
	}, nil
}

func (adapter *WebDAVAdapter) Put(
	ctx context.Context,
	objectKey string,
	reader io.Reader,
	size int64,
	contentType string,
) (FileInfo, error) {
	if size < 0 {
		return FileInfo{}, ErrInvalidRootInput
	}
	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil || canonicalKey == "" {
		return FileInfo{}, ErrInvalidRootInput
	}
	if err := adapter.ensureParentCollections(ctx, canonicalKey); err != nil {
		return FileInfo{}, err
	}
	request, err := adapter.request(ctx, http.MethodPut, canonicalKey, reader)
	if err != nil {
		return FileInfo{}, err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", defaultMIME(contentType, canonicalKey))
	response, err := adapter.client.Do(request)
	if err != nil {
		return FileInfo{}, fmt.Errorf("upload webdav object: %w", err)
	}
	defer response.Body.Close()
	if err := remoteStatusError(response); err != nil {
		return FileInfo{}, err
	}
	return adapter.Stat(ctx, canonicalKey)
}

func (adapter *WebDAVAdapter) ensureParentCollections(
	ctx context.Context,
	objectKey string,
) error {
	parent := path.Dir(objectKey)
	if parent == "." || parent == "/" || parent == "" {
		return nil
	}
	current := ""
	for _, segment := range strings.Split(parent, "/") {
		if segment == "" {
			continue
		}
		if current == "" {
			current = segment
		} else {
			current = path.Join(current, segment)
		}
		request, err := adapter.request(ctx, "MKCOL", current, nil)
		if err != nil {
			return err
		}
		response, err := adapter.client.Do(request)
		if err != nil {
			return fmt.Errorf("create webdav collection: %w", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		switch response.StatusCode {
		case http.StatusOK, http.StatusCreated, http.StatusNoContent,
			http.StatusMethodNotAllowed:
			continue
		default:
			return remoteStatusError(response)
		}
	}
	return nil
}

func (adapter *WebDAVAdapter) Move(
	ctx context.Context,
	sourceKey string,
	destinationKey string,
) error {
	return adapter.copyOrMove(ctx, "MOVE", sourceKey, destinationKey)
}

func (adapter *WebDAVAdapter) Copy(
	ctx context.Context,
	sourceKey string,
	destinationKey string,
) error {
	return adapter.copyOrMove(ctx, "COPY", sourceKey, destinationKey)
}

func (adapter *WebDAVAdapter) Delete(
	ctx context.Context,
	objectKey string,
) error {
	request, err := adapter.request(ctx, http.MethodDelete, objectKey, nil)
	if err != nil {
		return err
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return fmt.Errorf("delete webdav object: %w", err)
	}
	defer response.Body.Close()
	return remoteStatusError(response)
}

func (adapter *WebDAVAdapter) ProbeCapabilities(
	ctx context.Context,
) (map[string]bool, []string, error) {
	request, err := adapter.request(ctx, http.MethodOptions, "", nil)
	if err != nil {
		return nil, nil, err
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("probe webdav options: %w", err)
	}
	defer response.Body.Close()
	if err := remoteStatusError(response); err != nil {
		return nil, nil, err
	}
	capabilities := adapter.Capabilities()
	allow := strings.ToUpper(response.Header.Get("Allow"))
	warnings := make([]string, 0)
	for method, capability := range map[string]string{
		"PUT": "write", "MOVE": "move", "COPY": "copy", "DELETE": "delete",
	} {
		if allow != "" && !strings.Contains(allow, method) {
			capabilities[capability] = false
			warnings = append(
				warnings,
				fmt.Sprintf("服务器未声明 %s 支持", method),
			)
		}
	}
	if strings.TrimSpace(response.Header.Get("DAV")) == "" {
		warnings = append(warnings, "服务器未返回 DAV 能力头")
	}
	if _, err := adapter.List(ctx, ""); err != nil {
		return nil, warnings, err
	}
	return capabilities, warnings, nil
}

func (adapter *WebDAVAdapter) propFind(
	ctx context.Context,
	objectKey string,
	depth string,
) ([]FileInfo, error) {
	body := strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/>
<d:getcontentlength/><d:getlastmodified/><d:getcontenttype/>
<d:displayname/></d:prop></d:propfind>`)
	request, err := adapter.request(ctx, "PROPFIND", objectKey, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Depth", depth)
	request.Header.Set("Content-Type", "application/xml; charset=utf-8")
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list webdav object: %w", err)
	}
	defer response.Body.Close()
	if err := remoteStatusError(response); err != nil {
		return nil, err
	}
	var multiStatus webDAVMultiStatus
	decoder := xml.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(&multiStatus); err != nil {
		return nil, fmt.Errorf("decode webdav response: %w", err)
	}
	items := make([]FileInfo, 0, len(multiStatus.Responses))
	for _, item := range multiStatus.Responses {
		info, ok, err := adapter.responseInfo(item)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, info)
		}
	}
	return items, nil
}

func (adapter *WebDAVAdapter) responseInfo(
	item webDAVResponse,
) (FileInfo, bool, error) {
	var prop *webDAVProp
	for index := range item.PropStats {
		if strings.Contains(item.PropStats[index].Status, " 200 ") {
			prop = &item.PropStats[index].Prop
			break
		}
	}
	if prop == nil {
		return FileInfo{}, false, nil
	}
	href, err := url.Parse(item.Href)
	if err != nil {
		return FileInfo{}, false, nil
	}
	hrefPath, err := url.PathUnescape(href.Path)
	if err != nil {
		return FileInfo{}, false, nil
	}
	rootPath := strings.TrimSuffix(adapter.rootURL("").Path, "/")
	relative := strings.TrimPrefix(strings.TrimSuffix(hrefPath, "/"), rootPath)
	relative = strings.TrimPrefix(relative, "/")
	objectKey, err := NormalizeObjectKey(relative)
	if err != nil {
		return FileInfo{}, false, nil
	}
	kind := "file"
	if prop.ResourceType.Collection != nil {
		kind = "directory"
	}
	size := int64(0)
	if prop.ContentLength != "" {
		size, _ = strconv.ParseInt(prop.ContentLength, 10, 64)
	}
	modifiedAt := time.Unix(0, 0).UTC()
	if prop.LastModified != "" {
		if value, parseErr := http.ParseTime(prop.LastModified); parseErr == nil {
			modifiedAt = value.UTC()
		}
	}
	name := strings.TrimSpace(prop.DisplayName)
	if name == "" {
		name = path.Base(objectKey)
	}
	mimeType := prop.ContentType
	if mimeType == "" && kind == "file" {
		mimeType = defaultMIME("", objectKey)
	}
	return FileInfo{
		ObjectKey: objectKey, Name: name, Kind: kind,
		SizeBytes: size, ModifiedAt: modifiedAt, MIMEType: mimeType,
	}, true, nil
}

func (adapter *WebDAVAdapter) copyOrMove(
	ctx context.Context,
	method string,
	sourceKey string,
	destinationKey string,
) error {
	request, err := adapter.request(ctx, method, sourceKey, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Destination", adapter.rootURL(destinationKey).String())
	request.Header.Set("Overwrite", "F")
	response, err := adapter.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s webdav object: %w", strings.ToLower(method), err)
	}
	defer response.Body.Close()
	return remoteStatusError(response)
}

func (adapter *WebDAVAdapter) request(
	ctx context.Context,
	method string,
	objectKey string,
	body io.Reader,
) (*http.Request, error) {
	if body != nil {
		body = nonClosingReader{Reader: body}
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		adapter.rootURL(objectKey).String(),
		body,
	)
	if err != nil {
		return nil, err
	}
	if adapter.credentials.Username != "" {
		request.SetBasicAuth(
			adapter.credentials.Username,
			adapter.credentials.Password,
		)
	}
	request.Header.Set("User-Agent", "Review-Studio/1")
	return request, nil
}

type nonClosingReader struct {
	io.Reader
}

func (adapter *WebDAVAdapter) rootURL(objectKey string) *url.URL {
	result := *adapter.endpoint
	segments := make([]string, 0)
	for _, value := range []string{adapter.basePath, objectKey} {
		for _, segment := range strings.Split(strings.Trim(value, "/"), "/") {
			if segment != "" {
				segments = append(segments, url.PathEscape(segment))
			}
		}
	}
	base := strings.TrimSuffix(adapter.endpoint.EscapedPath(), "/")
	if len(segments) > 0 {
		result.RawPath = base + "/" + strings.Join(segments, "/")
		result.Path, _ = url.PathUnescape(result.RawPath)
	} else {
		result.RawPath = base + "/"
		result.Path, _ = url.PathUnescape(result.RawPath)
	}
	return &result
}

func remoteStatusError(response *http.Response) error {
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden:
		return ErrAuthenticationFailed
	case response.StatusCode == http.StatusNotFound:
		return os.ErrNotExist
	case response.StatusCode == http.StatusTooManyRequests:
		return ErrRemoteRateLimited
	case response.StatusCode >= 500:
		return ErrRootUnavailable
	default:
		return fmt.Errorf(
			"remote storage returned HTTP %d",
			response.StatusCode,
		)
	}
}

func defaultMIME(value string, objectKey string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	value = mime.TypeByExtension(strings.ToLower(path.Ext(objectKey)))
	if value == "" {
		return "application/octet-stream"
	}
	return value
}

func fingerprintRemoteObject(
	ctx context.Context,
	adapter Adapter,
	info FileInfo,
) (string, error) {
	hash := sha256.New()
	var encodedSize [8]byte
	binary.BigEndian.PutUint64(encodedSize[:], uint64(info.SizeBytes))
	if _, err := hash.Write(encodedSize[:]); err != nil {
		return "", err
	}
	firstLength := min64(info.SizeBytes, fingerprintChunkBytes)
	if err := hashAdapterRange(
		ctx,
		hash,
		adapter,
		info.ObjectKey,
		0,
		firstLength,
	); err != nil {
		return "", err
	}
	if info.SizeBytes > firstLength {
		lastLength := min64(
			info.SizeBytes-firstLength,
			fingerprintChunkBytes,
		)
		if err := hashAdapterRange(
			ctx,
			hash,
			adapter,
			info.ObjectKey,
			info.SizeBytes-lastLength,
			lastLength,
		); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashAdapterRange(
	ctx context.Context,
	hash io.Writer,
	adapter Adapter,
	objectKey string,
	offset int64,
	length int64,
) error {
	if length == 0 {
		return nil
	}
	reader, _, err := adapter.OpenRange(
		ctx,
		objectKey,
		ByteRange{Offset: offset, Length: length},
	)
	if err != nil {
		return err
	}
	defer reader.Close()
	_, err = io.Copy(hash, io.LimitReader(reader, length))
	return err
}
