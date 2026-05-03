package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/principal"
)

// markerCell holds the per-request "audited?" flag. Pointer-typed
// so handlers can flip it without replacing the context.
type markerCell struct {
	handled bool
}

type markerSentinelType struct{}

var markerSentinel markerSentinelType

// markRequestAudited flips the per-request audited flag. Called by
// auditEnqueue after the event is enqueued so auditMiddleware skips
// its default fallback emission.
func markRequestAudited(r *http.Request) {
	if cell, ok := r.Context().Value(markerSentinel).(*markerCell); ok {
		cell.handled = true
	}
}

// auditMiddleware ensures every authenticated request produces at
// least one audit row. After running the inner handler it emits a
// default `http.<method>` event UNLESS:
//
//  1. The request had no resolved principal (unauthenticated path
//     such as /api/health, /api/login pre-auth, 401-rejected calls).
//  2. A handler already called auditEnqueue (marker flipped via
//     markRequestAudited).
//
// The default action name is intentionally generic — handlers that
// want a semantic action keep calling the existing s.auditEnqueue
// helper. The middleware is the safety net that catches handlers
// that haven't been instrumented.
//
// Satisfies: B1 (every authenticated action attributable to a named
//
//	principal), RT-1 (universal audit middleware).
//
// Placement: routes.go wires this AFTER attachPrincipal (principal
// is in context) and BEFORE recoverMiddleware (panic-induced 500s
// still produce an audit row from the deferred emission).
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cell := &markerCell{}
		r = r.WithContext(context.WithValue(r.Context(), markerSentinel, cell))

		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)

		p, err := principal.FromContext(r.Context())
		if err != nil {
			return // unauthenticated; nothing to attribute
		}
		if cell.handled {
			return // handler already audited via auditEnqueue
		}

		evt := audit.Event{
			At:            time.Now().UTC(),
			Action:        "http." + strings.ToLower(r.Method),
			PrincipalID:   p.ID,
			PrincipalType: string(p.Type),
			WorkspaceID:   p.WorkspaceID,
			Metadata: map[string]string{
				"path":     r.URL.Path,
				"status":   strconv.Itoa(rec.status),
				"duration": time.Since(start).Round(time.Millisecond).String(),
			},
		}
		s.recordAudit(evt)
	})
}
