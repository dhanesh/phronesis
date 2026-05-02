package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dhanesh/phronesis/internal/wiki"
)

// handleWorkspacesList returns the list of available workspaces for any
// authenticated user. Used by the frontend top-bar selector.
func (s *Server) handleWorkspacesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspaces": s.workspaces.List(),
	})
}

// handleWorkspaceRoutes parses URLs of the form
//
//	/api/workspaces/<slug>/pages
//	/api/workspaces/<slug>/pages/<name>
//	/api/workspaces/<slug>/pages/<name>/events
//
// and dispatches to the existing page handlers with the workspace slug
// threaded through. The exact /api/workspaces path (no trailing slash)
// is served by handleWorkspacesList; this handler is registered at the
// /api/workspaces/ prefix.
func (s *Server) handleWorkspaceRoutes(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	tail = strings.Trim(tail, "/")
	if tail == "" {
		// Bare /api/workspaces/ — redirect callers to the list endpoint.
		s.handleWorkspacesList(w, r)
		return
	}
	parts := strings.SplitN(tail, "/", 2)
	slug := parts[0]
	if err := wiki.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace slug")
		return
	}
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	if rest == "" {
		// /api/workspaces/<slug> with no further path — return the
		// workspace's own page list (parallel to /api/pages).
		store, _, resolved := s.resolveWorkspace(w, slug)
		if resolved == "" {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		pages, err := store.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
		return
	}
	if !strings.HasPrefix(rest, "pages") {
		writeError(w, http.StatusNotFound, "unknown workspace subresource")
		return
	}
	pageTail := strings.TrimPrefix(rest, "pages")
	pageTail = strings.Trim(pageTail, "/")
	if pageTail == "" {
		// /api/workspaces/<slug>/pages — list pages.
		store, _, resolved := s.resolveWorkspace(w, slug)
		if resolved == "" {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		pages, err := store.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
		return
	}
	if strings.HasSuffix(pageTail, "/events") {
		name := strings.TrimSuffix(pageTail, "/events")
		s.handleEvents(w, r, slug, name)
		return
	}
	s.handlePage(w, r, slug, pageTail)
}

// handleAdminWorkspaces dispatches POST/DELETE on the admin
// workspace-management endpoint. POST creates; DELETE removes.
func (s *Server) handleAdminWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleAdminCreateWorkspace(w, r)
	case http.MethodDelete:
		s.handleAdminDeleteWorkspace(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleAdminCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := wiki.ValidateSlug(req.Slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	meta, err := s.workspaces.Create(req.Slug, req.Name)
	if err != nil {
		switch {
		case errors.Is(err, wiki.ErrWorkspaceExists):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.auditEnqueue("workspace.create", r, req.Slug, map[string]string{
		"name": meta.Name,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"workspace": meta})
}

func (s *Server) handleAdminDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	// Path shape: /api/admin/workspaces/<slug>
	tail := strings.TrimPrefix(r.URL.Path, "/api/admin/workspaces/")
	tail = strings.Trim(tail, "/")
	if tail == "" {
		writeError(w, http.StatusBadRequest, "missing workspace slug")
		return
	}
	if err := wiki.ValidateSlug(tail); err != nil {
		writeError(w, http.StatusBadRequest, "invalid slug")
		return
	}
	if err := s.workspaces.Delete(tail); err != nil {
		switch {
		case errors.Is(err, wiki.ErrWorkspaceNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, wiki.ErrCannotDeleteDefault):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.auditEnqueue("workspace.delete", r, tail, nil)
	w.WriteHeader(http.StatusNoContent)
}
