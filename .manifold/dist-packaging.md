# dist-packaging

## Outcome

Distribution packaging and a Makefile for local development.

Expanded intent: provide a reproducible build / test / lint / release workflow for the phronesis project. Locally a developer should be able to build the binary, run the full Go test suite, build the Svelte frontend, lint + vet, and produce distributable artifacts for common platforms with a small, memorable set of Make targets. The output is developer-facing tooling, not a new product feature — it sits alongside `go.mod`, `cmd/phronesis`, and `frontend/` and makes them easier to operate as one unit.

Scope locked during m1:
- Binary matrix: `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64` (no Windows in v1).
- Channels: GitHub Releases + Docker multi-arch (ghcr.io) + Homebrew tap (github.com/dhanesh/homebrew-phronesis) + Linux `.deb` / `.rpm` packages.
- Release model: SemVer tags → GitHub Actions → all channels.
- Reproducibility: deterministic flags (`-trimpath`, fixed `-ldflags`, pinned Go toolchain) — not fully hermetic.
- Frontend: embedded in the binary via `//go:embed frontend/dist`.

---

## Constraints

### Business

#### B1: Ship to all four distribution channels per release

Every SemVer release publishes to: (1) GitHub Releases (tarballs + checksums), (2) Docker multi-arch image on `ghcr.io`, (3) Homebrew tap at `github.com/dhanesh/homebrew-phronesis`, (4) `.deb` and `.rpm` packages.

> **Rationale:** Each channel has a distinct consumer (curl-users, containerized deploys, Mac dev laptops, Linux server operators). Dropping any channel would regress the distribution surface users can rely on.
> **Type:** goal (not invariant) — a specific channel can be deferred for a given release if m2 surfaces a cost-heavy tension; scope is negotiable with stakeholder approval.
> **Quality:** 3 / 2 / 2 — "per release" is measurable via CI outputs but "identical content across channels" is testable only by downloading and hashing.

#### B2: SemVer tagging is the only release trigger

Releases fire on git tags matching `v[MAJOR].[MINOR].[PATCH]`. No rolling-main or date-based releases. The tag drives the version string injected into the binary.

> **Rationale:** Locks consumer expectation — `brew install phronesis@0.2.0` points at a specific immutable artifact set. Changing this later (e.g., moving to CalVer) is a breaking contract change for downstream consumers, so it's INVARIANT from v1.
> **Quality:** 3 / 3 / 3.

---

### Technical

#### T1: Release produces 4 binaries — darwin × linux × amd64 × arm64

Every release must produce binaries for: `darwin-arm64` (Apple Silicon), `darwin-amd64` (Intel Mac), `linux-amd64` (standard x86 servers), `linux-arm64` (Graviton, Pi). Release fails if any of the 4 fail to build.

> **Rationale:** Explicitly scoped during m1. Windows deliberately excluded for v1 (can add later).
> **Threshold:** deterministic — exactly 4 binaries per release.
> **Quality:** 3 / 3 / 3.

#### T2: Builds use deterministic flags

All binary builds use `-trimpath`, fixed `-ldflags` (no wall-clock in build-time-only fields), and the Go toolchain pinned via `go.mod`'s `toolchain` directive. Same source + same toolchain must produce byte-identical output.

> **Rationale:** Cheap supply-chain hardening. Users can verify their downloaded binary matches a locally-rebuilt one. Opens the door to future cosign/SLSA attestation without re-architecting.
> **Challenger:** technical-reality — Go's deterministic-build story is well-understood; this is a known primitive.
> **Quality:** 3 / 2 / 2 — testable by building twice on one machine and diffing.

#### T3: Frontend is embedded in the binary via //go:embed

`make build` runs `vite build` to populate `frontend/dist/`, then the Go build embeds it via the existing `//go:embed web/*` pattern extended to `//go:embed frontend/dist`. No runtime `PHRONESIS_FRONTEND_DIST` override is required on the release path.

> **Rationale:** Eliminates "wrong asset version" runtime failures. Single-binary deploy is the simplest operator story. Binary size increase (~500KB gzipped) is acceptable.
> **Quality:** 3 / 3 / 3.

#### T4: Frontend dist is a declared make prerequisite

Any target that embeds frontend assets MUST declare `frontend/dist/index.html` (or equivalent) as a prerequisite in Make's dependency graph — never rely on the developer to "remember to rebuild first".

> **Rationale:** Pre-mortem #1C (avoidable): stale embed. Make's prerequisite model handles this cleanly if declared correctly; relying on human memory fails eventually.
> **Type:** boundary (structural requirement on every binary target).
> **Quality:** 3 / 3 / 3.

#### T5: `make test` completes in under 30 seconds on a fresh clone

`make test` = Go test suite across 13 internal packages. No frontend build, no cross-compile, no Docker operations. Measured as p95 over a fresh-clone first run on modern laptop hardware.

