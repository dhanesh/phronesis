# live-sse: See another user's edits live

- id: live-sse
- proven: ed7956f8fec5c4e526770203c441ca74989f5d3b
- anchors: internal/app/handlers_pages.go, internal/wiki/session.go

## What it is

Two browsers have the same page open. Text typed in one appears in the other without a reload, delivered over server-sent events.

## How to reach it

- Browser: two sessions on `/w/<page>`.
- API: `GET /api/pages/<name>/events` (SSE stream).

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" live-sse "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: second_client_sees_edit, edit_persisted_to_disk.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`second-client.png` shows the second browser with the typed text; `disk-content.md` holds the same text.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/live-sse/`); pass each to `record --artifact`.
Side effect to record: file written: data/pages/default/<page>.md.

## Gotchas

- Concurrent edits use a whole-document merge, not a CRDT; the `internal/crdt` room is not wired to handlers yet.
