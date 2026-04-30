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
- Auth: cookie session auth in `internal/auth` plus optional OIDC token-first
  login in `internal/auth/oidc`
- Authorization: `internal/principal` carries the canonical Principal (user vs
  service account, role, workspace) attached to request context
- Storage: local filesystem-backed Markdown files in `internal/wiki/store.go`,
  binary blobs in `internal/blob`, atomic writes in `internal/fsutil`
- Live updates: server-sent events in `internal/wiki/session.go` and
  `internal/app/server.go`; CRDT room/broadcaster scaffolding in
  `internal/crdt` (composed but not yet wired into editor handlers)
- Persistence side effects: append-only audit log in `internal/audit`,
  push-spillover journal in `internal/journal`, periodic workspace snapshots
  in `internal/snapshot`
- Markdown rendering: custom renderer in `internal/render/markdown.go`,
  XSS/CSP defenses in `internal/xssdefense`
- Cross-cutting middleware: rate limiter in `internal/ratelimit`, session
  store abstraction in `internal/sessions`, embedded frontend assets in
  `internal/webfs`

### Frontend

- Framework: Svelte `5` (currently authored in legacy / non-runes mode;
  migration to runes is in progress)
- Build tool: Vite `8`
- Language: JavaScript modules in `frontend/src`. Tooling configs and the
  Playwright e2e suite under `frontend/tests/e2e` are TypeScript.
- Editor: CodeMirror 6
- Styling: component-scoped CSS in Svelte plus `frontend/src/app.css`
- Package manager: npm
- E2E tests: Playwright (Chromium only in v1) under `frontend/tests/e2e`

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
- `@playwright/test` (devDependency)

## Repository Layout

- `cmd/phronesis`: executable entrypoint
- `internal/app`: server construction, routing, config, static asset serving,
  readyz, OIDC handler glue
- `internal/auth`: cookie session manager + credential handling
- `internal/auth/oidc`: OIDC verifier, claim mapping, token-first adapter
- `internal/principal`: canonical Principal (type/role/workspace) +
  context propagation
- `internal/wiki`: page store + live document hub
- `internal/render`: Markdown rendering and wiki metadata extraction
- `internal/xssdefense`: store-time and render-time HTML sanitization + CSP
- `internal/audit`: append-only event log + buffered async drainer
- `internal/blob`: content-addressed blob store with quotas
- `internal/media`: HTTP handler for `/media/<sha>` upload/download
- `internal/sessions`: pluggable session store interface + in-memory impl
- `internal/ratelimit`: sliding-window limiter + middleware
- `internal/snapshot`: workspace snapshot scheduler + local-FS target
- `internal/journal`: append-only push-spillover journal (replay on startup)
- `internal/crdt`: room + in-process broadcaster scaffolding
- `internal/fsutil`: atomic-write helper (tempfile + fsync + rename)
- `internal/webfs`: dev-stub vs prod-embedded frontend FS (build-tagged)
- `frontend`: Svelte/Vite application + Playwright e2e suite
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
- git sync to configurable remote (push-journal scaffolding exists in
  `internal/journal`; no remote driver wired)
- multi-workspace resolution from URL/subdomain (today everything resolves
  to the implicit `default` workspace)
- editor-side wiring of the CRDT room broadcaster in `internal/crdt`
  (composed in `Server` but not yet consumed by handlers)
- frontend migration from Svelte 4 legacy syntax (`export let`, `$:`,
  `on:click`, `createEventDispatcher`) to Svelte 5 runes

<!-- code-review-graph MCP tools -->
## MCP Tools: code-review-graph

**IMPORTANT: This project has a knowledge graph. ALWAYS use the
code-review-graph MCP tools BEFORE using Grep/Glob/Read to explore
the codebase.** The graph is faster, cheaper (fewer tokens), and gives
you structural context (callers, dependents, test coverage) that file
scanning cannot.

### When to use graph tools FIRST

- **Exploring code**: `semantic_search_nodes` or `query_graph` instead of Grep
- **Understanding impact**: `get_impact_radius` instead of manually tracing imports
- **Code review**: `detect_changes` + `get_review_context` instead of reading entire files
- **Finding relationships**: `query_graph` with callers_of/callees_of/imports_of/tests_for
- **Architecture questions**: `get_architecture_overview` + `list_communities`

Fall back to Grep/Glob/Read **only** when the graph doesn't cover what you need.

### Key Tools

| Tool | Use when |
|------|----------|
| `detect_changes` | Reviewing code changes — gives risk-scored analysis |
| `get_review_context` | Need source snippets for review — token-efficient |
| `get_impact_radius` | Understanding blast radius of a change |
| `get_affected_flows` | Finding which execution paths are impacted |
| `query_graph` | Tracing callers, callees, imports, tests, dependencies |
| `semantic_search_nodes` | Finding functions/classes by name or keyword |
| `get_architecture_overview` | Understanding high-level codebase structure |
| `refactor_tool` | Planning renames, finding dead code |

### Workflow

1. The graph auto-updates on file changes (via hooks).
2. Use `detect_changes` for code review.
3. Use `get_affected_flows` to understand impact.
4. Use `query_graph` pattern="tests_for" to check coverage.
