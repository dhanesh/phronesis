# Distribution & packaging — decision record

Each section maps a design choice to the constraints or tensions that
forced it. Entries follow the format: **Decision → Why → Alternatives
considered → Consequences**. For raw constraint text see
`.manifold/dist-packaging.md`.

## ADR-1: Build timestamp from git commit time

**Decision.** `main.buildTime` is populated from
`git log -1 --format=%cI <tag>`, not from wall-clock time at build.
`SOURCE_DATE_EPOCH` is set from `git log -1 --format=%ct <tag>`.

**Why.** T2 (deterministic builds) and O3 (build metadata embedded in
binary) would otherwise contradict: two machines building the same tag
at different seconds would produce different binaries. Using commit
time collapses that variability to zero.

**Alternatives.** Fixed epoch (0) — loses diagnostic value. Pure UTC
rounding to the minute — still varies across minute boundaries.

**Consequences.** Operators reading `--version` see when the tag was
AUTHORED, not when the binary was BUILT. Document this explicitly in
the `phronesis --version` help.

Traces to: TN1 (resolved), RT-2, T2, O3.

## ADR-2: GitHub Release is the atomic root; other channels are mirrors

**Decision.** A release is "shipped" when the GitHub Release stage
succeeds. Docker, Homebrew, and Linux-package publishing run as
downstream stages; any of them can fail independently without failing
the release as a whole. Failures produce alerts, not release rollbacks.

**Why.** B1 (ship all 4 channels per release) literally read as
"atomic all-or-nothing" conflicts with S4 (pipelines must be
independent). Picking one or the other would either make us hostage to
ghcr.io/Homebrew uptime (atomic) or produce incoherent partial
releases (pure independence). The GH Release is special because it
carries the tarballs consumers pin to; if the tarballs exist, other
channels can catch up later.

**Alternatives.**
- Fully atomic all-4 — tested in pre-mortem: a 45-minute ghcr.io
  outage would block otherwise-clean releases.
- Fully independent — orphans the "what version am I on" question.

**Consequences.** Release engineering must watch alerts on mirror
failures and re-run the affected channel manually. Docs must tell
Homebrew/Docker users that their channel may lag by hours in rare
cases.

Traces to: TN2 (resolved), RT-3, B1, S4.

## ADR-3: Tool pinning split — staticcheck in `go.mod`, goreleaser in the workflow

**Decision.** `staticcheck` is pinned via the Go 1.24 `tool` directive
in `go.mod`. `goreleaser` is pinned via
`goreleaser/goreleaser-action@v6` with `version: "~> v2"` in the
release workflow.

**Why.** TN3's resolution favored Go 1.24's `tool` directive for its
zero-install-step ergonomics. In practice, `go get -tool goreleaser`
pulls transitive deps that require Go ≥ 1.26, which would bump
`go.mod`'s `go` directive well past our 1.24.5 floor (T6). The split
keeps the lightweight tool in-module and the heavyweight tool in CI.

**Alternatives.**
- All tools in `go.mod` — breaks T6.
- All tools via separate installs — abandons T6/T7 reproducibility.

**Consequences.** Updating goreleaser is a workflow-file edit, not a
`go.mod` edit. Humans must remember both update paths. Documented
explicitly in `README.md`.

Traces to: TN3 (resolved), RT-5, T6, T7.

## ADR-4: Time budget split — T5 for tests, O5 for releases

**Decision.** T5's 30-second boundary applies ONLY to `make test` on a
fresh clone. A new constraint, O5, bounds the CI release pipeline at
p95 ≤ 10 minutes.

**Why.** Without the split, "fast workflow" was ambiguous: T5's 30s
was obviously impossible for cross-compile + Docker buildx + 4 smoke
runners, but stakeholders had cited T5 when challenging release scope.
Explicit split lets each budget govern the right activity.

**Alternatives.** Extend T5 to cover release — no useful granularity.
Drop the release budget — opens the door to 30-minute releases.

**Consequences.** Release wall-clock is tracked per-release via GH
Actions's built-in duration field. Trailing 10 median becomes the
baseline; breaches trigger a parallelism review.

Traces to: TN4 (resolved, introduced O5), RT-7, B1, T5.

## ADR-5: `CHANNEL=` env parametrizes `make release`, not separate targets

**Decision.** A single `release` target accepts an optional
`CHANNEL=<github|docker|homebrew|nfpm>` env var. Empty CHANNEL ships
all four.

