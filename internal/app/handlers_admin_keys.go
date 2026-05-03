package app

import (
	"net/http"
	"strconv"
	"strings"
)

// adminKeyRow is the JSON shape returned by GET /api/admin/keys.
//
// Satisfies: U3 (Admin Keys page lists keys with one-click revoke),
//
//	RT-9 (admin Web UI surface — server side).
type adminKeyRow struct {
	ID            int64  `json:"id"`
	OwnerName     string `json:"owner_name"`
	OwnerEmail    string `json:"owner_email"`
	WorkspaceSlug string `json:"workspace_slug"`
	Scope         string `json:"scope"`
	Label         string `json:"label"`
	KeyPrefix     string `json:"key_prefix"`
	CreatedAt     string `json:"created_at"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	RevokedAt     string `json:"revoked_at,omitempty"`
	LastUsedAt    string `json:"last_used_at,omitempty"`
}

// adminKeyRequestRow is the JSON shape for pending key requests
// surfaced to /api/admin/keys/requests (TN7 request->approve flow).
type adminKeyRequestRow struct {
	ID             int64  `json:"id"`
	UserID         int64  `json:"user_id"`
	OwnerName      string `json:"owner_name"`
	OwnerEmail     string `json:"owner_email"`
	WorkspaceSlug  string `json:"workspace_slug"`
	RequestedScope string `json:"requested_scope"`
	RequestedLabel string `json:"requested_label"`
	RequestedAt    string `json:"requested_at"`
	DecidedAt      string `json:"decided_at,omitempty"`
	Decision       string `json:"decision,omitempty"`
	ResultingKeyID int64  `json:"resulting_key_id,omitempty"`
}

// handleAdminKeys dispatches /api/admin/keys[...].
//
// Routes:
//
//	GET  /api/admin/keys                          -> list keys
//	POST /api/admin/keys/{id}/revoke              -> revoke a key
//	GET  /api/admin/keys/requests                 -> list key requests
//	POST /api/admin/keys/requests/{id}/deny       -> deny a pending request
//	POST /api/admin/keys/requests/{id}/approve    -> 501 (Stage 2)
func (s *Server) handleAdminKeys(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "key store is not configured")
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/api/admin/keys")
	tail = strings.Trim(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleAdminKeysList(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if strings.HasPrefix(tail, "requests") {
		reqTail := strings.TrimPrefix(tail, "requests")
		reqTail = strings.Trim(reqTail, "/")
		s.handleAdminKeyRequests(w, r, reqTail)
		return
	}
	// /api/admin/keys/{id}/revoke
	parts := strings.SplitN(tail, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if action != "revoke" {
		writeError(w, http.StatusNotFound, "unknown key action")
		return
	}
	s.handleAdminKeyRevoke(w, r, id)
}

func (s *Server) handleAdminKeysList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB().QueryContext(r.Context(), `
		SELECT k.id,
		       COALESCE(u.display_name, ''),
		       COALESCE(u.email, ''),
		       k.workspace_slug,
		       k.scope,
		       k.label,
		       k.key_prefix,
		       k.created_at,
		       COALESCE(k.expires_at, ''),
		       COALESCE(k.revoked_at, ''),
		       COALESCE(k.last_used_at, '')
		  FROM api_keys k
		  JOIN users u ON u.id = k.user_id
		 ORDER BY k.created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := make([]adminKeyRow, 0)
	for rows.Next() {
		var k adminKeyRow
		if err := rows.Scan(&k.ID, &k.OwnerName, &k.OwnerEmail,
			&k.WorkspaceSlug, &k.Scope, &k.Label, &k.KeyPrefix,
			&k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (s *Server) handleAdminKeyRevoke(w http.ResponseWriter, r *http.Request, id int64) {
	res, err := s.store.DB().ExecContext(r.Context(),
		`UPDATE api_keys
		    SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		  WHERE id = ? AND revoked_at IS NULL`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "key not found or already revoked")
		return
	}
	s.auditEnqueue("key.revoke", r, "", map[string]string{
		"key_id": strconv.FormatInt(id, 10),
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminKeyRequests dispatches /api/admin/keys/requests[...].
//
// reqTail is the path remainder after "requests".
func (s *Server) handleAdminKeyRequests(w http.ResponseWriter, r *http.Request, reqTail string) {
	if reqTail == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleAdminKeyRequestsList(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	parts := strings.SplitN(reqTail, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	switch action {
	case "deny":
		s.handleAdminKeyRequestDeny(w, r, id)
	case "approve":
		// Stage 2 implements full Argon2id-hashed key minting (S1).
		// Return 501 with a structured error so the UI can render
		// a useful message rather than generic 500.
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error":      "approve flow lands in Stage 2",
			"reason":     "key minting requires Argon2id hashing + plaintext-once delivery (S1)",
			"workaround": "deny the request and revisit after Stage 2 ships",
		})
	default:
		writeError(w, http.StatusNotFound, "unknown request action")
	}
}

func (s *Server) handleAdminKeyRequestsList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB().QueryContext(r.Context(), `
		SELECT kr.id,
		       kr.user_id,
		       COALESCE(u.display_name, ''),
		       COALESCE(u.email, ''),
		       kr.workspace_slug,
		       kr.requested_scope,
		       kr.requested_label,
		       kr.requested_at,
		       COALESCE(kr.decided_at, ''),
		       COALESCE(kr.decision, ''),
		       COALESCE(kr.resulting_key_id, 0)
		  FROM key_requests kr
		  JOIN users u ON u.id = kr.user_id
		 WHERE kr.decided_at IS NULL
		 ORDER BY kr.requested_at ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := make([]adminKeyRequestRow, 0)
	for rows.Next() {
		var k adminKeyRequestRow
		if err := rows.Scan(&k.ID, &k.UserID, &k.OwnerName, &k.OwnerEmail,
			&k.WorkspaceSlug, &k.RequestedScope, &k.RequestedLabel,
			&k.RequestedAt, &k.DecidedAt, &k.Decision, &k.ResultingKeyID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

func (s *Server) handleAdminKeyRequestDeny(w http.ResponseWriter, r *http.Request, id int64) {
	res, err := s.store.DB().ExecContext(r.Context(),
		`UPDATE key_requests
		    SET decided_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		        decision   = 'denied'
		  WHERE id = ? AND decided_at IS NULL`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "request not found or already decided")
		return
	}
	s.auditEnqueue("key.request.deny", r, "", map[string]string{
		"request_id": strconv.FormatInt(id, 10),
	})
	w.WriteHeader(http.StatusNoContent)
}
