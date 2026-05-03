# phronesis

`phronesis` is a self-hosted knowledge base for humans and AI agents. It stores everything as plain Markdown on disk, makes pages agent-readable through structured wiki primitives (links, tags, frontmatter, attributes), and serves a single-surface live editor for browser editing.

The product is positioned for individuals capturing personal notes, teams sharing context, and AI agents reading and writing alongside their humans. The source of truth stays as plain Markdown in a filesystem-backed space — portable, grep-able, version-controllable — so neither humans nor agents are locked into a proprietary format.

## Current Status

This repository implements:

- file-backed Markdown pages addressed by wiki-style page names, scoped per workspace
- built-in username/password authentication, optional OIDC sign-in, optional break-glass admin
- a Go HTTP server that serves both API endpoints and the compiled frontend
- autosave page editing over HTTP and live page updates over server-sent events
- derived wiki metadata for links, backlinks, tags, and task list items
- a Svelte frontend with a single CodeMirror-based editor surface and SilverBullet-style live preview (headings, bold/italic, inline code, lists, tables, images, fenced code, blockquotes, admonitions, frontmatter pill bar, attribute pills, hashtag chips, wiki-link chips)
- workspace + user + API key administration backed by SQLite, with admin endpoints for workspace CRUD, user suspension/reactivation/deletion, and API key minting/revocation
- an MCP HTTP transport at `/mcp` with OAuth 2.1 + PKCE, dynamic client registration (RFC 7591), JWKS publication, and a per-tool JSON-schema input registry — Claude Code and equivalent MCP clients can connect, complete the OAuth flow once, and use registered tools
- per-bearer-key rate limiting (sliding window + in-flight cap), an async audit pipeline with 90-day raw retention then aggregates, periodic workspace snapshots, and a fsync'd spillover journal for bounded loss-on-crash

Git sync to a remote, CLI automation, and a `/metrics` Prometheus
endpoint are still unimplemented — see "Known Limitations" below.

## Architecture

### Backend

The backend is written in Go and keeps the runtime dependency-light.

- [cmd/phronesis/main.go](./cmd/phronesis/main.go): application entrypoint
- [internal/app/server.go](./internal/app/server.go): HTTP server, routing, config loading, static asset serving, auth-protected API endpoints
- [internal/auth/auth.go](./internal/auth/auth.go): session cookie authentication
- [internal/wiki/store.go](./internal/wiki/store.go): filesystem-backed page storage and derived metadata lookups
- [internal/wiki/session.go](./internal/wiki/session.go): in-memory live document session hub and merge behavior for concurrent updates
- [internal/render/markdown.go](./internal/render/markdown.go): basic Markdown-to-HTML rendering plus wiki link, tag, and task extraction

### Frontend

The frontend is a Svelte app built with Vite.

- [frontend/src/App.svelte](./frontend/src/App.svelte): main application shell, login flow, page loading, autosave, SSE integration
- [frontend/src/lib/Editor.svelte](./frontend/src/lib/Editor.svelte): CodeMirror editor wrapper used as the single editing surface
- [frontend/src/main.js](./frontend/src/main.js): app bootstrap

The compiled frontend is emitted to `frontend/dist`, and the Go server serves that directory when present.

## Implemented Behavior

### Pages and Storage

- Every page is stored as a `.md` file under the configured pages directory.
- Page names are normalized to lowercase wiki-style paths.
- A missing page is treated as a new empty page rather than an error.

### Editing Model

- The frontend loads a page into a single editor surface.
- Changes autosave after a short pause.
- The server broadcasts updates to other connected clients with SSE.
- Concurrent edits currently use a lightweight whole-document merge fallback. This is not a CRDT implementation.

### Wiki Features

- `[[Wiki Links]]` are recognized and tracked.
- Backlinks are derived by scanning pages for references to the current page.
- `#tags` are extracted from Markdown text.
- Markdown task list syntax such as `- [ ] item` and `- [x] item` is recognized.

