package webassets

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandlerServesSinglePageAppAndKeepsAPIs(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":    {Data: []byte("studio")},
		"assets/app.js": {Data: []byte("console.log('visto')")},
	}
	api := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Visto-API", "yes")
		_, _ = response.Write([]byte("api"))
	})
	handler := Handler(fs.FS(assets), api)

	for _, requestPath := range []string{"/", "/projects/example/media", "/assets/app.js"} {
		request := httptest.NewRequest(http.MethodGet, requestPath, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", requestPath, response.Code)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("X-Visto-API") != "yes" || response.Body.String() != "api" {
		t.Fatalf("API response = %#v", response.Result())
	}
}
