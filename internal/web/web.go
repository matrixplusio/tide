// Package web serves the React build embedded in the binary, or proxies to the
// Vite dev server in development.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// The built frontend is not in the repository; `docker build` produces it and
// copies it in. dist/.keep is committed so this still compiles in a fresh
// clone — go:embed is resolved at compile time and fails on a missing
// directory, which would mean `go build ./...` did not work until somebody
// had run a frontend build.
//
//go:embed all:dist
var dist embed.FS

func Handler(devProxy string) (http.Handler, error) {
	if devProxy != "" {
		u, err := url.Parse(devProxy)
		if err != nil {
			return nil, err
		}
		// ReverseProxy handles the websocket upgrade Vite HMR needs.
		return httputil.NewSingleHostReverseProxy(u), nil
	}
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA route: serve index.html.
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "web build missing: run `make web`", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	}), nil
}
