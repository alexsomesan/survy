package app

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"survy/web"
)

func (a *App) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/live", a.handleLive)
	m.HandleFunc("GET /api/top-talkers", a.handleTopTalkers)
	m.HandleFunc("GET /api/devices/{id}", a.handleDevice)
	m.HandleFunc("GET /api/history", a.handleHistory)
	m.Handle("GET /", staticHandler(web.Dist()))
	a.mux = m
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func staticHandler(dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if _, err := fs.Stat(dist, "index.html"); err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = w.Write([]byte("frontend not built: run `make web`\n"))
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}
