package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	s3MultipartThreshold = int64(16 << 20)
	s3PartSize           = int64(8 << 20)
)

var ErrS3PaginationStalled = errors.New("s3 pagination token did not advance")

type s3Credentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

type S3Adapter struct {
	endpoint    *url.URL
	requestBase *url.URL
	region      string
	bucket      string
	basePath    string
	pathStyle   bool
	credentials s3Credentials
	client      *http.Client
	clock       func() time.Time
}

type s3ListResult struct {
	Contents       []s3Object       `xml:"Contents"`
	CommonPrefixes []s3CommonPrefix `xml:"CommonPrefixes"`
	IsTruncated    bool             `xml:"IsTruncated"`
	NextToken      string           `xml:"NextContinuationToken"`
}

type s3Object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

type s3CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type s3MultipartStart struct {
	UploadID string `xml:"UploadId"`
}

type s3CompletedUpload struct {
	XMLName xml.Name          `xml:"CompleteMultipartUpload"`
	Parts   []s3CompletedPart `xml:"Part"`
}

type s3CompletedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type s3ErrorResponse struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func NewS3Adapter(
	ctx context.Context,
	config AdapterConfig,
) (*S3Adapter, error) {
	allowPrivate := config.Config["allowPrivateNetwork"] == "true"
	endpoint, err := validateRemoteEndpoint(
		ctx,
		config.Config["endpoint"],
		allowPrivate,
	)
	if err != nil {
		return nil, err
	}
	var credentials s3Credentials
	if err := json.Unmarshal(config.Secret, &credentials); err != nil ||
		strings.TrimSpace(credentials.AccessKeyID) == "" ||
		credentials.SecretAccessKey == "" {
		return nil, errors.New("s3 credentials are invalid")
	}
	bucket := strings.TrimSpace(config.Config["bucket"])
	region := strings.TrimSpace(config.Config["region"])
	if bucket == "" || region == "" {
		return nil, ErrInvalidRootInput
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
	pathStyle := config.Config["pathStyle"] == "true"
	requestBase := *endpoint
	if !pathStyle {
		requestBase.Host = bucket + "." + endpoint.Host
		if err := validateRemoteHost(
			ctx,
			requestBase.Hostname(),
			allowPrivate,
		); err != nil {
			return nil, err
		}
	}
	return &S3Adapter{
		endpoint: endpoint, requestBase: &requestBase,
		region: region, bucket: bucket, basePath: basePath,
		pathStyle: pathStyle, credentials: credentials,
		client: newSafeHTTPClient(&requestBase, allowPrivate),
		clock:  time.Now,
	}, nil
}

func (adapter *S3Adapter) Kind() string {
	return "s3"
}

func (adapter *S3Adapter) Capabilities() map[string]bool {
	return map[string]bool{
		"read": true, "rangeRead": true, "list": true, "write": true,
		"move": true, "copy": true, "delete": true, "multipartUpload": true,
	}
}

func (adapter *S3Adapter) Stat(
	ctx context.Context,
	objectKey string,
) (FileInfo, error) {
	key, err := adapter.fullKey(objectKey)
	if err != nil {
		return FileInfo{}, err
	}
	request, err := adapter.newRequest(
		ctx,
		http.MethodHead,
		key,
		nil,
		nil,
		emptySHA256,
	)
	if err != nil {
		return FileInfo{}, err
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return FileInfo{}, fmt.Errorf("stat s3 object: %w", err)
	}
	defer response.Body.Close()
	if err := s3StatusError(response); err != nil {
		return FileInfo{}, err
	}
	size, _ := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64)
	modifiedAt := time.Unix(0, 0).UTC()
	if value := response.Header.Get("Last-Modified"); value != "" {
		if parsed, parseErr := http.ParseTime(value); parseErr == nil {
			modifiedAt = parsed.UTC()
		}
	}
	canonicalKey, _ := NormalizeObjectKey(objectKey)
	return FileInfo{
		ObjectKey: canonicalKey, Name: path.Base(canonicalKey), Kind: "file",
		SizeBytes: size, ModifiedAt: modifiedAt,
		MIMEType: defaultMIME(response.Header.Get("Content-Type"), canonicalKey),
	}, nil
}

