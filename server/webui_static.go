package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

func newWebUIHandler(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route HTML and RSC payloads must be checked again after a deployment.
		w.Header().Set("Cache-Control", "no-cache")
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		for _, candidate := range []string{p, p + "/index.html", p + ".html"} {
			if serveFileIfExists(w, r, root, candidate) {
				return
			}
		}
		http.ServeFileFS(w, r, root, "index.html")
	})
}

// serveFileIfExists serves name from fsys when it exists as a regular file.
func serveFileIfExists(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	st, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil || st.IsDir() {
		return false
	}
	// Next fingerprints these filenames. Keeping them in the browser cache is
	// safe across releases and avoids downloading the dashboard again on reload.
	if strings.HasPrefix(name, "_next/static/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, fsys, name)
	return true
}
