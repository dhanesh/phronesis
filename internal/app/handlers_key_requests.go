package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dhanesh/phronesis/internal/principal"
)

// maxKeyRequestLabel bounds the free-text label a user attaches to a key
// request (it is shown to the approving admin).
const maxKeyRequestLabel = 100

// handleKeyRequests is the user side of the TN7 request -> approve flow
// (decision 2026-11-27, docs/decisions.md).
//
//	POST /api/keys/requests  {"scope":"read"|"write","label":"..."} -> 201 {"request":{...}}
//	GET  /api/keys/requests  -> 200 {"requests":[...]} (the caller's own requests)
//
// The caller must be a projected users row (an OIDC user). The built-in
// password admin has no row and mints keys by approving requests instead.
// Scope "admin" cannot be requested. The workspace is the session's.
func (s *Server) handleKeyRequests(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "key store is not configured")
		return
	}
	p, err := principal.FromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var userID int64
	err = s.store.DB().QueryRowContext(r.Context(),
		`SELECT id FROM users WHERE oidc_sub = ? AND status = 'active'`, p.ID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusForbidden, "key requests need a signed-in user account")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "user lookup failed")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listOwnKeyRequests(w, r, userID)
	case http.MethodPost:
		s.createKeyRequest(w, r, p, userID)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) createKeyRequest(w http.ResponseWriter, r *http.Request, p principal.Principal, userID int64) {
	var body struct {
		Scope string `json:"scope"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Label = strings.TrimSpace(body.Label)
	if body.Scope != "read" && body.Scope != "write" {
		writeError(w, http.StatusBadRequest, "scope must be read or write")
		return
	}
	if body.Label == "" || len(body.Label) > maxKeyRequestLabel {
		writeError(w, http.StatusBadRequest, "label is required (at most 100 characters)")
		return
	}
	res, err := s.store.DB().ExecContext(r.Context(),
		`INSERT INTO key_requests (user_id, workspace_slug, requested_scope, requested_label) VALUES (?, ?, ?, ?)`,
		userID, p.WorkspaceID, body.Scope, body.Label)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create key request")
		return
	}
	id, _ := res.LastInsertId()
	s.auditEnqueue("key.request.create", r, "", map[string]string{
		"request_id": strconv.FormatInt(id, 10),
		"workspace":  p.WorkspaceID,
		"scope":      body.Scope,
	})
	row, err := s.keyRequestByID(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read key request")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"request": row})
}

func (s *Server) listOwnKeyRequests(w http.ResponseWriter, r *http.Request, userID int64) {
	rows, err := s.store.DB().QueryContext(r.Context(), keyRequestSelect+` WHERE kr.user_id = ? ORDER BY kr.requested_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list key requests")
		return
	}
	defer rows.Close()
	out := make([]adminKeyRequestRow, 0)
	for rows.Next() {
		k, err := scanKeyRequest(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list key requests")
			return
		}
		out = append(out, k)
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

const keyRequestSelect = `
	SELECT kr.id, kr.user_id, COALESCE(u.display_name, ''), COALESCE(u.email, ''),
	       kr.workspace_slug, kr.requested_scope, kr.requested_label, kr.requested_at,
	       COALESCE(kr.decided_at, ''), COALESCE(kr.decision, ''), COALESCE(kr.resulting_key_id, 0)
	  FROM key_requests kr JOIN users u ON u.id = kr.user_id`

func scanKeyRequest(sc interface{ Scan(...any) error }) (adminKeyRequestRow, error) {
	var k adminKeyRequestRow
	err := sc.Scan(&k.ID, &k.UserID, &k.OwnerName, &k.OwnerEmail, &k.WorkspaceSlug,
		&k.RequestedScope, &k.RequestedLabel, &k.RequestedAt, &k.DecidedAt, &k.Decision, &k.ResultingKeyID)
	return k, err
}

func (s *Server) keyRequestByID(r *http.Request, id int64) (adminKeyRequestRow, error) {
	return scanKeyRequest(s.store.DB().QueryRowContext(r.Context(), keyRequestSelect+` WHERE kr.id = ?`, id))
}
