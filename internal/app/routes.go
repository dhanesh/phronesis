package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/dhanesh/phronesis/internal/mcp"
	"github.com/dhanesh/phronesis/internal/ratelimit"
	"github.com/dhanesh/phronesis/internal/xssdefense"
)

// slogRateDeny emits a structured warning when the per-key limiter rejects
// a request. Kept small so route assembly stays readable.
//
// The prefix is the Principal.ID (key prefix display id, e.g. phr_live_…),
// which is safe to log per RT-6 — only the secret suffix is redacted.
func slogRateDeny(prefix, path string) {
	slog.LogAttrs(context.Background(), slog.LevelWarn, "rate limit per-key deny",
		slog.String("component", "phronesis"),
		slog.String("key_prefix", prefix),
		slog.String("path", path),
	)
}

// routes assembles the HTTP mux + middleware chain. The middleware order is
// (outermost -> innermost):
//
//	logging -> CSP -> attachPrincipal -> perKeyRateLimit -> perKeyConcurrency -> audit -> recover -> mux
//
// CSP sits between logging and principal so every response — including 401s
// from the principal middleware — carries the Content-Security-Policy header.
// loggingMiddleware stays outermost so its log line covers the full request,
// not just the inner handler.
//
// perKeyRateLimit + perKeyConcurrency both sit after attachPrincipal because
// they need the resolved Principal to key on. Both are no-ops for
// unauthenticated and user-cookie requests; only service-account principals
// (bearer phr_live_… or self-issued JWT) are gated. Together they close
// RT-7 + S6 — the sliding window bounds rate over time, the in-flight cap
// bounds parallel work at any one instant.
//
// recoverMiddleware sits closest to the mux so any handler panic is caught
// before bubbling to net/http's default recover (which would log to stderr
// outside the redact pipeline). Routes RT-6 (BINDING) cross-cutting wiring
// at the panic-recover boundary — closes G7 from m5-verify.
func (s *Server) routes(authRateLimiter, keyRateLimiter *ratelimit.Limiter, keyConcurrency *ratelimit.Semaphore) http.Handler {
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

	// user-mgmt-mcp Stage 3a: OAuth 2.1 server. Mounted unconditionally;
	// the handlers themselves return 503 when OAuth is not configured so
	// clients see a stable response shape regardless of toggle state.
	// /authorize, /token, /register share the per-IP auth-endpoint floor
	// (RT-10) — the same window that protects /api/login.
	mux.Handle("/oauth/authorize", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/oauth/"},
	}, http.HandlerFunc(s.handleOAuthAuthorize)))
	mux.Handle("/oauth/token", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/oauth/"},
	}, http.HandlerFunc(s.handleOAuthToken)))
	mux.Handle("/oauth/register", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/oauth/"},
	}, http.HandlerFunc(s.handleOAuthRegister)))
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleOAuthMetadata)
	mux.HandleFunc("/.well-known/jwks.json", s.handleJWKS)

	// user-mgmt-mcp Stage 3b: MCP HTTP transport. Mounted at /mcp
	// behind its own isolated recover middleware (RT-11 / T6) — a
	// panic inside any tool dispatcher returns a JSON-RPC error
	// envelope and never reaches net/http's default recover. When
	// OAuth is not configured the route returns 503 (s.mcp == nil).
	if s.mcp != nil {
		mux.Handle("/mcp", mcp.RecoverMiddleware(s.mcp))
	} else {
		mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "mcp transport requires oauth")
		})
	}

	mux.HandleFunc("/w/", s.withAuth(s.handleWikiPage))

	// INT-3: mount /media routes BEFORE the catch-all so the ServeMux's
	// longest-prefix match picks them up.
	s.media.Routes(mux)

	mux.HandleFunc("/", s.handleApp)

	concurrency := ratelimit.PerKeyConcurrencyMiddleware(ratelimit.PerKeyConcurrencyConfig{
		Semaphore: keyConcurrency,
		OnDeny: func(prefix, path string) {
			slogConcurrencyDeny(prefix, path)
		},
	}, s.auditMiddleware(recoverMiddleware(mux)))

	perKey := ratelimit.PerKeyMiddleware(ratelimit.PerKeyConfig{
		Limiter: keyRateLimiter,
		OnDeny: func(prefix, path string) {
			slogRateDeny(prefix, path)
		},
	}, concurrency)

	return loggingMiddleware(xssdefense.CSPMiddleware("",
		s.attachPrincipal(perKey)))
}

// slogConcurrencyDeny mirrors slogRateDeny for in-flight cap rejections.
// Same RT-6 redaction rationale — key_prefix is a non-secret display id.
func slogConcurrencyDeny(prefix, path string) {
	slog.LogAttrs(context.Background(), slog.LevelWarn, "rate limit per-key concurrency deny",
		slog.String("component", "phronesis"),
		slog.String("key_prefix", prefix),
		slog.String("path", path),
	)
}
