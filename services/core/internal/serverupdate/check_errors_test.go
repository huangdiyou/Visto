package serverupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"review-studio.local/core/internal/delivery"
)

func TestCheckFailureClassification(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(public)
	for _, tc := range []struct {
		name, body, code string
		status           int
		sign, root       bool
	}{
		{name: "network", status: 503, code: "network_failure"},
		{name: "signature", status: 200, body: `{}`, code: "verification_failed"},
		{name: "malformed signed manifest", status: 200, body: `{`, sign: true, code: "verification_failed"},
		{name: "invalid signed manifest", status: 200, body: `{}`, sign: true, code: "verification_failed"},
		{name: "key set network", status: 503, root: true, code: "network_failure"},
		{name: "key set signature", status: 200, root: true, body: `{}`, code: "verification_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.root && (r.URL.Path == "/latest.json" || r.URL.Path == "/latest.json.sig") {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				w.WriteHeader(tc.status)
				if r.URL.Path == "/latest.json.sig" && tc.sign {
					_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(tc.body)))))
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			config := CheckConfig{Sources: []string{server.URL}, PublicKey: key, AllowInsecure: true}
			if tc.root {
				config.RootPublicKey = key
			}
			_, err := Check(context.Background(), config)
			var classified *delivery.Error
			if !errors.As(err, &classified) || classified.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
			defer unavailable.Close()
			for _, sources := range [][]string{{server.URL, unavailable.URL}, {unavailable.URL, server.URL}} {
				config.Sources = sources
				_, err = Check(context.Background(), config)
				if !errors.As(err, &classified) || classified.Code != tc.code {
					t.Fatalf("mixed source classification: %v", err)
				}
			}
		})
	}
}