func (adapter *S3Adapter) List(
	ctx context.Context,
	objectKey string,
) ([]DirectoryEntry, error) {
	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return nil, err
	}
	prefix := adapter.basePath
	if canonicalKey != "" {
		prefix = path.Join(prefix, canonicalKey)
	}
	if prefix != "" {
		prefix += "/"
	}
	result := make([]DirectoryEntry, 0)
	token := ""
	seenTokens := make(map[string]struct{})
	for {
		query := url.Values{
			"list-type": {"2"},
			"delimiter": {"/"},
			"prefix":    {prefix},
		}
		if token != "" {
			query.Set("continuation-token", token)
		}
		request, err := adapter.newRequest(
			ctx,
			http.MethodGet,
			"",
			query,
			nil,
			emptySHA256,
		)
		if err != nil {
			return nil, err
		}
		response, err := adapter.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("list s3 objects: %w", err)
		}
		if err := s3StatusError(response); err != nil {
			response.Body.Close()
			return nil, err
		}
		var page s3ListResult
		err = xml.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&page)
		response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode s3 object list: %w", err)
		}
		for _, directory := range page.CommonPrefixes {
			key := strings.TrimSuffix(
				strings.TrimPrefix(directory.Prefix, adapter.prefixWithSlash()),
				"/",
			)
			key, err = NormalizeObjectKey(key)
			if err == nil && key != "" {
				result = append(result, DirectoryEntry{
					ObjectKey: key, Name: path.Base(key), Kind: "directory",
				})
			}
		}
		for _, object := range page.Contents {
			key := strings.TrimPrefix(object.Key, adapter.prefixWithSlash())
			key, err = NormalizeObjectKey(key)
			if err != nil || key == "" {
				continue
			}
			modifiedAt, _ := time.Parse(time.RFC3339, object.LastModified)
			result = append(result, DirectoryEntry{
				ObjectKey: key, Name: path.Base(key), Kind: "file",
				SizeBytes: object.Size, ModifiedAt: modifiedAt.UTC(),
			})
		}
		if len(result) > maxDirectoryListingEntries {
			return nil, ErrScanLimitExceeded
		}
		if !page.IsTruncated {
			break
		}
		nextToken := strings.TrimSpace(page.NextToken)
		if nextToken == "" || nextToken == token {
			return nil, ErrS3PaginationStalled
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, ErrS3PaginationStalled
		}
		seenTokens[nextToken] = struct{}{}
		token = nextToken
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return result[i].Name < result[j].Name
		}
		return result[i].Kind == "directory"
	})
	return result, nil
}

func (adapter *S3Adapter) OpenRange(
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
	if byteRange.Offset > info.SizeBytes {
		return nil, FileInfo{}, ErrRangeInvalid
	}
	key, _ := adapter.fullKey(objectKey)
	request, err := adapter.newRequest(
		ctx,
		http.MethodGet,
		key,
		nil,
		nil,
		emptySHA256,
	)
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
		adapter.sign(request, emptySHA256)
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, FileInfo{}, fmt.Errorf("read s3 object: %w", err)
	}
	if err := s3StatusError(response); err != nil {
		response.Body.Close()
		return nil, FileInfo{}, err
	}
	return response.Body, info, nil
}

func (adapter *S3Adapter) Observe(
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
		ObjectKey: info.ObjectKey, SizeBytes: info.SizeBytes,
		ModifiedAt: info.ModifiedAt, MIMEType: info.MIMEType,
		QuickFingerprint: fingerprint,
	}, nil
}

