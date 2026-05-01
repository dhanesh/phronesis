# SilverBullet-style Live Preview — Decision Records

Architectural decisions made during the manifold flow for this feature. Each decision references the constraints, tensions, and required truths it resolves. The full constraint network lives in [`.manifold/silverbullet-like-live-preview.{json,md}`](../../.manifold/silverbullet-like-live-preview.md).

## D1: Use `markdownLanguage` (GFM-enabled) instead of plain `markdown()`

**Resolves**: TN1, RT-1, U2, T3.

`@codemirror/lang-markdown` exports a pre-built `markdownLanguage` documented as "GFM plus subscript, superscript, and emoji syntax." Using it via `markdown({ base: markdownLanguage })` gives us GFM tables, task lists, strikethrough, and the rest of the V1 set without importing `@lezer/markdown` directly. T3 (no new deps) holds; `package.json` is unchanged.

**Verified by**: `scripts/verify-rt1-gfm-parsing.mjs` parses a sample doc and confirms all 13 V1 Lezer node types (ATXHeading1, StrongEmphasis, Emphasis, InlineCode, Link, BulletList, OrderedList, FencedCode, Blockquote, Table, TableRow, TableCell, Image) are present.

**Alternatives considered**:
- Importing `GFM` from `@lezer/markdown` directly. Works but `@lezer/markdown` is a transitive dep — relying on transitives is brittle and a soft T3 violation.
- Adding `@lezer/markdown` to `package.json` explicitly. Soft T3 relax; only needed if `markdownLanguage` had been insufficient. It wasn't.

## D2: One ViewPlugin composed from many `DecorationFamily` objects

**Resolves**: T4, TN8, O2.

A single `ViewPlugin` (in `base.ts`) walks the visible viewport once and dispatches each Lezer node to families that handle it. Per-family code is just `nodeTypes` + a `build` callback. Adding a new family is strictly additive — no edit to `Editor.svelte`, no second tree iteration.

**Why not one ViewPlugin per family**: 12 ViewPlugins each iterating the syntax tree over the same viewport is wasteful (12× the work). The bundle-size budget is also tight; the shared scaffold lets per-family code stay in the 50–100-line range.

**Why a `treeFamily()` helper for tree-driven families and a `scan()`-only contract for everything else**: most V1 constructs are standard markdown (headings, emphasis, code, etc.) and have distinct Lezer node types — these are tree-driven. Wiki-links (`[[Page]]`) are not standard markdown grammar; the family runs a regex over the visible doc slice. The contract handles both via the same `scan(ctx)` shape.

## D3: Inline + block sibling pattern families (not one unified family)

**Resolves**: TN2.

Inline decorations use `Decoration.replace({inclusive: false})` and `Decoration.mark`. Block decorations (fenced code, blockquote, tables) use `Decoration.line` and `Decoration.replace({block: true})`. The wikiLinkDecorations skeleton (ViewPlugin + RangeSetBuilder + selectionTouches) is shared; the `Decoration` shape per family differs.

Two physical sibling directories (`inline/` and `block/`) make the boundary explicit. They both import the same `base.ts` — so the contract is one, the implementations are split by scope.

## D4: Memoize widgets via `WidgetType.eq()`, not via custom caches

**Resolves**: TN3, T2.

CodeMirror's decoration diff calls `WidgetType.eq()` and skips DOM replacement when it returns true. Every widget implements `eq()` correctly comparing the meaningful display fields. Edits adjacent to but outside a table don't rebuild the table widget; the cache is implicit in CM's diff algorithm.

**Why not a custom WeakMap or LRU**: CM's diff is the canonical place for this. A custom cache layered on top would be a second source of truth — invalidation bugs are the predictable failure mode.

## D5: Segment rebuild policy — selection-change immediate, doc-change debounced

**Resolves**: TN4, U1, T2.

Selection-change rebuilds run synchronously in the plugin's `update()` because they only re-evaluate `selectionTouches()` against existing range data — no parse, no widget construction. Doc-change rebuilds are batched naturally by CodeMirror's frame-aligned update loop.

**Why not always synchronous**: doc-change rebuilds parse the visible viewport and (for some families) construct widget DOM trees. Doing this on every keystroke produces visible jank above 16ms. The frame-batching keeps it within budget.

**Why not always debounced**: the user's mental model is that the cursor entering a markdown construct *immediately* reveals the source. A 16–50ms latency on reveal would feel sluggish. Selection-change is the trigger we want to honour without delay.

## D6: Image URL scope — absolute (allow-listed) and `/media/<sha>` only for V1

**Resolves**: TN7, U2, S1, RT-12.

V1 image decoration accepts: (a) absolute URLs subject to `safeURL` allow-list (http/https), (b) `/media/<sha>` paths matching the existing blob handler in `internal/media`. Anything else (relative paths, `attachment:` shorthands, wiki-relative paths) does not decorate — the raw markdown source stays visible per U4.

**Why not also wiki-relative**: phronesis has no documented wiki-relative URL convention. Inventing one in this manifold would be scope creep into the wiki addressing model. V2 can extend.

## D7: Single Playwright e2e test per V1 family, all in one file

**Resolves**: TN5, O1.

`frontend/tests/e2e/live-preview/live-preview.spec.ts` contains one `test()` per V1 family, plus a wholesale-breakage smoke. All tests share a single seed-page fixture (`FIXTURE`) under 30 lines so every asserted construct renders in the initial viewport (this also satisfies TN6 and RT-11).

**Why not 12 separate spec files**: project's e2e suite has been the focus of recent stability work (5 of last 10 commits before this feature were Playwright fixes). One file with multiple test blocks is mechanically equivalent at the assertion level but smaller surface area for flake.

## D8: Bundle-size CI gate with stored baseline file

**Resolves**: O2, RT-13, TN8.

`scripts/check-bundle-size.sh` measures gzipped size, compares to `scripts/.bundle-size-baseline`, fails CI on >30 KB delta. Baseline is updated intentionally via `UPDATE_BASELINE=1 scripts/check-bundle-size.sh` and committed as a code change like any other.

**Why a stored baseline rather than a fixed budget**: the baseline files lets us see *cumulative* bundle drift over time and forces every meaningful regression to be acknowledged in a commit. A fixed budget either becomes too tight (blocks legitimate growth) or too loose (allows silent bloat).

**Why not bundlesize / size-limit**: T3 — no new npm dev dependencies. A 60-line bash script does the job.

## D9: `rebuildLivePreview` StateEffect for external triggers

**Resolves**: an interaction not surfaced as its own tension.

Wiki-link widgets render a "current page" CSS class when their target matches the active page. When the active page changes (the user navigates), the widgets need to re-evaluate — but `update.docChanged` / `update.viewportChanged` / `update.selectionSet` are all false on a page-prop change.

Solution: a custom `StateEffect` (`rebuildLivePreview`) that the plugin's `update()` watches for. `Editor.svelte`'s page-reconfigure `$effect` dispatches it. This avoids restoring the previous `pageFacet` + `pageCompartment` machinery; instead, a closure (`currentPageName`) is updated from the Svelte side, and a single transaction tells the plugin to re-evaluate.

**Why not a Facet**: the facet approach worked but coupled every family to CodeMirror's facet/compartment machinery. The closure + StateEffect pattern is cleaner — most families don't need it, and the ones that do (currently just wiki-links) read from a callable passed in via options.
