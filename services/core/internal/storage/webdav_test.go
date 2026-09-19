package storage

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type trackedReadCloser struct {
	*strings.Reader
	closed bool
}

func (reader *trackedReadCloser) Close() error {
	reader.closed = true
	return nil
}

func TestWebDAVRequestDoesNotCloseCallerReader(t *testing.T) {
	endpoint, err := url.Parse("https://example.test/dav")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	adapter := &WebDAVAdapter{endpoint: endpoint}
	body := &trackedReadCloser{Reader: strings.NewReader("payload")}
	request, err := adapter.request(
		context.Background(),
		http.MethodPut,
		"asset.bin",
		body,
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if _, err := io.Copy(io.Discard, request.Body); err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if err := request.Body.Close(); err != nil {
		t.Fatalf("close request body: %v", err)
	}
	if body.closed {
		t.Fatal("webdav request closed caller-owned reader")
	}
}
