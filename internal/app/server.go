package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/auth"
	"github.com/dhanesh/phronesis/internal/auth/oidc"
	"github.com/dhanesh/phronesis/internal/blob"
	"github.com/dhanesh/phronesis/internal/crdt"
	"github.com/dhanesh/phronesis/internal/journal"
	"github.com/dhanesh/phronesis/internal/media"
	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/ratelimit"
	"github.com/dhanesh/phronesis/internal/sessions"
	"github.com/dhanesh/phronesis/internal/snapshot"
	"github.com/dhanesh/phronesis/internal/webfs"
	"github.com/dhanesh/phronesis/internal/wiki"
	"github.com/dhanesh/phronesis/internal/xssdefense"
)

type Config struct {
	Addr          string
	PagesDir      string
	FrontendDist  string
	AdminUser     string
	AdminPassword string

	// Wave-2/3 additions (INT-4, INT-11).
	BlobDir        string // where media blobs live; default "./data/blobs"
	BlobQuotaBytes int64  // per-workspace storage quota (S6); default 5GB
	MaxUploadBytes int64  // per-request upload cap (S5/T5); default 2MB

	// INT-2: opt-in API-KEY authentication. If set, requests bearing a
	// matching API-KEY header authenticate as a workspace service account.
	// This is a v1 single-key stub; full user-PAT + per-workspace service-
	// account key management (S1) is deferred to a later wave.
	APIKey string

	// INT-5: audit log destination. Default "./data/audit.log". Auth + write
	// + read events are buffered and flushed asynchronously so the S9
	// off-hot-path contract holds.
	AuditLog string

	// RT-7 / RT-8 (Wave-5): push-journal file path. When set, /readyz blocks
	// on journal being empty (RT-7.2 gating). When empty, the journal check
	// is skipped — useful for tests and for deployments that have not yet
	// wired git-push spillover.
	JournalPath string

	// INT-14 (Wave-4): auth-endpoint rate-limit tuning. RT-10/TN7 floor
	// applies to /api/login + /api/auth/*. Default window=1min, max=50
	// (generous enough that legitimate users + tests never trip it; strict
	// enough that credential-stuffing at scale gets throttled).
	AuthRateLimitWindow time.Duration
	AuthRateLimitMax    int

	// INT-6 (Wave-3): snapshot scheduler. When SnapshotDir is non-empty,
	// NewServer constructs a snapshot.LocalFSTarget + Scheduler that
	// periodically backs up wiki.Store + blob.LocalFSStore.
	SnapshotDir      string        // default "./data/snapshots" when PagesDir is set
	SnapshotInterval time.Duration // default 1h per O7

	// INT-8 (Wave-3): OIDC login. When OIDCEnabled, NewServer constructs
	// an oidc.Adapter and mounts POST /api/auth/oidc/login. The default
	// verifier is HMAC-stub for testing; production deployments swap in
	// an RS256 verifier (not yet wired).
	OIDCEnabled  bool
	OIDCIssuer   string
	OIDCAudience string
	OIDCSecret   string // HMAC shared secret (stub mode only)
}

type Server struct {
	cfg      Config
	auth     *auth.Manager
	store    *wiki.Store
	hub      *wiki.Hub
	http     *http.Server
	staticFS http.Handler

	// Wave-2/3 additions (INT-3, INT-4, INT-5).
	blobStore    blob.Store
	media        *media.Handler
	auditSink    *audit.FileSink
	auditDrainer *audit.BufferedDrainer

	// INT-1: externalizable session store (optional).
	sessionStore sessions.Store

	// INT-6: periodic workspace snapshots (nil when disabled).
	snapshotScheduler *snapshot.Scheduler

	// INT-7: CRDT broadcaster + push-spillover journal (composed but not
	// yet consumed by editor handlers — deferred to editor-feature work).
	broadcaster *crdt.InProcBroadcaster
	journal     *journal.Journal

	// INT-8: OIDC adapter (nil when OIDCEnabled=false).
	oidc *oidc.Adapter
}