**Why.** U1 caps Makefile at 7 user-facing targets. Naive reading of B1
would need 4 × 2 = 8 targets for channel-specific build + publish,
busting U1. TRIZ P1 (Segmentation) + P15 (Dynamization): keep targets
orthogonal, vary behavior by runtime parameter.

**Alternatives.**
- Internal `_release-<channel>` targets hidden from help — still adds
  Make noise and `make -p` reveals them anyway.
- Expand to 11 targets — relaxes U1 under stakeholder pressure.

**Consequences.** `goreleaser` is the single source of truth for
channel branching; its config must stay consistent. `make help` prints
exactly 7 lines.

Traces to: TN5 (resolved), RT-8, U1, B1.

## ADR-6: Native GHA runners per platform; no QEMU

**Decision.** Release workflow uses a 4-runner matrix with explicit
labels: `macos-14`, `macos-13`, `ubuntu-24.04`, `ubuntu-24.04-arm`.
QEMU is used ONLY for Docker buildx cross-arch image staging, never
for Go runtime smoke tests.

**Why.** O2 requires each platform binary to actually execute before
its artifact publishes. Cross-compiled binaries on a linux-amd64
runner can't natively run on darwin-arm64 / linux-arm64. QEMU's
binfmt_misc emulation has documented flakiness under Go's runtime
scheduler — smoke-test false negatives are worse than skipping the
smoke, because they train operators to ignore the signal.

**Alternatives.**
- QEMU everywhere — documented flakiness.
- Drop smoke on arm64 — weakens O2 for half the matrix.

**Consequences.** Slight GH Actions minute increase (macos-* are more
expensive per minute). Accepted as cost of correctness. If
`ubuntu-24.04-arm` is unavailable for a release (runner pool scaling
event), fall back to QEMU with an explicit warning annotation on the
release (see RUNBOOK.md).

Traces to: TN6 (resolved), RT-11, RT-1, O2, T1.

## ADR-7: Embedded frontend gated by `-tags=prod`

**Decision.** `//go:embed dist` lives in
`internal/webfs/embed_prod.go` with `//go:build prod`. A sibling
`embed_dev.go` with `//go:build !prod` exports a hardcoded stub FS.
`make build` always adds `-tags=prod`; `go test ./...` and `go build`
(no tag) use the stub and log a loud startup warning.

**Why.** T3 (embed frontend) requires `frontend/dist/` at compile
time. Running `go test ./...` on a fresh clone without a prior
`npm run build` would fail T5 (30s budget). The build-tag split
separates the two concerns: prod binaries always have the real UI;
tests never depend on the frontend toolchain.

**Alternatives.**
- Always embed, always require `npm run build` — violates T5.
- Commit a placeholder `frontend/dist/` — stale artifact risk, no
  loud warning on forgotten tag.

**Consequences.** The binding constraint (RT-9) that unlocked every
other RT. `make build` must copy `frontend/dist/` into
`internal/webfs/dist/` because `//go:embed` can't reference paths
outside the Go file's directory. This staging step is a required
pre-build hook in both the Makefile and `.goreleaser.yaml`.

Traces to: TN7 (resolved), RT-9 (binding constraint), T3, T4, T5.

## ADR-8: SHA256SUMS in v1; cosign keyless in v2

**Decision.** v1 releases publish `SHA256SUMS` only. Keyless cosign
signing via GitHub Actions OIDC is documented as the v2 upgrade path
but not shipped now.

**Why.** S1 mandates integrity verification. `SHA256SUMS` covers that.
Cosign adds provenance + replay protection but requires Rekor
transparency log integration, ACL review for the tap repo, and
consumer verification docs — non-trivial surface for v1. The S1
rationale was amended in m1 iteration 2 to acknowledge this explicitly
(sign the digest, not the tag, per sigstore best practice).

**Alternatives.** Ship cosign from day one — extends the release
pipeline, more moving parts to debug.

**Consequences.** `RT-14` currently covers only `GITHUB_TOKEN` + tap
deploy key; adding cosign in v2 re-opens S1's attack surface analysis
(GAP-09). Noted in RUNBOOK.md as a future work item.

Traces to: S1 (v2 amendment), RT-12, RT-14.

---

## Open items for m5 / m6

- Measure first 10 releases' wall-clock — establish O5 baseline.
- First `--version` smoke-output from a tagged release — confirm
  buildTime matches `git log -1 --format=%cI <tag>`.
- First Homebrew formula emission — run `brew audit --strict --new`
  manually as well as the CI hook (belt-and-suspenders).
- Provision the Homebrew tap repo and `HOMEBREW_TAP_TOKEN` deploy
  secret before the first `v*.*.*` tag.
