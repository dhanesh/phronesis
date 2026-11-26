# phronesis beta

Thank you for trying phronesis. This beta is for teams who want a
self-hosted Markdown wiki that people and AI agents use together. We want to
know what works, what is confusing, and what is missing.

## Start

- Docker Compose with Dex SSO, or Kubernetes with Helm: see
  [deploy.md](deploy.md).
- Admin tasks (users, roles, API keys, MCP clients): see
  [admin-guide.md](admin-guide.md).
- Who can do what: see [roles.md](roles.md).

## What you can do in this beta

- **Write together.** Markdown pages with a live-preview editor, autosave,
  live updates between people on the same page, wiki links, backlinks,
  tags, task lists and image upload. Rename and delete pages from the
  command palette (Cmd/Ctrl-K).
- **Sign in with your IdP.** OIDC single sign-on (tested with Dex). New
  users are viewers until an admin promotes them to editor or admin.
- **Control access.** viewer / editor / admin roles, enforced on every page,
  live-update and media route.
- **Connect agents.** MCP at `/mcp` with OAuth 2.1 (consent screen) for
  clients such as Claude Code. Tools: `list_pages`, `read_page`,
  `write_page`, `search_pages`. Agents act with the lesser of the granted
  scope and the user's role.
- **Automate.** API keys (request in the app, admin approves) work on the
  HTTP API and on MCP, pinned to one workspace.
- **Organise.** Several workspaces with a switcher.
- **Operate.** Health and readiness endpoints, an audit log, rate limits,
  snapshots, and a backup script.

## Known limits

- No full-text search page in the UI yet (agents have `search_pages`).
- No page history or restore yet. Back up the data volume.
- IdP groups do not map to roles yet; promote users in Manage users.
- Roles apply to the whole server, not per workspace.
- Renaming a page does not rewrite links that point at it.
- Sessions are in memory: everyone signs in again after a restart.
- One instance per data volume (SQLite).
- Concurrent edits merge per document; there is no character-level
  real-time co-editing yet.

## Feedback we want

1. Where did you get stuck in the first 15 minutes?
2. Which missing feature stopped you from moving real notes in?
3. Did agents (MCP) read and write what you expected?
4. Was the viewer / editor / admin model right for your team?

Open an issue in the repository with what you did, what you expected and
what happened.
