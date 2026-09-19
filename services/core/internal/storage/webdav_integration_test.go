package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebDAVAdapterOperationsAndNetworkPolicy(t *testing.T) {
	t.Parallel()

	server := newWebDAVTestServer(t)
	defer server.Close()

	credentials, err := json.Marshal(webDAVCredentials{
		Username: "review",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("encode credentials: %v", err)
	}
	config := AdapterConfig{
		Kind: "webdav",
		Config: map[string]string{
			"endpoint":            server.URL + "/dav",
			"basePath":            "",
			"allowPrivateNetwork": "true",
		},
		Secret: credentials,
	}
	adapter, err := NewWebDAVAdapter(context.Background(), config)
	if err != nil {
		t.Fatalf("create webdav adapter: %v", err)
	}

	report, warnings, err := adapter.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("probe webdav: %v", err)
	}
	if !report["write"] || !report["rangeRead"] || len(warnings) != 0 {
		t.Fatalf("unexpected capabilities: %#v warnings=%#v", report, warnings)
	}

	payload := []byte("hello remote review")
	info, err := adapter.Put(
		context.Background(),
		"素材/clip.txt",
		bytes.NewReader(payload),
		int64(len(payload)),
		"text/plain",
	)
	if err != nil {
		t.Fatalf("put webdav object: %v", err)
	}
	if info.SizeBytes != int64(len(payload)) {
		t.Fatalf("unexpected uploaded size: %#v", info)
	}

	items, err := adapter.List(context.Background(), "素材")
	if err != nil {
		t.Fatalf("list webdav directory: %v", err)
	}
	if len(items) != 1 || items[0].ObjectKey != "素材/clip.txt" {
		t.Fatalf("unexpected webdav list: %#v", items)
	}

	reader, _, err := adapter.OpenRange(
		context.Background(),
		"素材/clip.txt",
		ByteRange{Offset: 6, Length: 6},
	)
	if err != nil {
		t.Fatalf("read webdav range: %v", err)
	}
	part, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(part) != "remote" {
		t.Fatalf("unexpected range: %q err=%v", part, err)
	}

	if err := adapter.Copy(
		context.Background(),
		"素材/clip.txt",
		"素材/copy.txt",
	); err != nil {
		t.Fatalf("copy webdav object: %v", err)
	}
	if err := adapter.Move(
		context.Background(),
		"素材/copy.txt",
		"素材/moved.txt",
	); err != nil {
		t.Fatalf("move webdav object: %v", err)
	}
	if err := adapter.Delete(
		context.Background(),
		"素材/moved.txt",
	); err != nil {
		t.Fatalf("delete webdav object: %v", err)
	}

	config.Config["allowPrivateNetwork"] = "false"
	if _, err := NewWebDAVAdapter(context.Background(), config); err == nil ||
		!errors.Is(err, ErrEndpointForbidden) {
		t.Fatalf("expected private endpoint rejection, got %v", err)
	}
}

