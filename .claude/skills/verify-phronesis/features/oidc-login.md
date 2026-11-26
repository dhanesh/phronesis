# oidc-login: Sign in with OIDC and get your own role

- id: oidc-login
- proven: no
- anchors: internal/app/handlers_oidc.go:projectOIDCUser, internal/app/handlers_auth.go:principalFromRequest, internal/auth/oidc/projection.go:ProjectClaims
- constraints: collab-wiki:B2, user-mgmt-mcp:T2

## What it is

A user signs in through the configured OIDC provider. phronesis projects them into its
`users` table (OIDC is the identity source, SQLite keeps projected state) and authorises
them with their projected role from workspace RBAC (viewer / editor / admin). A user with
no admin role claim cannot call admin endpoints.

## How to reach it

- API: `POST /api/auth/oidc/login` with `{"id_token": "<jwt>"}`; then `GET /api/session`.
- Admin view of the result: `GET /api/admin/users` and the `Manage users` dialog.
- Needs an instance launched with `launch.sh <instance> --oidc` (HMAC stub verifier).

## Drive it

```sh
"$SKILL/scripts/launch.sh" "$INSTANCE_OIDC" --oidc
node "$SKILL/scripts/drive.mjs" oidc-login "$INSTANCE_OIDC"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: oidc_login_200,
session_authenticated, role_not_admin_without_claim, admin_endpoint_forbidden,
user_projected_into_users. Exit 1 means a check failed; exit 2 means the instance is not running.

## Proof

`session.json` shows a non-admin role; `admin-post.txt` shows 403 for `POST /api/admin/workspaces`;
`users.json` lists the projected user.
Side effect to record: row in users (SQLite at data/phronesis.db); no workspace created.

## Gotchas

- A new user is `role=user` in SQLite and `viewer` in `/api/session`; the role is read
  from the row on every request (decision 2026-11-27 in docs/decisions.md).
- The stub token is HS256 with secret `verify-oidc-secret`, `iss=verify-iss`, `aud=verify-aud`.