func LoadConfig() Config {
	return Config{
		Addr:          env("PHRONESIS_ADDR", ":8080"),
		PagesDir:      env("PHRONESIS_PAGES_DIR", "./data/pages"),
		FrontendDist:  env("PHRONESIS_FRONTEND_DIST", "./frontend/dist"),
		AdminUser:     env("PHRONESIS_ADMIN_USER", "admin"),
		AdminPassword: env("PHRONESIS_ADMIN_PASSWORD", "admin123"),

		// Wave-2/3 defaults (S5 2MB body cap, S6 5GB per-workspace quota).
		BlobDir:        env("PHRONESIS_BLOB_DIR", "./data/blobs"),
		BlobQuotaBytes: blob.DefaultQuotaBytes,
		MaxUploadBytes: 2 * 1024 * 1024,

		// INT-2: API-KEY auth is off unless explicitly configured.
		APIKey: env("PHRONESIS_API_KEY", ""),

		// INT-5: audit log default under data dir.
		AuditLog: env("PHRONESIS_AUDIT_LOG", "./data/audit.log"),

		// Wave-5: journal path. Empty disables journal readiness check.
		// Populate via env when wiring git-push spillover (future integration).
		JournalPath: env("PHRONESIS_JOURNAL_PATH", ""),

		// INT-6: snapshot scheduler defaults.
		SnapshotDir:      env("PHRONESIS_SNAPSHOT_DIR", ""),
		SnapshotInterval: time.Hour,

		// INT-8: OIDC disabled by default. Enable by setting
		// PHRONESIS_OIDC_ENABLED=1 + the provider fields below.
		OIDCEnabled:  env("PHRONESIS_OIDC_ENABLED", "") != "",
		OIDCIssuer:   env("PHRONESIS_OIDC_ISSUER", ""),
		OIDCAudience: env("PHRONESIS_OIDC_AUDIENCE", ""),
		OIDCSecret:   env("PHRONESIS_OIDC_SECRET", ""),
	}
}

// defaultWorkspaceID names the single implicit workspace v1 ships with.
// Wave-3b (or later) introduces real workspace resolution from URL/subdomain.
const defaultWorkspaceID = "default"

