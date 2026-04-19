# CLAUDE.md

This file describes the current project stack and implementation boundaries for `phronesis`.

## Project Purpose

`phronesis` is a Go + Svelte Markdown wiki/document server. The source of truth is Markdown files on disk. The application is intended to evolve into a self-hosted project knowledge system with browser editing, agent-facing automation, and git synchronization.

## Stack Specification

### Backend

- Language: Go
- Go version: `1.24.5`
- Module path: `github.com/dhanesh/phronesis`
- HTTP server: standard library `net/http`
- Auth: custom cookie session auth in `internal/auth`
- Storage: local filesystem-backed Markdown files in `internal/wiki/store.go`
- Live updates: server-sent events in `internal/wiki/session.go` and `internal/app/server.go`
- Markdown rendering: custom renderer in `internal/render/markdown.go`

### Frontend

- Framework: Svelte `5`
- Build tool: Vite `6`
- Language: JavaScript modules, not TypeScript
- Editor: CodeMirror 6
- Styling: component-scoped CSS in Svelte plus `frontend/src/app.css`
- Package manager: npm

### Current Frontend Libraries

- `svelte`
- `@codemirror/view`
- `@codemirror/state`
- `@codemirror/commands`
- `@codemirror/language`
- `@codemirror/lang-markdown`
- `@lezer/highlight`
- `vite`
- `@sveltejs/vite-plugin-svelte`

## Repository Layout

- `cmd/phronesis`: executable entrypoint
- `internal/app`: server construction, routing, config, static asset serving
- `internal/auth`: session and credential handling
- `internal/wiki`: page storage and live session coordination
- `internal/render`: Markdown rendering and wiki metadata extraction
- `frontend`: Svelte/Vite application
- `data/pages`: default on-disk content root used at runtime

## Architectural Constraints

- Markdown files on disk are the source of truth.
- Keep the backend dependency-light unless a new dependency clearly unlocks a major capability.
- Preserve the separation between:
  - document storage/indexing concerns
  - transport concerns
  - frontend/editor concerns
- The frontend should continue moving toward a single-surface live editor model rather than reverting to a split preview/editor design.
- CLI, MCP, and git sync are planned but not yet implemented. Do not imply they already exist.

## Current Product Semantics

- Pages are addressed by normalized wiki-style names.
- Missing pages are treated as new editable pages.
- Editing autosaves after a short debounce.
- Live client updates are delivered over SSE.
- Wiki links, tags, backlinks, and task list syntax are extracted today.
- Rich SilverBullet-style live preview is only partially implemented. Inline wiki-link rendering exists; full rendered Markdown-in-editor behavior does not.

## Development Conventions

- Prefer `make test` / `make lint` / `make build` over raw `go` / `npm`
  invocations. The Makefile wraps them with the right flags (deterministic
  ldflags, `-tags=prod`, frontend staging). Raw `go test ./...` and
  `cd frontend && npm run build` still work as a fallback.
- Backend tests use a dev-stub frontend (see `internal/webfs/`) so
  `make test` runs without a prior `npm run build`.
- Prefer ASCII unless a file already requires Unicode.
- Keep public behavior and docs aligned with the actual codebase state.
- If changing stack choices, update this file and the README in the same change.

## Pending Major Areas

- richer CodeMirror live-preview behavior
- stable HTTP document API
- CLI interface
- MCP interface
- git sync to configurable remote
- stronger auth and multi-user collaboration semantics
