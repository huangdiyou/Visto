package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3AdapterOperationsIncludingMultipart(t *testing.T) {
	t.Parallel()

	server, store := newS3TestServer(t)
	defer server.Close()
	secret, err := json.Marshal(s3Credentials{
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	})
	if err != nil {
		t.Fatalf("encode s3 credentials: %v", err)
	}
	adapter, err := NewS3Adapter(context.Background(), AdapterConfig{
		Kind: "s3",
		Config: map[string]string{
			"endpoint":            server.URL,
			"region":              "us-test-1",
			"bucket":              "review-bucket",
			"basePath":            "studio",
			"pathStyle":           "true",
			"allowPrivateNetwork": "true",
		},
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("create s3 adapter: %v", err)
	}

	capabilities, warnings, err := adapter.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("probe s3: %v", err)
	}
	if !capabilities["multipartUpload"] || len(warnings) != 0 {
		t.Fatalf("unexpected s3 capabilities: %#v %#v", capabilities, warnings)
	}

	small := []byte("hello s3 range")
	if _, err := adapter.Put(
		context.Background(),
		"clips/demo.txt",
		bytes.NewReader(small),
		int64(len(small)),
		"text/plain",
	); err != nil {
		t.Fatalf("put s3 object: %v", err)
	}
	reader, _, err := adapter.OpenRange(
		context.Background(),
		"clips/demo.txt",
		ByteRange{Offset: 6, Length: 2},
	)
	if err != nil {
		t.Fatalf("open s3 range: %v", err)
	}
	part, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(part) != "s3" {
		t.Fatalf("unexpected s3 range %q err=%v", part, err)
	}
	if err := adapter.Copy(
		context.Background(),
		"clips/demo.txt",
		"clips/copy.txt",
	); err != nil {
		t.Fatalf("copy s3 object: %v", err)
	}
	if err := adapter.Move(
		context.Background(),
		"clips/copy.txt",
		"clips/moved.txt",
	); err != nil {
		t.Fatalf("move s3 object: %v", err)
	}
	if err := adapter.Delete(
		context.Background(),
		"clips/moved.txt",
	); err != nil {
		t.Fatalf("delete s3 object: %v", err)
	}

	large := bytes.Repeat([]byte("multipart-review-"), 1_100_000)
	if int64(len(large)) < s3MultipartThreshold {
		t.Fatalf("multipart fixture is too small: %d", len(large))
	}
	if _, err := adapter.Put(
		context.Background(),
		"large/source.bin",
		bytes.NewReader(large),
		int64(len(large)),
		"application/octet-stream",
	); err != nil {
		t.Fatalf("multipart s3 upload: %v", err)
	}
	store.mu.Lock()
	stored := bytes.Clone(store.objects["studio/large/source.bin"])
	multipartCount := store.completedMultipart
	sawAuthorization := store.sawAuthorization
	store.mu.Unlock()
	if !bytes.Equal(stored, large) {
		t.Fatalf("multipart payload mismatch: got=%d want=%d", len(stored), len(large))
	}
	if multipartCount != 1 {
		t.Fatalf("expected one completed multipart upload, got %d", multipartCount)
	}
	if !sawAuthorization {
		t.Fatal("s3 requests were not signed")
	}
}