For the full list of supported Markdown syntax (live editor and
server-rendered HTML), see [`docs/markdown-dialect.md`](docs/markdown-dialect.md).

### Authentication

- Built-in login uses a single configured username and password.
- Session state is tracked with an HTTP-only cookie.
- Unauthenticated API requests are rejected.

## Development

### Prerequisites

- Go `1.24.5`
- Node.js `22.x`
- npm `10.x`
- `goreleaser` (only for local releases; `brew install goreleaser`). CI uses
  `goreleaser-action@v6` with a pinned version — see
  [docs/dist-packaging/README.md](./docs/dist-packaging/README.md).

### Quick start

The project ships a [`Makefile`](./Makefile) with seven targets covering the
common dev loop. Run `make help` for the full list.

```bash
make test    # Backend tests with -race (dev stub frontend — no npm build needed)
make lint    # go vet + staticcheck (staticcheck pinned via go.mod tool directive)
make build   # Production binary: builds frontend + go build -tags=prod
make clean   # Remove build outputs
```

The longhand commands below still work; `make` wraps them with the right
flags (deterministic ldflags, `-tags=prod`, frontend staging) so production
binaries match what CI emits. See
[docs/dist-packaging/README.md](./docs/dist-packaging/README.md) for the
details.

### Run the Backend

```bash
go run ./cmd/phronesis
```

By default the server listens on `:8080`. Binaries built without
`-tags=prod` (such as `go run` or plain `go build`) serve a dev-stub
frontend and log a startup warning.

### Local evaluation (stub OIDC mode)

The bundled OIDC adapter ships with an HMAC-stub verifier that lets you
exercise sign-in flows without configuring an external IdP. Stub mode is
intended for local development and evaluation only; production
deployments configure a real OIDC issuer (Auth0, Okta, Keycloak,
Dex, etc).

| Mode  | Trigger | Behaviour |
|-------|---------|-----------|
| Stub  | `PHRONESIS_OIDC_ENABLED=1` with `PHRONESIS_OIDC_SECRET=<any>` | HMAC-signed tokens are accepted. The server logs `OIDC adapter is using the HMAC-stub verifier` at startup. |
| Real  | Set `PHRONESIS_OIDC_ISSUER=<https://your-idp/...>` (and `_AUDIENCE` / `_SECRET`) | Adapter swaps to the configured verifier. Stub-mode warning is suppressed. |
| Disabled | `PHRONESIS_OIDC_ENABLED` unset or empty | OIDC route returns 404; server falls back to cookie-session admin auth. |

Stub mode emits a loud `slog.Warn` at startup mirroring the dev-frontend
warning — if you see `oidc=stub` in your logs, do not point a real
client at the server until you've configured a production verifier.

### Break-glass admin (optional)

When the OIDC IdP is unreachable (planned outage, misconfiguration on
first deploy, expired credentials), set `PHRONESIS_BREAKGLASS=<phc>` —
the value must be an Argon2id PHC string of the desired admin secret.
With this set, `POST /admin/break-glass` accepts the secret in the
`X-Breakglass-Secret` header and returns `200 ok`. Every successful use
emits `severity=high event=breakglass.use` in the audit log so abuse is
post-hoc detectable. Default state: unset → the route does not exist
(returns 404, not 401).

### Build the Frontend

```bash
cd frontend
npm install
npm run build
```

For local frontend iteration:

```bash
cd frontend
npm run dev
```

### Tests

Backend tests:

```bash
go test ./...       # or: make test
```

Frontend production build check:

```bash
cd frontend
npm run build       # also covered by: make build
```

## Configuration

The backend reads these environment variables. Sensible defaults are
baked in; override only when needed. Operational tuning details
(rate limits, retention, snapshots, OIDC, OAuth) and the admin
workflow live in [`docs/admin-guide.md`](docs/admin-guide.md).

### Server basics

- `PHRONESIS_ADDR`: HTTP listen address, default `:8080`
- `PHRONESIS_PAGES_DIR`: root directory for Markdown pages, default `./data/pages`
- `PHRONESIS_FRONTEND_DIST`: compiled frontend directory, default `./frontend/dist`

