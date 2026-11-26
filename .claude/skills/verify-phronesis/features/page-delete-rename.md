# page-delete-rename: Rename and delete pages from the command palette

- id: page-delete-rename
- proven: no
- anchors: internal/wiki/delete_rename.go, internal/app/handlers_pages.go:handleRename, frontend/src/lib/CommandPalette.svelte, frontend/src/App.svelte:renamePage
- constraints:

## What it is

An editor (or admin) opens a page, types a new name in the command palette and picks
`Rename "<page>" to "<new>"`; the Markdown file moves on disk and the renamed page opens.
`Delete page "<page>"` asks for confirmation and removes the file. Viewers do not see
these commands and get 403 from the API.

## How to reach it

- Browser: Cmd/Ctrl-K, then type a new name (rename) or `delete`.
- API: `POST /api/pages/<name>/rename` `{"to":"<new>"}` (409 if the target exists, 404 if
  missing); `DELETE /api/pages/<name>` (204, 404). Both also under
  `/api/workspaces/<slug>/pages/<name>`.

## Drive it

```sh
"$SKILL/scripts/launch.sh" "$INSTANCE"
node "$SKILL/scripts/drive.mjs" page-delete-rename "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: page_created, file_moved_on_disk,
renamed_page_opened, file_deleted_on_disk, page_gone_from_list.
Exit 1 means a check failed; exit 2 means the instance is not running.

## Proof

`rename-command.png` shows the rename command in the palette; the drive observes
`data/pages/default/<name>-renamed.md` appear and `<name>.md` disappear, then the renamed
file disappear; `pages-after.json` no longer lists the page.
Side effect to record: file moved then removed under data/pages/default/.

## Gotchas

- Delete uses a browser `confirm()`; the harness accepts it with `page.once('dialog')`.
- Rename does not rewrite inbound `[[links]]`.