> **Rationale:** Tight feedback loop is the difference between "I run tests constantly" and "I run tests when I remember". Scope explicitly excludes Docker/cross-compile to keep the budget achievable.
> **Threshold:** statistical, p95 ≤ 30s.
> **Quality:** 3 / 3 / 3.

#### T6: Lint tool versions are pinned in-repo

`staticcheck`, `gofmt`, and any other lint-adjacent tools run by `make lint` must be versioned in `tools.go` (or equivalent mechanism) so developers and CI use the same versions. `go vet` is part of the standard toolchain and ships with Go.

> **Rationale:** Pre-mortem #2D (assumption violation): unpinned linters drift between machines and hide/surface different findings. Pinning removes this class of surprise.
> **Quality:** 3 / 3 / 3.

#### T7: Release tooling versions are pinned

`goreleaser` (or whatever drives the release) must be pinned to an exact version, installable via `make tools` or an equivalent bootstrap step. CI must not use `@latest`. The release pipeline runs `goreleaser check` before any artifacts are built — this validates the `.goreleaser.yaml` schema against the pinned version and fails fast on config drift.

> **Rationale:** Pre-mortem #3C (external): a goreleaser minor-version bump silently breaks config parsing mid-release. Pinning prevents the version drift; `goreleaser check` catches schema errors at the start of the pipeline rather than halfway through a cross-compile.
> **Source:** WebSearch amendment (iter-2) — goreleaser docs explicitly recommend `goreleaser check` as the pre-commit / pre-CI validator.
> **Quality:** 3 / 3 / 3.

---

### User Experience

#### U1: Seven core make targets — build, test, lint, fmt, clean, release, help

The Makefile exposes exactly these 7 first-class targets. Platform-specific build targets (`build-darwin-arm64` etc.) may exist as internal dependencies of `release` but are not listed in `make help`. `release` composes lint + test + cross-compile + packaging + channel publish (or a subset if partial-release mode).

> **Rationale:** Target surface stays memorizable. New developers discover what's available via `make help` and don't face a 30-target wall.
> **Quality:** 3 / 3 / 2 — "first-class" is measurable via help output but "memorizable" is subjective.

#### U2: `make help` auto-generates from target docstrings

`make help` output is built from in-Makefile comments (e.g., `target: ## description`) at runtime, not a hand-maintained help block. Prevents drift between actual targets and documented targets.

> **Rationale:** Every Makefile that maintains a hand-written help block eventually lies. Auto-extraction is a well-known pattern (`grep -E '^[a-zA-Z_-]+:.*?## '` one-liner).
> **Type:** goal (nice-to-have; `make help` without auto-extract still works).
> **Quality:** 3 / 3 / 3.

---

### Security

#### S1: Every release publishes SHA256SUMS alongside artifacts

A `SHA256SUMS` file covering every artifact in the release (tarballs, zips, `.deb`, `.rpm`) is published to GitHub Releases. Consumers can verify with `sha256sum -c SHA256SUMS`.

> **Rationale:** Baseline integrity. Doesn't prove provenance (SHA256SUMS itself isn't signed) but detects corruption and simple tampering.
> **Planned v2 upgrade (not v1 scope):** Add `cosign` keyless signing via GitHub Actions OIDC. The `id-token: write` permission in the release workflow lets Fulcio issue a short-lived cert tied to the workflow identity; consumers verify with `cosign verify-blob --certificate-identity <workflow> --certificate-oidc-issuer https://token.actions.githubusercontent.com`. Critical rule when that lands: **sign the digest, not the tag** — tags are mutable and can re-point at different content. This also unlocks the SLSA L3 "verified history" posture mentioned by sigstore docs. Pre-mortem #3A (OIDC drift) becomes the dominant risk once keyless signing is the primary verification path; not currently a concern with SHA256SUMS alone.
> **Source:** WebSearch amendment (iter-2) — sigstore/cosign docs + GitHub Blog's container-signing guide.
> **Quality:** 3 / 3 / 3.

#### S2: Homebrew formula DOWNLOADS the release tarball; does not rebuild

The `Formula/phronesis.rb` in `homebrew-phronesis` references the release tarball's URL + SHA256 and installs by extracting it. It does NOT `system "go", "build"` from source.

> **Rationale:** Pre-mortem #2A (the surprise you picked): rebuilding via Homebrew's Go toolchain produces a different binary than the goreleaser-built tarball — different linker, different stdlib, different reproducibility guarantee. Single source of truth prevents user-visible behavior drift between channels.
> **Quality:** 3 / 3 / 3.

#### S3: No secrets in tarballs, Makefile, or committed config

Release signing keys, registry tokens, and any other credentials must live in CI secret storage only. Tarballs must not contain `.env`, credentials, or any file matching the standard secrets-leaked patterns. Grep-based pre-release check enforces.