func NewServer(cfg Config) (*Server, error) {
	// Review response I5/M5: emit the frontend-mode signal at construction
	// time so operators see it immediately in logs rather than on the first
	// HTTP request. The stub warning is explicitly loud so a missing
	// -tags=prod on a production build is impossible to overlook.
	if webfs.IsStub() {
		log.Printf("[phronesis] WARNING: dev-stub frontend active. " +
			"Build with `make build` or `go build -tags=prod ./cmd/phronesis` for production.")
	} else {
		log.Printf("[phronesis] frontend: serving embedded production assets")
	}

	// Apply defaults for Wave-2/3 Config fields so minimal Configs (e.g. tests)
	// continue to work unchanged. LoadConfig sets richer defaults via env().
	if cfg.BlobDir == "" {
		cfg.BlobDir = filepath.Join(cfg.PagesDir, "..", "blobs")
	}
	if cfg.BlobQuotaBytes == 0 {
		cfg.BlobQuotaBytes = blob.DefaultQuotaBytes
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 2 * 1024 * 1024
	}
	if cfg.AuditLog == "" {
		cfg.AuditLog = filepath.Join(cfg.PagesDir, "..", "audit.log")
	}
	if cfg.AuthRateLimitWindow == 0 {
		cfg.AuthRateLimitWindow = time.Minute
	}
	if cfg.AuthRateLimitMax == 0 {
		cfg.AuthRateLimitMax = 50
	}

	store, err := wiki.NewStore(cfg.PagesDir)
	if err != nil {
		return nil, err
	}

	// INT-4: blob store for media. Keeps binary content OUT of the markdown
	// corpus (TN4). Per-workspace quota and content-type allow-list are
	// enforced inside the store (S6).
	blobStore, err := blob.NewLocalFSStore(cfg.BlobDir, blob.Config{
		QuotaBytes: cfg.BlobQuotaBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create blob store: %w", err)
	}

	// INT-3: media HTTP handler. Serves GET /media/<sha> and POST /media.
	mediaHandler := media.NewHandler(blobStore, cfg.MaxUploadBytes)

	// INT-5: audit sink + async buffered drainer. The drainer enforces S9
	// (off-hot-path) by accepting events in O(1) Enqueue calls and flushing
	// to the FileSink from a background goroutine.
	auditSink, err := audit.NewFileSink(cfg.AuditLog)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	auditDrainer := audit.NewBufferedDrainer(auditSink, audit.DrainerConfig{})

	// INT-1: externalizable session store. Default is in-process MemStore,
	// consumed by auth.Manager via WithStore. A future deployment can swap
	// in a Postgres/Redis-backed Store without touching auth.Manager's API.
	sessionStore := sessions.NewMemStore()
	authManager := auth.NewManager(cfg.AdminUser, cfg.AdminPassword).WithStore(sessionStore)

	// INT-7: CRDT broadcaster (in-process fan-out). Composed as Server
	// field so a future editor-feature wave can consume it; no handler
	// wiring happens at this layer to preserve the TN3 resolution
	// (single-replica v1 with abstractions).
	broadcaster := crdt.NewInProcBroadcaster(64)

	// INT-7: push-spillover journal. Opened when JournalPath is set; the
	// /readyz check (Wave-5) already guards against unreplayed entries.
	var journalFile *journal.Journal
	if cfg.JournalPath != "" {
		journalFile, err = journal.Open(cfg.JournalPath)
		if err != nil {
			return nil, fmt.Errorf("open journal: %w", err)
		}
	}

	// INT-8: OIDC adapter. Token-first flow — POST /api/auth/oidc/login
	// accepts a signed id_token and issues a session cookie on success.
	var oidcAdapter *oidc.Adapter
	if cfg.OIDCEnabled {
		if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" || cfg.OIDCSecret == "" {
			return nil, fmt.Errorf("oidc: Issuer, Audience, and Secret are required when OIDCEnabled=true")
		}
		oidcAdapter, err = oidc.NewAdapter(oidc.Config{
			Issuer:   cfg.OIDCIssuer,
			Audience: cfg.OIDCAudience,
			Verifier: &oidc.HMACVerifier{Secret: []byte(cfg.OIDCSecret)},
			ClaimMapping: oidc.ClaimMapping{
				SchemaVersion:    oidc.CurrentSchemaVersion,
				UserIDClaim:      "sub",
				DisplayNameClaim: "name",
				RoleBinding:      func(map[string]any) principal.Role { return principal.RoleAdmin },
				WorkspaceBinding: func(map[string]any) string { return defaultWorkspaceID },
			},
		})
		if err != nil {
			return nil, fmt.Errorf("oidc: %w", err)
		}
	}

	app := &Server{
		cfg:          cfg,
		auth:         authManager,
		store:        store,
		hub:          wiki.NewHub(store),
		staticFS:     staticHandler(cfg.FrontendDist),
		blobStore:    blobStore,
		media:        mediaHandler,
		auditSink:    auditSink,
		auditDrainer: auditDrainer,
		sessionStore: sessionStore,
		broadcaster:  broadcaster,
		journal:      journalFile,
		oidc:         oidcAdapter,
	}

	// INT-6: snapshot scheduler. When SnapshotDir is non-empty, construct
	// a LocalFSTarget + Scheduler walking wiki.Store + blob.LocalFSStore
	// on the default workspace. Scheduler is started after the server is
	// assembled (so tests can observe it) and stopped by Server.Close.
	if cfg.SnapshotDir != "" {
		target, err := snapshot.NewLocalFSTarget(cfg.SnapshotDir)
		if err != nil {
			return nil, fmt.Errorf("snapshot target: %w", err)
		}
		app.snapshotScheduler = snapshot.NewScheduler(target, &wikiSource{store: store, blobs: blobStore}, cfg.SnapshotInterval, nil)
	}

	// INT-14: always-on rate-limit floor for auth endpoints. Satisfies RT-10,
	// TN7 (server-side backstop even if reverse proxy is misconfigured).
	authRateLimiter := ratelimit.NewLimiter(cfg.AuthRateLimitWindow, cfg.AuthRateLimitMax)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", app.handleHealth)
	mux.HandleFunc("/api/readyz", app.handleReadyz) // Wave-5 / RT-8
	mux.Handle("/api/login", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/api/login", "/api/auth/"},
	}, http.HandlerFunc(app.handleLogin)))
	// INT-8: OIDC login route is always mounted; the handler returns 404
	// when OIDC is not configured. This keeps the path predictable for
	// clients and makes "is OIDC enabled?" introspectable via HTTP.
	mux.Handle("/api/auth/oidc/login", ratelimit.Middleware(ratelimit.Config{
		Limiter:      authRateLimiter,
		PathPrefixes: []string{"/api/auth/"},
	}, http.HandlerFunc(app.handleOIDCLogin)))
	mux.HandleFunc("/api/logout", app.withAuth(app.handleLogout))
	mux.HandleFunc("/api/session", app.handleSession)
	mux.HandleFunc("/api/pages", app.withAuth(app.handlePages))
	mux.HandleFunc("/api/pages/", app.withAuth(app.handlePageRoutes))
	mux.HandleFunc("/w/", app.withAuth(app.handleWikiPage))

	// INT-3: mount /media routes BEFORE the catch-all so the ServeMux's
	// longest-prefix match picks them up.
	mediaHandler.Routes(mux)

	mux.HandleFunc("/", app.handleApp)

	// Middleware chain (outermost -> innermost):
	//   logging -> CSP (INT-13, RT-9.3) -> attachPrincipal (INT-2, RT-5) -> mux
	// CSP sits between logging and principal so every response — including
	// 401s from the principal middleware — carries the Content-Security-Policy
	// header. Only loggingMiddleware is outside CSP so we can log requests
	// that are served through the CSP wrapper without re-entry issues.
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           loggingMiddleware(xssdefense.CSPMiddleware("", app.attachPrincipal(mux))),
		ReadHeaderTimeout: 5 * time.Second,
	}
	app.http = server

	// INT-6: start the snapshot scheduler after the Server is fully
	// assembled. Stop is handled by Server.Close.
	if app.snapshotScheduler != nil {
		if err := app.snapshotScheduler.Start(); err != nil {
			return nil, fmt.Errorf("snapshot scheduler: %w", err)
		}
	}

	return app, nil
}

