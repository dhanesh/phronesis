// Package app composes the HTTP server. server.go owns the Config, the
// Server struct, and the lifecycle (NewServer / Serve / Close). Handlers,
// middleware, audit fan-out, route assembly, and the snapshot source are
// each in their own file; see routes.go for the entry point.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/dhanesh/phronesis/internal/store/sqlite"
	"github.com/dhanesh/phronesis/internal/webfs"
	"github.com/dhanesh/phronesis/internal/wiki"
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

	// user-mgmt-mcp Stage 1a/1b: SQLite-backed projection store for
	// users + api_keys + key_requests + audit_events. Empty disables
	// the store; admin endpoints that depend on it return 503.
	//
	// Default (when unset and PagesDir is set) is "data/phronesis.db",
	// derived in applyConfigDefaults so tests with an empty Config get
	// the in-memory-feeling temp-dir behaviour they expect.
	StorePath string
}

type Server struct {
	cfg        Config
	auth       *auth.Manager
	workspaces *wiki.Workspaces
	http       *http.Server
	staticFS   http.Handler

	// shutdownCh is closed by http.Server's RegisterOnShutdown callback.
	// Long-lived handlers (e.g., SSE) select on it alongside r.Context().Done()
	// so http.Shutdown doesn't have to wait for every client to disconnect.
	// Without this, a single open /api/pages/<name>/events stream would hold
	// Shutdown for the full drainTimeout budget. See commit log for the bug
	// that motivated this (Ctrl-C with active SSE = 30s hang).
	shutdownCh chan struct{}

	// Wave-2/3 additions (INT-3, INT-4, INT-5).
	blobStore    blob.Store
	media        *media.Handler
	auditSink    audit.Sink
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

	// user-mgmt-mcp Stage 1a/1b: SQLite-backed projection store. Nil
	// when StorePath is empty; admin endpoints that depend on it
	// return 503 in that mode.
	store *sqlite.Store

	// Stage 2b-retention: daily audit compactor. Nil when StorePath
	// is empty (FileSink-only path). Stop is called from Server.Close.
	auditCompactor *audit.Compactor

	// Stage 2c-cache: in-process cache mapping bearer-key prefix
	// (phr_live_<...>) to resolved Principal. TTL=30s per TN4 belt;
	// Invalidate is called by revoke/suspend handlers. Nil when
	// StorePath is empty (no keys exist; nothing to cache).
	authCache *auth.Cache
}

// defaultWorkspaceID names the single implicit workspace v1 ships with.
// Wave-3b (or later) introduces real workspace resolution from URL/subdomain.
const defaultWorkspaceID = "default"

// defaultAdminPassword is the password used when PHRONESIS_ADMIN_PASSWORD is
// unset. NewServer logs a loud warning when the running config still uses it
// so a misconfigured production deploy is impossible to miss.
const defaultAdminPassword = "admin123"

