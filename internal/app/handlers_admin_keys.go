package app

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/dhanesh/phronesis/internal/auth"
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
//	POST /api/admin/keys/requests/{id}/approve    -> mint key, return plaintext once
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
	// Look up the prefix BEFORE the UPDATE — once revoked_at is set
	// the row's still there, but we want the prefix for cache
	// invalidation regardless. (And if the row doesn't exist, the
	// SELECT 0-rows is the same not-found signal we'd get below.)
	var keyPrefix string
	_ = s.store.DB().QueryRowContext(r.Context(),
		`SELECT key_prefix FROM api_keys WHERE id = ?`, id).Scan(&keyPrefix)

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
	// Stage 2c-cache: short-circuit the 30s TTL belt by invalidating
	// the cached principal immediately. Subsequent requests with this
	// key bypass the cache, hit the slow path, and see revoked_at IS
	// NOT NULL → ErrKeyRevoked.
	if keyPrefix != "" {
		s.authCache.Invalidate(keyPrefix)
	}
	s.auditEnqueue("key.revoke", r, "", map[string]string{
		"key_id":     strconv.FormatInt(id, 10),
		"key_prefix": keyPrefix,
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
		s.handleAdminKeyRequestApprove(w, r, id)
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

// handleAdminKeyRequestApprove mints a new API key for the
// requesting user, marks the key_request as approved, and returns
// the plaintext exactly once.
//
// Satisfies: TN7 (request->approve flow end-to-end),
//
//	S1 (Argon2id hash; plaintext shown once),
//	RT-3 (workspace + scope from the request row threaded
//	      onto the resulting service-account principal).
//
// The plaintext is returned ONLY in this response. Subsequent
// listings expose key_prefix as a non-secret display id.
func (s *Server) handleAdminKeyRequestApprove(w http.ResponseWriter, r *http.Request, id int64) {
	// Fetch the pending request.
	var (
		userID    int64
		workspace string
		scope     string
		label     string
		decided   sql.NullString
	)
	err := s.store.DB().QueryRowContext(r.Context(), `
		SELECT user_id, workspace_slug, requested_scope, requested_label, decided_at
		  FROM key_requests
		 WHERE id = ?
	`, id).Scan(&userID, &workspace, &scope, &label, &decided)
	switch {
	case err == sql.ErrNoRows:
		writeError(w, http.StatusNotFound, "request not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if decided.Valid {
		writeError(w, http.StatusConflict, "request already decided")
		return
	}

	row, err := auth.MintKey(r.Context(), s.store.DB(), userID, workspace, scope, label, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Mark the request approved + link to the new key. If this
	// fails after the key was minted, the key still exists but the
	// request stays pending — admin will see two pending entries
	// for the same user; the duplicate can be denied. Acceptable
	// degraded mode; better than rolling back the mint and losing
	// the plaintext (which the user has now seen).
	if _, err := s.store.DB().ExecContext(r.Context(), `
		UPDATE key_requests
		   SET decided_at       = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		       decision         = 'approved',
		       resulting_key_id = ?
		 WHERE id = ? AND decided_at IS NULL
	`, row.ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.auditEnqueue("key.request.approve", r, "", map[string]string{
		"request_id": strconv.FormatInt(id, 10),
		"key_id":     strconv.FormatInt(row.ID, 10),
		"key_prefix": row.Prefix,
		"workspace":  workspace,
		"scope":      scope,
	})

	// 201 Created — the response body carries the plaintext
	// exactly once. Document this clearly on the wire so a
	// well-behaved client knows to capture it.
	writeJSON(w, http.StatusCreated, map[string]any{
		"key": map[string]any{
			"id":        row.ID,
			"prefix":    row.Prefix,
			"plaintext": row.Plaintext,
			"workspace": workspace,
			"scope":     scope,
			"label":     label,
		},
		"warning": "plaintext is shown ONCE; copy it now or revoke and re-issue",
	})
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
