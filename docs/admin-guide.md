# Admin guide

This document covers the operator-facing surfaces an admin uses to run
phronesis: workspaces, users, API keys, and the OAuth + MCP setup
that lets AI agents (Claude Code and equivalents) connect.

If you're only running phronesis as a single-user local instance, you
can skip most of this — the cookie-session admin login + the default
workspace are enough. The surfaces below come into play when you need
multiple users, scoped access, or agent integration.

## Roles and authentication paths

Phronesis recognises three principal types and four authentication
paths. They all converge on the same `principal.Principal` shape so
authorisation downstream is uniform regardless of how the request
arrived.

| Principal type | Auth path | When |
|---|---|---|
| `user` | Cookie session via `POST /api/login` | Built-in admin login (default `admin/admin123`) |
| `user` | Cookie session via `POST /api/auth/oidc/login` | OIDC sign-in (Auth0, Okta, Keycloak, Dex, …) |
| `user` (admin) | `POST /admin/break-glass` with `X-Breakglass-Secret` | When OIDC is unreachable |
| `service_account` | `Authorization: Bearer phr_live_<key>` | API keys minted for users |
| `service_account` | `Authorization: Bearer <jwt>` | OAuth access tokens issued by phronesis (MCP clients) |

Roles: `viewer` (read), `editor` (read/write), `admin` (full
including these admin endpoints). The role is set per user in the
SQLite store; OIDC sign-ins derive the role from a configurable group
claim.

## Workspaces

A workspace is the top-level scoping unit. Pages, blob attachments,
audit events, and API keys are all scoped to a workspace. The default
workspace is `default` and is created automatically.

### Listing workspaces

```bash
curl -b cookies.txt http://localhost:8080/api/workspaces
```

Any authenticated user can list workspaces. CRUD is admin-only.

### Creating, updating, deleting (admin)

```bash
# Create
curl -b cookies.txt -X POST http://localhost:8080/api/admin/workspaces \
  -H 'Content-Type: application/json' \
  -d '{"slug":"team-a","name":"Team A"}'

# Update metadata
curl -b cookies.txt -X PATCH http://localhost:8080/api/admin/workspaces/team-a \
  -H 'Content-Type: application/json' \
  -d '{"name":"Team Alpha"}'

# Delete (preserves the on-disk markdown; removes the workspace metadata)
curl -b cookies.txt -X DELETE http://localhost:8080/api/admin/workspaces/team-a
```

Each workspace gets its own pages directory under
`PHRONESIS_PAGES_DIR/<slug>/`. The metadata index lives at
`PHRONESIS_PAGES_DIR/../workspaces.json`.

## Users

Users are projected into the local SQLite store either by OIDC
sign-in (the canonical path) or by an admin minting an API key on
behalf of a user. The list at `/api/admin/users` gives admins
visibility into who's active, who's suspended, and who has pending
key requests.

### Listing users

```bash
curl -b cookies.txt http://localhost:8080/api/admin/users
```

Returns one row per projected user with: `id`, `oidc_sub`, `email`,
`display_name`, `role`, `status`, `created_at`, `last_seen_at`,
`active_key_count`, `pending_request_count`.

### Suspending and reactivating

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/admin/users/123/suspend
curl -b cookies.txt -X POST http://localhost:8080/api/admin/users/123/reactivate
```

Suspension takes effect within seconds — the in-process auth cache
is invalidated synchronously so the user's keys stop working on the
next request. The S5 manifold contract guarantees ≤60s propagation;
in practice it's sub-second.

### Deleting

```bash
curl -b cookies.txt -X DELETE http://localhost:8080/api/admin/users/123
```

Deletion cascades to the user's API keys.

## API keys

API keys are bearer tokens that authenticate as a workspace-scoped
service account. They're how a CLI script or a non-OAuth MCP client
authenticates to the HTTP API.

### Token format

A minted key looks like:

```
phr_live_<12-char-prefix>_<32-char-suffix>
```

The `phr_live_<prefix>` portion is a non-secret display id — it's
what shows up in admin listings, audit events, and rate-limit logs.
Only the full token can authenticate. The plaintext is shown to the
admin **exactly once** at minting; only an Argon2id hash is stored.
A database compromise does not expose the tokens.

### Minting a key (admin → user flow)

The user requests a key via `POST /api/admin/keys/requests`; the
admin approves with:

```bash
curl -b cookies.txt -X POST \
  http://localhost:8080/api/admin/keys/requests/42/approve \
  -H 'Content-Type: application/json' \
  -d '{"scope":"write","label":"my-laptop","expires_at":"2027-01-01T00:00:00Z"}'
