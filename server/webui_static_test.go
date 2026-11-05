package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestWebUIStaticCaching(t *testing.T) {
	handler := newWebUIHandler(fstest.MapFS{
		"index.html":                     {Data: []byte("home")},
		"chat/index.html":                {Data: []byte("chat")},
		"chat/index.txt":                 {Data: []byte("route data")},
		"_next/static/chunks/app-123.js": {Data: []byte("javascript")},
	})
	for _, tc := range []struct {
		path, cache, body string
	}{
		{"/chat/", "no-cache", "chat"},
		{"/chat/index.txt?_rsc=123", "no-cache", "route data"},
		{"/_next/static/chunks/app-123.js", "public, max-age=31536000, immutable", "javascript"},
		// A missing asset must not cache the HTML fallback as immutable.
		{"/_next/static/chunks/missing.js", "no-cache", "home"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != http.StatusOK || w.Body.String() != tc.body {
				t.Fatalf("response = %d %q", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != tc.cache {
				t.Fatalf("Cache-Control = %q, want %q", got, tc.cache)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/_next/static/chunks/app-123.js", nil)
	request.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusPartialContent || w.Body.String() != "java" {
		t.Fatalf("range response = %d %q", w.Code, w.Body.String())
	}
}
