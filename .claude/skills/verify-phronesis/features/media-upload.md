# media-upload: Upload and serve images

- id: media-upload
- proven: 696d2475b27c8d8dc0b2aaa689fdf4fac9a35b3b
- anchors: internal/media/handler.go, internal/blob
- constraints: collab-wiki:S6

## What it is

A signed-in user uploads an image; it is stored content-addressed and served back at `/media/<sha>`. Disallowed content types are refused.

## How to reach it

- API: `POST /media` (raw body, `Content-Type: image/png`), `GET /media/<sha>`.
- Editor image embeds reference `/media/<sha>`.

## Drive it

```sh
node "$SKILL/scripts/drive.mjs" media-upload "$INSTANCE"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: upload_accepted, download_matches, blob_on_disk, disallowed_type_rejected.
Exit 1 means a check failed (each failed check has `"ok": false` and a `detail`); exit 2 means the instance is not running.

## Proof

`upload-response.json` carries the `/media/<sha>` URL; downloaded bytes equal the upload; `blob-files.txt` lists the blob under `data/blobs/`.
Artifacts are listed in the JSON `artifacts` array (under `.verify-run/<instance>/artifacts/media-upload/`); pass each to `record --artifact`.
Side effect to record: blob file written under data/blobs/.

## Gotchas

- There is no upload button in the UI; the user path is the HTTP endpoint.
