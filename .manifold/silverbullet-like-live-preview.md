# silverbullet-like-live-preview

## Outcome

The phronesis editor renders markdown live inside the CodeMirror surface, in the style of SilverBullet.md: when the cursor is outside a markdown construct, the source is replaced by its rendered form (headings appear at heading sizes, `**bold**` shows as bold without the asterisks, `` `code` `` shows as inline code without backticks, `[label](url)` shows as a link chip without the bracket syntax, list bullets render as bullets, etc.). When the cursor enters a construct, the raw source is revealed for editing. There is no split preview pane — editing and reading happen on the same surface.

The existing `wikiLinkDecorations` ViewPlugin in `frontend/src/lib/Editor.svelte` already implements this pattern for `[[wiki-links]]` and is the working template to extend.

Out of scope for this manifold: full WYSIWYG (no toolbar, no contenteditable rewrites), block-level embeds (iframes, diagrams beyond images), and CRDT-backed collaborative cursors.

---

## Constraints

### Business

#### B1: Single editing surface preserved
Editing and rendering happen on one CodeMirror surface. No split preview pane, no separate read-mode view, no toolbar overlay that hides the source.

> **Rationale:** CLAUDE.md commits to "the frontend should continue moving toward a single-surface live editor model rather than reverting to a split preview/editor design." This work must reinforce that direction, not break it.

#### B2: Markdown files on disk remain the source of truth
The editor must not introduce any persisted state outside the markdown file itself. Decoration-driven views are derivable from file content alone; there is no shadow document, no rendered cache that diverges.

> **Rationale:** CLAUDE.md "Architectural Constraints" — first bullet. The wiki's portability story rests on this; if the rendered editor state ever leaks into persistence, every promise about Markdown-on-disk breaks.

### Technical

#### T1: Decorations are visual-only; document text is never mutated
ViewPlugin updates may issue `Decoration.replace` / `Decoration.mark` / `Decoration.widget` ranges, but must never call `view.dispatch({changes: ...})` to alter `state.doc` from inside the decoration pipeline. Copy/cut, autosave, SSE snapshot, and `view.state.doc.toString()` must continue to read raw markdown source.

> **Rationale:** If a heading widget rewrites `# Title` to `Title` in state.doc, autosave POSTs the rendered form to `/api/pages/<n>` and the file on disk is corrupted. Same hazard for SSE snapshots and concurrent merge in `internal/wiki/session.go`.

#### T2: Viewport-scoped, debounced decoration rebuild — p95 < 16ms
The decoration ViewPlugin builds ranges only for the EditorView's visible viewport, not the full document. Edit-driven rebuilds debounce/coalesce within one animation frame. Target: p95 rebuild cost < 16ms for typical edits inside the viewport on the test laptop.

> **Rationale:** Phronesis pages can grow to tens of KB. A naive full-doc decorate-on-every-keystroke loop produces visible jank well before 50KB. The user explicitly chose "viewport + debounce" over a hard size cliff or accepting jank, in m1 interview.

#### T3: No new npm dependencies
Implementation uses only packages already in `frontend/package.json`: `@codemirror/state`, `@codemirror/view`, `@codemirror/lang-markdown`, `@codemirror/language`, `@codemirror/commands`, `@lezer/highlight`. No new runtime, no new dev-only build helper.

> **Rationale:** CLAUDE.md "keep the backend dependency-light" extends to the frontend by precedent. Lezer markdown trees + CM's existing decoration primitives are sufficient — adding a remark/rehype pipeline would duplicate parsing.

#### T4: Reuse the wikiLinkDecorations decoration pattern
New decorations follow the same shape: `ViewPlugin.fromClass(...)` with a `decorations` field, built by a `RangeSetBuilder` walking the document, using `Decoration.replace({widget: ..., inclusive: false})` and `selectionTouches()` to suppress decoration when the cursor is inside the range.

> **Rationale:** Reduces cognitive load and means the existing wiki-link unit of behaviour is the contract for everything else. Tagged as `assumption` in m1 because we have not validated this pattern at scale across all V1 constructs (especially block-level: tables, fenced code, blockquotes). m3 must verify before m4 generates code.

#### T5: Server-side render output is unchanged
This work touches `frontend/src/lib/Editor.svelte` and supporting decoration files only. `internal/render/markdown.go`, `internal/wiki/store.go`, `internal/app/handlers_pages.go`, and the `/api/pages/<n>` JSON contract stay byte-identical.

> **Rationale:** The server's render is consumed by non-editor surfaces (backlinks panel, future API clients, agent automation). Decoupling guarantees this feature does not regress them.

### User Experience