> **Rationale:** Defense in depth. Challenger: regulation — this is a hard floor regardless of stakeholder preference.
> **Quality:** 3 / 2 / 3 — "matches standard patterns" measurable via `gitleaks` or similar.

#### S4: Release stages are independent — one channel outage must not block others

The GitHub Release stage publishes FIRST. Docker, Homebrew, and Linux package stages run AFTER and AT MOST wait on each other; a failure in Docker publish does not roll back the GitHub Release. Each stage has its own retry envelope.

> **Rationale:** Pre-mortem #3D (external): ghcr.io outage would otherwise block GitHub Release availability. Users grabbing `curl | tar` don't care about Docker; making them wait for Docker is wrong. Challenger: stakeholder — you could argue the opposite ("atomic release or nothing") but the m1 preference is independent stages.
> **Type:** boundary — the constraint is on release ORDERING, not on success guarantees for every channel.
> **Quality:** 3 / 3 / 2 — orderedness is testable; "independence" at retry-envelope level is a design choice to verify by inspection.

#### S5: Homebrew formula passes `brew audit --strict --new` against current API

The formula in `homebrew-phronesis` must pass `brew audit --strict --new` against the current stable Homebrew release in CI. Uses only non-deprecated DSL entries — specifically does not call `needs`, `conflicts_with formula:`, or any member flagged by `odeprecated` at the current Homebrew version. The audit runs as a pipeline gate before the formula update PR is created.

> **Rationale:** Pre-mortem #3B (external) — Homebrew changes formula API, a user's `brew install` starts failing. WebSearch surfaced that Homebrew 5.0.0 (Nov 2025) formally deprecated `Formula#needs` and `conflicts_with formula:` via the `odeprecated` mechanism, and introduced `compatibility_version` + `no_linkage` DSL. A formula using the old DSL will produce audit warnings now and hard failures in a later release. This constraint makes the audit the gate that catches Homebrew's deprecation cycle before users hit it.
> **Source:** WebSearch amendment (iter-2) — brew.sh/2025/11/12/homebrew-5.0.0 + docs.brew.sh/Deprecating-Disabling-and-Removing.
> **Type:** invariant — the audit is not optional for a published formula.
> **Challenger:** technical-reality — we don't control Homebrew's deprecation schedule.
> **Quality:** 3 / 3 / 3 — `brew audit --strict --new` is a binary pass/fail check.

---

### Operational

#### O1: `make release` runs from GitHub Actions only

Release builds execute on GitHub Actions runners (never on a developer laptop), driven by a `.github/workflows/release.yml` that invokes `make release` after the SemVer tag. Developer laptops can run `make build` / `make test` / `make lint` freely but must not push release artifacts.

> **Rationale:** Reproducibility (T2) + secret hygiene (S3) + version consistency depend on a known execution environment. Laptop releases are a supply-chain risk and an operator headache.
> **Type:** boundary — running locally isn't forbidden for debugging, but `make release` guards push/publish behind an env check.
> **Quality:** 3 / 2 / 2.

#### O2: Every release smoke-tests each target platform before publish

For each of the 4 platforms, CI runs `phronesis --version` (and ideally `phronesis --help`) on an appropriate runner/emulator. If any smoke test fails, the entire release fails — no artifacts published for any platform.

> **Rationale:** Pre-mortem #1A (obvious): shipping a broken binary for a platform you don't use locally. Smoke test is cheap (milliseconds) and catches the 80% of cross-compile failures (linker errors, missing syscalls on a platform).
> **Quality:** 3 / 3 / 3.

#### O3: Binary embeds version, git SHA, build timestamp, and Go toolchain via -ldflags

Four pieces of metadata flow into the binary via `-ldflags -X`:
- `main.version` = git tag (e.g., `v0.2.0`) or `dev-<short-sha>` for non-tagged builds
- `main.commit` = full git commit SHA
- `main.buildTime` = **commit time** (SOURCE_DATE_EPOCH), NOT wall-clock build time (preserves T2 reproducibility)
- `main.goVersion` = `runtime.Version()` at runtime (no build-time cost)

Exposed via `phronesis --version` flag.

> **Rationale:** Operators diagnosing a report need to know which exact binary is running. The commit-time-not-wall-clock choice resolves the T2-vs-build-metadata tension cleanly (see DRT-2).
> **Quality:** 3 / 3 / 3.

#### O4: Release fails atomically per platform on smoke-test failure

If the linux-arm64 smoke test fails, no linux-arm64 artifact is published AND the overall release is marked failed. Partial per-channel success for OTHER platforms is OK (S4), but within a single platform there's no "publish the tarball, retry the Docker image later" path — the platform either builds + smokes cleanly or it's dropped from the release.

