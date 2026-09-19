// Package webassets serves a built Studio bundle alongside the Core API for
// self-contained Server packages. Development and Docker may still use a
// dedicated web server.
package webassets

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler keeps API namespaces with the supplied API handler and serves every
// other GET or HEAD request from the built single-page application.
func Handler(assets fs.FS, api http.Handler) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if isAPIRequest(request.URL.Path) || (request.Method != http.MethodGet && request.Method != http.MethodHead) {
			api.ServeHTTP(response, request)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
		if name == "" || !fileExists(assets, name) {
			request = request.Clone(request.Context())
			// Let FileServer resolve its root index directly, avoiding its
			// index.html redirect for client-side routes.
			request.URL.Path = "/"
		}
		files.ServeHTTP(response, request)
	})
}

func isAPIRequest(requestPath string) bool {
	for _, prefix := range []string{"/api/", "/health/", "/share-api/", "/join-api/"} {
		if strings.HasPrefix(requestPath, prefix) {
			return true
		}
	}
	return false
}

func fileExists(assets fs.FS, name string) bool {
	info, err := fs.Stat(assets, name)
	return err == nil && !info.IsDir()
}