#### U1: Cursor inside a decorated region reveals source for that one region
When the cursor enters the byte range of a decorated construct, only that range's decoration is suppressed; neighbouring decorations remain rendered. Same behaviour `wikiLinkDecorations` uses today via its `selectionTouches` helper.

> **Rationale:** Users must always be able to see the raw markdown they're editing. Whole-document source-on-focus would defeat the live-preview point.

#### U2: V1 decoration coverage is the Full set
V1 must decorate: ATX headings (`#` to `######`), `**bold**` and `__bold__`, `*italic*` and `_italic_`, `` `inline code` ``, fenced code blocks (rendered as a styled monospace block, source revealed when cursor enters), `[label](url)` markdown links, `[label][ref]` reference-style links if cheap, unordered list bullets (`-`, `*`, `+`), ordered list bullets (`1.` etc.), GFM tables, `> blockquote` blocks, `![alt](src)` images. Wiki-links (`[[Page]]`) are already done and remain.

> **Rationale:** User chose "Full" over "Lean" or "Mid" in m1 interview. Tables and images are explicitly in V1 even though they are the harder decorations. Out-of-V1: footnotes, definition lists, HTML embeds, math.

#### U3: Plain typing must not move the cursor or scroll the viewport
After a single-character insert in undecorated text, `view.state.selection.main.head` is exactly `previous-position + 1` and `view.scrollDOM.scrollTop` is unchanged. Decoration rebuilds must not dispatch effects that reset selection or trigger `scrollIntoView`.

> **Rationale:** SilverBullet's responsiveness is a defining feel — any cursor jitter on typing reads as broken regardless of how good the decoration looks.

#### U4: Markdown constructs not in U2 render as raw source unchanged
For any markdown syntax not covered by V1 decorations (footnotes, math, HTML embeds, etc.), the editor displays the raw source exactly as today's behaviour. No half-styled indicators, no "missing decoration" markers.

> **Rationale:** User chose "Leave raw source unchanged" over "faint indicator" in m1 interview. Predictability beats partial coverage hints.

### Security

#### S1: Editor URL rendering uses the safeURL allow-list
Any decoration that produces a clickable link (markdown links, image src) must run the URL through the same allow-list logic as `internal/render/markdown.go::safeURL`: http/https/mailto/relative/fragment pass through; everything else collapses to `#`. Implementation lives in `frontend/src/lib/safeURL.ts` (or equivalent), unit-tested for parity with the Go version.

> **Rationale:** Commit `84a4d27` closed the `javascript:`/`data:`/`vbscript:` XSS vector at server render. Editor decorations that build their own anchors would re-open it if they trusted the source URL directly.

#### S2: Decoration widgets set content via textContent, never innerHTML
Inside any `WidgetType.toDOM()` (or equivalent), text content of the rendered widget — link labels, code spans, heading text, table cells, blockquote text — is assigned via `element.textContent = …` (or `document.createTextNode`), never `element.innerHTML = …`. Embedded raw HTML in markdown source stays inert.

> **Rationale:** Markdown allows raw HTML. If a decoration widget unwraps `**bold**` to `<strong>` via innerHTML and the user wrote `**<script>alert()</script>**`, the script becomes live in the editor — same hazard the server-side `xssdefense.SanitizeHTML` blocks for the rendered preview pane.

### Operational

#### O1: Playwright e2e covers each V1 decoration family
For every construct family in U2, at least one e2e test asserts: (a) decorated when cursor is outside, (b) source revealed when cursor is inside the range. Tests live under `frontend/tests/e2e/` and run as part of the existing Playwright suite.

> **Rationale:** Without e2e, decoration regressions only surface during manual review (which is how the broken `EditorState.facet` and missing `backlinks` field shipped to the working tree this week). Component-level unit tests are insufficient — the cursor-in/cursor-out interaction is genuinely integration-shaped.

#### O2: Bundle-size delta ≤ 30 KB gzipped
After this feature, the production bundle (`dist/assets/index-*.js`) gzipped size grows by no more than 30 KB over the current ~191 KB baseline. Measured by `make build` output.

> **Rationale:** Phronesis ships as a single embedded binary. Bundle bloat directly impacts cold-load time for the wiki UI. Decorations are CodeMirror extensions and should be small; if we exceed 30 KB we have probably duplicated parsing or imported a markdown library we don't need.

#### O3: Smoke e2e fails loud if all decorations break
A single Playwright test asserts at least one inline decoration (`**bold**` or `# Heading`) renders correctly on a known seed page. Failure indicates wholesale decoration breakage (e.g. CodeMirror major-version API drift) and gates CI.