> **Rationale:** Pre-mortem #1A extension + interaction with S4. S4 is channel-level independence; O4 is platform-level atomicity per channel.
> **Quality:** 3 / 3 / 3.

#### O5: End-to-end release completes in ≤10 min on GitHub Actions

From `git push --tags` to all channels published (or platform-drop decided), wall-clock p95 must be under 10 minutes on standard GitHub-hosted runners for the full 4-platform × 4-channel matrix.

> **Rationale:** Added by TN4 resolution. B1 (ship 4 channels) implicitly competed with T5 (30s test budget) because users read "budget" as "all work fast." Splitting the budgets explicitly — T5 governs `make test`; O5 governs `make release` on CI — removes the ambiguity. 10 min accommodates cross-compile + Docker buildx + 4 native smoke runners in parallel. Exceeding 10 min is a signal to parallelize further, not to drop scope.
> **Source:** pre-mortem (derived during m2-tension).
> **Threshold:** statistical — p95 ≤ 10 min measured across the trailing 10 tagged releases.
> **Quality:** 3 / 3 / 3.

---

## Tensions

### TN1: Deterministic builds vs embedded build-time metadata

**Between:** T2 (deterministic flags) × O3 (build metadata in `--version`).

T2 demands byte-identical output across machines. O3 wants build-time embedded in the binary via `-ldflags -X main.buildTime`. Wall-clock time breaks T2 the instant two machines build the same tag.

**TRIZ:** Physical contradiction — `buildTime` must be present (for O3) and must not vary (for T2). Parameter pair: Reliability vs Information. Principles: **P10 Prior action** (compute deterministic value *before* build), **P35 Parameter changes** (change what "build time" means).

> **Resolution:** (Option A) Derive `buildTime` from `SOURCE_DATE_EPOCH` set to `git log -1 --format=%ct <tag>` — commit time, not wall-clock. Two machines building the same tag get the same timestamp. Formalized as DRT-2.

**Propagation check:**
- T2 (deterministic): LOOSENED — removes the only timestamp source of non-determinism.
- O3 (build metadata): TIGHTENED slightly — `buildTime` now means "commit time," which operators must understand.
- T6 (pinned Go): UNCHANGED — Go version still needs pinning for determinism.

**Verdict:** SAFE.

**Validation criteria:**
- `go build` twice on different hosts with the same tag → SHA256 of binary matches.
- `phronesis --version` prints the commit timestamp, not the build-host time.

---

### TN2: Ship-all-4-channels atomically vs channel-pipeline independence