```

The response carries `key_plaintext` — copy it once and hand it to
the user. There is no way to retrieve it afterwards.

### Listing keys

```bash
curl -b cookies.txt http://localhost:8080/api/admin/keys
```

Returns workspace key inventory: owner display name, `key_prefix`,
scope (`read` / `write` / `admin`), created/expires/last-used
timestamps, and revocation status.

### Revoking a key

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/admin/keys/45/revoke
```

Like suspension, revocation invalidates the auth cache synchronously
so the key stops working on the very next request.

### Using a key

```bash
curl -H 'Authorization: Bearer phr_live_abcd1234efgh_…' \
  http://localhost:8080/api/pages/my-page
```

## OAuth + MCP setup

The MCP transport requires OAuth 2.1 authentication (PKCE). When
configured, the binary runs as both an OAuth authorization server and
the resource server that validates its own access tokens.

### Enable OAuth

Set these environment variables before starting the binary:

```bash
export PHRONESIS_OAUTH_ENABLED=1
export PHRONESIS_OAUTH_ISSUER=https://phronesis.your-domain.example
# Optional — defaults to ./data/oauth-key.pem; auto-generated on first start.
export PHRONESIS_OAUTH_KEY_PATH=/var/lib/phronesis/oauth-key.pem
```

`PHRONESIS_OAUTH_ISSUER` must be the canonical public URL clients
can reach (including any reverse-proxy host). Discovery clients
fetch `<issuer>/.well-known/oauth-authorization-server` so the
issuer is the trust anchor.

On first start, phronesis generates a 2048-bit RSA signing key and
writes it to `PHRONESIS_OAUTH_KEY_PATH` with mode 0600. To rotate
the key, replace the file and restart — the `kid` is derived from
the modulus hash so client JWKS caches see a new key id and re-fetch.

### Discovery endpoints

Clients discover phronesis by fetching:

```
GET <issuer>/.well-known/oauth-authorization-server
```

This returns the standard RFC 8414 metadata document including
`authorization_endpoint`, `token_endpoint`, `registration_endpoint`,
`jwks_uri`, and the supported algs (`code` response type, `S256`
PKCE method, `RS256` token signing).

### Configuring an MCP client (e.g. Claude Code)

The end-user flow is intentionally minimal: paste the discovery URL
into your MCP client's MCP-server configuration, complete the OAuth
flow once in your browser, done.

For Claude Code:

1. Open MCP server settings.
2. Add a new server with the URL `https://phronesis.your-domain.example`.
3. The client performs RFC 7591 dynamic client registration against
   `/oauth/register` automatically.
4. Your browser opens the phronesis cookie-login page (or your
   configured OIDC IdP) for the OAuth consent step.
5. The client receives an access token + refresh token. Done.

The client sees the `echo` tool by default. Real wiki tools (page
list, get, write, search) land in subsequent stages.

### Direct OAuth flow (for custom clients)

If you're building a non-MCP client and want to integrate against the
OAuth surface:

```
POST /oauth/register                       (RFC 7591)
GET  /oauth/authorize                      (PKCE, S256, response_type=code)
POST /oauth/token                          (grant_type=authorization_code | refresh_token)
GET  /.well-known/jwks.json                (verify access tokens)
```

Token shape: RS256 JWT with claims `iss`, `sub`, `aud`, `client_id`,
`workspace`, `scope`, `iat`, `exp`. `client_id` becomes the
`Principal.ID` after server-side verification; `workspace` and
`scope` drive role mapping (`scope=admin` → admin role,
`scope=write` → editor, otherwise viewer).

## Operational tuning

Rate limits, retention, and audit are all configurable via
environment variables. Sensible defaults are baked in; override only
when you've measured a need.

### Rate limiting

Three layers are wired, each with code defaults:

