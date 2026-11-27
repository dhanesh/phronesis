# live-preview: SilverBullet-style live preview in the editor

- id: live-preview
- proven: 696d2475b27c8d8dc0b2aaa689fdf4fac9a35b3b
- anchors: frontend/src/lib/Editor.svelte, frontend/src/lib/editor/decorations
- constraints: silverbullet-like-live-preview:T1

## What it is

Markdown is styled in place while editing: headings, bold, italic, inline code, blockquotes and list markers get decorations, and the stored text stays raw Markdown.

## How to reach it

- Browser: any page at `/w/<page>`.

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" live-preview "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: decoration_cm-md-line-heading-1, decoration_cm-md-strong, decoration_cm-md-emphasis, decoration_cm-md-inline-code, decoration_cm-md-blockquote, decoration_cm-md-list-marker, document_text_not_mutated.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`preview.png` shows the styled text; the `.md` on disk is byte-identical to what was written (decorations are visual only).
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/live-preview/`); pass each to `record --artifact`.
Side effect to record: none: decorations are visual-only; the harness checks the .md file is unchanged.

## Gotchas

- Decorations reveal raw source on the line holding the cursor, so do not click into a decorated line before checking it.
- The `.cm-md-*` classes are the stable handles; they are what the e2e suite uses.