> **Rationale:** Pre-mortem story #3 — silent regression where decorations no-op and users see raw source for weeks before anyone notices. A coarse "is this thing on" check is cheaper than discovering it from a user complaint.

---

## Tensions

### TN1: Full V1 coverage vs no new npm deps
**Between:** U2 (Full set including tables/images/blockquotes) and T3 (no new dependencies).
**Type:** trade_off. TRIZ: Technical contradiction, Capability vs Simplicity, principles P15/P40.

`@codemirror/lang-markdown` ships with optional GFM extensions (tables, task lists) reachable via `@lezer/markdown`. If GFM is configurable through the existing transitive dep, U2 stands. If not, we'd need to add `@lezer/markdown` (or another markdown extension) as a top-level package and relax T3.

> **Resolution:** Use `@codemirror/lang-markdown`'s exported `markdownLanguage` (the GFM-enabled variant — "GFM plus subscript, superscript, and emoji syntax" per its docstring) via `markdown({ base: markdownLanguage })`. RT-1 verification probe (`scripts/verify-rt1-gfm-parsing.mjs`) ran on 2026-05-01 and confirmed all V1 Lezer node types parse correctly: ATXHeading1, StrongEmphasis, Emphasis, InlineCode, Link, BulletList, OrderedList, FencedCode, Blockquote, Table, TableRow, TableCell, Image. No import from `@lezer/markdown` was needed; `package.json` is untouched and T3 stays clean.

### TN2: Block-level constructs vs inline-only wikiLinkDecorations pattern
**Between:** U2 (tables, fenced code, blockquotes are block-level) and T4 (reuse the `wikiLinkDecorations` pattern, which is inline-only).
**Type:** trade_off. TRIZ: Physical contradiction, principle P1 (Segmentation).

> **Resolution:** Two sibling pattern families share the same skeleton (ViewPlugin + RangeSetBuilder + selectionTouches). Inline plugins use `Decoration.replace({inclusive: false})`; block plugins use `Decoration.replace({block: true})` or line decorations. T4 refined to "reuse the *skeleton*, not necessarily the exact replace shape." Modules live under `frontend/src/lib/editor/decorations/{inline,block}/`.

### TN3: 16ms frame budget vs expensive table widget construction
**Between:** T2 (p95 < 16ms viewport rebuild) and U2 (tables and fenced code blocks have non-trivial widget DOM cost).
**Type:** resource_tension. TRIZ: Speed vs Capability, principles P10 (Prior action) + P11 (Beforehand cushioning).

> **Resolution:** Memoize widget instances keyed by `(node range, source-slice hash)`. `WidgetType.eq()` returns true for unchanged widgets, so `Decoration.replace` short-circuits. Edits adjacent to but outside a table produce zero new table widgets. Cache invalidation: automatic via WeakMap; eviction on doc-change covering the range. No TTL.

### TN4: Immediate source-reveal vs debounced rebuild
**Between:** U1 (cursor enters → source revealed for that region) and T2 (rebuilds debounced).
**Type:** trade_off. TRIZ: Physical contradiction, P1 (Segmentation by trigger type).

> **Resolution:** Segment rebuild policy by trigger. Selection-change rebuilds run immediately (cheap — only `selectionTouches` recheck, no parse). Doc-change rebuilds debounced via `requestAnimationFrame`. T2's spec is now two-pathed; the 16ms budget applies to doc-change rebuilds only.

### TN5: Per-family e2e tests vs CI flakiness budget
**Between:** O1 (e2e per V1 family — ~12 tests) and the project's recent e2e stability work (5 of last 10 commits were Playwright fixes).
**Type:** resource_tension. TRIZ: Coverage vs Cost, principle P5 (Merging).

> **Resolution:** ONE Playwright test loads a seed page containing every V1 construct family, then asserts cursor-out (rendered) and cursor-in (source) for each. ~12 tests collapse to 1 with thorough assertions inside it. O1 refined.

### TN6: Smoke test vs viewport-only decoration
**Between:** O3 (smoke test fails loud if decorations break) and T2 (viewport-scoped decoration).
**Type:** hidden_dependency.

> **Resolution:** O3's fixture page must fit in one initial viewport so all asserted constructs render on load. Without this, the smoke could pass vacuously when off-viewport decoration breaks. Fixture spec: ≤ 30 lines of markdown, asserted constructs all in initial viewport.

### TN7: Image rendering vs on-disk source-of-truth URL convention
**Between:** U2 (`![alt](src)` decoration) and B2 (markdown on disk, no shadow representation).
**Type:** hidden_dependency.