func TestWebDAVOpenRangeFollowsSafeDownloadRedirect(t *testing.T) {
	payload := []byte("hello redirected media")
	var cdnAuthorization string
	cdn := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		cdnAuthorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "video/mp4")
		_, _ = response.Write(payload)
	}))
	defer cdn.Close()

	var webdavGetAuthorization string
	webdav := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "review" || password != "secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		key, ok := webDAVTestKey(request.URL.Path)
		if !ok || key != "video.mp4" {
			http.NotFound(response, request)
			return
		}
		switch request.Method {
		case "PROPFIND":
			response.Header().Set("Content-Type", "application/xml")
			response.WriteHeader(http.StatusMultiStatus)
			_, _ = fmt.Fprintf(response,
				`<?xml version="1.0" encoding="utf-8"?>`+
					`<d:multistatus xmlns:d="DAV:">`+
					`<d:response><d:href>/dav/video.mp4</d:href>`+
					`<d:propstat><d:prop>`+
					`<d:getcontentlength>%d</d:getcontentlength>`+
					`<d:getlastmodified>%s</d:getlastmodified>`+
					`<d:getcontenttype>video/mp4</d:getcontenttype>`+
					`<d:displayname>video.mp4</d:displayname>`+
					`</d:prop><d:status>HTTP/1.1 200 OK</d:status>`+
					`</d:propstat></d:response></d:multistatus>`,
				len(payload),
				time.Now().UTC().Format(http.TimeFormat),
			)
		case http.MethodGet:
			webdavGetAuthorization = request.Header.Get("Authorization")
			http.Redirect(response, request, cdn.URL+"/download", http.StatusFound)
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer webdav.Close()

	credentials, err := json.Marshal(webDAVCredentials{
		Username: "review",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("encode credentials: %v", err)
	}
	adapter, err := NewWebDAVAdapter(context.Background(), AdapterConfig{
		Kind: "webdav",
		Config: map[string]string{
			"endpoint":            webdav.URL + "/dav",
			"basePath":            "",
			"allowPrivateNetwork": "true",
		},
		Secret: credentials,
	})
	if err != nil {
		t.Fatalf("create webdav adapter: %v", err)
	}

	reader, info, err := adapter.OpenRange(
		context.Background(),
		"video.mp4",
		ByteRange{},
	)
	if err != nil {
		t.Fatalf("read redirected webdav object: %v", err)
	}
	body, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(payload) || info.SizeBytes != int64(len(payload)) {
		t.Fatalf("unexpected redirected object body=%q info=%#v", body, info)
	}
	if webdavGetAuthorization == "" {
		t.Fatal("expected original webdav GET to include credentials")
	}
	if cdnAuthorization != "" {
		t.Fatalf("redirect target received storage credentials: %q", cdnAuthorization)
	}
}

type webDAVTestStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newWebDAVTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := &webDAVTestStore{objects: make(map[string][]byte)}
	return httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "review" || password != "secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Method == http.MethodOptions {
			response.Header().Set(
				"Allow",
				"OPTIONS, PROPFIND, GET, PUT, COPY, MOVE, DELETE, MKCOL",
			)
			response.Header().Set("DAV", "1, 2")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		key, ok := webDAVTestKey(request.URL.Path)
		if !ok {
			http.NotFound(response, request)
			return
		}
		switch request.Method {
		case "PROPFIND":
			store.handlePropFind(response, request, key)
		case "MKCOL":
			response.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			value, err := io.ReadAll(request.Body)
			if err != nil {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			store.mu.Lock()
			store.objects[key] = value
			store.mu.Unlock()
			response.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			store.handleHead(response, key)
		case http.MethodGet:
			store.handleGet(response, request, key)
		case "COPY", "MOVE":
			store.handleCopyMove(response, request, key)
		case http.MethodDelete:
			store.mu.Lock()
			_, exists := store.objects[key]
			delete(store.objects, key)
			store.mu.Unlock()
			if !exists {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func webDAVTestKey(requestPath string) (string, bool) {
	if !strings.HasPrefix(requestPath, "/dav") {
		return "", false
	}
	value, err := url.PathUnescape(strings.TrimPrefix(requestPath, "/dav"))
	if err != nil {
		return "", false
	}
	return strings.Trim(value, "/"), true
}

func (store *webDAVTestStore) handlePropFind(
	response http.ResponseWriter,
	request *http.Request,
	key string,
) {
	store.mu.Lock()
	defer store.mu.Unlock()
	depth := request.Header.Get("Depth")
	_, fileExists := store.objects[key]
	directoryExists := key == ""
	if !directoryExists {
		prefix := key + "/"
		for objectKey := range store.objects {
			if strings.HasPrefix(objectKey, prefix) {
				directoryExists = true
				break
			}
		}
	}
	if !fileExists && !directoryExists {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	entries := []string{key}
	if depth == "1" && directoryExists {
		prefix := ""
		if key != "" {
			prefix = key + "/"
		}
		seen := make(map[string]bool)
		for objectKey := range store.objects {
			if !strings.HasPrefix(objectKey, prefix) {
				continue
			}
			remainder := strings.TrimPrefix(objectKey, prefix)
			child := strings.SplitN(remainder, "/", 2)[0]
			childKey := path.Join(prefix, child)
			if !seen[childKey] {
				entries = append(entries, childKey)
				seen[childKey] = true
			}
		}
	}
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(response, `<?xml version="1.0" encoding="utf-8"?>`+
		`<d:multistatus xmlns:d="DAV:">`)
	for _, entry := range entries {
		value, isFile := store.objects[entry]
		isDirectory := !isFile
		if !isDirectory {
			for objectKey := range store.objects {
				if strings.HasPrefix(objectKey, entry+"/") {
					isDirectory = true
					break
				}
			}
		}
		href := "/dav/"
		if entry != "" {
			href += strings.Join(escapePathSegments(entry), "/")
			if isDirectory {
				href += "/"
			}
		}
		resourceType := ""
		if isDirectory {
			resourceType = "<d:collection/>"
		}
		_, _ = fmt.Fprintf(response,
			`<d:response><d:href>%s</d:href><d:propstat><d:prop>`+
				`<d:resourcetype>%s</d:resourcetype>`+
				`<d:getcontentlength>%d</d:getcontentlength>`+
				`<d:getlastmodified>%s</d:getlastmodified>`+
				`<d:getcontenttype>text/plain</d:getcontenttype>`+
				`<d:displayname>%s</d:displayname>`+
				`</d:prop><d:status>HTTP/1.1 200 OK</d:status>`+
				`</d:propstat></d:response>`,
			href,
			resourceType,
			len(value),
			time.Now().UTC().Format(http.TimeFormat),
			path.Base(entry),
		)
	}
	_, _ = io.WriteString(response, `</d:multistatus>`)
}

func (store *webDAVTestStore) handleHead(
	response http.ResponseWriter,
	key string,
) {
	store.mu.Lock()
	value, ok := store.objects[key]
	store.mu.Unlock()
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(value)))
	response.Header().Set("Content-Type", "text/plain")
	response.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	response.WriteHeader(http.StatusOK)
}

func (store *webDAVTestStore) handleGet(
	response http.ResponseWriter,
	request *http.Request,
	key string,
) {
	store.mu.Lock()
	value, ok := store.objects[key]
	store.mu.Unlock()
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.Header().Set("Content-Type", "text/plain")
	if request.Header.Get("Range") != "" {
		start, end, ok := testByteRange(request.Header.Get("Range"), int64(len(value)))
		if !ok {
			response.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		response.WriteHeader(http.StatusPartialContent)
		_, _ = response.Write(value[start : end+1])
		return
	}
	_, _ = response.Write(value)
}

func (store *webDAVTestStore) handleCopyMove(
	response http.ResponseWriter,
	request *http.Request,
	source string,
) {
	destination, err := url.Parse(request.Header.Get("Destination"))
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	target, ok := webDAVTestKey(destination.Path)
	if !ok {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	store.mu.Lock()
	value, exists := store.objects[source]
	if exists {
		store.objects[target] = bytes.Clone(value)
		if request.Method == "MOVE" {
			delete(store.objects, source)
		}
	}
	store.mu.Unlock()
	if !exists {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.WriteHeader(http.StatusCreated)
}

func escapePathSegments(value string) []string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return parts
}

func testByteRange(value string, size int64) (int64, int64, bool) {
	value = strings.TrimPrefix(value, "bytes=")
	startText, endText, ok := strings.Cut(value, "-")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if endText != "" {
		end, err = strconv.ParseInt(endText, 10, 64)
		if err != nil || end < start || end >= size {
			return 0, 0, false
		}
	}
	return start, end, true
}
