package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

var staticFS = mustSubFS(dist, "dist")

func IndexHTML() ([]byte, error) {
	return fs.ReadFile(staticFS, "index.html")
}

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.Trim(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"), "/")
		if serveAsset(w, r, clean) {
			return
		}
		last := path.Base(clean)
		if strings.HasPrefix(clean, "assets/") || strings.Contains(last, ".") {
			http.NotFound(w, r)
			return
		}
		if serveAsset(w, r, "index.html") {
			return
		}
		http.NotFound(w, r)
	})
}

func serveAsset(w http.ResponseWriter, r *http.Request, name string) bool {
	if name == "" {
		name = "index.html"
	}
	for _, candidate := range assetCandidates(name) {
		info, err := fs.Stat(staticFS, candidate)
		if err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, staticFS, candidate)
			return true
		}
	}
	return false
}

func assetCandidates(name string) []string {
	if name == "index.html" {
		return []string{name}
	}
	return []string{name, path.Join(name, "index.html"), name + ".html"}
}

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
