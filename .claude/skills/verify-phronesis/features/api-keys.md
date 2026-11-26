# api-keys: Request, approve, use and revoke an API key

- id: api-keys
- proven: no
- anchors: internal/app/handlers_admin_keys.go, internal/auth/keys.go, frontend/src/lib/KeysManager.svelte
- constraints: user-mgmt-mcp:S1, user-mgmt-mcp:S5

## What it is

Per docs/admin-guide.md: a user requests a key (`POST /api/admin/keys/requests`), an admin approves it and sees the `phr_live_` plaintext once, the key authenticates as a bearer token, and revoking it makes it fail within 60 s.

## How to reach it

- Browser: `Manage API keys` dialog (Pending requests table, `keys-approve`, `keys-revoke`).
- API: `GET /api/admin/keys`, `GET /api/admin/keys/requests`, `POST /api/admin/keys/requests/<id>/approve|deny`, `POST /api/admin/keys/<id>/revoke`.

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" api-keys "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: request_created, request_listed_pending, approve_returns_plaintext_once, bearer_key_authenticates, revoke_ok, revoked_key_rejected.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`request-response.txt` shows 201 with a request id; approve returns `key.plaintext` starting `phr_live_`; the bearer call answers 200 before revoke and 401 after.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/api-keys/`); pass each to `record --artifact`.
Side effect to record: rows in key_requests and api_keys (SQLite at data/phronesis.db).

## Gotchas

- KNOWN FAILING at the time of writing: no endpoint creates a key request (POST answers 405), so nothing can be approved. See `.verify/<instance>/findings/`. Do not edit this entry to match that behaviour.
- `key_requests.user_id` must reference a `users` row, and users are only projected from OIDC sign-in.
