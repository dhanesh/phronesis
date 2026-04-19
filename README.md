# phronesis

`phronesis` is a self-hosted document server for team knowledge, project notes, and wiki-style documentation. It stores pages as Markdown files on disk, serves them through a Go backend, and provides a Svelte-based web client with a single-surface editor designed to move toward SilverBullet-style live wiki editing.

The long-term goal is broader than a notes app: `phronesis` is intended to become a documentation and project context layer that can replace a mix of Confluence, Google Docs, and parts of Jira or Linear, while remaining readable and portable because the source of truth is plain Markdown in a filesystem-backed space.

## Current Status

This repository currently implements the first working phase:

- file-backed Markdown pages addressed by wiki-style page names
- built-in username/password authentication
- a Go HTTP server that serves both API endpoints and the compiled frontend
- autosave page editing over HTTP
- live page updates over server-sent events
- derived wiki metadata for links, backlinks, tags, and task list items
- a Svelte frontend with a single CodeMirror-based editor surface
- inline wiki-link rendering inside the editor when the cursor is outside the link syntax

This is still an early foundation. Git sync, MCP exposure, CLI automation, richer live preview behavior, structured work-management concepts, and production-grade collaboration are not implemented yet.

## Architecture

### Backend

The backend is written in Go and keeps the runtime dependency-light.

- [cmd/phronesis/main.go](./cmd/phronesis/main.go): application entrypoint
- [internal/app/server.go](./internal/app/server.go): HTTP server, routing, config loading, static asset serving, auth-protected API endpoints
- [internal/auth/auth.go](./internal/auth/auth.go): session cookie authentication
- [internal/wiki/store.go](./internal/wiki/store.go): filesystem-backed page storage and derived metadata lookups
- [internal/wiki/session.go](./internal/wiki/session.go): in-memory live document session hub and merge behavior for concurrent updates
- [internal/render/markdown.go](./internal/render/markdown.go): basic Markdown-to-HTML rendering plus wiki link, tag, and task extraction

### Frontend

The frontend is a Svelte app built with Vite.

- [frontend/src/App.svelte](./frontend/src/App.svelte): main application shell, login flow, page loading, autosave, SSE integration
- [frontend/src/lib/Editor.svelte](./frontend/src/lib/Editor.svelte): CodeMirror editor wrapper used as the single editing surface
- [frontend/src/main.js](./frontend/src/main.js): app bootstrap

The compiled frontend is emitted to `frontend/dist`, and the Go server serves that directory when present.

## Implemented Behavior

### Pages and Storage

- Every page is stored as a `.md` file under the configured pages directory.
- Page names are normalized to lowercase wiki-style paths.
- A missing page is treated as a new empty page rather than an error.

### Editing Model

- The frontend loads a page into a single editor surface.
- Changes autosave after a short pause.
- The server broadcasts updates to other connected clients with SSE.
- Concurrent edits currently use a lightweight whole-document merge fallback. This is not a CRDT implementation.

### Wiki Features

- `[[Wiki Links]]` are recognized and tracked.
- Backlinks are derived by scanning pages for references to the current page.
- `#tags` are extracted from Markdown text.
- Markdown task list syntax such as `- [ ] item` and `- [x] item` is recognized.

### Authentication

- Built-in login uses a single configured username and password.
- Session state is tracked with an HTTP-only cookie.
- Unauthenticated API requests are rejected.

## Development

### Prerequisites

- Go `1.24.5`
- Node.js `22.x`
- npm `10.x`

### Run the Backend

```bash
go run ./cmd/phronesis
```

By default the server listens on `:8080`.

### Build the Frontend

```bash
cd frontend
npm install
npm run build
```

For local frontend iteration:

```bash
cd frontend
npm run dev
```

### Tests

Backend tests:

```bash
go test ./...
```

Frontend production build check:

```bash
cd frontend
npm run build
```

## Configuration

The backend currently reads these environment variables:

- `PHRONESIS_ADDR`: HTTP listen address, default `:8080`
- `PHRONESIS_PAGES_DIR`: root directory for Markdown pages, default `./data/pages`
- `PHRONESIS_FRONTEND_DIST`: compiled frontend directory, default `./frontend/dist`
- `PHRONESIS_ADMIN_USER`: login username, default `admin`
- `PHRONESIS_ADMIN_PASSWORD`: login password, default `admin123`

## Known Limitations

- Markdown rendering is intentionally basic and not CommonMark-complete.
- The editor is moving toward live preview behavior, but only wiki links currently render inline in the editing surface.
- Collaboration is session-based with simple merge logic, not operational transform or CRDT-based.
- Authentication is suitable only for early internal use and should not be treated as production-ready security.
- There is no git sync, no MCP server, no CLI automation layer, and no search/index service yet.

## Near-Term Direction

The next meaningful steps are:

1. improve the editor’s live-preview behavior beyond wiki links
2. add a stable HTTP API for document operations
3. layer CLI and MCP interfaces on top of the same document operations
4. add git-backed synchronization and operational visibility
5. harden authentication, persistence, and collaboration semantics