| Layer | Default | What it bounds |
|---|---|---|
| Per-IP auth endpoint floor | `1m` / `50` | `/api/login` + `/api/auth/*` + `/oauth/*` |
| Per-bearer-key sliding window | `1m` / `60` | Each `phr_live_…` key or self-issued JWT |
| Per-bearer-key in-flight cap | `5` | Parallel requests per key |

The per-IP window protects credential-stuffing surfaces; the per-key
window + concurrency cap prevent one rogue agent from starving
others. User-cookie sessions are not gated by the per-key limiters
(rate is bounded by what one human can issue).

The defaults are not currently env-tunable — to override, construct
`app.Config` programmatically with custom `AuthRateLimit*`,
`KeyRateLimit*`, and `KeyConcurrencyMax` fields. Env-var plumbing
is a future polish.

### Audit + retention

| Variable | Default | Behaviour |
|---|---|---|
| `PHRONESIS_AUDIT_LOG` | `./data/audit.log` | File-sink fallback when SQLite is disabled |
| `PHRONESIS_STORE_PATH` | `./data/phronesis.db` | SQLite store for users/keys/audit events |

Raw audit rows are retained for 90 days then folded into per-day
aggregates by a daily compactor; aggregates are kept indefinitely.
Bounded loss-on-crash is achieved via an fsync'd JSONL spillover
journal that's replayed on startup.

### Snapshots

| Variable | Default | Behaviour |
|---|---|---|
| `PHRONESIS_SNAPSHOT_DIR` | unset (disabled) | Directory for periodic workspace snapshots |

The cadence is hard-coded to 1h. Override programmatically via
`app.Config.SnapshotInterval` if you need different timing; env-var
support is a future polish.

### OIDC

| Variable | Default | Behaviour |
|---|---|---|
| `PHRONESIS_OIDC_ENABLED` | unset (disabled) | Mounts `/api/auth/oidc/login` |
| `PHRONESIS_OIDC_ISSUER` | unset | Canonical iss; required when enabled |
| `PHRONESIS_OIDC_AUDIENCE` | unset | Expected aud; required when enabled |
| `PHRONESIS_OIDC_SECRET` | unset | HMAC secret (stub mode); required when enabled |

When `PHRONESIS_OIDC_ENABLED=1` is set without an `_ISSUER` pointing
at a real IdP, phronesis runs in **stub mode** — the HMAC verifier
accepts dev tokens and the server logs a loud `oidc=stub` warning at
startup. Suitable for local evaluation only; never expose stub mode
to a real network.

### Break-glass admin

| Variable | Default | Behaviour |
|---|---|---|
| `PHRONESIS_BREAKGLASS` | unset (route 404s) | Argon2id PHC of the break-glass secret |

When set, `POST /admin/break-glass` accepts the secret in
`X-Breakglass-Secret` and grants admin role. Every successful use
emits `severity=high event=breakglass.use` to the audit log so abuse
is post-hoc detectable. Use only when OIDC is unreachable.

## What's not yet here

- **Web UI for admin tasks.** The Users/Keys panels exist
  (`/admin/users`, `/admin/keys` in the frontend), but workspace
  CRUD is currently API-only.
- **Per-workspace OAuth issuers.** All workspaces share one issuer.
- **Wiki-aware MCP tools.** Stage 3c shipped the framework + an echo
  tool. Real tools (`pages/list`, `pages/get`, `pages/write`,
  `pages/search`) come later.
- **`/metrics` Prometheus endpoint.** The audit pipeline is
  observable; Prometheus export is a future operational follow-up.

## Pointers

- Manifold (the constraint set this build-out satisfies):
  [`.manifold/user-mgmt-mcp.md`](../.manifold/user-mgmt-mcp.md)
- Audit pipeline implementation: [`internal/audit/`](../internal/audit/)
- Auth pipeline source: [`internal/auth/`](../internal/auth/),
  [`internal/auth/oauth/`](../internal/auth/oauth/),
  [`internal/auth/oidc/`](../internal/auth/oidc/)
- MCP transport: [`internal/mcp/`](../internal/mcp/)
- Rate-limit middleware: [`internal/ratelimit/`](../internal/ratelimit/)
