package app

import (
	"encoding/json"
	"github.com/dhanesh/phronesis/internal/principal"
	"net/http"
	"strconv"
	"strings"
)

// adminUserRow is the JSON shape returned by GET /api/admin/users.
//
// Satisfies: U2 (Admin Users page lists users with status, last-seen,
//
//	active-key count and pending-request count),
//	RT-9 (admin Web UI surface — server side).
type adminUserRow struct {
	ID                  int64  `json:"id"`
	OIDCSub             string `json:"oidc_sub"`
	Email               string `json:"email"`
	DisplayName         string `json:"display_name"`
	Role                string `json:"role"`
	Status              string `json:"status"`
	CreatedAt           string `json:"created_at"`
	LastSeenAt          string `json:"last_seen_at,omitempty"`
	ActiveKeyCount      int    `json:"active_key_count"`
	PendingRequestCount int    `json:"pending_request_count"`
}

// handleAdminUsers dispatches /api/admin/users[/<id>[/<action>]].
//
// Routes:
//
//	GET    /api/admin/users                     -> list
//	POST   /api/admin/users/{id}/suspend
//	POST   /api/admin/users/{id}/reactivate
//	POST   /api/admin/users/{id}/role   {"role":"viewer"|"editor"|"admin"}
//	DELETE /api/admin/users/{id}
//
// Wired in routes.go behind withAuth + withAdmin.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "user store is not configured")
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/api/admin/users")
	tail = strings.Trim(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleAdminUsersList(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	parts := strings.SplitN(tail, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch r.Method {
	case http.MethodPost:
		switch action {
		case "suspend":
			s.handleAdminUserStatus(w, r, id, "suspended")
		case "reactivate":
			s.handleAdminUserStatus(w, r, id, "active")
		case "role":
			s.handleAdminUserRole(w, r, id)
		default:
			writeError(w, http.StatusNotFound, "unknown user action")
		}
	case http.MethodDelete:
		if action != "" {
			writeError(w, http.StatusBadRequest, "DELETE does not take an action")
			return
		}
		s.handleAdminUserDelete(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleAdminUsersList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB().QueryContext(r.Context(), `
		SELECT u.id,
		       u.oidc_sub,
		       COALESCE(u.email, ''),
		       COALESCE(u.display_name, ''),
		       u.role,
		       u.status,
		       u.created_at,
		       COALESCE(u.last_seen_at, ''),
		       (SELECT COUNT(*) FROM api_keys k     WHERE k.user_id = u.id AND k.revoked_at IS NULL) AS active_key_count,
		       (SELECT COUNT(*) FROM key_requests r WHERE r.user_id = u.id AND r.decided_at IS NULL) AS pending_request_count
		  FROM users u
		 ORDER BY u.created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := make([]adminUserRow, 0)
	for rows.Next() {
		var u adminUserRow
		if err := rows.Scan(&u.ID, &u.OIDCSub, &u.Email, &u.DisplayName,
			&u.Role, &u.Status, &u.CreatedAt, &u.LastSeenAt,
			&u.ActiveKeyCount, &u.PendingRequestCount); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleAdminUserStatus(w http.ResponseWriter, r *http.Request, id int64, status string) {
	res, err := s.store.DB().ExecContext(r.Context(),
		`UPDATE users SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	// Stage 2c-cache: a suspension MUST propagate to all of the
	// user's keys within seconds (S5 invariant). The slow path
	// already checks users.status='suspended' and returns
	// ErrKeyRevoked, so cache invalidation is what closes the gap
	// between an admin clicking Suspend and the next request with
	// the user's key getting 401.
	if status == "suspended" && s.authCache != nil {
		_ = s.authCache.InvalidateByUser(r.Context(), s.store.DB(), id)
	}
	s.auditEnqueue("user."+status, r, "", map[string]string{
		"target_user_id": strconv.FormatInt(id, 10),
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminUserRole sets a user's wiki role (docs/roles.md). The role is
// read from the users row on every request, so the change applies to the
// user's open sessions at once; cached API-key principals are dropped so
// their owner-role cap is re-evaluated.
func (s *Server) handleAdminUserRole(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch principal.Role(body.Role) {
	case principal.RoleViewer, principal.RoleEditor, principal.RoleAdmin:
	default:
		writeError(w, http.StatusBadRequest, "role must be viewer, editor or admin")
		return
	}
	res, err := s.store.DB().ExecContext(r.Context(),
		`UPDATE users SET role = ? WHERE id = ?`, body.Role, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if s.authCache != nil {
		_ = s.authCache.InvalidateByUser(r.Context(), s.store.DB(), id)
	}
	s.auditEnqueue("user.role", r, "", map[string]string{
		"target_user_id": strconv.FormatInt(id, 10),
		"role":           body.Role,
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "role": body.Role})
}

func (s *Server) handleAdminUserDelete(w http.ResponseWriter, r *http.Request, id int64) {
	res, err := s.store.DB().ExecContext(r.Context(),
		`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	s.auditEnqueue("user.delete", r, "", map[string]string{
		"target_user_id": strconv.FormatInt(id, 10),
	})
	w.WriteHeader(http.StatusNoContent)
}
