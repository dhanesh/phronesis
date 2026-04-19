// Package media exposes HTTP endpoints for content-addressed media storage.
// Markdown documents reference media by /media/<sha256-hex> URLs; this package
// serves those URLs from the blob.Store abstraction.
//
// Satisfies: RT-6.2 (markdown-to-media URL routing), TN4 (media out of git),
// S6 (content-type allow-list + quota enforced by blob.Store), S5 (2MB body cap).
package media

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/dhanesh/phronesis/internal/blob"
	"github.com/dhanesh/phronesis/internal/principal"
)

// Handler exposes media HTTP routes. Mount its Routes under a parent mux.
//
// Security model:
//   - GET  /media/<hash>  : read access (principal.Can("read") for the
//     workspace scoped by the request) — WorkspaceID extracted from the
//     request context's Principal.
//   - POST /media         : upload; requires principal.Can("write").
//
// The blob.Store enforces S6 (content-type allow-list, per-workspace quota);
// this handler only validates shape and forwards to the store.
type Handler struct {
	store          blob.Store
	maxUploadBytes int64 // S5 alignment: default 2MB if 0
}

// NewHandler constructs a Handler. If maxUploadBytes is 0, it defaults to the
// S5 per-request body cap (2MB).
func NewHandler(store blob.Store, maxUploadBytes int64) *Handler {
	if maxUploadBytes <= 0 {
		maxUploadBytes = 2 * 1024 * 1024
	}
	return &Handler{store: store, maxUploadBytes: maxUploadBytes}
}

// Routes registers the media endpoints on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.Handle("GET /media/", http.HandlerFunc(h.get))
	mux.Handle("POST /media", http.HandlerFunc(h.upload))
}

// get serves a blob by its sha256 hash. Request path: /media/<hash>.
//
// Satisfies: RT-6.2
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := principal.FromContext(r.Context())
	if err != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if !p.Can("read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	hash := strings.TrimPrefix(r.URL.Path, "/media/")
	if hash == "" || !validHex(hash) {
		http.Error(w, "invalid media id", http.StatusBadRequest)
		return
	}

	rc, info, err := h.store.Get(r.Context(), p.WorkspaceID, hash)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	if info.ContentType != "" {
		w.Header().Set("Content-Type", info.ContentType)
	}
	// Content-addressed URL is safely cacheable forever.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = io.Copy(w, rc)
}

type uploadResponse struct {
	URL         string `json:"url"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// upload accepts a raw-body upload. The caller sets Content-Type; the blob
// store rejects types outside the S6 allow-list.
//
// For multipart/form-data uploads a separate endpoint can be added later; the
// raw-body path is simpler for agent / API-key flows (S1) and streaming clients.
//
// Satisfies: RT-6.2, S5 (body cap), S6 (content-type + quota enforced by store)
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	p, err := principal.FromContext(r.Context())
	if err != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if !p.Can("write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		http.Error(w, "content-type required", http.StatusBadRequest)
		return
	}

	// S5: cap the request body at maxUploadBytes.
	body := http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	defer body.Close()

	info, err := h.store.Put(r.Context(), p.WorkspaceID, body, contentType)
	if err != nil {
		switch {
		case errors.Is(err, blob.ErrContentTypeDisallowed):
			http.Error(w, "content-type not allowed", http.StatusUnsupportedMediaType)
		case errors.Is(err, blob.ErrQuotaExceeded):
			http.Error(w, "workspace quota exceeded", http.StatusRequestEntityTooLarge)
		default:
			// Includes body too large from MaxBytesReader.
			if strings.Contains(err.Error(), "request body too large") {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "upload failed", http.StatusBadRequest)
		}
		return
	}

	resp := uploadResponse{
		URL:         "/media/" + info.Hash,
		Hash:        info.Hash,
		Size:        info.Size,
		ContentType: info.ContentType,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func validHex(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