### Auth

- `PHRONESIS_ADMIN_USER`: built-in login username, default `admin`
- `PHRONESIS_ADMIN_PASSWORD`: built-in login password, default `admin123` (a startup warning fires if this is left at the default)
- `PHRONESIS_OIDC_ENABLED` / `PHRONESIS_OIDC_ISSUER` / `PHRONESIS_OIDC_AUDIENCE` / `PHRONESIS_OIDC_SECRET`: OIDC sign-in (stub mode when only `_ENABLED` is set)
- `PHRONESIS_BREAKGLASS`: Argon2id PHC of a break-glass admin secret; route 404s when unset

### Storage + retention

- `PHRONESIS_STORE_PATH`: SQLite store for users, keys, and audit events; default `./data/phronesis.db`
- `PHRONESIS_AUDIT_LOG`: file-sink fallback when SQLite is disabled; default `./data/audit.log`
- `PHRONESIS_JOURNAL_PATH`: push-spillover journal path; unset disables the readyz journal check
- `PHRONESIS_SNAPSHOT_DIR`: periodic workspace snapshot directory; unset disables. Cadence is currently hard-coded to 1h.
- `PHRONESIS_BLOB_DIR`: directory for binary attachments; default `./data/blobs`

### OAuth + MCP

- `PHRONESIS_OAUTH_ENABLED`: mounts the OAuth 2.1 server + MCP transport
- `PHRONESIS_OAUTH_ISSUER`: canonical iss URL clients can reach (required when enabled)
- `PHRONESIS_OAUTH_KEY_PATH`: RSA signing key path; auto-generated on first start, default `./data/oauth-key.pem`

### Rate limiting (code defaults, not env-tunable yet)

The per-IP auth-endpoint window (`1m` / `50`), per-bearer-key sliding
window (`1m` / `60`), and per-key in-flight cap (`5`) are
code-defaults today. Override via `app.Config` programmatically;
env-var plumbing is a future polish.

### API auth (legacy / fallback)

- `PHRONESIS_API_KEY`: opt-in single-key bearer auth; a transitional escape hatch from before the per-user API key system landed

## Known Limitations

- The server's Markdown-to-HTML rendering is intentionally basic and not CommonMark-complete; richer rendering happens in the live editor (see [`docs/markdown-dialect.md`](docs/markdown-dialect.md)).
- Collaboration is session-based with whole-document merge logic, not operational transform or CRDT-based.
- The MCP transport ships a tool framework + an `echo` tool; wiki-aware tools (page list, get, write, search) are not yet implemented.
- There is no git sync to a remote, no CLI automation layer, no `/metrics` Prometheus endpoint, and no search/index service yet.
- Workspace administration is currently API-only; the user/key admin panels exist in the Web UI, but workspace CRUD requires `curl` against `/api/admin/workspaces` for now.

## Near-Term Direction

The next meaningful steps are:

1. wiki-aware MCP tools (`pages/list`, `pages/get`, `pages/write`, `pages/search`)
2. workspace CRUD in the Web UI
3. git-backed synchronization to a configurable remote
4. CLI automation layer on top of the same document API
5. `/metrics` Prometheus endpoint for operational visibility
6. richer collaboration semantics (real CRDT path; the scaffolding is composed but not yet consumed)

## Documentation index

- [`docs/markdown-dialect.md`](docs/markdown-dialect.md) — supported Markdown syntax with examples
- [`docs/admin-guide.md`](docs/admin-guide.md) — operator guide for workspaces, users, API keys, OAuth + MCP
- [`docs/silverbullet-like-live-preview/README.md`](docs/silverbullet-like-live-preview/README.md) — live-preview architecture and how to add a decoration family
- [`docs/dist-packaging/README.md`](docs/dist-packaging/README.md) — release tooling and distribution
- [`docs/collab-wiki/PRD.md`](docs/collab-wiki/PRD.md) — product requirements document
