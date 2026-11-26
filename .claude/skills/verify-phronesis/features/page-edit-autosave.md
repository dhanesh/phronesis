# page-edit-autosave: Edit a page and have it autosave to Markdown on disk

- id: page-edit-autosave
- proven: 4ba3375383c4d0e0108f2d42aa21d39a93d42f76
- anchors: frontend/src/App.svelte:saveDraft, internal/app/handlers_pages.go, internal/wiki/store.go
- constraints: collab-wiki:T1

## What it is

A signed-in user opens `/w/<page>`, types into the single CodeMirror surface, and the text is saved (after a ~600 ms debounce) to `<pages>/default/<page>.md`. A missing page opens as a new empty page.

## How to reach it

- Browser: `/w/<page-name>` or the sidebar page list.
- API: `GET|POST /api/pages/<name>` (`{"content","baseVersion"}`).

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" page-edit-autosave "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: md_file_written, api_returns_typed_text, text_survives_reload.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`disk-content.md` (copied from `.verify-run/<instance>/data/pages/default/<page>.md`) contains the typed text; `after-reload.png` shows it after a reload.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/page-edit-autosave/`); pass each to `record --artifact`.
Side effect to record: file written: data/pages/default/<page>.md.

## Gotchas

- Pages live under a workspace directory: `pages/default/<name>.md`, not `pages/<name>.md`.
- Autosave is debounced; the harness polls the file for up to 10 s.