> **Resolution:** V1 image decoration accepts (a) absolute URLs (subject to S1 allow-list), (b) `/media/<sha>` paths matching `internal/media`'s blob handler. Wiki-relative paths and attachment shorthands are deferred to V2 — per U4 they render as raw markdown source. U2's image scope is narrowed accordingly.

### TN8: Bundle budget vs Full V1 breadth
**Between:** O2 (≤ 30 KB gzipped delta) and U2 (~12 decoration families).
**Type:** resource_tension. TRIZ: Cost vs Capability, principles P5 (Merging) + P10 (Prior action).

> **Resolution:** Single shared `Decorator` base module under `frontend/src/lib/editor/decorations/base.ts` provides the ViewPlugin scaffold, RangeSetBuilder driver, `selectionTouches` helper, and `safeURL` port. Per-family code is just node-type matching + widget DOM. Add a CI bundle-size gate so regressions fail loud rather than at manual review time.

---

## Required Truths

### RT-1: GFM-aware Lezer parse tree distinguishes all V1 constructs

For U2's "Full" V1 scope to be achievable while honoring T3 (no new top-level deps), the existing `@codemirror/lang-markdown` must expose configuration to enable GFM extensions (tables, task lists) sourced from the already-transitive `@lezer/markdown`. The parse tree must distinguish Heading / Emphasis / InlineCode / FencedCode / Link / List / Table / Blockquote / Image nodes by name.

**Gap:** Not yet verified in this codebase. `@lezer/markdown` is a transitive dep of `@codemirror/lang-markdown` but the configuration surface (`markdown({ extensions: [...] })`) hasn't been exercised here. m4 must produce a verification artifact (a parse-tree dump on a GFM-table fixture) BEFORE generating decoration code.

### RT-2: Decoration system renders widgets without mutating state.doc

CodeMirror 6's `Decoration.replace`, `Decoration.line`, and `Decoration.widget` are visual layers over the document. State.doc returns the raw markdown source unaltered.

**Gap:** None — guaranteed by CM 6 API contract; already proven in this codebase by `wikiLinkDecorations`.

### RT-3: ViewUpdate exposes viewport scope and trigger-type info

`ViewUpdate.viewportChanged`, `ViewUpdate.docChanged`, `ViewUpdate.selectionSet` are independent flags. `EditorView.visibleRanges` provides the viewport byte range. The selection-change rebuild path can run synchronously while doc-change rebuilds debounce.

**Gap:** Verifiable now via grep on `@codemirror/view` types; assumed-true based on CM 6 docs.

### RT-4: WidgetType.eq() short-circuits unchanged-widget rebuild

CodeMirror's decoration diff algorithm calls `WidgetType.eq()` and avoids replacing widget DOM if it returns true. Implementing `eq()` correctly is sufficient to satisfy TN3's memoization plan.

**Gap:** None at the API level; correct `eq()` implementation per family is m4's job.

### RT-5: safeURL TypeScript port reaches behavioral parity with Go reference

The allow-list logic in `internal/render/markdown.go::safeURL` is a pure function. A TS port with the same scheme list (http/https/mailto/relative/fragment/scheme-relative) and the same fallback (`#`) produces the same output for every observable input.

**Gap:** Parity test fixture must be generated alongside the TS port — same input cases run against both implementations, byte-equal output asserted.

### RT-6: Widget DOM construction uses textContent only

Every `WidgetType.toDOM()` and helper assigns user-derived text via `element.textContent = …` or `document.createTextNode(…)`. No `innerHTML`, no `outerHTML`, no template-string-with-interpolation patterns.

**Gap:** Enforceable via code review at m4 output time and ESLint rule (no-inner-html) post-merge.

### RT-7: Server-side render contract is byte-stable

`internal/render/markdown.go`, `internal/wiki/store.go`, `internal/app/handlers_pages.go`, and the `/api/pages/<n>` JSON shape do not change as part of this work.

**Gap:** Already true today (RT-7 is `SATISFIED`). The check is "did we accidentally edit these files?" which is verifiable from m4's diff.

### RT-8: wikiLinkDecorations skeleton generalises to inline + block via shared base

The skeleton (`ViewPlugin.fromClass` + `RangeSetBuilder` + `selectionTouches`) drives both inline `Decoration.replace({inclusive: false})` and block `Decoration.replace({block: true})` / `Decoration.line()` from the same base.

**Gap:** Inline is proven via the existing wikilink plugin. The block sibling pattern is documented in CM 6 but not exercised in this codebase yet — first block decoration (likely fenced code or blockquote) is the validation point.

### RT-9: Bundle delta ≤ 30 KB gzipped over baseline

