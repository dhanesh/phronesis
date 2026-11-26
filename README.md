# phronesis

`phronesis` is a self-hosted knowledge base for humans and AI agents. It stores everything as plain Markdown on disk, makes pages agent-readable through structured wiki primitives (links, tags, frontmatter, attributes), and serves a single-surface live editor for browser editing.

The product is positioned for individuals capturing personal notes, teams sharing context, and AI agents reading and writing alongside their humans. The source of truth stays as plain Markdown in a filesystem-backed space — portable, grep-able, version-controllable — so neither humans nor agents are locked into a proprietary format.

## Current Status

This repository implements:

- file-backed Markdown pages addressed by wiki-style page names, scoped per workspace
- built-in username/password authentication, OIDC single sign-on against a real IdP (authorization code + PKCE; tested with Dex), optional break-glass admin
- viewer / editor / admin RBAC enforced on every page, live-update and media route ([roles](docs/roles.md))
- a Go HTTP server that serves both API endpoints and the compiled frontend
- autosave page editing over HTTP and live page updates over server-sent events
- derived wiki metadata for links, backlinks, tags, and task list items
- a Svelte frontend with a single CodeMirror-based editor surface and SilverBullet-style live preview (headings, bold/italic, inline code, lists, tables, images, fenced code, blockquotes, admonitions, frontmatter pill bar, attribute pills, hashtag chips, wiki-link chips)
- workspace + user + API key administration backed by SQLite, with admin endpoints for workspace CRUD, user suspension/reactivation/deletion, and API key minting/revocation
- an MCP HTTP transport at `/mcp` with OAuth 2.1 + PKCE, a consent screen, dynamic client registration (RFC 7591; loopback redirect URIs only without an admin), and wiki tools `list_pages`, `read_page`, `write_page`, `search_pages` — Claude Code and equivalent MCP clients connect, approve once, and work on pages
- API keys (user requests, admin approves) that work on the HTTP API and on MCP, capped at the owner's role and pinned to one workspace
- a production container image, a Docker Compose stack with Dex, and a Helm chart — see [docs/deploy.md](docs/deploy.md)
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

- Built-in login uses a single configured username and password (the admin).
- OIDC single sign-on: "Sign in with SSO" on the login page. New SSO users
  are viewers until an admin promotes them.
- Session state is tracked with an HTTP-only cookie; API keys use
  `Authorization: Bearer phr_live_...`.
- Unauthenticated API requests are rejected.

## Development

### Prerequisites

- Go `1.25` (see `go.mod`)
- Node.js `22.x`
- npm `10.x`
- `goreleaser` (only for local releases; `brew install goreleaser`). CI uses
  `goreleaser-action@v6` with a pinned version — see
  [docs/dist-packaging/README.md](./docs/dist-packaging/README.md).

### Quick start

The project ships a [`Makefile`](./Makefile) covering the common dev loop.
Run `make help` for the full list.

```bash
make test          # Backend tests with -race (dev stub frontend — no npm build needed)
make lint          # gofmt + go vet + staticcheck
make build         # Production binary: builds frontend + go build -tags=prod
make image         # Release container image in Colima (phronesis:dev)
make compose-test  # phronesis + Dex in Docker Compose, browser smoke test
make helm-test     # Helm chart + Dex in Colima k3s, browser smoke test
make clean         # Remove build outputs
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
| Real  | Set `PHRONESIS_OIDC_CLIENT_ID`, `_CLIENT_SECRET`, `_ISSUER` and `PHRONESIS_PUBLIC_URL` | Browser SSO (authorization code + PKCE), RS256 via the IdP's JWKS. The HMAC stub is off. See [docs/deploy.md](docs/deploy.md). |
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
- `PHRONESIS_ADMIN_PASSWORD`: built-in login password, default `admin123` (a startup warning fires if this is left at the default; with `PHRONESIS_ENV=production`, as in the container image, the server refuses to start)
- `PHRONESIS_OIDC_ENABLED` / `PHRONESIS_OIDC_ISSUER` / `PHRONESIS_OIDC_CLIENT_ID` / `PHRONESIS_OIDC_CLIENT_SECRET` / `PHRONESIS_OIDC_PROVIDER_URL` / `PHRONESIS_OIDC_REDIRECT_URL` / `PHRONESIS_OIDC_AUDIENCE`: OIDC single sign-on (details in [docs/deploy.md](docs/deploy.md))
- `PHRONESIS_OIDC_SECRET`: HMAC secret for the development stub (no client id)
- `PHRONESIS_PUBLIC_URL`: browser-facing base URL (SSO redirect URI)
- `PHRONESIS_ENV=production`: refuse unsafe defaults at start-up
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
- MCP search (`search_pages`) is a substring scan; there is no search UI or index yet.
- There is no page history, no git sync to a remote, no CLI automation layer, and no `/metrics` Prometheus endpoint yet.
- IdP groups are not mapped to roles; roles are instance-wide, not per workspace.
- Sessions and OAuth clients/refresh tokens are in memory (users sign in and MCP clients authorize again after a restart); one instance per data volume.

## Near-Term Direction

The next meaningful steps are:

1. beta feedback (see [docs/BETA.md](docs/BETA.md)); search UI and page history
2. IdP group-to-role mapping and per-workspace roles ([docs/roles.md](docs/roles.md))
3. git-backed synchronization to a configurable remote
4. CLI automation layer on top of the same document API
5. `/metrics` Prometheus endpoint for operational visibility
6. richer collaboration semantics (real CRDT path; the scaffolding is composed but not yet consumed)

## Documentation index

- [`docs/markdown-dialect.md`](docs/markdown-dialect.md) — supported Markdown syntax with examples
- [`docs/BETA.md`](docs/BETA.md) — beta release notes, known limits, feedback
- [`docs/deploy.md`](docs/deploy.md) — Docker Compose, Helm, OIDC providers, backups
- [`docs/roles.md`](docs/roles.md) — role catalog and permission model
- [`docs/admin-guide.md`](docs/admin-guide.md) — operator guide for workspaces, users, API keys, OAuth + MCP
- [`docs/silverbullet-like-live-preview/README.md`](docs/silverbullet-like-live-preview/README.md) — live-preview architecture and how to add a decoration family
- [`docs/dist-packaging/README.md`](docs/dist-packaging/README.md) — release tooling and distribution
- [`docs/collab-wiki/PRD.md`](docs/collab-wiki/PRD.md) — product requirements document