func LoadConfig() Config {
	return Config{
		Addr:          env("PHRONESIS_ADDR", ":8080"),
		PagesDir:      env("PHRONESIS_PAGES_DIR", "./data/pages"),
		FrontendDist:  env("PHRONESIS_FRONTEND_DIST", "./frontend/dist"),
		AdminUser:     env("PHRONESIS_ADMIN_USER", "admin"),
		AdminPassword: env("PHRONESIS_ADMIN_PASSWORD", defaultAdminPassword),

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
		// PHRONESIS_OIDC_ENABLED to a parseable bool ("1", "true", "yes")
		// plus the provider fields below. Anything else (including unset,
		// "false", "0", "no", or unparseable values) leaves OIDC off.
		OIDCEnabled:  envBool("PHRONESIS_OIDC_ENABLED"),
		OIDCIssuer:   env("PHRONESIS_OIDC_ISSUER", ""),
		OIDCAudience: env("PHRONESIS_OIDC_AUDIENCE", ""),
		OIDCSecret:   env("PHRONESIS_OIDC_SECRET", ""),

		// user-mgmt-mcp Stage 1a/1b: SQLite-backed store. Empty disables
		// (admin endpoints return 503). Default for the bundled binary is
		// "data/phronesis.db"; tests pass a temp path explicitly.
		StorePath: env("PHRONESIS_STORE_PATH", "./data/phronesis.db"),
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// envBool reads a bool-shaped environment variable. Recognises the values
// strconv.ParseBool accepts ("1", "t", "true", "0", "f", "false", and their
// case variants). Unset or unparseable values return false — the strict
// failure mode that matters for security flags like OIDCEnabled.
func envBool(key string) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return v
}

func applyConfigDefaults(cfg Config) Config {
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
	return cfg
}

func NewServer(cfg Config) (*Server, error) {
	// Review response I5/M5: emit the frontend-mode signal at construction
	// time so operators see it immediately in logs rather than on the first
	// HTTP request. The stub warning is explicitly loud so a missing
	// -tags=prod on a production build is impossible to overlook.
	if webfs.IsStub() {
		slog.Warn("dev-stub frontend active; build with `make build` or "+
			"`go build -tags=prod ./cmd/phronesis` for production",
			slog.String("component", "phronesis"),
			slog.String("frontend", "stub"),
		)
	} else {
		slog.Info("serving embedded production frontend assets",
			slog.String("component", "phronesis"),
			slog.String("frontend", "embedded"),
		)
	}

	if cfg.AdminPassword == defaultAdminPassword {
		slog.Warn("PHRONESIS_ADMIN_PASSWORD is set to the built-in default; "+
			"set the env var to a real value before exposing this server outside localhost",
			slog.String("component", "phronesis"),
		)
	}

	// Satisfies: RT-12 (stub OIDC dev path with loud startup warning).
	// The HMAC stub is the only verifier wired today; until a real OIDC
	// verifier ships, OIDCEnabled implies stub mode. Mirror the
	// webfs.IsStub() loud-warning convention so an operator scanning logs
	// can't miss it.
	if cfg.OIDCEnabled {
		slog.Warn("OIDC adapter is using the HMAC-stub verifier; suitable for dev/eval only. "+
			"Configure a real IdP before exposing this server beyond a trusted network.",
			slog.String("component", "phronesis"),
			slog.String("oidc", "stub"),
		)
	}

	cfg = applyConfigDefaults(cfg)

	workspaceMetaPath := filepath.Join(filepath.Dir(cfg.PagesDir), "workspaces.json")
	workspaces, err := wiki.NewWorkspaces(cfg.PagesDir, workspaceMetaPath)
	if err != nil {
		return nil, err
	}

	// user-mgmt-mcp Stage 1a/1b: open the SQLite store when StorePath
	// is set. Empty disables; admin user/key endpoints fall back to 503
	// rather than crashing — preserves backward compatibility for tests
	// that pass minimal Config{}.
	//
	// O3: Open returns an error and leaves the DB closed if any
	// migration fails; we propagate that as a NewServer failure so the
	// binary refuses to bind a port on a half-migrated schema.
	var sqliteStore *sqlite.Store
	if cfg.StorePath != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.StorePath), 0o755); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
		sqliteStore, err = sqlite.Open(cfg.StorePath)
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}
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

	// INT-5 / RT-10: audit sink + async buffered drainer. The drainer
	// enforces S9 (off-hot-path) by accepting events in O(1) Enqueue
	// calls and flushing from a background goroutine.
	//
	// Stage 2b-substrate: prefer the SQLiteSink when a store is
	// configured. Stage 2b-retention: wrap with SpilloverSink so a
	// crash between batch-append and inner-Write is bounded — the
	// next startup's ReplaySpillover drains pending events into the
	// sink before serving.
	//
	// Tests and configs without a StorePath fall back to the FileSink
	// path so behaviour is preserved for legacy paths.
	var auditSink audit.Sink
	if sqliteStore != nil {
		base := audit.NewSQLiteSink(sqliteStore.DB())

		// Replay any pending spillover from a previous incarnation
		// BEFORE wrapping the sink — the replay path doesn't go
		// through the spillover layer (avoids re-appending the
		// events we're trying to drain).
		spilloverPath := filepath.Join(filepath.Dir(cfg.StorePath), "audit-spillover.jsonl")
		if n, err := audit.ReplaySpillover(context.Background(), spilloverPath, base); err != nil {
			slog.Warn("audit spillover replay failed; continuing with empty journal",
				slog.String("component", "phronesis"),
				slog.String("path", spilloverPath),
				slog.String("err", err.Error()))
		} else if n > 0 {
			slog.Info("audit spillover replayed",
				slog.String("component", "phronesis"),
				slog.Int("events", n))
		}

		spilloverSink, err := audit.NewSpilloverSink(base, spilloverPath)
		if err != nil {
			return nil, fmt.Errorf("open audit spillover: %w", err)
		}
		auditSink = spilloverSink
	} else {
		var err error
		auditSink, err = audit.NewFileSink(cfg.AuditLog)
		if err != nil {
			return nil, fmt.Errorf("open audit log: %w", err)
		}
	}
	auditDrainer := audit.NewBufferedDrainer(auditSink, audit.DrainerConfig{})

	// Stage 2b-retention: schedule the daily compactor when a SQLite
	// store is wired. Folds raw audit_events older than 90 days into
	// per-day audit_aggregates. Stop is called from Server.Close.
	var auditCompactor *audit.Compactor
	if sqliteStore != nil {
		auditCompactor = audit.NewCompactor(sqliteStore.DB(), audit.CompactorConfig{})
		auditCompactor.Start(context.Background())
	}

	// Stage 2c-cache: auth cache for bearer-key resolution. TTL=30s
	// per TN4 belt; revoke/suspend handlers call Invalidate to
	// short-circuit the belt. Nil when StorePath is empty (no keys
	// exist; the cache would have nothing to cache).
	var authCache *auth.Cache
	if sqliteStore != nil {
		authCache = auth.NewCache(30 * time.Second)
	}

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

	oidcAdapter, err := buildOIDCAdapter(cfg)
	if err != nil {
		return nil, err
	}

	app := &Server{
		cfg:            cfg,
		auth:           authManager,
		workspaces:     workspaces,
		staticFS:       staticHandler(cfg.FrontendDist),
		blobStore:      blobStore,
		media:          mediaHandler,
		auditSink:      auditSink,
		auditDrainer:   auditDrainer,
		sessionStore:   sessionStore,
		broadcaster:    broadcaster,
		journal:        journalFile,
		oidc:           oidcAdapter,
		store:          sqliteStore,
		auditCompactor: auditCompactor,
		authCache:      authCache,
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
		app.snapshotScheduler = snapshot.NewScheduler(target, &wikiSource{workspaces: workspaces, blobs: blobStore}, cfg.SnapshotInterval, nil)
	}

	// INT-14: always-on rate-limit floor for auth endpoints. Satisfies RT-10,
	// TN7 (server-side backstop even if reverse proxy is misconfigured).
	authRateLimiter := ratelimit.NewLimiter(cfg.AuthRateLimitWindow, cfg.AuthRateLimitMax)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.routes(authRateLimiter),
		ReadHeaderTimeout: 5 * time.Second,
	}
	app.http = server

	// Wire the server-shutdown signal so long-lived handlers (SSE) can exit
	// promptly instead of waiting for the client to disconnect.
	app.shutdownCh = make(chan struct{})
	server.RegisterOnShutdown(func() { close(app.shutdownCh) })

	// INT-6: start the snapshot scheduler after the Server is fully
	// assembled. Stop is handled by Server.Close.
	if app.snapshotScheduler != nil {
		if err := app.snapshotScheduler.Start(); err != nil {
			return nil, fmt.Errorf("snapshot scheduler: %w", err)
		}
	}

	return app, nil
}

