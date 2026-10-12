package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"review-studio.local/core/internal/systemsettings"
)

// The page tells the Owner the change takes effect immediately, so saving has to
// reach the running processor rather than only the database.
func TestMediaEncodingEndpointPushesTheChoiceIntoTheRunningProcessor(t *testing.T) {
	config := testConfig(t)
	var applied []string
	config.ApplyMediaEncoder = func(preferredEncoder string) bool {
		applied = append(applied, preferredEncoder)
		return true
	}
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	set := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libopenh264","revision":1}`, cookie)
	if set.Code != http.StatusOK {
		t.Fatalf("set encoder: status %d: %s", set.Code, set.Body.String())
	}
	clear := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"","revision":2}`, cookie)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear encoder: status %d: %s", clear.Code, clear.Body.String())
	}

	if len(applied) != 2 || applied[0] != "libopenh264" || applied[1] != "" {
		t.Fatalf("applied = %v, want [libopenh264 ]", applied)
	}
}

// A processor that refuses the choice must not block the save: the design accepts
// an unusable encoder and lets the next job trip the breaker, so the Owner keeps
// the value they asked for.
func TestMediaEncodingEndpointReportsFailureWhenChoiceCannotBeApplied(t *testing.T) {
	config := testConfig(t)
	config.ApplyMediaEncoder = func(string) bool { return false }
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	response := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libopenh264","revision":1}`, cookie)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", response.Code)
	}
	if body := getMediaEncoding(t, handler, cookie); body.PreferredEncoder != "libopenh264" {
		t.Fatalf("stored = %q, want the value the Owner asked for", body.PreferredEncoder)
	}
}

// A build without an applier still accepts and stores the choice; it simply
// cannot take effect until the next start.
func TestMediaEncodingEndpointWorksWithoutAnApplier(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	response := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libx264","revision":1}`, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", response.Code)
	}
}

// Applying the choice also moves the runtime choice, so the row read before the
// apply no longer describes what the next job will use. The response has to be
// the settled state, or the page shows the Owner an encoder they did not pick.
func TestMediaEncodingEndpointAnswersWithTheSettledState(t *testing.T) {
	config := testConfig(t)
	config.ApplyMediaEncoder = func(preferredEncoder string) bool {
		if preferredEncoder == "" {
			return true
		}
		if _, err := config.SystemSettings.RecordMediaEncodingRuntime(
			context.Background(),
			systemsettings.RecordMediaEncodingRuntimeInput{
				ActiveEncoder: preferredEncoder,
				UpdatedBy:     "media-encoder",
			},
		); err != nil {
			t.Errorf("record the runtime choice: %v", err)
		}
		return true
	}
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)

	response := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libopenh264","revision":1}`, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body.String())
	}
	var body systemMediaEncodingSettingsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ActiveEncoder != "libopenh264" || body.EffectiveEncoder != "libopenh264" {
		t.Fatalf("active/effective = %q/%q, want the encoder just applied",
			body.ActiveEncoder, body.EffectiveEncoder)
	}
}

func TestMediaEncodingEndpointRejectsUnsupportedVAAPI(t *testing.T) {
	handler, cookie := mediaEncodingHandler(t)
	response := performJSONRequest(handler, http.MethodPut, "/api/v1/system/media-encoding", `{"preferredEncoder":"h264_vaapi","revision":1}`, cookie)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status %d", response.Code)
	}
	if body := getMediaEncoding(t, handler, cookie); body.PreferredEncoder != "" || body.Revision != 1 {
		t.Fatalf("unsupported choice was stored: %+v", body)
	}
}
