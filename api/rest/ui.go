package rest

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/vectorcore/vectorcore-mmsc/web"
)

func mountUI(mux *http.ServeMux) {
	root, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		return
	}

	fileServer := http.FileServer(http.FS(root))
	mux.Handle("/assets/", fileServer)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/metrics" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			http.NotFound(w, r)
			return
		}
		target := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if target == "." || target == "" {
			target = "index.html"
		}
		if _, err := fs.Stat(root, target); err == nil && !strings.HasSuffix(target, "/") {
			if target == "index.html" {
				serveEmbeddedFile(w, root, target)
				return
			}
			r.URL.Path = "/" + target
			fileServer.ServeHTTP(w, r)
			return
		}
		serveEmbeddedFile(w, root, "index.html")
	})
}

func serveEmbeddedFile(w http.ResponseWriter, root fs.FS, name string) {
	file, err := root.Open(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, file)
}