func (adapter *S3Adapter) Put(
	ctx context.Context,
	objectKey string,
	reader io.Reader,
	size int64,
	contentType string,
) (FileInfo, error) {
	if size < 0 {
		return FileInfo{}, ErrInvalidRootInput
	}
	if size >= s3MultipartThreshold {
		if err := adapter.multipartPut(
			ctx,
			objectKey,
			reader,
			size,
			contentType,
		); err != nil {
			return FileInfo{}, err
		}
		return adapter.Stat(ctx, objectKey)
	}
	payload, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return FileInfo{}, fmt.Errorf("read s3 upload: %w", err)
	}
	if int64(len(payload)) != size {
		return FileInfo{}, ErrInvalidRootInput
	}
	key, err := adapter.fullKey(objectKey)
	if err != nil {
		return FileInfo{}, err
	}
	payloadHash := sha256Hex(payload)
	request, err := adapter.newRequest(
		ctx,
		http.MethodPut,
		key,
		nil,
		bytes.NewReader(payload),
		payloadHash,
	)
	if err != nil {
		return FileInfo{}, err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", defaultMIME(contentType, objectKey))
	adapter.sign(request, payloadHash)
	response, err := adapter.client.Do(request)
	if err != nil {
		return FileInfo{}, fmt.Errorf("upload s3 object: %w", err)
	}
	defer response.Body.Close()
	if err := s3StatusError(response); err != nil {
		return FileInfo{}, err
	}
	return adapter.Stat(ctx, objectKey)
}

func (adapter *S3Adapter) Move(
	ctx context.Context,
	sourceKey string,
	destinationKey string,
) error {
	if err := adapter.Copy(ctx, sourceKey, destinationKey); err != nil {
		return err
	}
	return adapter.Delete(ctx, sourceKey)
}

func (adapter *S3Adapter) Copy(
	ctx context.Context,
	sourceKey string,
	destinationKey string,
) error {
	source, err := adapter.fullKey(sourceKey)
	if err != nil {
		return err
	}
	destination, err := adapter.fullKey(destinationKey)
	if err != nil {
		return err
	}
	request, err := adapter.newRequest(
		ctx,
		http.MethodPut,
		destination,
		nil,
		nil,
		emptySHA256,
	)
	if err != nil {
		return err
	}
	request.Header.Set(
		"x-amz-copy-source",
		"/"+adapter.bucket+"/"+escapeS3Path(source),
	)
	adapter.sign(request, emptySHA256)
	response, err := adapter.client.Do(request)
	if err != nil {
		return fmt.Errorf("copy s3 object: %w", err)
	}
	defer response.Body.Close()
	return s3StatusError(response)
}

func (adapter *S3Adapter) Delete(
	ctx context.Context,
	objectKey string,
) error {
	key, err := adapter.fullKey(objectKey)
	if err != nil {
		return err
	}
	request, err := adapter.newRequest(
		ctx,
		http.MethodDelete,
		key,
		nil,
		nil,
		emptySHA256,
	)
	if err != nil {
		return err
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return fmt.Errorf("delete s3 object: %w", err)
	}
	defer response.Body.Close()
	return s3StatusError(response)
}

func (adapter *S3Adapter) ProbeCapabilities(
	ctx context.Context,
) (map[string]bool, []string, error) {
	if _, err := adapter.List(ctx, ""); err != nil {
		return nil, nil, err
	}
	return adapter.Capabilities(), nil, nil
}