// buildOIDCAdapter constructs the OIDC adapter when OIDC is enabled in cfg.
// Returns (nil, nil) when disabled — the OIDC route is always mounted but
// returns 404 when the adapter is nil.
func buildOIDCAdapter(cfg Config) (*oidc.Adapter, error) {
	if !cfg.OIDCEnabled {
		return nil, nil
	}
	if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" || cfg.OIDCSecret == "" {
		return nil, fmt.Errorf("oidc: Issuer, Audience, and Secret are required when OIDCEnabled=true")
	}
	adapter, err := oidc.NewAdapter(oidc.Config{
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
	return adapter, nil
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
//	if err := server.Serve(ctx, 30*time.Second); err != nil { slog.Error("serve", slog.String("err", err.Error())); os.Exit(1) }
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
	// Stop the compactor BEFORE closing the SQLite store so its
	// in-flight tx (if any) commits cleanly.
	if s.auditCompactor != nil {
		s.auditCompactor.Stop()
	}
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			if firstErr != nil {
				return fmt.Errorf("%w; sqlite store close: %v", firstErr, err)
			}
			firstErr = fmt.Errorf("sqlite store close: %w", err)
		}
	}
	return firstErr
}

// Must panics if err is non-nil. Useful at startup where there's no recovery.
func Must(err error) {
	if err != nil {
		panic(err)
	}
}
