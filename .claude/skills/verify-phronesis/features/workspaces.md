# workspaces: Create, use and delete workspaces

- id: workspaces
- proven: 696d2475b27c8d8dc0b2aaa689fdf4fac9a35b3b
- anchors: internal/app/handlers_workspaces.go, frontend/src/lib/TopBar.svelte, frontend/src/lib/WorkspaceManager.svelte

## What it is

An admin creates a workspace, writes pages that live only in it, sees the workspace switcher, and deletes it. The `default` workspace cannot be deleted.

## How to reach it

- Browser: the `Switch workspace` select in the top bar (`Manage workspaces…` option for admins).
- API: `POST|DELETE /api/admin/workspaces[/<slug>]`, `GET /api/workspaces`, `/api/workspaces/<slug>/pages/<name>`.

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" workspaces "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: create_201, write_in_workspace, file_under_workspace_dir, default_workspace_isolated, workspace_switcher_visible, default_not_deletable, delete_204, gone_from_list.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`data/pages/<slug>/probe.md` exists while the workspace exists; `workspaces-after-delete.json` no longer lists it; `shell.png` shows the switcher.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/workspaces/`); pass each to `record --artifact`.
Side effect to record: directory data/pages/<slug>/ and probe.md written.

## Gotchas

- The switcher is a `<select>`: address it as `getByRole('combobox', { name: 'Switch workspace' })`, not a button.
