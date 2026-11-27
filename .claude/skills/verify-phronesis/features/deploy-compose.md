# deploy-compose: Run phronesis + Dex SSO with Docker Compose

- id: deploy-compose
- proven: 696d2475b27c8d8dc0b2aaa689fdf4fac9a35b3b
- anchors: Dockerfile, deploy/compose/compose.yaml, deploy/compose/smoke.sh, deploy/smoke/smoke.mjs, internal/app/handlers_sso.go, internal/auth/oidc/rp.go
- constraints:

## What it is

The release image runs with a real Dex OIDC provider under Docker Compose in Colima. A
user clicks "Sign in with SSO", signs in at Dex, and lands in the wiki as a viewer; the
password admin promotes them; they edit; they authorize an MCP agent through the consent
page and the agent writes a page, until a demotion takes write away from the issued token;
an API key works on the HTTP API; after the
container is recreated the page, the role and the key are still there, and the container
health check reports healthy.

## How to reach it

- `make image` (image `phronesis:dev`, label `org.opencontainers.image.revision` = HEAD).
- `deploy/compose/compose.yaml` (project name = instance; ports from the claim).
- Browser: `http://localhost:<port>` -> `Sign in with SSO` (`data-testid=sso-signin`) ->
  Dex form `#login`, `#password`, `#submit-login`.

## Drive it

```sh
make image
"$SKILL/scripts/container.sh" deploy-compose "$INSTANCE_COMPOSE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks (prefixed `full:` and
`persist:`): sso_advertised, readyz_ok, sso_login_authenticated, sso_user_is_viewer,
display_name_not_opaque_sub, whoami_shows_name, readonly_banner_for_viewer,
viewer_write_403, sso_user_projected, promote_to_editor, editor_no_banner,
editor_autosave_persists, key_request_201, key_approved, key_reads_api,
mcp_client_registered, consent_shows_client, consent_returns_code, oauth_token_issued,
mcp_agent_writes_page, mcp_agent_reads_page, demotion_limits_issued_token,
key_writes_marker_page, second_sso_user_is_viewer; marker_page_survives_restart,
promoted_role_survives_restart, api_key_survives_restart, sso_after_restart_keeps_editor;
then smoke_script_pass, image_revision_is_head, container_healthy.
Exit 1 means a check failed; exit 2 means the image is not HEAD or ports could not be claimed.

## Proof

`full.json` and `persist.json` hold every browser check; `signin-page.png`, `dex-login.png`,
`viewer-after-sso.png`, `editor-after-promotion.png`, `consent.png` and `whoami-viewer.png`
show what the user sees; `smoke.log` holds the compose
output, the recreate and `container health: healthy`.
Side effect to record: compose volume <instance>_phronesis-data held the page across a recreate.

## Gotchas

- Colima forwards container ports to localhost; the Dex issuer and redirect URI are
  built from the claimed ports.
- The smoke removes only its own compose project (`down -v`); never the user's stacks.
- Sessions are in memory: the persist phase signs in again.