func (adapter *S3Adapter) multipartPut(
	ctx context.Context,
	objectKey string,
	reader io.Reader,
	size int64,
	contentType string,
) error {
	key, err := adapter.fullKey(objectKey)
	if err != nil {
		return err
	}
	startRequest, err := adapter.newRequest(
		ctx,
		http.MethodPost,
		key,
		url.Values{"uploads": {""}},
		nil,
		emptySHA256,
	)
	if err != nil {
		return err
	}
	startResponse, err := adapter.client.Do(startRequest)
	if err != nil {
		return fmt.Errorf("start s3 multipart upload: %w", err)
	}
	if err := s3StatusError(startResponse); err != nil {
		startResponse.Body.Close()
		return err
	}
	var started s3MultipartStart
	err = xml.NewDecoder(
		io.LimitReader(startResponse.Body, 1<<20),
	).Decode(&started)
	startResponse.Body.Close()
	if err != nil || started.UploadID == "" {
		return errors.New("s3 multipart upload id is missing")
	}
	abort := func() {
		request, requestErr := adapter.newRequest(
			context.WithoutCancel(ctx),
			http.MethodDelete,
			key,
			url.Values{"uploadId": {started.UploadID}},
			nil,
			emptySHA256,
		)
		if requestErr == nil {
			response, requestErr := adapter.client.Do(request)
			if requestErr == nil {
				response.Body.Close()
			}
		}
	}
	parts := make([]s3CompletedPart, 0, (size+s3PartSize-1)/s3PartSize)
	remaining := size
	for partNumber := 1; remaining > 0; partNumber++ {
		partLength := min64(remaining, s3PartSize)
		partData, err := io.ReadAll(io.LimitReader(reader, partLength))
		if err != nil || int64(len(partData)) != partLength {
			abort()
			return ErrInvalidRootInput
		}
		payloadHash := sha256Hex(partData)
		request, err := adapter.newRequest(
			ctx,
			http.MethodPut,
			key,
			url.Values{
				"partNumber": {strconv.Itoa(partNumber)},
				"uploadId":   {started.UploadID},
			},
			bytes.NewReader(partData),
			payloadHash,
		)
		if err != nil {
			abort()
			return err
		}
		request.ContentLength = partLength
		response, err := adapter.client.Do(request)
		if err != nil {
			abort()
			return fmt.Errorf("upload s3 multipart part: %w", err)
		}
		if err := s3StatusError(response); err != nil {
			response.Body.Close()
			abort()
			return err
		}
		etag := response.Header.Get("ETag")
		response.Body.Close()
		if etag == "" {
			abort()
			return errors.New("s3 multipart part etag is missing")
		}
		parts = append(parts, s3CompletedPart{
			PartNumber: partNumber,
			ETag:       etag,
		})
		remaining -= partLength
	}
	body, err := xml.Marshal(s3CompletedUpload{Parts: parts})
	if err != nil {
		abort()
		return err
	}
	payloadHash := sha256Hex(body)
	completeRequest, err := adapter.newRequest(
		ctx,
		http.MethodPost,
		key,
		url.Values{"uploadId": {started.UploadID}},
		bytes.NewReader(body),
		payloadHash,
	)
	if err != nil {
		abort()
		return err
	}
	completeRequest.ContentLength = int64(len(body))
	completeRequest.Header.Set("Content-Type", "application/xml")
	adapter.sign(completeRequest, payloadHash)
	completeResponse, err := adapter.client.Do(completeRequest)
	if err != nil {
		abort()
		return fmt.Errorf("complete s3 multipart upload: %w", err)
	}
	defer completeResponse.Body.Close()
	if err := s3StatusError(completeResponse); err != nil {
		abort()
		return err
	}
	return nil
}

func (adapter *S3Adapter) fullKey(objectKey string) (string, error) {
	canonicalKey, err := NormalizeObjectKey(objectKey)
	if err != nil {
		return "", err
	}
	return path.Join(adapter.basePath, canonicalKey), nil
}

func (adapter *S3Adapter) prefixWithSlash() string {
	if adapter.basePath == "" {
		return ""
	}
	return adapter.basePath + "/"
}

func (adapter *S3Adapter) newRequest(
	ctx context.Context,
	method string,
	key string,
	query url.Values,
	body io.Reader,
	payloadHash string,
) (*http.Request, error) {
	requestURL := *adapter.requestBase
	segments := make([]string, 0)
	if adapter.pathStyle {
		segments = append(segments, adapter.bucket)
	}
	if key != "" {
		segments = append(segments, strings.Split(key, "/")...)
	}
	basePath := strings.TrimSuffix(adapter.requestBase.EscapedPath(), "/")
	if len(segments) > 0 {
		requestURL.RawPath = basePath + "/" + escapeS3Path(
			strings.Join(segments, "/"),
		)
		requestURL.Path, _ = url.PathUnescape(requestURL.RawPath)
	} else {
		requestURL.RawPath = basePath + "/"
		requestURL.Path, _ = url.PathUnescape(requestURL.RawPath)
	}
	requestURL.RawQuery = canonicalS3Query(query)
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		requestURL.String(),
		body,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Review-Studio/1")
	adapter.sign(request, payloadHash)
	return request, nil
}