// HTTP returns the underlying *http.Server so callers can call ListenAndServe
// or Shutdown directly. Most callers should use Server.Serve and Server.Close
// instead (they compose the HTTP lifecycle with background goroutine drain).
func (s *Server) HTTP() *http.Server { return s.http }

// Serve runs the HTTP server until ctx is cancelled, then performs graceful
// shutdown: stops accepting new connections (bounded by drainTimeout),
// finishes in-flight requests, and drains background goroutines via Close.
// Returns nil on clean shutdown, or an error from either ListenAndServe
// (pre-shutdown failures) or the shutdown chain.
//
// Satisfies: O5 (graceful drain), RT-12.1 (bounded drain window), INT-10
//
// Typical usage (main.go):
//
//	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
//	defer cancel()
//	if err := server.Serve(ctx, 30*time.Second); err != nil { log.Fatal(err) }
func (s *Server) Serve(ctx context.Context, drainTimeout time.Duration) error {
	if drainTimeout <= 0 {
		drainTimeout = 30 * time.Second
	}

	// Run ListenAndServe in a goroutine so we can select on it alongside ctx.
	// http.ErrServerClosed is the expected termination from Shutdown and is
	// not surfaced as an error.
	serverErr := make(chan error, 1)
	go func() {
		err := s.http.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		// ListenAndServe failed BEFORE any shutdown signal (port in use, etc).
		// Still drain audit buffer so any events that made it past the hot
		// path are persisted.
		closeCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		_ = s.Close(closeCtx)
		return err
	case <-ctx.Done():
		// Graceful shutdown path. Bound both phases by drainTimeout.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()

		var firstErr error
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			firstErr = fmt.Errorf("http shutdown: %w", err)
		}
		if err := s.Close(shutdownCtx); err != nil {
			if firstErr != nil {
				return fmt.Errorf("%w; close: %v", firstErr, err)
			}
			firstErr = fmt.Errorf("close: %w", err)
		}
		// Wait for the ListenAndServe goroutine to actually exit so we don't
		// leak it past Serve's return.
		<-serverErr
		return firstErr
	}
}

