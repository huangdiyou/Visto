package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"review-studio.local/core/internal/media"
)

func mediaEncodingHandler(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	config := testConfig(t)
	// The pane has to report what the next job will actually use, so the test
	// gives Core a configured encoder to fall back to.
	config.VideoAcceleration = media.VideoAccelerationSettingsForMode(media.VideoAccelerationQSV)
	handler := NewHandler(config)
	cookie, _ := setupInstance(t, handler)
	return handler, cookie
}

func getMediaEncoding(t *testing.T, handler http.Handler, cookie *http.Cookie) systemMediaEncodingSettingsResponse {
	t.Helper()
	response := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/media-encoding", "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("get media encoding: status %d: %s", response.Code, response.Body.String())
	}
	var body systemMediaEncodingSettingsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode media encoding response: %v", err)
	}
	return body
}

// An instance that never chose anything must read as "nothing decided", and the
// pane still has to say which encoder is in effect.
func TestMediaEncodingEndpointReportsDefaults(t *testing.T) {
	handler, cookie := mediaEncodingHandler(t)
	body := getMediaEncoding(t, handler, cookie)
	if body.PreferredEncoder != "" {
		t.Fatalf("preferred encoder = %q, want empty", body.PreferredEncoder)
	}
	if body.ActiveEncoder != "" || body.FailureCount != 0 || body.TrippedEncoder != nil {
		t.Fatalf("unexpected breaker state: %#v", body)
	}
	if body.EffectiveEncoder != "h264_qsv" {
		t.Fatalf("effective encoder = %q, want the configured h264_qsv", body.EffectiveEncoder)
	}
	if body.Revision != 1 {
		t.Fatalf("revision = %d, want 1", body.Revision)
	}
	if body.DetectedEncoders == nil {
		t.Fatal("detected encoders must serialize as an empty list, not null")
	}
}

func TestMediaEncodingEndpointStoresTheOwnerChoice(t *testing.T) {
	handler, cookie := mediaEncodingHandler(t)
	response := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libopenh264","revision":1}`, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("put media encoding: status %d: %s", response.Code, response.Body.String())
	}
	var body systemMediaEncodingSettingsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PreferredEncoder != "libopenh264" || body.Revision != 2 {
		t.Fatalf("response = %#v", body)
	}
	if body.EffectiveEncoder != "libopenh264" {
		t.Fatalf("effective encoder = %q, want the Owner choice", body.EffectiveEncoder)
	}
	// The choice has to survive the round trip, not just the response.
	if reread := getMediaEncoding(t, handler, cookie); reread.PreferredEncoder != "libopenh264" {
		t.Fatalf("re-read = %#v", reread)
	}
}

func TestMediaEncodingEndpointRejectsAStaleRevision(t *testing.T) {
	handler, cookie := mediaEncodingHandler(t)
	first := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libopenh264","revision":1}`, cookie)
	if first.Code != http.StatusOK {
		t.Fatalf("first put: status %d", first.Code)
	}
	second := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libx264","revision":1}`, cookie)
	if second.Code != http.StatusConflict {
		t.Fatalf("stale revision: status %d, want 409", second.Code)
	}
}

func TestMediaEncodingEndpointRefusesAnUnknownEncoder(t *testing.T) {
	handler, cookie := mediaEncodingHandler(t)
	response := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"h264_bogus","revision":1}`, cookie)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown encoder: status %d, want 400", response.Code)
	}
}

func TestMediaEncodingEndpointRequiresOwner(t *testing.T) {
	config := testConfig(t)
	handler := NewHandler(config)
	cookie, workspaceID := setupInstance(t, handler)
	member := memberCookie(t, handler, config, workspaceID)

	if response := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/media-encoding", "", member,
	); response.Code == http.StatusOK {
		t.Fatal("a member must not read the media encoding settings")
	}
	if response := performJSONRequest(
		handler, http.MethodPut, "/api/v1/system/media-encoding",
		`{"preferredEncoder":"libx264","revision":1}`, member,
	); response.Code == http.StatusOK {
		t.Fatal("a member must not change the media encoding settings")
	}
	// The Owner still can, so the refusals above are about the role and not a
	// broken route.
	if response := performJSONRequest(
		handler, http.MethodGet, "/api/v1/system/media-encoding", "", cookie,
	); response.Code != http.StatusOK {
		t.Fatalf("owner get: status %d", response.Code)
	}
}
