# admin-ui

## Outcome

A complete admin Web UI for the surfaces shipped by the user-mgmt-mcp
manifold (Stages 1-3) so that an admin can perform every available
operation in the browser without resorting to `curl`. Today the
`/admin/users` and `/admin/keys` panels exist in the Svelte frontend,
but the underlying admin surface area is broader and only partially
exposed — workspace CRUD, key minting with once-shown plaintext, the
key-request approval flow, and OAuth/MCP visibility are either
API-only or rough.

In scope (m1 ratification, 2026-05-04):

- **Workspaces** — list, create, **rename display-name** (not slug),
  delete via existing `/api/admin/workspaces/*` endpoints. Slug is
  immutable; renaming the slug would break every page URL and every
  external bookmark.
- **Users** — existing list + suspend/reactivate/delete is good. No
  changes needed to UsersManager.svelte beyond pending-request badge
  cosmetics.
- **API keys** — minting flow's once-shown plaintext display is the
  binding UX surface (S1's "shown exactly once" contract).
  Approve flow needs scope / label / expires_at form fields.
- **Key-request approval** — already wired; needs the form + plaintext
  modal noted above.
- **OAuth / MCP visibility** — minimum surface: a settings panel
  showing the discovery URL + JWKS URL with copy-to-clipboard. Skip
  registered-clients listing (no new server endpoints).

Out of scope (deferred per m1 ratification):

- Audit-log viewer in UI (audit pipeline writes are server-side; a
  read-only viewer is a separate feature).
- SSE-driven real-time admin updates (manual refresh is fine; S5's
  60s belt is already TESTED at the API layer).
- Rate-limit / concurrency dashboard.
- Registered OAuth clients list with revoke.
- Workspace-aware "switch workspaces" UX for non-admin users.
- Per-tenant theming or role customisation.

The UI integrates into the existing Svelte 5 admin shell (Cmd-K
palette gating already exists in `CommandPalette.svelte`) and respects
the existing `Role === 'admin'` server-side gate. No new npm
dependencies are introduced.

---

## Constraints

### Business

#### B1: Admin can perform every Stage 1-3 user-mgmt-mcp surface from the UI

Workspace CRUD (create, rename display-name, delete), user lifecycle
(suspend/reactivate/delete), API key lifecycle (request → approve
with form + plaintext modal → revoke), and OAuth/MCP discovery URL
visibility — every operation an admin can perform via `/api/admin/*`
must have a UI path. No `curl` required.

> **Rationale:** This is the whole point of the manifold. The README
> currently steers admins to `curl` against `/api/admin/workspaces`
> for workspace operations; that's the gap to close.
> **Quality:** 3 / 3 / 3 — verifiable by walking each admin endpoint.
> **Source:** interview. **Challenger:** stakeholder.

#### B2: Time-to-mint visible plaintext ≤ 2s wall-clock from approve click

When an admin clicks "Approve" on a pending key request, the
plaintext modal appears within 2 seconds. Bounded by network +
Argon2id hash time (T4 ceiling: 100ms p95 cold) + render time.

> **Rationale:** Admin batch-processing several requests should not
> stall on perceived latency. 2s is generous but human-tolerable.
> **Quality:** 3 / 3 / 3 — Playwright assertion.
> **Source:** interview. **Challenger:** stakeholder.

#### B3: Workspace slugs are immutable in the UI

The "rename" affordance edits workspace display name only. Slug
edits would orphan every page URL (`/w/<slug>/<page>`) and every
external bookmark to the wiki. The UI exposes no slug-edit path.

> **Rationale:** From pre-mortem A1 (admin renames workspace, breaks
> every webhook + reverse-proxy URL pinned to the old slug). The
> server side currently allows slug edits via PATCH; the UI's
> conscious choice not to surface them is the soft enforcement.
> **Quality:** 3 / 3 / 3.
> **Source:** pre-mortem. **Challenger:** technical-reality.

---

### Technical

#### T1: Svelte 5 runes; no new npm dependencies

Every new component uses `$state` / `$derived` / `$effect` / `$props`.
No new entries land in `frontend/package.json`. The existing
`UsersManager.svelte` / `KeysManager.svelte` / `WorkspaceManager.svelte`
patterns are the template.

> **Rationale:** Project ethos (CLAUDE.md "dependency-light"; matches
> silverbullet-like-live-preview T3). Adding a dialog library or
> form library would blow the bundle-size budget.
> **Quality:** 3 / 3 / 3 — `git diff frontend/package.json` must be
> empty after the change.
> **Source:** interview. **Challenger:** technical-reality.

#### T2: All admin state derives from /api/admin/* — no client-side cache

After every mutating action (suspend, revoke, approve, create,
delete, rename), the affected list refetches from the server.
Optimistic updates are not used. No `localStorage`-backed admin
state.

> **Rationale:** From the existing UsersManager + KeysManager + S5
> 60s revocation propagation contract — keeping the client cache-
> free is the simplest way to honour it. Optimistic updates would
> introduce a divergence window that could mask a server-side
> rejection.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** technical-reality.

#### T3: Bundle-size delta ≤ 30 KB gzipped

The full admin-ui slice must not push `frontend/dist/assets/index-*.js`
gzipped size more than 30 KB above the current baseline at
`scripts/.bundle-size-baseline`. Verified by `make bundle-size` in CI.

> **Rationale:** Existing CI gate from the silverbullet manifold (O2
> there). Honouring the same budget keeps the broader
> "single-binary, fast cold-load" promise intact.
> **Quality:** 3 / 3 / 3 — automated CI check.
> **Source:** interview. **Challenger:** technical-reality.
> **Threshold:** `{"kind": "deterministic", "ceiling": "30 KB gzipped"}`

---

### User Experience

#### U1: API key plaintext displayed exactly once via blocking modal with explicit acknowledgment

When the approve endpoint returns `key_plaintext`, a modal renders
that:
1. Displays the plaintext in a monospace `<input readonly>` with a
   Copy-to-Clipboard button.
2. Has a checkbox "I have copied this token; I understand it cannot
   be retrieved" that is unchecked by default.
3. Disables the "Done" / dismiss button until the checkbox is checked.
4. Suppresses the Escape-key dismiss path until the checkbox is
   checked.

The modal is the ONLY surface that ever sees the plaintext. The
admin can choose to never see it again by closing the modal — the
server has no recovery path.

> **Rationale:** Surfaces S1 (regulation-tagged "shown exactly once")
> in the UX layer. Pre-mortem A2: admin Esc-dismisses thinking it's
> a confirmation, loses the token. Explicit checkbox prevents the
> fat-finger loss. Industry convention (AWS IAM, GitHub PATs).
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** regulation.

#### U2: Every destructive action confirms via modal/dialog

Delete user, revoke key, delete workspace, suspend user (because it
revokes the user's keys server-side via S5's invalidate path) all
require an explicit "Yes, proceed" click. `window.confirm()` is
acceptable as the bare minimum; in-app modal is preferred for tone.

> **Rationale:** Existing UsersManager already uses `window.confirm`
> for delete. Codifies the pattern across the new surfaces too.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** stakeholder.

#### U3: OAuth/MCP discovery URL + JWKS URL are one-click copyable

The MCP setup panel renders the two URLs as monospace text with a
Copy-to-Clipboard button per URL. Clicking Copy provides immediate
visual feedback ("Copied!" badge that fades).

> **Rationale:** Honours U1 from the user-mgmt-mcp manifold ("paste
> 1 URL, complete OAuth flow once"). The whole point is to make this
> the easy way; if the admin has to fish the URL out of an env-var
> dump or `/.well-known/...` directly, the promise breaks.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** stakeholder.

#### U4: Loading + error states present for every async action

Every fetch call surfaces a loading affordance (button-disabled +
spinner / busy class) AND on failure surfaces a structured error
inline (not a toast that disappears). The existing pattern in
KeysManager (`{#if error}<p class="keys-error">{error}</p>{/if}`)
is the template.

> **Rationale:** Async-without-feedback is one of the top complaints
> from existing admin tools. The pattern is already in use; codify
> it as an invariant so new components follow it.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** stakeholder.

#### U5: Existing Cmd-K palette gating preserved

`CommandPalette.svelte` already gates "Manage workspaces", "Manage
users", "Manage API keys" behind `isAdmin`. New entries (e.g. "MCP
setup") follow the same gate. No new top-bar buttons; the palette
is the canonical entry.

> **Rationale:** Consistency with the established admin shell. New
> top-bar admin buttons would clutter the workspace switcher line.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** stakeholder.

---

### Security

#### S1: Plaintext API key never persisted to client storage or URL

The plaintext value lives only in JavaScript memory inside the
modal's component tree. No `localStorage`, `sessionStorage`,
`IndexedDB`, URL query string, or hash fragment ever holds it. On
modal dismiss, the bound state variable is cleared.

> **Rationale:** Surfaces S2/RT-6 (BINDING — bearer-token redaction)
> in the client. A malicious browser extension or shared-laptop user
> reading `localStorage` after the admin walks away is a real threat
> vector.
> **Quality:** 3 / 3 / 3 — Playwright + DevTools storage assertion.
> **Source:** interview. **Challenger:** regulation.

#### S2: Admin role check is server-authoritative; client check is UX only

The Cmd-K palette and component gates check `isAdmin` for hiding
non-admin entries. The actual authorisation is `withAdmin` server-
side. A non-admin who finds the modal route by URL still gets 403
from the server. The client gate is for cleanliness, not security.

> **Rationale:** Existing pattern (App.svelte computes isAdmin from
> /api/session response; CommandPalette filters). Codified as an
> invariant so a future contributor doesn't accidentally start
> trusting client-side role checks.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** technical-reality.

#### S3: Plaintext key never echoed to console, slog, or error responses

When the approve flow's modal mounts, no `console.log(token)`,
no `console.error(response)` that would dump the response body, no
slog/network-trace log. The redacted error path matches the server's
RT-6 binding.

> **Rationale:** Pre-mortem B1: a developer adds `console.log(resp)`
> for debugging and forgets to remove it; the plaintext lands in
> someone's browser console history. Codify a positive: the
> client side mirrors the server's RT-6 redaction discipline.
> **Quality:** 3 / 3 / 3 — review checklist + Playwright console
> capture in the e2e flow.
> **Source:** pre-mortem. **Challenger:** regulation.

---

### Operational

#### O1: New flows covered by extended admin/* e2e specs

`frontend/tests/e2e/admin/users-keys.spec.ts` extended (or split
into `users-keys.spec.ts` + `workspaces.spec.ts`) to cover:
plaintext modal + acknowledgment gate, approve form fields,
workspace rename, MCP setup panel copy-button feedback, S3 console-
silence assertion. Existing 78 tests must stay green.

> **Rationale:** Existing e2e harness is the regression net. Stage 1-3
> already pinned the API contracts; this slice pins the UX contracts.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** stakeholder.

#### O2: Bundle-size CI gate continues to pass

`make bundle-size` (gzipped size of `frontend/dist/assets/index-*.js`
vs. `scripts/.bundle-size-baseline`) stays under the +30 KB budget
after this slice merges. If the slice blows the budget, the slice
revisits its scope rather than rolling the baseline.

> **Rationale:** Mirrors T3 from the technical category — the same
> constraint surfaced from the operational angle (CI enforcement).
> Avoids the "death by a thousand 5-KB additions" anti-pattern.
> **Quality:** 3 / 3 / 3.
> **Source:** interview. **Challenger:** technical-reality.

---

## Tensions

### TN1: Full UI parity vs bundle budget

**Between:** B1 (full UI parity for every Stage 1-3 admin surface) × T3 (≤30 KB gzipped bundle delta).

The plaintext modal, the MCP setup panel, the workspace-rename form,
and the approve-form fields each add code weight. T3 caps the
aggregate at 30 KB gzipped — the existing CI gate (`make
bundle-size`) is the enforcement.

**TRIZ:** Cost vs Quality. Type: Technical contradiction ("more UI =
more bundle"). Principles: P1 (Segmentation — keep each modal
self-contained), P10 (Prior action — measure budget before adding
each feature, not after the slice merges).

**Challenger profile:** B1 challenger=`stakeholder` × T3
challenger=`technical-reality`. Direction: tighten the
stakeholder-tagged side's scope rather than the
technical-reality-tagged ceiling.

> **Resolution:** Reuse the existing `KeysManager` / `UsersManager` /
> `WorkspaceManager` modal patterns instead of extracting a shared
> Modal primitive (a primitive would add abstraction overhead before
> the third instance pays it back). No new npm dependencies (T1).
> No icon library, animation library, form library. Validate
> incrementally: run `make bundle-size` after each new component
> rather than only at the end. If the budget tightens, cut the
> least-essential surface first — typically the MCP setup panel
> (smallest user pain, since the URLs are findable via docs).
>
> **Validation criteria:**
> 1. `make bundle-size` shows delta < 30 KB after the slice merges.
> 2. `git diff frontend/package.json` is empty after the slice.
> 3. No shared Modal primitive extracted (each manager owns its modal).
>
> **Propagation:** T3 reaffirmed. B1 may TIGHTEN slightly if a
> sub-feature has to be cut for budget — flagged but acceptable
> because B1 is `stakeholder`-tagged. No constraint VIOLATED.
>
> **Reversibility:** TWO_WAY. Bundle additions are easily reverted.

### TN2: Plaintext visible until ack vs shoulder-surfing exposure

**Between:** U1 (plaintext visible until acknowledgment) × S1 (no
client storage / no ambient exposure of credentials).

U1 keeps the modal open — and the plaintext visible — until the
admin actively checks the acknowledgment box. That's correct against
a fat-finger Esc-dismiss, but it leaves the plaintext on screen the
whole time, exposing it to anyone behind the admin (pre-mortem C1:
admin grabs coffee, colleague reads token off the screen).

**TRIZ:** Privacy vs Usability. Type: Physical contradiction ("must
be visible to copy AND must not be visible ambient"). Principles:
P10 (Prior action — require an explicit click before reveal),
P15 (Dynamization — toggle reveal state on demand).

**Challenger profile:** U1 challenger=`regulation` × S1
challenger=`regulation`. Both immovable; resolution must satisfy
both.

> **Resolution:** Plaintext renders **blurred by default** (CSS
> `filter: blur(8px)`) when the modal mounts. An explicit "Reveal"
> button (or click on the blurred element) toggles the blur off.
> Copy-to-clipboard works through the blur — the clipboard API
> operates on the underlying string value, not the rendered DOM. On
> modal unmount, both the blur-toggle state and the bound plaintext
> string clear. Industry convention (1Password, AWS console
> credentials, GitHub PATs).
>
> **Validation criteria:**
> 1. After modal mount, the plaintext element has `filter:
>    blur(...)` applied via CSS class.
> 2. Click on the plaintext or the Reveal button toggles the blur
>    class off.
> 3. Copy-to-clipboard returns the unobscured plaintext value
>    regardless of blur state (Playwright `expect(clipboard).toBe(token)`).
> 4. After modal unmount, the bound plaintext state variable is
>    `''` and not retained anywhere in the component tree.
>
> **Propagation:** S1 LOOSENED — additional protection layer beyond
> "no storage." U1 LOOSENED — the ack-gate semantics still hold;
> blur is orthogonal. No constraint VIOLATED.
>
> **Failure cascade:** If the click-to-reveal handler fails (broken
> JS, browser extension interference), the blurred plaintext is
> still copyable via the Copy button (which is independent of
> reveal state). Worst case: admin can't read the value but can
> still copy + paste it elsewhere. Acceptable degradation.
>
> **Reversibility:** TWO_WAY. CSS class addition is trivially reverted.

### TN3: Loading + error states vs plaintext-in-error-surface leak

**Between:** U4 (loading + error states present for every async
action) × S3 (plaintext key never echoed to console / slog / error
responses).

U4 wants visual feedback on async failures (typically by surfacing
the response body or message). The approve endpoint's response body
contains the plaintext on success. If a developer adds an error-
rendering path that reads the response body unconditionally, the
plaintext can leak into the error-message DOM or
`console.error(resp)`.

**TRIZ:** Privacy vs Usability. Type: Technical contradiction
("more error info = more useful to admin = more risk of leaking
credentials"). Principles: P1 (Segmentation — separate code paths
for success vs error), P10 (Prior action — extract plaintext into
typed state before any error path can run), P24 (Intermediary — a
single helper function reads `response.json()` exactly once at
the success branch).

**Challenger profile:** U4 challenger=`stakeholder` × S3
challenger=`regulation`. Direction: tighten U4's scope around
credential-bearing endpoints; do NOT relax S3.

> **Resolution:** P1 + P10 + P24 combined: the approve handler reads
> `response.json()` exactly **once**, on the success branch
> (`response.ok`), and immediately extracts `key_plaintext` into a
> typed state variable. The error branch reads only
> `response.status` and a server-provided `error.message` string —
> never the raw response. Apply this discipline as a typed wrapper:
> a `mintApiKey()` helper that returns either `{ok: true, plaintext:
> string}` or `{ok: false, status: number, message: string}`. The
> wrapper localises the discipline so future maintainers cannot
> accidentally regress it.
>
> **Validation criteria:**
> 1. The approve handler accesses `response.json()` on at most one
>    code path (the success branch).
> 2. Error rendering renders only HTTP status + the trimmed
>    `error.message` string; never the raw response.
> 3. e2e test asserts that on a synthetic 500 from approve, the
>    rendered error message contains no `phr_live_` substring.
> 4. e2e test asserts that on a synthetic 201 with plaintext, no
>    `console.error` / `console.log` / `console.warn` invocation
>    contains the plaintext (Playwright console capture).
>
> **Propagation:** U4 TIGHTENED — error rendering for credential-
> bearing endpoints must use the structured wrapper, not raw
> response. Acceptable: U4 challenger=`stakeholder`, tightening is
> negotiable. S3 LOOSENED — explicit code-path discipline.
>
> **Reversibility:** TWO_WAY. Wrapper is trivially refactorable.

---

## Required Truths

### RT-1: Plaintext modal with blur/reveal + acknowledgment gate + on-unmount state clear

For "admin can mint a key in the UI without losing the plaintext" to
hold, a Svelte 5 component must exist that:

1. Mounts when the approve flow returns `key_plaintext` (201 Created).
2. Renders the plaintext via CSS `filter: blur(...)` until an
   explicit Reveal click (TN2 resolution).
3. Provides Copy-to-Clipboard via `navigator.clipboard.writeText()`
   that returns the unobscured value regardless of blur state.
4. Has an "I have copied this token; I understand it cannot be
   retrieved" checkbox; the dismiss button is disabled until checked.
5. Suppresses Escape-key dismissal until the checkbox is checked.
6. Clears the bound plaintext + reveal-state variables on unmount.

**Maps to:** U1, S1, TN2.
**Gap:** No such component exists. `KeysManager.svelte:99-121`'s
approve handler currently calls `await refresh()` after the response
without ever reading the body — the plaintext is unreachable.

### RT-2: `mintApiKey()` typed wrapper localising single-response-read + filtered error rendering

For "an error in the approve flow does not leak the plaintext" to
hold, the request handler must read `response.json()` exactly once,
on the success branch (`response.ok`), and immediately destructure
`key_plaintext`. The error branch reads only `response.status` and
a server-provided `error.message` string — never the raw response.
A typed return shape (`{ok: true, plaintext} | {ok: false, status,
message}`) localises the discipline so future maintainers cannot
accidentally regress it.

**Maps to:** S3, U4, TN3.
**Gap:** Current `KeysManager.svelte:99` reads the response body
inconsistently and surfaces errors via raw response. No typed
wrapper exists.

### RT-3: Approve form with scope / label / expires_at fields, client-side validated

For "approve flow respects the server-accepted parameters" to hold,
the KeysManager approve flow must collect `scope` (read | write |
admin), `label`, `expires_at` (optional ISO-8601) before calling
the approve endpoint. Client-side validation: scope is a single
allowed value; expires_at is in the future when set; label is
non-empty.

**Maps to:** B1, GAP-14.
**Gap:** Current approve flow sends an empty `POST` body. The
server accepts these fields but the UI never collects them.

### RT-4: WorkspaceManager renames display-name via PATCH; no slug-edit affordance

For "admin can rename a workspace without breaking pinned URLs"
(B3) to hold, `WorkspaceManager.svelte` must expose a rename input
on existing rows that fires `PATCH /api/admin/workspaces/{slug}`
with `{name: "..."}`. The slug field must NOT be editable in the
UI. Rename appears inline (existing row → editable → save) rather
than as a separate modal.

**Maps to:** B1, B3.
**Gap:** Current `WorkspaceManager.svelte` does create + delete
only. No rename path. Server-side PATCH already exists.

### RT-5: MCPSetupPanel surfaces discovery URL + JWKS URL with copy buttons + visual feedback

For "admin can paste-and-go an MCP client at phronesis" (the U1
promise from the user-mgmt-mcp manifold) to hold via the UI, a
panel must render the OAuth issuer + the two well-known URLs
(`<issuer>/.well-known/oauth-authorization-server` and
`<issuer>/.well-known/jwks.json`) as monospace strings each with a
Copy button. Click feedback: "Copied!" badge that fades after ~1s.
When OAuth is not configured (`PHRONESIS_OAUTH_ENABLED=0`), the
panel shows a configuration helper instead of broken URLs.

**Maps to:** U3, B1.
**Gap:** No such panel exists. Admins currently fish the URLs from
docs or env-var dumps.

### RT-6: Cmd-K palette entry "MCP setup" gated by `isAdmin`; App.svelte mounts the panel

For "the admin shell stays a single canonical entry point" to hold,
the new MCPSetupPanel mounts via a Cmd-K palette entry following the
existing pattern (`cmd:workspace.manage`, `cmd:users.manage`,
`cmd:keys.manage`). `CommandPalette.svelte` adds the entry under
the existing `isAdmin` gate; `App.svelte` adds a `mcpSetupOpen`
$state flag and routes `open-mcp-setup` to flip it.

**Maps to:** U5, S2.
**Gap:** Palette currently has three admin entries; no MCP entry.

### RT-7: Playwright e2e covers all four new UX surfaces + the console-silence assertion

For "regressions surface in CI before they ship" (O1) to hold, the
admin e2e suite must extend to cover:

- Plaintext modal: ack-gate prevents dismiss; reveal toggles blur;
  Copy returns plaintext; on dismiss the bound state clears.
- Approve form: scope / label / expires_at fields wire through;
  invalid inputs reject client-side; valid inputs reach the server.
- Workspace rename: PATCH-on-blur-or-save updates display-name;
  slug field is read-only.
- MCP setup panel: copy buttons fire `clipboard.writeText` with the
  expected URL; "Copied!" feedback appears.
- Console-silence: synthetic 500 from approve produces no
  `phr_live_` substring in DOM; synthetic 201 produces no
  `console.error` invocation containing the plaintext (Playwright
  `page.on('console')` capture).

**Maps to:** O1, S3.
**Gap:** Existing `admin/users-keys.spec.ts` covers list rendering
and basic actions. Tests for new flows and the console-silence
assertion don't exist.

### RT-8: Bundle-size delta ≤30 KB gzipped at PR merge

For "the slice does not blow the budget" (T3 / O2) to hold, the
bundle-size CI gate must pass on every commit in the PR. The
baseline at `scripts/.bundle-size-baseline` is updated only with
`UPDATE_BASELINE=1` and an explicit commit-message rationale.

**Maps to:** T3, O2.
**Gap:** None today (gate exists). Risk is in the slice itself
adding too much weight; mitigated by incremental measurement
(TN1 resolution).

### RT-9: No `frontend/package.json` diff at PR merge

For "the dependency-light promise" (T1) to hold, the slice must
not introduce new entries in `dependencies` or `devDependencies`.
Modal, dialog, clipboard, and form behaviour are hand-rolled or
browser-native (`navigator.clipboard.writeText`, native `<input>`
validation, CSS-only blur).

**Maps to:** T1.
**Gap:** None today. Enforcement is via PR review + a `git diff
--stat frontend/package.json` check at merge.

### RT-10: Plaintext key never reaches client storage or URL during the flow

For S1 to hold end-to-end, no code path between modal mount and
modal unmount writes the plaintext to `localStorage`,
`sessionStorage`, `IndexedDB`, the URL hash/query, the document
title, or any other persistent client-side surface. Verified by
Playwright assertion: after the flow, `localStorage.length`,
`sessionStorage.length`, and the `IndexedDB` databases list are
all unchanged from before the flow.

**Maps to:** S1.
**Gap:** None today (no plaintext flow exists). The risk is in the
implementation accidentally adding storage; covered by RT-7's
Playwright assertion.

---

## Binding Constraint

**RT-1: Plaintext modal with blur/reveal + acknowledgment gate.**

> **Reason:** RT-1 is the single hardest artifact in the slice — it
> binds three regulation-tagged constraints (U1, S1, S3 via the
> on-unmount clear) and the cross-cutting TN2 resolution. Every
> other RT is incremental in comparison: RT-3 is a form, RT-4 is an
> inline edit, RT-5 is two strings + buttons, RT-6 is a palette
> entry. RT-1 is where the safety semantics live; if RT-1 ships
> wrong, the whole feature is unsafe regardless of how clean the
> other surfaces look. Verification difficulty is the highest too —
> the on-unmount state-clear and the storage-leak assertions are
> structural properties that can't be eyeballed.
>
> **Dependency chain:** RT-1 → RT-2 (the wrapper feeds RT-1's
> mount) → RT-3 (the form invokes the wrapper) → RT-7 + RT-10 (the
> e2e + storage assertion validate RT-1's properties).

---

## Solution Space

### Option A: Surgical patch ← Recommended

Add the four missing pieces directly into the existing managers, no
shared primitive:

- New file `frontend/src/lib/PlaintextModal.svelte` — the binding
  artifact (RT-1). ~120 lines + scoped CSS.
- New helper `frontend/src/lib/api/mintKey.ts` — the typed wrapper
  (RT-2). ~30 lines.
- Edits to `KeysManager.svelte` — approve form (RT-3) + invoke the
  wrapper + mount the modal on success.
- Edits to `WorkspaceManager.svelte` — inline rename (RT-4).
- New file `frontend/src/lib/MCPSetupPanel.svelte` (RT-5).
- Edits to `CommandPalette.svelte` + `App.svelte` for routing (RT-6).
- Extends `admin/users-keys.spec.ts` + new `admin/mcp-setup.spec.ts`
  + assertions in existing `admin/workspaces.spec.ts` if it exists,
  else new (RT-7).

**Satisfies:** all 10 RTs.
**Complexity:** Medium-low. Each component is contained.
**Estimated bundle delta:** ~10-15 KB gzipped (well under T3).
**Reversibility:** TWO_WAY. Each addition is independently
revertable.
**Tension validation:** TN1 CONFIRMED (no shared primitive). TN2
CONFIRMED (blur + reveal). TN3 CONFIRMED (typed wrapper).

### Option B: Extract shared Modal primitive first, then add features

Refactor the four existing manager modals (Users / Keys /
Workspaces — three) to share a `<Modal>` Svelte component.
Build the new features atop the primitive.

**Satisfies:** all 10 RTs (eventually).
**Complexity:** Higher. Refactor + feature work bundled; the slice
ships only when both halves are done.
**Estimated bundle delta:** Possibly net-negative (deduplication)
but introduces refactor risk on already-shipping admin flows.
**Reversibility:** REVERSIBLE_WITH_COST. Reverting the primitive
extraction means re-duplicating; not free.
**Tension validation:** TN1 REOPENED — the m2 resolution explicitly
chose "no shared primitive" because abstraction overhead at 3
instances is higher than copy-paste-modify. Choosing this option
contradicts that resolution.

### Option C: Move admin to a separate /admin SPA route

Decompose admin into its own routing layer with a global admin
state store (Svelte 5 `$state` exported from a module).

**Satisfies:** the 10 RTs but adds substantial off-spec work.
**Complexity:** Highest. Architectural shift on a feature that
already works.
**Estimated bundle delta:** Negative on individual loads (lazy
chunk) but positive on initial bundle (router config + state
plumbing). Likely violates T3.
**Reversibility:** ONE_WAY. Routing decisions are sticky once they
ship.
**Tension validation:** TN1 LIKELY VIOLATED (bundle delta), TN3
LIKELY VIOLATED (global state introduces a new leak surface for
the plaintext). DO NOT PURSUE.

---

## Tension Validation (Option A)

- **TN1** (B1 × T3 — bundle vs parity): **CONFIRMED**. Option A
  reuses existing patterns and adds ~10-15 KB; well under the 30 KB
  ceiling.
- **TN2** (U1 × S1 — visible plaintext vs shoulder-surfing):
  **CONFIRMED**. RT-1 codifies the blur + reveal toggle.
- **TN3** (U4 × S3 — error states vs plaintext leak): **CONFIRMED**.
  RT-2 codifies the typed wrapper with single-response-read.

Zero tensions REOPENED under Option A. Zero `regulation`-tagged
constraints traded off.

---

## Recommendation: Option A.
