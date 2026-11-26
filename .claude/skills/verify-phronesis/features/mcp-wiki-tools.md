# mcp-wiki-tools: Agents list, read, write and search pages over MCP

- id: mcp-wiki-tools
- proven: 6ca46d12a3830b909ff6b6b099524ab191b33123
- anchors: internal/app/mcp_tools.go, internal/mcp/server.go, internal/wiki/session.go
- constraints:

## What it is

An AI agent connected to `/mcp` uses the tools `list_pages`, `read_page`, `write_page` and
`search_pages`. Each call is checked like the HTTP API: the credential's role must allow
read or write, and a workspace-pinned key only reaches its own workspace. What an agent
writes is a normal Markdown page that people see in the editor.

## How to reach it

- `POST /mcp` JSON-RPC `tools/list`, `tools/call` with `Authorization: Bearer <key or
  OAuth token>`.

## Drive it

```sh
"$SKILL/scripts/launch.sh" "$INSTANCE_KEYS" --oidc --oauth
node "$SKILL/scripts/drive.mjs" mcp-wiki-tools "$INSTANCE_KEYS"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: tools_listed,
write_page_with_write_key, markdown_file_written_by_agent, read_key_cannot_write,
read_page, list_pages, search_pages, agent_edit_visible_in_ui.
Exit 1 means a check failed; exit 2 means the instance is not running.

## Proof

`mcp-tools-transcript.json` holds every tool result (including the `forbidden` error for
the read key); the drive reads `data/pages/default/agent-note-*.md` from disk;
`agent-page-in-ui.png` shows the agent's text in the editor.
Side effect to record: file written: data/pages/default/agent-note-*.md.

## Gotchas

- Needs `--oidc --oauth`: keys come from the request -> approve flow (an OIDC user
  promoted to editor, so the write key is not capped to read).
- Use the key from a cookie-less context; a session cookie outranks a bearer token.
