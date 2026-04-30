package app

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dhanesh/phronesis/internal/webfs"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
	s.serveIndex(w, r)
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		s.serveIndex(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") {
		s.staticFS.ServeHTTP(w, r)
		return
	}
	s.serveIndex(w, r)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(s.cfg.FrontendDist, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return
	}

	data, err := fs.ReadFile(webfs.FS(), "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(data); err != nil {
		slog.Warn("serveIndex write failed",
			slog.String("path", r.URL.Path),
			slog.String("err", err.Error()),
		)
	}
}

// staticHandler serves the built frontend assets. Prefers the on-disk
// FrontendDist directory when it exists (dev / docker volume mounts); falls
// back to the embedded webfs.FS produced by `make build`.
func staticHandler(frontendDist string) http.Handler {
	if info, err := os.Stat(frontendDist); err == nil && info.IsDir() {
		return http.FileServer(http.Dir(frontendDist))
	}
	return http.FileServerFS(webfs.FS())
}
