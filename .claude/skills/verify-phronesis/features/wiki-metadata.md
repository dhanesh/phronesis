# wiki-metadata: Wiki links, backlinks, tags and tasks

- id: wiki-metadata
- proven: 4c4f87c9182ff85c6252556d44ce8d15d4b17a26
- anchors: internal/render/markdown.go, internal/wiki/store.go, frontend/src/App.svelte, frontend/src/lib/editor/decorations/inline/tasks.ts
- constraints: collab-wiki:U4

## What it is

Writing `[[page]]`, `#tag` and `- [ ] task` / `- [x] task` in a page is recognised: the server extracts the page's links, tags and tasks (`internal/render/markdown.go`), the page's rail shows its outgoing links and tag chips, the editor shows each task as a checkbox, and the linked page shows a backlink pill.

## How to reach it

- Browser, on `/w/<page>`: outgoing link pills ("Wiki Graph" card, `button.pill`), tag chips ("Tags / Metadata" card, `button.tag-chip` with text `#<tag>`) and backlink pills ("Backlinks" card) in the right rail; tasks as checkboxes inside the editor (`input.cm-md-task-checkbox`, checked for `[x]`).
- API: `render.links|tags|tasks|backlinks` in `GET /api/pages/<name>` (each task is `{"text","checked"}`).
- Disk: the page is `data/pages/default/<name>.md` (under launch: `.verify-run/<instance>/data/pages/default/`).

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" wiki-metadata "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: links_extracted, tags_extracted, tasks_extracted (text and checked state of both tasks), backlink_on_target, pages_on_disk, tag_chip_in_ui, task_checkboxes_in_ui (states `[false,true]`), backlink_pill_in_ui.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`source-render.json` lists the link, tag and two tasks; `target-render.json` lists the backlink; `source-disk.md` is the source page as written to disk; `source-tags-tasks.png` shows the `#verifytag` chip in the rail and the two task checkboxes (one checked) in the editor; `target-backlinks.png` shows the backlink pill; `result.json` is the harness output.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/wiki-metadata/`); pass each to `record --artifact`.
Side effect to record: two .md files written under data/pages/default/.

## Gotchas

- Tags are returned without the `#`; the rail chip adds it back (`#verifytag`).
- The frontend never reads `render.tasks`. The task checkboxes come from the editor's own Markdown parser (CodeMirror live preview), so `task_checkboxes_in_ui` proves tasks are shown, while backend task extraction is proven only by `tasks_extracted` on the API body.
- A task checkbox is replaced by its raw `[ ]` / `[x]` source while the selection touches that marker (`selectionTouches` in `frontend/src/lib/editor/decorations/base.ts`), so do not click into the markers before checking the boxes.
