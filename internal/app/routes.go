package app

import (
	"net/http"

	"github.com/dhanesh/phronesis/internal/ratelimit"
	"github.com/dhanesh/phronesis/internal/xssdefense"
)

// routes assembles the HTTP mux + middleware chain. The middleware order is
// (outermost -> innermost):
//
//	logging -> CSP -> attachPrincipal -> recover -> mux
//
// CSP sits between logging and principal so every response — including 401s
// from the principal middleware — carries the Content-Security-Policy header.
// loggingMiddleware stays outermost so its log line covers the full request,
// not just the inner handler.
//
// recoverMiddleware sits closest to the mux so any handler panic is caught
// before bubbling to net/http's default recover (which would log to stderr
// outside the redact pipeline). Routes RT-6 (BINDING) cross-cutting wiring
// at the panic-recover boundary — closes G7 from m5-verify.
func (s *Server) routes(authRateLimiter *ratelimit.Limiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/readyz", s.handleReadyz) // Wave-5 / RT-8
	mux.Handle("/api/login", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/api/login", "/api/auth/"},
	}, http.HandlerFunc(s.handleLogin)))
	// INT-8: OIDC login route is always mounted; the handler returns 404
	// when OIDC is not configured. This keeps the path predictable for
	// clients and makes "is OIDC enabled?" introspectable via HTTP.
	mux.Handle("/api/auth/oidc/login", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/api/auth/"},
	}, http.HandlerFunc(s.handleOIDCLogin)))
	mux.HandleFunc("/api/logout", s.withAuth(s.handleLogout))
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/pages", s.withAuth(s.handlePages))
	mux.HandleFunc("/api/pages/", s.withAuth(s.handlePageRoutes))
	// Multi-workspace: list metadata for any authed user; CRUD requires
	// admin role (gated via withAdmin).
	mux.HandleFunc("/api/workspaces", s.withAuth(s.handleWorkspacesList))
	mux.HandleFunc("/api/workspaces/", s.withAuth(s.handleWorkspaceRoutes))
	mux.HandleFunc("/api/admin/workspaces", s.withAuth(s.withAdmin(s.handleAdminWorkspaces)))
	mux.HandleFunc("/api/admin/workspaces/", s.withAuth(s.withAdmin(s.handleAdminWorkspaces)))

	// user-mgmt-mcp Stage 1b: admin Users + Keys + Key-Request surfaces
	// (RT-9 server side). Backed by the SQLite store (RT-8); endpoints
	// return 503 when StorePath is unset.
	mux.HandleFunc("/api/admin/users", s.withAuth(s.withAdmin(s.handleAdminUsers)))
	mux.HandleFunc("/api/admin/users/", s.withAuth(s.withAdmin(s.handleAdminUsers)))
	mux.HandleFunc("/api/admin/keys", s.withAuth(s.withAdmin(s.handleAdminKeys)))
	mux.HandleFunc("/api/admin/keys/", s.withAuth(s.withAdmin(s.handleAdminKeys)))

	mux.HandleFunc("/w/", s.withAuth(s.handleWikiPage))

	// INT-3: mount /media routes BEFORE the catch-all so the ServeMux's
	// longest-prefix match picks them up.
	s.media.Routes(mux)

	mux.HandleFunc("/", s.handleApp)

	return loggingMiddleware(xssdefense.CSPMiddleware("",
		s.attachPrincipal(s.auditMiddleware(recoverMiddleware(mux)))))
}
