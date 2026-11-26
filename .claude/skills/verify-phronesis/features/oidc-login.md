# oidc-login: Sign in with OIDC and get your own role

- id: oidc-login
- proven: ed7956f8fec5c4e526770203c441ca74989f5d3b
- anchors: internal/app/handlers_oidc.go:projectOIDCUser, internal/app/handlers_auth.go:principalFromRequest, internal/auth/oidc/projection.go:ProjectClaims, internal/app/authz.go, frontend/src/lib/UsersManager.svelte
- constraints: collab-wiki:B2, user-mgmt-mcp:T2

## What it is

A user signs in through the configured OIDC provider. phronesis projects them into its
`users` table (OIDC is the identity source, SQLite keeps projected state) and authorises
them with their projected role (viewer / editor / admin, docs/roles.md). A new user is a
viewer: the editor is read-only with a banner and page writes get 403. An admin promotes
them to editor in the Users panel (role picker), and after a reload they can edit.

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
user_projected_into_users, new_oidc_user_is_viewer, viewer_sees_readonly_banner,
viewer_editor_not_editable, viewer_write_forbidden, admin_promotes_to_editor_in_ui,
editor_editable_after_promotion, editor_write_ok. Exit 1 means a check failed; exit 2 means the instance is not running.

## Proof

`session.json` shows a non-admin role; `admin-post.txt` shows 403 for `POST /api/admin/workspaces`;
`users.json` lists the projected user; `viewer-readonly.png` shows the read-only banner;
`users-role-picker.png` shows the role set to editor in Manage users.
Side effect to record: row in users (SQLite at data/phronesis.db); no workspace created.

## Gotchas

- A new user is `role=viewer` in SQLite (migration 003); the role is read from the row
  on every request, so the promotion applies without a new sign-in.
- The stub token is HS256 with secret `verify-oidc-secret`, `iss=verify-iss`, `aud=verify-aud`.
