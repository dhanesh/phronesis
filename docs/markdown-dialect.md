# Markdown dialect

Phronesis stores pages as plain Markdown on disk and renders a subset of
Markdown plus a few wiki-specific extensions live inside the editor.
This page documents what's recognised, where it shows up (live editor
vs. server-rendered HTML), and a couple of behaviours that diverge from
CommonMark.

## What renders where

There are two rendering paths.

**Live editor (frontend, the canonical surface).** When you edit a page
in the browser, the editor renders Markdown in place — `# Heading`
appears at heading sizes, `**bold**` shows bold without the asterisks,
tables render as real `<table>` elements, etc. Place the cursor inside
a construct and the raw Markdown source for that one region reappears
for editing; everything else stays rendered. This is the SilverBullet-
style live preview surface (see
[`docs/silverbullet-like-live-preview/README.md`](silverbullet-like-live-preview/README.md)
for the architecture).

**Server-rendered HTML (backend, `internal/render/markdown.go`).** A
deliberately minimal Markdown-to-HTML pass used when the API returns
rendered output (and to extract derived metadata like links, tags, and
tasks). The server renderer is **not** CommonMark-complete and is not
the primary surface — it's the fallback for non-browser clients and
the metadata extractor for things like backlinks. Treat the live
editor as the source of truth for what's supported visually.

## Supported syntax

### Text

| Syntax | Renders as | Live editor | Server HTML |
|---|---|:-:|:-:|
| `# Heading 1` to `###### Heading 6` (ATX) | `<h1>` … `<h6>` | ✓ | ✓ |
| `**bold**` | **bold** | ✓ | — |
| `*italic*` or `_italic_` | *italic* | ✓ | — |
| `` `inline code` `` | `inline code` | ✓ | — |
| `"smart quotes"` outside code | curly quotes | ✓ | — |

### Links

| Syntax | Renders as |
|---|---|
| `[label](https://example.com)` | external link |
| `[[Page Name]]` | wiki link to `/w/page-name` (lowercased + slugified) |
| `[[Page Name\|alias]]` | wiki link with display text "alias" |
| `![alt](https://example.com/img.png)` | inline `<img>` |

URLs go through an allow-list. `http`, `https`, `mailto`, relative
paths, and fragment-only references render as live links. Anything
else (`javascript:`, `data:`, `vbscript:`, `file:`, `about:`) is
collapsed to `href="#"` so it cannot fire.

### Lists

| Syntax | Renders as |
|---|---|
| `- item` or `* item` | unordered list |
| `1. item` | ordered list |
| `- [ ] todo` / `- [x] done` | task list with a clickable checkbox |

Task checkboxes are interactive in the live editor — clicking one
toggles `[ ]` ↔ `[x]` in the source.

### Blocks

| Syntax | Renders as |
|---|---|
| `` ``` ``` `` (with or without a language tag) | fenced code block; copy button on hover |
| `> quoted` | blockquote |
| `\| col \| col \|` with a `\|---\|---\|` separator | rendered table |

### Wiki primitives

These are the agent-readable structured surfaces; they're indexed and
exposed through the API so tools (and the MCP server) can discover and
navigate them.

| Syntax | Meaning |
|---|---|
| `[[Page]]` | wiki link; the target appears in the source page's `links` and the target's `backlinks` |
| `#tag` | tag; available on the page's `tags` list and indexed by the tag → pages reverse index |
| YAML frontmatter (`---` block at the very top) | per-page metadata; renders as a compact pill bar in the editor |
| `[key:: value]` | inline attribute pair; renders as a "key: value" pill in the editor |

### GitHub-style admonitions

A blockquote whose first line is `[!note]`, `[!tip]`, `[!important]`,
`[!warning]`, `[!caution]`, or `[!danger]` renders with a distinctive
callout style.

```markdown
> [!warning]
> This is a warning callout.
```

## Divergences from CommonMark

The server renderer is a custom pass, not a CommonMark library. The
divergences that matter:

- **No reference-style links.** `[label][ref]` and `[ref]: url` aren't
  recognised. Use inline `[label](url)`.
- **No setext headings.** `Heading\n=======` isn't a heading; use ATX
  (`# Heading`).
- **No automatic link autolinking.** A bare URL in text isn't turned
  into a link; wrap it as `[example.com](https://example.com)`.
- **No HTML passthrough.** Embedded `<script>`, `onerror=`, and similar
  HTML stay inert (rendered as text or stripped).
- **Frontmatter must be at the very top.** A `---` block anywhere else
  is treated as a thematic break, not metadata.

## Editor vs. server feature gap

Several features render in the live editor but not in the server's
HTML output (because the server renderer is intentionally minimal):
bold/italic, inline code, tables, blockquotes, images, frontmatter
pill bar, attribute pills, admonitions, hashtag chips. If you need
fully rendered HTML for a page, the canonical path is the live editor;
the server's `html` field on the page API is best for plain-text
extraction and metadata, not for end-user display.

## Pointers

- Live-preview architecture and how to add a new decoration family:
  [`docs/silverbullet-like-live-preview/README.md`](silverbullet-like-live-preview/README.md)
- Server-side renderer source: [`internal/render/markdown.go`](../internal/render/markdown.go)
- URL allow-list (frontend): [`frontend/src/lib/safeURL.ts`](../frontend/src/lib/safeURL.ts)