// Close drains background goroutines: audit buffer (INT-5), and later the
// snapshot scheduler (INT-6) + CRDT rooms (INT-7). It DOES NOT stop the HTTP
// listener — call s.HTTP().Shutdown(ctx) first, then Close(ctx). This split
// lets SIGTERM handling drain in-flight requests before flushing audit events
// queued by those requests.
//
// Satisfies: O5 partial (INT-5 audit drain), RT-12.2 partial (INT-10 full drain)
//
// Close order (important):
//  1. Stop snapshot scheduler (INT-6) — no new snapshots kick off during drain.
//  2. Close audit drainer (INT-5) — flush in-flight events to disk.
//  3. Close journal (INT-7) — flush any queued push entries.
//
// Each step's error is collected so operators see the full picture even if
// multiple subsystems misbehave.
func (s *Server) Close(ctx context.Context) error {
	var firstErr error
	if s.snapshotScheduler != nil {
		if err := s.snapshotScheduler.Stop(ctx); err != nil {
			firstErr = fmt.Errorf("snapshot scheduler stop: %w", err)
		}
	}
	if s.auditDrainer != nil {
		if err := s.auditDrainer.Close(ctx); err != nil {
			if firstErr != nil {
				return fmt.Errorf("%w; audit drainer close: %v", firstErr, err)
			}
			firstErr = fmt.Errorf("audit drainer close: %w", err)
		}
	}
	if s.journal != nil {
		if err := s.journal.Close(); err != nil {
			if firstErr != nil {
				return fmt.Errorf("%w; journal close: %v", firstErr, err)
			}
			firstErr = fmt.Errorf("journal close: %w", err)
		}
	}
	return firstErr
}

// auditEnqueue is a hot-path-safe shorthand for drainer.Enqueue, guarded for
// the nil-drainer case (not currently reachable but defensive).
func (s *Server) auditEnqueue(action string, r *http.Request, resourceID string, extra map[string]string) {
	if s.auditDrainer == nil {
		return
	}
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
	s.auditDrainer.Enqueue(evt)
}

// principalFromRequest resolves the request's credentials to a Principal.
// Returns (Principal{}, false) if no valid credential is present.
//
// Satisfies: RT-5 (canonical Principal over multiple auth planes), INT-2
//
// V1 resolution order:
//  1. Cookie session from auth.Manager  -> user principal (admin of default workspace)
//  2. API-KEY header (if PHRONESIS_API_KEY configured) -> service_account principal (editor)
//
// Both paths converge on principal.Principal, so downstream authz is identical
// regardless of auth mechanism.
func (s *Server) principalFromRequest(r *http.Request) (principal.Principal, bool) {
	if resolved, ok := s.auth.Resolve(r); ok {
		// Prefer the store-backed session fields (OIDC sets PrincipalType +
		// WorkspaceID + auth_method=oidc metadata) and fall back to defaults
		// for the legacy in-memory path which only carries username.
		// Satisfies: I1 review fix, RT-5 (correct auth_method preserved in audit).
		ptype := principal.Type(resolved.PrincipalType)
		if ptype == "" {
			ptype = principal.TypeUser
		}
		id := resolved.UserID
		if id == "" {
			id = resolved.Username
		}
		wsID := resolved.WorkspaceID
		if wsID == "" {
			wsID = defaultWorkspaceID
		}
		claims := map[string]string{"auth_method": "password"}
		if resolved.Metadata != nil {
			if am, ok := resolved.Metadata["auth_method"]; ok && am != "" {
				claims["auth_method"] = am
			}
		}
		return principal.Principal{
			Type:        ptype,
			ID:          id,
			WorkspaceID: wsID,
			Role:        principal.RoleAdmin,
			Claims:      claims,
		}, true
	}
	if s.cfg.APIKey != "" {
		if key := r.Header.Get("API-KEY"); key != "" && key == s.cfg.APIKey {
			return principal.Principal{
				Type:        principal.TypeServiceAccount,
				ID:          "default-api-key",
				WorkspaceID: defaultWorkspaceID,
				Role:        principal.RoleEditor,
				Claims:      map[string]string{"auth_method": "api_key"},
			}, true
		}
	}
	return principal.Principal{}, false
}

