# api-keys: Request, approve, use and revoke an API key

- id: api-keys
- proven: no
- anchors: internal/app/handlers_key_requests.go, internal/app/handlers_admin_keys.go, internal/auth/keys.go, frontend/src/lib/KeyRequestPanel.svelte, frontend/src/lib/KeysManager.svelte
- constraints: user-mgmt-mcp:S1, user-mgmt-mcp:S5

## What it is

A signed-in OIDC user asks for an API key (scope `read` or `write`, plus a label). An
admin approves the request and sees the `phr_live_` plaintext exactly once, to hand to
the user. The key authenticates as a workspace service account, and revoking it makes
it fail within 60 s.

## How to reach it

- User, browser: `/settings/keys/request` or "Request an API key" in the Cmd-K palette
  (dialog `Request an API key`; `data-testid` `kr-scope`, `kr-label`, `kr-submit`, `kr-row`).
- User, API: `POST /api/keys/requests` `{"scope","label"}`, `GET /api/keys/requests`.
- Admin, browser: `Manage API keys` dialog (`keys-approve`, `keys-revoke`).
- Admin, API: `GET /api/admin/keys/requests`, `POST /api/admin/keys/requests/<id>/approve|deny`,
  `GET /api/admin/keys`, `POST /api/admin/keys/<id>/revoke`.
- Key use: `Authorization: Bearer phr_live_...` on `/mcp` (service-account surface).

## Drive it

```sh
"$SKILL/scripts/launch.sh" "$INSTANCE_KEYS" --oidc --oauth
node "$SKILL/scripts/drive.mjs" api-keys "$INSTANCE_KEYS"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: request_form_submitted,
own_request_listed_pending, admin_sees_pending_request, approve_returns_plaintext_once,
plaintext_not_in_listing, bearer_key_authenticates, revoke_ok, revoked_key_rejected_within_60s.
Exit 1 means a check failed; exit 2 means the instance is not running.

## Proof

`request-form.png` shows the user's pending request; `admin-pending.json` lists it for
the admin; approve answers 201 with `key.plaintext`; `keys-list.json` holds the key
without its plaintext; the bearer `tools/list` call answers 200 before revoke and 401
after. `pages-with-key.txt` records what `/api/pages` answers to the key (informational).
Side effect to record: rows in key_requests and api_keys (SQLite at data/phronesis.db).

## Gotchas

- Needs `--oidc` (the requester must be a projected users row; the password admin has
  none) and `--oauth` (`/mcp` is not mounted without it).
- The approve endpoint uses the requested scope and label; it ignores any body.
- Session cookies outrank bearer tokens, so use the key from a cookie-less context.
