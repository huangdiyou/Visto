package serverupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"review-studio.local/core/internal/delivery"
)

// A usable manifest is now only a version announcement, so classification still
// has to separate "the source is unreachable" from "the source answered with
// something unusable" — the Owner page reports those differently.
func TestCheckFailureClassification(t *testing.T) {
	valid := `{"schemaVersion":1,"channel":"stable","version":"1.0.1","publishedAt":"2026-09-02T00:00:00Z"}`
	for _, testCase := range []struct {
		name   string
		body   string
		status int
		code   string
		usable bool
	}{
		{name: "network", status: 503, code: "network_failure"},
		{name: "malformed json", status: 200, body: `{`, code: "verification_failed"},
		{name: "empty object", status: 200, body: `{}`, code: "verification_failed"},
		{
			name:   "unsupported schema",
			status: 200,
			body:   `{"schemaVersion":2,"channel":"stable","version":"1.0.1","publishedAt":"2026-09-02T00:00:00Z"}`,
			code:   "verification_failed",
		},
		{
			name:   "non stable channel",
			status: 200,
			body:   `{"schemaVersion":1,"channel":"beta","version":"1.0.1","publishedAt":"2026-09-02T00:00:00Z"}`,
			code:   "verification_failed",
		},
		{
			name:   "missing version",
			status: 200,
			body:   `{"schemaVersion":1,"channel":"stable","publishedAt":"2026-09-02T00:00:00Z"}`,
			code:   "verification_failed",
		},
		{
			name:   "missing publication time",
			status: 200,
			body:   `{"schemaVersion":1,"channel":"stable","version":"1.0.1"}`,
			code:   "verification_failed",
		},
		{name: "usable manifest", status: 200, body: valid, usable: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.WriteHeader(testCase.status)
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer server.Close()

			config := CheckConfig{Sources: []string{server.URL}, AllowInsecure: true}
			result, err := Check(context.Background(), config)
			if testCase.usable {
				if err != nil {
					t.Fatalf("expected a usable manifest, got %v", err)
				}
				if result.Manifest.Version != "1.0.1" {
					t.Fatalf("unexpected manifest: %#v", result.Manifest)
				}
				return
			}
			var classified *delivery.Error
			if !errors.As(err, &classified) || classified.Code != testCase.code {
				t.Fatalf("got %v, want %s", err, testCase.code)
			}
			// Ordering must not change the verdict: a failing source first or
			// last, with an unreachable source beside it, still reports the same
			// code.
			unavailable := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.WriteHeader(503)
			}))
			defer unavailable.Close()
			for _, sources := range [][]string{{server.URL, unavailable.URL}, {unavailable.URL, server.URL}} {
				config.Sources = sources
				_, err = Check(context.Background(), config)
				if !errors.As(err, &classified) || classified.Code != testCase.code {
					t.Fatalf("mixed source classification: %v", err)
				}
			}
		})
	}
}
