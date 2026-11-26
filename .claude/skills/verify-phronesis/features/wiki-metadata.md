# wiki-metadata: Wiki links, backlinks, tags and tasks

- id: wiki-metadata
- proven: 4ba3375383c4d0e0108f2d42aa21d39a93d42f76
- anchors: internal/render/markdown.go, internal/wiki/store.go, frontend/src/App.svelte
- constraints: collab-wiki:U4

## What it is

Writing `[[page]]`, `#tag` and `- [ ] task` in a page is recognised: the page lists its links, tags and tasks, and the linked page shows a backlink pill.

## How to reach it

- Browser: backlink / link pills and tag chips beside the editor on `/w/<page>`.
- API: `render.links|tags|tasks|backlinks` in `GET /api/pages/<name>`.

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" wiki-metadata "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: links_extracted, tags_extracted, tasks_extracted, backlink_on_target, backlink_pill_in_ui.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`source-render.json` lists the link, tag and two tasks; `target-render.json` lists the backlink; `target-backlinks.png` shows the pill.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/wiki-metadata/`); pass each to `record --artifact`.
Side effect to record: two .md files written under data/pages/default/.

## Gotchas

- Tags are returned without the `#`.
