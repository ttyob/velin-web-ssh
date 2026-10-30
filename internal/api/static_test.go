package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ttyob/velin-web-ssh/internal/config"
)

func TestStaticFrontendFromEmbeddedOrDiskFiles(t *testing.T) {
	const index = "<!doctype html><title>VelinWebSSH</title>"
	const script = "console.log('embedded frontend');"
	embedded := fstest.MapFS{
		"index.html":    {Data: []byte(index)},
		"assets/app.js": {Data: []byte(script)},
	}
	disk := t.TempDir()
	for name, file := range embedded {
		target := filepath.Join(disk, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, file.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []struct {
		name string
		cfg  config.Config
	}{
		{"embedded", config.Config{WebFS: embedded, WebDist: filepath.Join(t.TempDir(), "missing-web-dist")}},
		{"disk", config.Config{WebDist: disk}},
	} {
		t.Run(source.name, func(t *testing.T) {
			handler := (&API{cfg: source.cfg}).staticHandler()
			for _, test := range []struct {
				name, method, url, byteRange string
				status                       int
				body, contentType            string
			}{
				{"home", "GET", "/", "", 200, index, "text/html"},
				{"deep-link", "GET", "/share/session-token", "", 200, index, "text/html"},
				{"script", "GET", "/assets/app.js", "", 200, script, "javascript"},
				{"head", "HEAD", "/assets/app.js", "", 200, "", "javascript"},
				{"range", "GET", "/assets/app.js", "bytes=0-6", 206, script[:7], "javascript"},
				{"no-directory-listing", "GET", "/assets/", "", 200, index, "text/html"},
				{"unknown-api", "GET", "/api/missing", "", 404, "404 page not found\n", "text/plain"},
				{"unknown-websocket", "GET", "/ws/missing", "", 404, "404 page not found\n", "text/plain"},
			} {
				t.Run(test.name, func(t *testing.T) {
					request := httptest.NewRequest(test.method, test.url, nil)
					if test.byteRange != "" {
						request.Header.Set("Range", test.byteRange)
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != test.status || response.Body.String() != test.body {
						t.Fatalf("status=%d body=%q; want status=%d body=%q", response.Code, response.Body.String(), test.status, test.body)
					}
					if !strings.Contains(response.Header().Get("Content-Type"), test.contentType) {
						t.Fatalf("content-type=%q, want %q", response.Header().Get("Content-Type"), test.contentType)
					}
					if test.byteRange != "" && response.Header().Get("Content-Range") == "" {
						t.Fatal("partial response did not include Content-Range")
					}
				})
			}
		})
	}
}

func TestStaticFrontendMissingBuild(t *testing.T) {
	for _, assets := range []fs.FS{nil, fstest.MapFS{}} {
		handler := (&API{cfg: config.Config{WebFS: assets, WebDist: t.TempDir()}}).staticHandler()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503", response.Code)
		}
	}
}
