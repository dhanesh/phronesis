# mcp-oauth: Connect an MCP client with OAuth 2.1 + PKCE

- id: mcp-oauth
- proven: 4ba3375383c4d0e0108f2d42aa21d39a93d42f76
- anchors: internal/app/handlers_oauth.go, internal/app/handlers_oauth_discovery.go, internal/mcp, frontend/src/App.svelte:safeNextPath
- constraints: user-mgmt-mcp:T1, admin-ui:U3

## What it is

An MCP client (for example Claude Code) discovers phronesis, registers itself, sends a signed-out user through sign-in in the browser, receives an authorization code, swaps it for a token and calls `/mcp` (`initialize`, `tools/list`, `tools/call echo`).

## How to reach it

- Discovery: `/.well-known/oauth-authorization-server`, `/.well-known/jwks.json`.
- OAuth: `/oauth/register`, `/oauth/authorize` (browser), `/oauth/token`.
- MCP: `POST /mcp` with `Authorization: Bearer <access_token>`.
- Browser: `Connect an MCP client` panel shows the discovery and JWKS URLs.

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" mcp-oauth "$INSTANCE_OAUTH"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: discovery_metadata, dynamic_registration_201, login_returns_to_client_with_code, token_exchange, mcp_initialize, mcp_tools_list, mcp_echo_call, mcp_rejects_without_token.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`callback-url.txt` holds `<redirect_uri>?code=phr_code_…&state=…`; `mcp-transcript.json` shows `serverInfo.name=phronesis`, the `echo` tool and the echoed message.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/mcp-oauth/`); pass each to `record --artifact`.
Side effect to record: OAuth client + code rows created; key file data/oauth-key.pem generated.

## Gotchas

- Needs an instance launched with `--oauth`; without it `/mcp` answers 503.
- A session cookie outranks a bearer token, so drive the MCP client from a cookie-less context.
- Callback ports must not be on the browser unsafe-port list (9, 4190, 6000, ...): Chrome refuses them before Playwright sees the request. `page.route` does not catch a server 302 target; watch `request` events.
- The only MCP tool today is `echo`.