func (adapter *S3Adapter) sign(request *http.Request, payloadHash string) {
	now := adapter.clock().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	request.Header.Set("x-amz-date", amzDate)
	request.Header.Set("x-amz-content-sha256", payloadHash)

	headerNames := []string{"host"}
	headers := map[string]string{
		"host": strings.TrimSpace(request.Host),
	}
	if headers["host"] == "" {
		headers["host"] = request.URL.Host
	}
	for name, values := range request.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") {
			headers[lower] = strings.Join(values, ",")
			if lower != "x-amz-date" && lower != "x-amz-content-sha256" {
				headerNames = append(headerNames, lower)
			}
		}
	}
	headerNames = append(headerNames, "x-amz-content-sha256", "x-amz-date")
	sort.Strings(headerNames)
	canonicalHeaders := strings.Builder{}
	for _, name := range headerNames {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(headers[name]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")
	canonicalRequest := strings.Join([]string{
		request.Method,
		request.URL.EscapedPath(),
		request.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := date + "/" + adapter.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	dateKey := hmacSHA256(
		[]byte("AWS4"+adapter.credentials.SecretAccessKey),
		date,
	)
	regionKey := hmacSHA256(dateKey, adapter.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request.Header.Set(
		"Authorization",
		"AWS4-HMAC-SHA256 Credential="+adapter.credentials.AccessKeyID+
			"/"+scope+", SignedHeaders="+signedHeaders+
			", Signature="+signature,
	)
}

func normalizeS3ProviderInput(
	ctx context.Context,
	input CreateProviderInput,
) (map[string]string, []byte, error) {
	endpoint, err := validateRemoteEndpoint(
		ctx,
		input.Endpoint,
		input.AllowPrivateNetwork,
	)
	if err != nil {
		return nil, nil, err
	}
	input.Region = strings.TrimSpace(input.Region)
	input.Bucket = strings.TrimSpace(input.Bucket)
	input.AccessKeyID = strings.TrimSpace(input.AccessKeyID)
	if input.Region == "" || input.Bucket == "" ||
		input.AccessKeyID == "" || input.SecretAccessKey == "" {
		return nil, nil, ErrInvalidRootInput
	}
	basePath, err := NormalizeObjectKey(input.BasePath)
	if err != nil {
		return nil, nil, err
	}
	config := map[string]string{
		"endpoint": endpoint.String(), "region": input.Region,
		"bucket": input.Bucket, "basePath": basePath,
		"pathStyle":           fmt.Sprint(input.PathStyle),
		"allowPrivateNetwork": fmt.Sprint(input.AllowPrivateNetwork),
	}
	secret, err := json.Marshal(s3Credentials{
		AccessKeyID: input.AccessKeyID, SecretAccessKey: input.SecretAccessKey,
	})
	return config, secret, err
}

func s3StatusError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	var payload s3ErrorResponse
	_ = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload)
	switch payload.Code {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return ErrAuthenticationFailed
	case "SlowDown", "Throttling", "TooManyRequests":
		return ErrRemoteRateLimited
	case "NoSuchKey", "NoSuchBucket":
		return os.ErrNotExist
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuthenticationFailed
	case http.StatusNotFound:
		return os.ErrNotExist
	case http.StatusTooManyRequests:
		return ErrRemoteRateLimited
	}
	if response.StatusCode >= 500 {
		return ErrRootUnavailable
	}
	return fmt.Errorf("s3 returned HTTP %d", response.StatusCode)
}

func canonicalS3Query(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0)
	for _, key := range keys {
		items := append([]string(nil), values[key]...)
		sort.Strings(items)
		if len(items) == 0 {
			items = []string{""}
		}
		for _, value := range items {
			parts = append(
				parts,
				awsEncode(key)+"="+awsEncode(value),
			)
		}
	}
	return strings.Join(parts, "&")
}

func escapeS3Path(value string) string {
	segments := strings.Split(value, "/")
	for index := range segments {
		segments[index] = awsEncode(segments[index])
	}
	return strings.Join(segments, "/")
}

func awsEncode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write([]byte(value))
	return hash.Sum(nil)
}

var emptySHA256 = sha256Hex(nil)