// attachPrincipal is a middleware that resolves Principal from the request and
// attaches it to the context. It DOES NOT enforce authentication; handlers that
// need authn call principal.FromContext/Require. Handlers that don't need it
// (e.g. /api/health, /api/login) simply ignore the context value.
func (s *Server) attachPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.principalFromRequest(r); ok {
			r = r.WithContext(principal.WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

func staticHandler(frontendDist string) http.Handler {
	if info, err := os.Stat(frontendDist); err == nil && info.IsDir() {
		return http.FileServer(http.Dir(frontendDist))
	}
	return http.FileServerFS(webfs.FS())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	username, ok := s.auth.Username(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": ok,
		"username":      username,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login request")
		return
	}
	token, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		// INT-5: record failed login attempts. Useful for S2 audit and for
		// future RT-10 rate-limit floor (attacker-visibility via audit).
		s.auditDrainer.Enqueue(audit.Event{
			At:       time.Now().UTC(),
			Action:   "auth.login_failed",
			Metadata: map[string]string{"username": req.Username, "reason": err.Error()},
		})
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// INT-5: record successful login. Principal not yet attached to ctx
	// (login is pre-auth), so we record the resolved username directly.
	s.auditDrainer.Enqueue(audit.Event{
		At:            time.Now().UTC(),
		Action:        "auth.login",
		PrincipalID:   req.Username,
		PrincipalType: string(principal.TypeUser),
		WorkspaceID:   defaultWorkspaceID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"username": req.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err == nil {
		s.auth.Logout(cookie.Value)
	}
	// INT-5: logout event (Principal still attached to ctx via attachPrincipal).
	s.auditEnqueue("auth.logout", r, "", nil)
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	pages, err := s.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (s *Server) handlePageRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pages/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing page name")
		return
	}
	if strings.HasSuffix(path, "/events") {
		name := strings.TrimSuffix(path, "/events")
		s.handleEvents(w, r, name)
		return
	}
	s.handlePage(w, r, path)
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		page, err := s.hub.Snapshot(name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// INT-5: document view (read event). S9 contract: Enqueue is O(1)
		// and does not block the read hot path.
		s.auditEnqueue("doc.view", r, name, nil)
		writeJSON(w, http.StatusOK, map[string]any{"page": page})
	case http.MethodPost:
		var req struct {
			Content     string `json:"content"`
			BaseVersion int64  `json:"baseVersion"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid update request")
			return
		}
		username, _ := s.auth.Username(r)
		page, merged, err := s.hub.Apply(name, req.BaseVersion, req.Content, username)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// INT-5: document write event. `merged` flag indicates conflict
		// resolution ran, which is operationally interesting.
		s.auditEnqueue("doc.edit", r, name, map[string]string{
			"merged": fmt.Sprintf("%t", merged),
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"page":   page,
			"merged": merged,
		})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel, err := s.hub.Subscribe(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case event := <-ch:
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
	pageName := strings.TrimPrefix(r.URL.Path, "/w/")
	if pageName == "" {
		pageName = "home"
	}
	page, err := s.hub.Snapshot(pageName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": page})
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		s.serveIndex(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") {
		s.staticFS.ServeHTTP(w, r)
		return
	}
	s.serveIndex(w, r)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/health" {
			next(w, r)
			return
		}
		if _, ok := s.auth.Username(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "authentication required")
			} else {
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			}
			return
		}
		next(w, r)
	}
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(s.cfg.FrontendDist, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return
	}

	data, err := fs.ReadFile(webfs.FS(), "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func Must(err error) {
	if err != nil {
		panic(err)
	}
}

// handleOIDCLogin is the token-first OIDC login handler (INT-8).
//
// Request:  POST /api/auth/oidc/login  Body: {"id_token": "..."}
// Success: 200 + session cookie + {"username": "<principal-id>"}
// Failure: 401 with a sanitized error message (never echoes the token back).
//
// Satisfies: RT-11, S4, S8, TN5
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Error(w, "oidc not enabled", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IDToken == "" {
		writeError(w, http.StatusBadRequest, "invalid oidc login request")
		return
	}
	p, err := s.oidc.Authenticate(r.Context(), req.IDToken)
	if err != nil {
		// Audit the failure but do not leak error details to the client.
		s.auditDrainer.Enqueue(audit.Event{
			At:       time.Now().UTC(),
			Action:   "auth.oidc_failed",
			Metadata: map[string]string{"reason": "invalid id_token"},
		})
		writeError(w, http.StatusUnauthorized, "invalid id_token")
		return
	}

	// Issue a session using the same store-backed Login path as password auth.
	// We directly persist a session record keyed by a random token so the
	// Principal's id becomes the session's username.
	token, err := issueOIDCSession(s.sessionStore, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session issue failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	s.auditDrainer.Enqueue(audit.Event{
		At:            time.Now().UTC(),
		Action:        "auth.oidc_login",
		PrincipalID:   p.ID,
		PrincipalType: string(p.Type),
		WorkspaceID:   p.WorkspaceID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"username": p.ID})
}

// issueOIDCSession creates a cookie-session record in the supplied store for
// the given principal. Kept as a package-private helper to avoid coupling
// Server internals to auth.Manager's password-specific login path.
func issueOIDCSession(store sessions.Store, p principal.Principal) (string, error) {
	// Reuse the same token generator auth.Manager uses for password logins.
	buf := make([]byte, 32)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	token := hexEncode(buf)
	err := store.Put(context.Background(), sessions.Session{
		ID:            token,
		UserID:        p.ID,
		WorkspaceID:   p.WorkspaceID,
		PrincipalType: string(p.Type),
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UTC(),
		Metadata:      map[string]string{"auth_method": "oidc"},
	})
	return token, err
}

// wikiSource adapts wiki.Store + blob.LocalFSStore into snapshot.Source for
// INT-6. V1 enumerates a single "default" workspace; multi-workspace
// enumeration is a future-wave extension.
type wikiSource struct {
	store *wiki.Store
	blobs blob.Store
}

func (w *wikiSource) Workspaces(_ context.Context) ([]string, error) {
	return []string{defaultWorkspaceID}, nil
}

func (w *wikiSource) SnapshotFor(_ context.Context, workspaceID string) (snapshot.Snapshot, error) {
	if workspaceID != defaultWorkspaceID {
		return snapshot.Snapshot{}, fmt.Errorf("unknown workspace %q", workspaceID)
	}
	summaries, err := w.store.List()
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	md := make(map[string][]byte, len(summaries))
	for _, sum := range summaries {
		page, err := w.store.Get(sum.Name)
		if err != nil {
			continue
		}
		md[sum.Name+".md"] = []byte(page.Content)
	}
	// TN4: snapshots include blobs too. Blob enumeration from blob.Store
	// requires per-workspace listing (not currently exposed); v1 skips blob
	// capture here and documents that as a known incompleteness. Future:
	// add blob.Store.Enumerate(workspaceID) + populate snap.Blobs.
	return snapshot.Snapshot{
		WorkspaceID: workspaceID,
		Markdown:    md,
	}, nil
}

// randRead + hexEncode are thin stdlib wrappers over crypto/rand + encoding/hex.
func randRead(buf []byte) (int, error) { return rand.Read(buf) }
func hexEncode(buf []byte) string      { return hex.EncodeToString(buf) }