After all V1 decoration modules + shared base are added, `dist/assets/index-*.js` gzipped size grows ≤ 30 KB over today's ~191 KB baseline.

**Gap:** Not measurable until m4 produces code. Mitigated by the shared-base architecture (TN8) that prevents per-family duplication.

### RT-10: Playwright drives cursor-position assertions in CodeMirror

Existing wikilink e2e tests already exercise `page.keyboard.press('ArrowRight')` style interactions and assert on `.cm-wikilink` class presence, so the framework capability is proven.

**Gap:** None at framework level. The new tests need to be written.

### RT-11: O3 smoke fixture renders all asserted constructs in initial viewport

Smoke fixture markdown is ≤ 30 lines. At default editor min-height (60vh, ~480px on a 1080p screen), all asserted constructs are visible without scroll.

**Gap:** Fixture content must be authored and viewport-fit verified at m4 time.

### RT-12: Image URL decoration scope is bounded to absolute + /media/<sha>

URL classifier accepts: (a) http/https/mailto absolute URLs subject to S1 allow-list, (b) `/media/<sha>` paths matching `internal/media`'s blob handler. Anything else (relative paths, `attachment:` shorthands) produces no decoration — raw markdown source remains visible per U4.

**Gap:** Classifier is a small pure function; written and unit-tested at m4 time.

### RT-13: CI bundle-size gate fails the build on >30 KB regression

CI workflow runs `make build`, computes gzipped delta against a stored baseline, and exits non-zero if delta exceeds 30 KB. Must be in place before any V1 PR merges so the gate prevents regression rather than just reporting it.

**Gap:** No bundle-size CI gate exists today. Needs a small script + workflow step + baseline file.

---

## Solution Space

### Option A: Honor all constraints — build Full V1 as specified ← Recommended

**Reversibility:** TWO_WAY (each component is independently testable; rollback is reverting the merged commits).

- GFM extensions reached via the existing transitive `@lezer/markdown` (assumes RT-1 closes).
- Two sibling decoration pattern families under `frontend/src/lib/editor/decorations/{inline,block}/`, both importing a shared `decorations/base.ts` (ViewPlugin scaffold, RangeSetBuilder driver, selectionTouches helper, safeURL port).
- Memoized widgets, viewport-scoped builds, segmented rebuild policy (selection-change immediate, doc-change debounced).
- All m2 tension resolutions honored as written.

**Satisfies:** every RT from RT-2 through RT-13.
**Depends on:** RT-1 closing (binding).

### Option B: Constructive relax of U2 — drop tables/blockquotes/images from V1

**Reversibility:** REVERSIBLE_WITH_COST. V2 has to add the block work later, by which point the inline implementation is already merged and the inline-vs-block pattern split would need to be retrofitted.

- Falls back to "Mid" V1 scope (the option the user did NOT pick in m1).
- Single inline pattern only; no block sibling.
- Activates if RT-1 fails to close (GFM not reachable).

**Satisfies:** every RT from RT-2 through RT-13 *minus block-construct coverage*.
**Relaxes:** U2 (Full → Mid).

### Option C: Constructive relax of T3 — explicitly add `@lezer/markdown`

**Reversibility:** TWO_WAY (deps are removable; this is a soft policy commitment rather than a hard architectural one).

- Activates if RT-1 fails AND user prefers Full V1 over the dep-light commitment.
- T3's `challenger: stakeholder` makes this negotiable; CLAUDE.md's "keep dependency-light" is a precedent, not a contract.

**Satisfies:** every RT.
**Relaxes:** T3 (one new explicit top-level dep).

### Option D: Replace `@codemirror/lang-markdown` with `remark`/`unified` parsing

**Reversibility:** ONE_WAY in practice (large architectural change).

VIOLATES T3 hard. Not recommended.

---

## Tension Validation (Option A)

| Tension | Status | Note |
|---|---|---|
| TN1 | CONFIRMED | RT-1 closed via the verification probe on 2026-05-01; markdownLanguage exposes all V1 node types without a new dep. |
| TN2 | CONFIRMED | A explicitly creates inline + block sibling families. |
| TN3 | CONFIRMED | A memoizes widgets per the recorded resolution. |
| TN4 | CONFIRMED | A segments rebuild policy by trigger type. |
| TN5 | CONFIRMED | A produces a single Playwright file enumerating all V1 families. |
| TN6 | CONFIRMED | A includes the viewport-bounded smoke fixture. |
| TN7 | CONFIRMED | A scopes image URLs to absolute + /media/<sha>. |
| TN8 | CONFIRMED | A uses the shared base + CI bundle-size gate. |
