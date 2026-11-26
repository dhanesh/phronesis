# auth-login: Sign in and sign out

- id: auth-login
- proven: 4c4f87c9182ff85c6252556d44ce8d15d4b17a26
- anchors: frontend/src/App.svelte:login, internal/app/handlers_auth.go:handleLogin, internal/auth/auth.go
- constraints: collab-wiki:T9

## What it is

A user opens phronesis, signs in with the built-in username and password, lands on the editor, and can sign out. A wrong password is refused and protected APIs answer 401 once signed out.

## How to reach it

- Browser: `/` shows the sign-in form (labels `Username`, `Password`, button `Sign in`).
- API: `POST /api/login` with `{"username","password"}`, `GET /api/session`, `POST /api/logout`.
- OIDC sign-in (`/api/auth/oidc/login`) is a separate entry point, not driven here (needs `PHRONESIS_OIDC_*`).

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" auth-login "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: bad_password_rejected, editor_visible_after_login, session_authenticated, logout_clears_session, api_rejects_after_logout.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`session-after-login.json` shows `"authenticated": true`; `logged-in.png` shows the editor; after logout `/api/pages` answers 401.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/auth-login/`); pass each to `record --artifact`.
Side effect to record: session cookie issued then invalidated (observed via /api/session).

## Gotchas

- The launch admin password is `verify-admin-pw`, not the repo default `admin123` (that one is only for the e2e fixture).
- Every cookie session gets role `admin` today (see findings), so this drive does not prove RBAC.
