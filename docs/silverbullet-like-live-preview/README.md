# SilverBullet-style Live Preview

The phronesis editor renders markdown live inside the CodeMirror editing surface — headings appear at heading sizes, `**bold**` shows bold without the asterisks, links render as chips, tables render as real `<table>` elements — all on one surface with no split preview pane. When the cursor enters a markdown construct, the raw source is revealed for that region only; everything else stays rendered.

This document covers the shape of the system and how to add a new decoration family. The full design, constraint set, tensions, and reasoning live in [`.manifold/silverbullet-like-live-preview.{json,md}`](../../.manifold/silverbullet-like-live-preview.md).

## Module layout

```
frontend/src/lib/
├── safeURL.ts                         URL allow-list (TS port of Go safeURL)
├── Editor.svelte                      Composes the live-preview extension into
│                                      the CodeMirror editor instance
└── editor/decorations/
    ├── base.ts                        DecorationFamily contract,
    │                                  liveDecorationPlugin composer,
    │                                  selectionTouches, treeFamily(),
    │                                  rebuildLivePreview StateEffect
    ├── index.ts                       Barrel + composeV1Families registry
    ├── inline/
    │   ├── wiki-links.ts              [[Page]] → chip widget
    │   ├── headings.ts                # Heading → styled heading
    │   ├── emphasis.ts                **bold** / *italic*
    │   ├── inline-code.ts             `code`
    │   ├── markdown-links.ts          [label](url) → link chip
    │   ├── lists.ts                   - / 1. list markers
    │   └── images.ts                  ![alt](src) → <img> widget
    └── block/
        ├── fenced-code.ts             ```code``` block styling
        ├── blockquote.ts              > blockquote
        └── tables.ts                  | tables | → <table> widget
```

## Architecture

### One ViewPlugin, many families

A single CodeMirror `ViewPlugin` is composed from a list of `DecorationFamily` objects (see `liveDecorationPlugin` in `base.ts`). On every relevant `ViewUpdate`, the plugin walks the visible viewport once, dispatches each visible Lezer node to the families that handle it, and merges all produced decoration ranges into a single sorted `RangeSet`.

This means adding a new family is a strictly additive change — no edits to `Editor.svelte`, no second ViewPlugin, no new `tree.iterate` traversal. The shared base is the only place that knows about CodeMirror plugin lifecycle.

### Tree-driven vs regex-driven families

Most families are *tree-driven* — they declare a list of Lezer node types (e.g. `ATXHeading1`, `Emphasis`, `Table`) and a builder, and `treeFamily()` from `base.ts` wires them into the plugin's tree iteration. This is the primary path.

Wiki-links (`[[Page]]`) are not standard markdown grammar, so the wiki-links family is *regex-driven* — its `scan()` runs a regex over the visible doc slice. The contract is identical (it returns `Range<Decoration>[]` from `scan(ctx)`); only the matching strategy differs.

### Cursor-aware source reveal

Every family receives `isCursorInRange(from, to)` in its `build()` callback. The standard pattern: when the cursor is inside the construct's range, the family suppresses the *replacement* decoration (so the raw source stays visible) but may still emit `mark` decorations for styling. When the cursor is outside, the family adds the full set of decorations including any source-hiding `replace`.

### Rebuild policy (T2 + TN4)

The plugin rebuilds on three triggers:
- `update.docChanged` — debounced naturally by CodeMirror's frame-batched update loop.
- `update.viewportChanged` — when the visible range changes (scroll, resize).
- `update.selectionSet` — every selection change. Cheap because it doesn't re-parse; it just runs `selectionTouches()` against pre-cached node ranges.

A fourth trigger is the `rebuildLivePreview` `StateEffect` — fired by `Editor.svelte` when an external dependency of the decoration set changes (currently: the active page name, used by wiki-link widgets to mark themselves "current").

### Bundle-size gate (O2 + RT-13)

`scripts/check-bundle-size.sh` measures the gzipped size of `frontend/dist/assets/index-*.js` and compares it to the baseline at `scripts/.bundle-size-baseline`. CI fails on >30 KB delta. Update the baseline intentionally with `UPDATE_BASELINE=1 scripts/check-bundle-size.sh`.

## Adding a new decoration family

1. **Identify the Lezer node types** you want to decorate. The full set produced by `markdownLanguage` (the GFM-enabled variant) is enumerated in `scripts/verify-rt1-gfm-parsing.mjs` — run it to list every node type your family can match against.

2. **Decide the decoration shape**:
   - Inline mark (style only, source stays): `Decoration.mark({class: 'cm-md-...'})`
   - Inline replace (substitute source with widget): `Decoration.replace({widget: new MyWidget(...)})`
   - Block line (style whole lines): `Decoration.line({class: 'cm-md-...'})`
   - Block replace (substitute whole block with widget): `Decoration.replace({widget: ..., block: true})`

3. **Create a file** under `frontend/src/lib/editor/decorations/inline/` or `block/`. Use `treeFamily()` for tree-driven families. Reference shape:

   ```ts
   import { treeFamily, Decoration } from '../base';
   import type { DecorationFamily } from '../base';

   export function myThingFamily(): DecorationFamily {
     return treeFamily({
       name: 'my-thing',
       nodeTypes: ['MyNodeType'],
       build({ node, isCursorInRange }) {
         if (isCursorInRange) return null;  // source visible while editing
         return [
           Decoration.mark({ class: 'cm-md-my-thing' }).range(node.from, node.to),
         ];
       },
     });
   }
   ```

4. **Register in `index.ts`**:

   ```ts
   import { myThingFamily } from './inline/my-thing';

   export function composeV1Families(opts: V1Options): readonly DecorationFamily[] {
     return [
       wikiLinksFamily({ ... }),
       // ... existing families
       myThingFamily(),
     ];
   }
   ```

5. **Add a test case** to `frontend/tests/e2e/live-preview/live-preview.spec.ts`.

6. **Add CSS** to `Editor.svelte`'s `EditorView.theme({...})` block for the new class names.

7. **Run `make build && make bundle-size`** — if your delta blows the budget, the family probably duplicates parsing or imports something unnecessarily large.

## Hard constraints (from the manifold)

If your change violates any of these, stop and revisit the manifold rather than working around them:

- **T1**: Decorations are visual-only. `view.dispatch({changes: ...})` from a family is a release-blocking bug — autosave will write rendered HTML to disk and corrupt the wiki.
- **T3**: No new npm dependencies. If you need a parser CodeMirror doesn't expose, the answer is to read `markdownLanguage`'s Lezer tree more carefully, not to add `remark` / `rehype` / `unified`.
- **S1**: Any rendered URL must go through `safeURL()` from `frontend/src/lib/safeURL.ts`. `javascript:` / `data:` / `vbscript:` collapse to `#`.
- **S2**: Widget DOM construction uses `textContent` only, never `innerHTML`. Embedded HTML in markdown source must stay inert.
- **U1**: The cursor entering a construct's range reveals the source for *that one region* — neighbouring decorations stay rendered.
- **U4**: Constructs not yet covered render as raw markdown source. No half-styled indicators.

## Pointers

- Constraint manifold: [`.manifold/silverbullet-like-live-preview.md`](../../.manifold/silverbullet-like-live-preview.md)
- Design decisions: [`DECISIONS.md`](DECISIONS.md)
- RT-1 verification probe (re-run any time): `node scripts/verify-rt1-gfm-parsing.mjs`
- Bundle gate: `make bundle-size`
- E2E suite: `cd frontend && npx playwright test live-preview/`
