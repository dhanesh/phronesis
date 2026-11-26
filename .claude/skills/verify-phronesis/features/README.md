# phronesis feature map

One file per user-facing feature, in sweep order. Drive each with `node "$SKILL/scripts/drive.mjs" <id> "$INSTANCE"`, or `scripts/container.sh <id> <instance>` for the deploy-* features.

- [auth-login](auth-login.md) — Sign in and sign out
- [page-edit-autosave](page-edit-autosave.md) — Edit a page and have it autosave to Markdown on disk
- [live-sse](live-sse.md) — See another user's edits live
- [wiki-metadata](wiki-metadata.md) — Wiki links, backlinks, tags and tasks
- [live-preview](live-preview.md) — SilverBullet-style live preview in the editor
- [media-upload](media-upload.md) — Upload and serve images
- [workspaces](workspaces.md) — Create, use and delete workspaces
- [oidc-login](oidc-login.md) — Sign in with OIDC and get your own role
- [api-keys](api-keys.md) — Request, approve, use and revoke an API key
- [mcp-oauth](mcp-oauth.md) — Connect an MCP client with OAuth 2.1 + PKCE
- [page-delete-rename](page-delete-rename.md) — Rename and delete pages from the command palette
- [mcp-wiki-tools](mcp-wiki-tools.md) — Agents list, read, write and search pages over MCP
- [deploy-compose](deploy-compose.md) — Run phronesis + Dex SSO with Docker Compose
- [deploy-helm](deploy-helm.md) — Install the Helm chart with Dex SSO on Kubernetes
