package app

import (
	"net/http"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/principal"
)

// recordAudit enqueues evt on the buffered audit drainer when one is wired.
// The nil-guard is the single source of truth for audit fan-out — all other
// audit-emitting helpers route through here so a future "no audit drainer"
// configuration only needs to update one site.
func (s *Server) recordAudit(evt audit.Event) {
	if s.auditDrainer == nil {
		return
	}
	s.auditDrainer.Enqueue(evt)
}

// auditEnqueue is a hot-path-safe shorthand that derives the principal
// fields from the request context. Use it for post-auth handlers; pre-auth
// handlers (login/OIDC) build the Event directly and call recordAudit.
//
// Side effect: flips the per-request audit marker via markRequestAudited
// so auditMiddleware (Stage 2b RT-1) skips its default `http.<method>`
// fallback emission. Handlers that emit a semantic action (e.g.
// "workspace.create") therefore replace — not duplicate — the
// middleware-default row.
func (s *Server) auditEnqueue(action string, r *http.Request, resourceID string, extra map[string]string) {
	evt := audit.Event{
		At:       time.Now().UTC(),
		Action:   action,
		Metadata: extra,
	}
	if p, err := principal.FromContext(r.Context()); err == nil {
		evt.PrincipalID = p.ID
		evt.PrincipalType = string(p.Type)
		evt.WorkspaceID = p.WorkspaceID
	}
	evt.ResourceID = resourceID
	s.recordAudit(evt)
	markRequestAudited(r)
}
