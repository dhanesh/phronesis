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

Roles: `viewer` (read), `editor` (read and write pages, upload media),
`admin` (everything, including these admin endpoints). The full role
catalog, including the roles planned for enterprise use, is in
[roles.md](roles.md).

- The built-in password user is always `admin`.
- An OIDC sign-in projects the user into the SQLite `users` table. A new
  user is a `viewer`; an admin promotes them in the Users panel or with
  `POST /api/admin/users/{id}/role`. Later sign-ins keep that role.
- The role is read from the row on every request, so promotion, demotion,
  suspension and deletion apply to open sessions at once.
- Every page, workspace-page, live-update and media route checks the role.
  A viewer sees a read-only editor with a banner; a write gets 403.
- An API key or OAuth token never outranks its owner: its effective role is
  the lesser of its scope and the owner's current role.
- Mapping IdP group claims to roles is not built yet.

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

### Changing a role

```bash
curl -b cookies.txt -X POST -H 'Content-Type: application/json' \
  -d '{"role":"editor"}' http://localhost:8080/api/admin/users/123/role
```

`role` is `viewer`, `editor` or `admin`. The Users panel (command
palette, "Manage users") has the same control. The change applies to
the user's open sessions and to their API keys at once.

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

A signed-in OIDC user requests a key from the web UI at
`/settings/keys/request` (or "Request an API key" in the Cmd-K
palette), or over HTTP:

```bash
curl -b user-cookies.txt -X POST http://localhost:8080/api/keys/requests \
  -H 'Content-Type: application/json' \
  -d '{"scope":"write","label":"my-laptop"}'
```

`scope` is `read` or `write`. `GET /api/keys/requests` lists the
caller's own requests and their decision. The built-in password admin
has no `users` row and cannot file requests.

The admin sees pending requests in the "Manage API keys" dialog, or
lists them with `GET /api/admin/keys/requests`, and approves with:

```bash
curl -b cookies.txt -X POST \
  http://localhost:8080/api/admin/keys/requests/42/approve
```

The key gets the scope and label the user requested. The `201`
response carries `key.plaintext`: copy it once and hand it to the
user. There is no way to retrieve it afterwards.

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

A key works on `/mcp` and on the HTTP API alike:

```bash
curl -H 'Authorization: Bearer phr_live_abcd1234efgh_…' \
  http://localhost:8080/api/pages/my-page
```

- A `read` key reads pages; a write returns 403. A `write` key also edits
  pages and uploads media.
- A key is pinned to the workspace it was requested in. It gets 403 on
  other workspaces, and `GET /api/workspaces` lists only its own.
- The key's role is capped at its owner's current role, so a write key
  owned by a viewer acts as read.

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
4. Your browser opens the phronesis sign-in page if you are signed out,
   then a consent page that names the client (marked "unverified": the
   name is self-asserted), where it sends you back to, and the access it
   gets. Nothing is issued until you click Approve; Deny returns
   `error=access_denied` to the client.
5. The client receives an access token + refresh token. Done.

Anonymous dynamic registration accepts only loopback redirect URIs
(`http://127.0.0.1`, `http://[::1]`, `http://localhost`), which is what
local MCP clients use. Registering any other redirect URI needs an admin
session. OAuth access tokens work on `/mcp` only; use an API key for the
HTTP API.

Tools: `list_pages`, `read_page`, `write_page` (pass `base_version` from
`read_page` to merge with concurrent edits), `search_pages` (case-insensitive
substring), plus `echo`. Each call is checked like the HTTP API: the token's
role must allow read or write, and edits are audited with `via=mcp`.

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
`scope=write` → editor, otherwise viewer). The scope granted at consent is
capped at the signing-in user's role: a viewer asking for `write` gets
`read`.

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

Real-IdP single sign-on (authorization code + PKCE, RS256 via JWKS) is
configured with `PHRONESIS_OIDC_ENABLED=1`, `_ISSUER`, `_CLIENT_ID`,
`_CLIENT_SECRET` and `PHRONESIS_PUBLIC_URL`; the full table, the
back-channel `PHRONESIS_OIDC_PROVIDER_URL`, and provider notes are in
[deploy.md](deploy.md#using-another-oidc-provider). The login page then
shows "Sign in with SSO".

| Variable | Default | Behaviour |
|---|---|---|
| `PHRONESIS_OIDC_SECRET` | unset | HMAC secret for the **stub** (no client id): dev tokens posted to `/api/auth/oidc/login`. Refused when `PHRONESIS_ENV=production`. |

Stub mode logs a loud `oidc=stub` warning at startup. Never expose it to a
real network.

### Break-glass admin

| Variable | Default | Behaviour |
|---|---|---|
| `PHRONESIS_BREAKGLASS` | unset (route 404s) | Argon2id PHC of the break-glass secret |

When set, `POST /admin/break-glass` accepts the secret in
`X-Breakglass-Secret` and grants admin role. Every successful use
emits `severity=high event=breakglass.use` to the audit log so abuse
is post-hoc detectable. Use only when OIDC is unreachable.

## What's not yet here

- **Per-workspace OAuth issuers.** All workspaces share one issuer.
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