**Between:** B1 (ship all 4 channels per release) × S4 (pipelines must be independent — ghcr outage doesn't block GH Release).

Literal reading of B1 implies "release fails if any channel fails." Literal reading of S4 says "channels are independent stages — partial success is OK." The two can't both be true without a tie-breaker.

**TRIZ:** Technical contradiction — more atomicity → less resilience to 3rd-party outages. Parameter pair: Reliability vs Availability. Principles: **P1 Segmentation** (split "release" into phases), **P13 The other way round** (invert — the GitHub Release IS the release; other channels mirror).

> **Resolution:** (Option A) GitHub Release is the *atomic root* — it must succeed or the release is considered failed. Docker / Homebrew / Linux packages are *best-effort mirrors* that can retry independently. SemVer tag points to the GH Release artifacts; other channels are verified-but-non-blocking. Formalized as DRT-3 refinement.

**Propagation check:**
- B1 (4 channels): TIGHTENED wording — "ship to all 4" now means "publish GH + best-effort the other 3 with alerting on partial failure."
- S4 (independent pipelines): SATISFIED as stated — each downstream has its own retry.
- O2 (smoke test per platform): UNCHANGED — smoke still gates the GH artifacts.

**Verdict:** SAFE. B1 re-scoped, not violated. Stakeholder (challenger: stakeholder on B1) must acknowledge the re-scope explicitly during m3.

**Validation criteria:**
- Simulated ghcr.io 503 → GitHub Release still publishes; Docker stage retries; monitoring alerts.
- Simulated GH Release API failure → entire release marked failed; no downstream mirrors attempt.

**Failure cascade:**
- Q: "What if Docker retry also fails?" → A: Alert + manual rerun of `release-channel` workflow. Accept-loss only after 3 automatic retries.
- Q: "What if GH Release API is down for >10 min?" → A: Release delayed; SemVer tag stays; no partial publish. Human intervention.

---

### TN3: Tool-version pinning vs minimal Makefile surface

**Between:** T6 / T7 (pin staticcheck + goreleaser versions reproducibly) × U1 (≤7 core Make targets).

Reproducible linting needs a pinned staticcheck; reproducible releases need a pinned goreleaser. These tools have historically been installed via `go install <module>@<version>`, which is non-reproducible by default (resolves `@latest` if tag is a float). A `make tools` target fixes this — but it's an 8th target.

**TRIZ:** Technical contradiction — more pinned tools → more Make surface. Parameter pair: Reliability vs Simplicity. Principles: **P25 Self-service** (Go 1.24 can host the tool list itself), **P2 Extraction** (extract tool mgmt out of Makefile).

> **Resolution:** (Option A) Use Go 1.24's `tool` directive in `go.mod` (Feb 2025 feature). `go tool staticcheck`, `go tool goreleaser` — no separate install step, version tracked in `go.mod` + `go.sum`. No `make tools` target needed. DRT-5 re-stated: "Tool versions pinned in `go.mod` tool directive; invokable via `go tool <name>`."

**Propagation check:**
- T6 (pinned versions): SATISFIED via Go 1.24 `tool` directive.
- T7 (reproducible release tooling): SATISFIED — goreleaser version is in `go.sum`.
- U1 (≤7 targets): SATISFIED — no new target added.
- T4 (frontend build order): UNCHANGED.

**Verdict:** SAFE. Requires Go 1.24+ as an explicit floor — already assumed (project is on 1.24.5 per `go.mod`).

**Validation criteria:**
- `go mod graph | grep staticcheck` returns a pinned version.
- `make lint` works on a fresh clone with zero manual tool install.
- `go tool goreleaser --version` matches the version in `go.mod`.

---

### TN4: 4-channel breadth vs global time budget

**Between:** B1 (ship 4 channels) × T5 (30s p95 test budget on fresh clone).

Stakeholder expectation was that "the workflow is fast." T5 codifies that as the test-suite budget, but the ambiguity is whether `make release` also has a time ceiling — and if so, 30s is impossible with cross-compile + Docker buildx + Homebrew formula updates + package signing.

**TRIZ:** Resource tension — no budget for release wall-clock. Parameter pair: Productivity vs Reliability (release scope). Principles: **P1 Segmentation** (split budget per activity), **P17 Another dimension** (different time regime for CI vs local dev).

> **Resolution:** (Option A) Split the budget explicitly. T5 governs `make test` (unit tests, local loop, 30s p95 on fresh clone). New **O5** governs `make release` on GitHub Actions (p95 ≤ 10 min for the full 4×4 matrix). Local release is not time-budgeted; it's a CI-first workflow.

**Propagation check:**
- B1 (4 channels): UNCHANGED — release now has its own 10-min budget to fit in.
- T5 (30s tests): UNCHANGED — scope clarified to `make test` only, not `make release`.
- O2 (smoke per platform): TIGHTENED — must fit into 10-min O5 budget; native-parallel runners (TN6) enable this.

**Verdict:** SAFE. New constraint O5 added to operational category.

**Validation criteria:**
- Measured wall-clock of last 10 tagged releases: p95 ≤ 10 min.
- `make test` p95 on fresh clone ≤ 30s (separate measurement, unchanged from T5).

---

### TN5: Channel matrix vs Makefile surface

**Between:** U1 (≤7 core Make targets) × B1 (4 channels × build/publish verbs).

Naive interpretation needs 4 build targets + 4 publish targets for channels — explodes past 7 even before `help`, `test`, `lint`, `clean`.

**TRIZ:** Technical contradiction — more channel explicitness → more targets. Parameter pair: Flexibility vs Simplicity. Principles: **P1 Segmentation** (split channel from target), **P15 Dynamization** (target behavior varies by parameter).

> **Resolution:** (Option A) Keep 7 user-facing targets: `help`, `build`, `test`, `lint`, `release`, `docker`, `clean`. Channel selection is a runtime parameter: `make release CHANNEL=homebrew` or (default) `make release` for all. `goreleaser` consumes one config and branches internally; the Makefile is a thin wrapper.

**Propagation check:**
- U1 (≤7 targets): SATISFIED.
- B1 (4 channels): UNCHANGED — all 4 still fire from `make release`.
- U2 (help auto-extracted): UNCHANGED.
- T7 (goreleaser pinned): TIGHTENED — goreleaser is now the single source of truth for channel logic; config must cover all 4 channels in one file.

**Verdict:** SAFE.

**Validation criteria:**
- `make help` prints exactly 7 target lines.
- `make release CHANNEL=homebrew` publishes only to Homebrew; `make release` publishes to all 4.
- `goreleaser check` passes on the config that covers all channels.

---

### TN6: Smoke-test all platforms vs native execution environment

**Between:** O2 (smoke-test each platform binary in CI) × T1 (4-platform cross-compile matrix).

Cross-compiled binaries from a linux-amd64 runner cannot execute natively on darwin-*/linux-arm64. Without a real runtime check, "it builds" is not the same as "it runs."

**TRIZ:** Resource tension — each platform needs its own executor. Parameter pair: Quality of verification vs Cost. Principles: **P1 Segmentation** (per-platform CI lane), **P24 Intermediary** (QEMU as alternative).

> **Resolution:** (Option A) GitHub Actions matrix with native runners per platform: `macos-14` (darwin-arm64), `macos-13` (darwin-amd64), `ubuntu-24.04` (linux-amd64), `ubuntu-24.04-arm` (linux-arm64, GA Dec 2024). Each runner executes `phronesis --version` and any platform-specific smoke assertion. QEMU rejected because Go's runtime scheduler has documented flakiness under binfmt_misc emulation.

**Propagation check:**
- O2 (smoke test): SATISFIED — real execution on real silicon.
- T1 (4-platform build): UNCHANGED.
- O5 (10-min release budget): TIGHTENED — 4 parallel runners + coordination must fit in 10 min; verified by measurement.
- B1 (GHA runner cost): slight cost bump for macos-* runners; accepted as cost of correctness.

**Verdict:** SAFE.

**Validation criteria:**
- CI matrix shows 4 native smoke jobs per release.
- Smoke failure on any one platform → that platform is dropped per O4; others proceed.
- `actions/runner-images` label verified in workflow (no silent drift to QEMU).

**Failure cascade:**
- Q: "What if `ubuntu-24.04-arm` is unavailable?" → A: Fall back to QEMU smoke with explicit warning annotation; O2 is considered *partially satisfied* for that release. Human flag.

---

### TN7: Embedded frontend vs fast backend tests

**Between:** T3 (`//go:embed frontend/dist`) + T4 (frontend built before Go compile) × T5 (`make test` ≤ 30s).

`//go:embed` requires `frontend/dist/*` at compile time. Without it, `go build` and `go test` both fail on `pattern frontend/dist: no matching files found`. Requiring `npm run build` before every `go test` busts T5 and breaks the "backend tests run independently" dev experience.

**TRIZ:** Technical contradiction — more embed discipline → slower tests. Parameter pair: Integration vs Dev loop speed. Principles: **P1 Segmentation** (compile-time branch), **P15 Dynamization** (behavior varies by build config), **P40 Composite** (two artifacts: dev + prod).

> **Resolution:** (Option A) Build-tag gate the embed. `internal/webfs/embed_prod.go` with `//go:build prod` hosts the `//go:embed` directive; `internal/webfs/embed_dev.go` with `//go:build !prod` returns a stub (empty FS or dev-proxy). Production binary built with `go build -tags=prod`; tests and dev builds use the stub. `make build` adds `-tags=prod` automatically.

**Propagation check:**
- T3 (embed frontend): SATISFIED in production build.
- T4 (frontend before Go compile): TIGHTENED — only required for `-tags=prod` builds.
- T5 (30s tests): SATISFIED — tests skip frontend entirely.
- U1 (7 targets): UNCHANGED — tag is internal to `make build`.
- T6 / T7 (reproducibility): UNCHANGED — prod tag is deterministic.

**Verdict:** SAFE.

**Validation criteria:**
- `go test ./...` passes with no `frontend/dist` directory.
- `go build -tags=prod ./cmd/phronesis` fails cleanly if `frontend/dist/` is empty (no silent empty binary).
- `phronesis` built without `-tags=prod` serves the stub and logs a loud warning at startup.
- `make build` wraps `go build -tags=prod` so developers can't accidentally ship a stub binary.

---

## Required Truths

Derived by backward reasoning from the outcome: "reproducible build/test/lint/release workflow, 4-platform × 4-channel distribution, minimal Make surface."

### RT-1: Each platform binary executes a smoke-test before publication

`phronesis --version` must run without error on real `darwin-arm64`, `darwin-amd64`, `linux-amd64`, and `linux-arm64` hardware before any channel publishes the corresponding artifact.

**Maps to:** T1, O2, O4.
**Gap:** No CI workflow exists; no smoke-test step defined.
**Resolved by:** TN6 (native GHA runner matrix).

### RT-2: Build timestamp in `--version` derives from git commit time

`main.buildTime` embedded via `-ldflags -X` uses `SOURCE_DATE_EPOCH = git log -1 --format=%ct <tag>`, never wall-clock. Two builds of the same tag on different hosts produce byte-identical binaries.

**Maps to:** T2, O3.
**Gap:** No `-ldflags` injection wired yet.
**Resolved by:** TN1 (SOURCE_DATE_EPOCH from commit time).

### RT-3: Release pipeline stages are independent; GitHub Release is the atomic root

GitHub Release publication must succeed for a release to count as shipped. Docker / Homebrew / Linux-packages run as best-effort downstream stages with their own retry + alerting, isolated from each other.

**Maps to:** B1, S4, O4.
**Gap:** No pipeline exists.
**Resolved by:** TN2 (GH atomic root, others mirrors).

### RT-4: Homebrew formula downloads the release tarball and verifies SHA256

`brew install phronesis` must not rebuild from source with Homebrew's toolchain. It downloads the `.tar.gz` published on GitHub Releases, verifies the `SHA256SUMS` entry, and installs the pre-built binary.

**Maps to:** S2, B1 (Homebrew channel).
**Gap:** No Homebrew tap exists; no formula template defined.
**Resolved by:** goreleaser `brews` module (emits formula automatically).

### RT-5: Release tool versions are pinned via Go 1.24 `tool` directive in `go.mod`

`staticcheck`, `goreleaser`, and any other lint/release tooling are declared in `go.mod`'s `tool` block (Feb 2025 feature). Invokable via `go tool <name>` with no separate install step. Version tracked in `go.sum`.

**Maps to:** T6, T7.
**Gap:** `go.mod` currently has no `tool` directive; tools not pinned.
**Resolved by:** TN3 (Go 1.24 tool directive).

### RT-6: `make help` auto-extracts target descriptions from in-Makefile docstrings

`make help` parses the Makefile itself — targets followed by `## description` comments become help lines. No hand-maintained help list that can drift from actual targets.

**Maps to:** U1, U2.
**Gap:** No Makefile exists.
**Resolved by:** Standard `awk` pattern on `/^[a-zA-Z_-]+:.*## /`.

### RT-7: End-to-end release wall-clock completes in ≤10 min p95 on GitHub Actions

From `git push --tags` to all-channels-published (or platform-drop decided), p95 measured across trailing 10 tagged releases is ≤10 min. Breaching this signals the need to parallelize further, not to drop scope.

**Maps to:** O5, B1, O2 (via TN6 runner count).
**Gap:** No release pipeline to measure yet.
**Resolved by:** TN4 + TN6 (split budget + parallel native runners).

### RT-8: `make release` with no args ships all 4 channels; `CHANNEL=<name>` scopes to one

Make surface stays at 7 targets. Channel selection is a runtime env var: `make release` triggers all four, `make release CHANNEL=homebrew` triggers only the Homebrew path. `goreleaser` consumes one config and branches on the env.

**Maps to:** U1, B1.
**Gap:** No Makefile or goreleaser config.
**Resolved by:** TN5 (CHANNEL= env parametrization).

### RT-9: Embedded frontend is gated by `-tags=prod` build tag

`//go:embed frontend/dist` lives in `internal/webfs/embed_prod.go` with `//go:build prod`. A sibling `embed_dev.go` with `//go:build !prod` exports a stub FS. Tests run with the stub (no frontend build needed); production `make build` adds `-tags=prod`. Binaries built without the tag emit a loud startup warning.

**Maps to:** T3, T4, T5.
**Gap:** No `internal/webfs/` package; `go test ./...` currently works because nothing imports `//go:embed` of `frontend/dist` yet.
**Resolved by:** TN7 (build-tag gated embed).

### RT-10: Homebrew formula passes `brew audit --strict --new` against v5.0+ API

CI step runs `brew audit` on the emitted formula before pushing to the tap. Catches deprecated DSL (`needs`, `conflicts_with formula:`) per Homebrew 5.0.0 (Nov 2025).

**Maps to:** S5.
**Gap:** No formula emission or audit step.
**Resolved by:** CI audit step in GH Actions workflow (post-goreleaser, pre-push-to-tap).

### RT-11: CI matrix pins native runner labels per platform

GitHub Actions workflow pins: `macos-14` (darwin-arm64), `macos-13` (darwin-amd64), `ubuntu-24.04` (linux-amd64), `ubuntu-24.04-arm` (linux-arm64, GA Dec 2024). No QEMU. No `runs-on: latest`.

**Maps to:** O1, O2, T1.
**Gap:** No workflow file exists.
**Resolved by:** TN6 (explicit native runner pins).

### RT-12: Every release publishes `SHA256SUMS` alongside artifacts

`SHA256SUMS` is a plain-text file listing `<sha256>  <filename>` for each tarball/package in the release. Consumers run `shasum -a 256 -c SHA256SUMS` to verify integrity.

**Maps to:** S1.
**Gap:** No release pipeline. (v2 adds `SHA256SUMS.sig` via cosign keyless — see S1 rationale; not in v1 scope.)
**Resolved by:** goreleaser `checksum` block.

### RT-13: Release workflow triggers only on `v[MAJOR].[MINOR].[PATCH]` tag pushes

GH Actions workflow filter: `on: push: tags: ['v*.*.*']`. No rolling-main, no manual trigger, no date-based. The tag is the version.

**Maps to:** B2.
**Gap:** No workflow.
**Resolved by:** GH Actions `on` filter.

### RT-14: Release workflow uses OIDC for publish auth; no long-lived secrets

`ghcr.io` push uses GitHub Actions's built-in `GITHUB_TOKEN`. Homebrew tap push uses a fine-scoped deploy key stored as an encrypted secret. No AWS/PyPI-style long-lived PATs. Future cosign signing (S1 v2 path) uses keyless OIDC — no private keys at rest.

**Maps to:** S3.
**Gap:** Secrets plan undefined; tap deploy key not provisioned.
**Resolved by:** Spec + GH Actions OIDC configuration.

---

## Binding Constraint

**RT-9 (Embedded frontend gated by `-tags=prod`).** This is the load-bearing prerequisite: until the stub/prod split is in place, `go test ./...` cannot be run reliably on a fresh clone, which blocks verification of every other RT. Every CI step, every release gate, every evidence check downstream depends on a working test loop — so RT-9 must close before anything else can be trusted.

**Dependency chain:** RT-9 unlocks → RT-5 (tool pinning, so `go tool staticcheck` works) → RT-6 (make help) + RT-8 (make targets) → RT-11 (CI matrix) → RT-1 (smoke) + RT-13 (tag trigger) → RT-3 (pipeline stages) → RT-4, RT-10, RT-12 (channel publishing + audit + checksums) → RT-2 + RT-7 + RT-14 (measurement + auth concerns finalized).

m4 should generate RT-9 artifacts first.

---

## Solution Space

All three options satisfy all 14 required truths at completion. They differ in sequencing — what lands in which pull request — which governs revert blast radius and time-to-first-value.

### Option A: Single-PR "kitchen sink" [TWO_WAY]
One commit: Makefile + build tags + goreleaser + GH Actions + Homebrew tap bootstrap + audit step.

- **Satisfies:** All 14 RTs.
- **Gaps:** None.
- **Complexity:** High (wide change surface; harder to review).
- **Reversibility:** TWO_WAY but a bad merge drags the whole system back.
- **Why not:** Change surface fights review discipline.

### Option B: Two-phase (local tooling → release automation) [TWO_WAY]
- PR1: Makefile, `-tags=prod` gate, tool directive, `make build/test/lint/help`.
- PR2: `.goreleaser.yaml`, GH Actions workflows, Homebrew tap, audit + checksums.

- **Satisfies:** All 14 RTs by end of PR2.
- **Complexity:** Medium.
- **Reversibility:** TWO_WAY; each PR cleanly revertible.

### Option C: Three-phase (local → CI tests → release automation) [TWO_WAY] ← Recommended
- **PR1 (closes binding constraint):** Makefile, `-tags=prod` embed stub/prod split, Go 1.24 tool directive, `make build/test/lint/help/clean`. Closes RT-5, RT-6, RT-8, RT-9.
- **PR2 (verify loop):** GH Actions CI workflow for PRs — native runner matrix runs `make test && make lint` on every push. Closes RT-11 (partial — CI shape).
- **PR3 (release automation):** `.goreleaser.yaml`, release workflow on `v*.*.*` tags, `SHA256SUMS`, Homebrew tap formula emission + `brew audit`, Docker multi-arch, `.deb`/`.rpm`. Closes RT-1, RT-2, RT-3, RT-4, RT-7, RT-10, RT-12, RT-13, RT-14.

- **Satisfies:** All 14 RTs by end of PR3.
- **Complexity:** Medium (three small PRs).
- **Reversibility:** TWO_WAY; each PR independently useful.
- **Why recommended:** PR1 closes the binding constraint first, giving a working verification loop before any release automation exists. PR2 validates that loop on CI before we trust it to gate publishing. PR3 builds atop proven foundation. Follows Theory-of-Constraints ordering: fix the bottleneck first, then widen the pipe.

---

## Irreversible / Cost-Bearing Steps

Flagged for explicit acknowledgment during m4. None of these are ONE_WAY at the code level, but all have consumer-visible consequences that make reversal expensive.

| Step | Reversibility | Cost on reversal |
|------|---------------|------------------|
| Publish first SemVer tag `v0.1.0` | REVERSIBLE_WITH_COST | Consumers may have pinned; retagging breaks their lockfiles. Can only move forward (v0.1.1, etc.). |
| Create public `homebrew-phronesis` tap repo | REVERSIBLE_WITH_COST | Once documented in README, renaming breaks `brew tap` commands. Can deprecate but old URLs linger. |
| Push first Docker image to `ghcr.io/dhanesh/phronesis:v0.1.0` | REVERSIBLE_WITH_COST | `ghcr` retains pulled digests; deletion doesn't retract existing deployments. |
| Publish formula referencing tap | REVERSIBLE_WITH_COST | Dependents of the tap (brewfiles, CI configs) must update. |

No pure ONE_WAY steps. All are deferred-consequence — the cost kicks in when real consumers arrive.

---