func TestS3AdapterListRejectsNonAdvancingContinuationToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("list-type") != "2" {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(response, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>same-token</NextContinuationToken></ListBucketResult>`)
	}))
	defer server.Close()

	secret, err := json.Marshal(s3Credentials{AccessKeyID: "test-access-key", SecretAccessKey: "test-secret-key"})
	if err != nil {
		t.Fatalf("encode credentials: %v", err)
	}
	adapter, err := NewS3Adapter(context.Background(), AdapterConfig{
		Kind: "s3",
		Config: map[string]string{
			"endpoint":            server.URL,
			"region":              "us-test-1",
			"bucket":              "review-bucket",
			"pathStyle":           "true",
			"allowPrivateNetwork": "true",
		},
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	_, err = adapter.List(context.Background(), "")
	if !errors.Is(err, ErrS3PaginationStalled) {
		t.Fatalf("list error = %v, want ErrS3PaginationStalled", err)
	}
}

func TestS3AdapterListRejectsOversizedPage(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated>`)
	for index := 0; index <= maxDirectoryListingEntries; index++ {
		body.WriteString(fmt.Sprintf(`<Contents><Key>objects/file-%06d.bin</Key><Size>1</Size></Contents>`, index))
	}
	body.WriteString(`</ListBucketResult>`)
	payload := body.String()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("list-type") != "2" {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(response, payload)
	}))
	defer server.Close()

	secret, err := json.Marshal(s3Credentials{AccessKeyID: "test-access-key", SecretAccessKey: "test-secret-key"})
	if err != nil {
		t.Fatalf("encode credentials: %v", err)
	}
	adapter, err := NewS3Adapter(context.Background(), AdapterConfig{
		Kind: "s3",
		Config: map[string]string{
			"endpoint":            server.URL,
			"region":              "us-test-1",
			"bucket":              "review-bucket",
			"pathStyle":           "true",
			"allowPrivateNetwork": "true",
		},
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	_, err = adapter.List(context.Background(), "")
	if !errors.Is(err, ErrScanLimitExceeded) {
		t.Fatalf("list error = %v, want ErrScanLimitExceeded", err)
	}
}

type s3TestStore struct {
	mu                 sync.Mutex
	objects            map[string][]byte
	uploads            map[string]map[int][]byte
	completedMultipart int
	sawAuthorization   bool
}

func newS3TestServer(t *testing.T) (*httptest.Server, *s3TestStore) {
	t.Helper()
	store := &s3TestStore{
		objects: make(map[string][]byte),
		uploads: make(map[string]map[int][]byte),
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if strings.HasPrefix(
			request.Header.Get("Authorization"),
			"AWS4-HMAC-SHA256 ",
		) {
			store.mu.Lock()
			store.sawAuthorization = true
			store.mu.Unlock()
		} else {
			response.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(
				response,
				`<Error><Code>SignatureDoesNotMatch</Code></Error>`,
			)
			return
		}
		key, ok := s3TestKey(request.URL.Path)
		if !ok {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		query := request.URL.Query()
		switch {
		case request.Method == http.MethodGet && query.Get("list-type") == "2":
			store.handleList(response, query)
		case request.Method == http.MethodPost && query.Has("uploads"):
			store.handleStartMultipart(response)
		case request.Method == http.MethodPut && query.Get("uploadId") != "":
			store.handleUploadPart(response, request, query)
		case request.Method == http.MethodPost && query.Get("uploadId") != "":
			store.handleCompleteMultipart(response, request, key, query)
		case request.Method == http.MethodDelete && query.Get("uploadId") != "":
			store.mu.Lock()
			delete(store.uploads, query.Get("uploadId"))
			store.mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodHead:
			store.handleHead(response, key)
		case request.Method == http.MethodGet:
			store.handleGet(response, request, key)
		case request.Method == http.MethodPut &&
			request.Header.Get("x-amz-copy-source") != "":
			store.handleCopy(response, request, key)
		case request.Method == http.MethodPut:
			value, err := io.ReadAll(request.Body)
			if err != nil {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			store.mu.Lock()
			store.objects[key] = value
			store.mu.Unlock()
			response.Header().Set("ETag", `"single"`)
			response.WriteHeader(http.StatusOK)
		case request.Method == http.MethodDelete:
			store.mu.Lock()
			delete(store.objects, key)
			store.mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	return server, store
}

func s3TestKey(requestPath string) (string, bool) {
	const prefix = "/review-bucket"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", false
	}
	value, err := url.PathUnescape(strings.TrimPrefix(requestPath, prefix))
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(value, "/"), true
}

func (store *s3TestStore) handleList(
	response http.ResponseWriter,
	query url.Values,
) {
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	type object struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
	}
	type commonPrefix struct {
		Prefix string `xml:"Prefix"`
	}
	type result struct {
		XMLName        xml.Name       `xml:"ListBucketResult"`
		Contents       []object       `xml:"Contents"`
		CommonPrefixes []commonPrefix `xml:"CommonPrefixes"`
		IsTruncated    bool           `xml:"IsTruncated"`
	}
	payload := result{}
	directories := make(map[string]bool)
	store.mu.Lock()
	for key, value := range store.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(key, prefix)
		if delimiter != "" && strings.Contains(remainder, delimiter) {
			directory := prefix + strings.SplitN(remainder, delimiter, 2)[0] + delimiter
			directories[directory] = true
			continue
		}
		payload.Contents = append(payload.Contents, object{
			Key: key, LastModified: time.Now().UTC().Format(time.RFC3339),
			ETag: `"etag"`, Size: len(value),
		})
	}
	store.mu.Unlock()
	for directory := range directories {
		payload.CommonPrefixes = append(
			payload.CommonPrefixes,
			commonPrefix{Prefix: directory},
		)
	}
	sort.Slice(payload.Contents, func(i, j int) bool {
		return payload.Contents[i].Key < payload.Contents[j].Key
	})
	sort.Slice(payload.CommonPrefixes, func(i, j int) bool {
		return payload.CommonPrefixes[i].Prefix < payload.CommonPrefixes[j].Prefix
	})
	response.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(response).Encode(payload)
}

func (store *s3TestStore) handleStartMultipart(
	response http.ResponseWriter,
) {
	const uploadID = "upload-1"
	store.mu.Lock()
	store.uploads[uploadID] = make(map[int][]byte)
	store.mu.Unlock()
	response.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(
		response,
		`<InitiateMultipartUploadResult><UploadId>`+
			uploadID+
			`</UploadId></InitiateMultipartUploadResult>`,
	)
}

func (store *s3TestStore) handleUploadPart(
	response http.ResponseWriter,
	request *http.Request,
	query url.Values,
) {
	partNumber, _ := strconv.Atoi(query.Get("partNumber"))
	value, err := io.ReadAll(request.Body)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	store.mu.Lock()
	parts := store.uploads[query.Get("uploadId")]
	if parts != nil {
		parts[partNumber] = value
	}
	store.mu.Unlock()
	if parts == nil {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, partNumber))
	response.WriteHeader(http.StatusOK)
}

func (store *s3TestStore) handleCompleteMultipart(
	response http.ResponseWriter,
	request *http.Request,
	key string,
	query url.Values,
) {
	var completed s3CompletedUpload
	if err := xml.NewDecoder(request.Body).Decode(&completed); err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	store.mu.Lock()
	parts := store.uploads[query.Get("uploadId")]
	var value []byte
	for _, part := range completed.Parts {
		value = append(value, parts[part.PartNumber]...)
	}
	store.objects[key] = value
	delete(store.uploads, query.Get("uploadId"))
	store.completedMultipart++
	store.mu.Unlock()
	response.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(
		response,
		`<CompleteMultipartUploadResult><ETag>"complete"</ETag>`+
			`</CompleteMultipartUploadResult>`,
	)
}

func (store *s3TestStore) handleHead(
	response http.ResponseWriter,
	key string,
) {
	store.mu.Lock()
	value, ok := store.objects[key]
	store.mu.Unlock()
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(response, `<Error><Code>NoSuchKey</Code></Error>`)
		return
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(value)))
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	response.WriteHeader(http.StatusOK)
}

func (store *s3TestStore) handleGet(
	response http.ResponseWriter,
	request *http.Request,
	key string,
) {
	store.mu.Lock()
	value, ok := store.objects[key]
	store.mu.Unlock()
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(response, `<Error><Code>NoSuchKey</Code></Error>`)
		return
	}
	if request.Header.Get("Range") != "" {
		start, end, valid := testByteRange(
			request.Header.Get("Range"),
			int64(len(value)),
		)
		if !valid {
			response.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		response.WriteHeader(http.StatusPartialContent)
		_, _ = response.Write(value[start : end+1])
		return
	}
	_, _ = response.Write(value)
}

func (store *s3TestStore) handleCopy(
	response http.ResponseWriter,
	request *http.Request,
	destination string,
) {
	source, err := url.PathUnescape(
		strings.TrimPrefix(
			request.Header.Get("x-amz-copy-source"),
			"/review-bucket/",
		),
	)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	store.mu.Lock()
	value, ok := store.objects[source]
	if ok {
		store.objects[destination] = bytes.Clone(value)
	}
	store.mu.Unlock()
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(
		response,
		`<CopyObjectResult><ETag>"copy"</ETag></CopyObjectResult>`,
	)
}
