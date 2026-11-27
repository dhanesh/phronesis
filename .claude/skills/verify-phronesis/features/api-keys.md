# api-keys: Request, approve, use and revoke an API key

- id: api-keys
- proven: 696d2475b27c8d8dc0b2aaa689fdf4fac9a35b3b
- anchors: internal/app/handlers_key_requests.go, internal/app/handlers_admin_keys.go, internal/auth/keys.go, frontend/src/lib/KeyRequestPanel.svelte, frontend/src/lib/KeysManager.svelte, internal/app/authz.go, internal/app/middleware.go
- constraints: user-mgmt-mcp:S1, user-mgmt-mcp:S5

## What it is

A signed-in OIDC user asks for an API key (scope `read` or `write`, plus a label). An
admin approves the request and sees the `phr_live_` plaintext exactly once, to hand to
the user. The key authenticates as a workspace service account on `/mcp` and on the
HTTP API alike (decision 2026-11-27), within its scope (a read key cannot write pages)
and pinned to its workspace; its role never exceeds its owner's. Revoking it makes it
fail within 60 s.

## How to reach it

- User, browser: `/settings/keys/request` or "Request an API key" in the Cmd-K palette
  (dialog `Request an API key`; `data-testid` `kr-scope`, `kr-label`, `kr-submit`, `kr-row`).
- User, API: `POST /api/keys/requests` `{"scope","label"}`, `GET /api/keys/requests`.
- Admin, browser: `Manage API keys` dialog (`keys-approve`, `keys-revoke`).
- Admin, API: `GET /api/admin/keys/requests`, `POST /api/admin/keys/requests/<id>/approve|deny`,
  `GET /api/admin/keys`, `POST /api/admin/keys/<id>/revoke`.
- Key use: `Authorization: Bearer phr_live_...` on `/mcp` and `/api/*`.

## Drive it

```sh
"$SKILL/scripts/launch.sh" "$INSTANCE_KEYS" --oidc --oauth
node "$SKILL/scripts/drive.mjs" api-keys "$INSTANCE_KEYS"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: request_form_submitted,
own_request_listed_pending, admin_sees_pending_request, approve_returns_plaintext_once,
plaintext_not_in_listing, bearer_key_authenticates, read_key_lists_pages_on_http_api,
read_key_cannot_write_pages, key_pinned_to_its_workspace, revoke_ok, revoked_key_rejected_within_60s.
Exit 1 means a check failed; exit 2 means the instance is not running.

## Proof

`request-form.png` shows the user's pending request; `admin-pending.json` lists it for
the admin; approve answers 201 with `key.plaintext`; `keys-list.json` holds the key
without its plaintext; the bearer `tools/list` call answers 200 before revoke and 401
after. `pages-with-key.txt` shows `/api/pages` answering 200 to the key.
Side effect to record: rows in key_requests and api_keys (SQLite at data/phronesis.db).

## Gotchas

- Needs `--oidc` (the requester must be a projected users row; the password admin has
  none) and `--oauth` (`/mcp` is not mounted without it).
- The approve endpoint uses the requested scope and label; it ignores any body.
- Session cookies outrank bearer tokens, so use the key from a cookie-less context.
