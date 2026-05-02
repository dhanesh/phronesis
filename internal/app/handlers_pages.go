package app

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/wiki"
)

// resolveWorkspace returns the per-workspace Store + Hub. Returns
// (nil, nil, "") and writes a 404 if the slug is unknown. Empty slug
// is normalised to the default workspace.
func (s *Server) resolveWorkspace(w http.ResponseWriter, slug string) (*wiki.Store, *wiki.Hub, string) {
	if slug == "" {
		slug = wiki.DefaultWorkspaceSlug
	}
	store, hub, ok := s.workspaces.Get(slug)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown workspace")
		return nil, nil, ""
	}
	return store, hub, slug
}

func (s *Server) handlePages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	store, _, slug := s.resolveWorkspace(w, "")
	if slug == "" {
		return
	}
	pages, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (s *Server) handlePageRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pages/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing page name")
		return
	}
	if strings.HasSuffix(path, "/events") {
		name := strings.TrimSuffix(path, "/events")
		s.handleEvents(w, r, "", name)
		return
	}
	s.handlePage(w, r, "", path)
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request, workspaceSlug, name string) {
	_, hub, slug := s.resolveWorkspace(w, workspaceSlug)
	if slug == "" {
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, err := hub.Snapshot(name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// INT-5: document view (read event). S9 contract: Enqueue is O(1)
		// and does not block the read hot path.
		s.auditEnqueue("doc.view", r, name, nil)
		writeJSON(w, http.StatusOK, map[string]any{"page": page})
	case http.MethodPost:
		var req struct {
			Content     string `json:"content"`
			BaseVersion int64  `json:"baseVersion"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid update request")
			return
		}
		username, _ := s.auth.Username(r)
		page, merged, err := hub.Apply(name, req.BaseVersion, req.Content, username)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// INT-5: document write event. `merged` flag indicates conflict
		// resolution ran, which is operationally interesting.
		s.auditEnqueue("doc.edit", r, name, map[string]string{
			"merged":    fmt.Sprintf("%t", merged),
			"workspace": slug,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"page":   page,
			"merged": merged,
		})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, workspaceSlug, name string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	_, hub, slug := s.resolveWorkspace(w, workspaceSlug)
	if slug == "" {
		return
	}
	ch, cancel, err := hub.Subscribe(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdownCh:
			// Server-initiated shutdown: return promptly so http.Shutdown
			// doesn't block waiting for this long-lived handler. The client
			// will reconnect when the new server comes up.
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case event := <-ch:
			payload, err := json.Marshal(event)
			if err != nil {
				slog.Warn("sse event marshal failed",
					slog.String("page", name),
					slog.String("event_type", event.Type),
					slog.String("err", err.Error()),
				)
				continue
			}
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}
