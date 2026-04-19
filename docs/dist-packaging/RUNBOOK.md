# Release runbook

Operational procedures for when the release pipeline misbehaves. Each
procedure lists: **Trigger → Related constraint → Severity →
Reversibility → Steps → Escalation**.

## RB-1: Smoke test fails on one platform

**Trigger.** In the `smoke` matrix, exactly one platform job fails
while the others pass.

**Related constraint.** O4 (platform-atomic release), RT-1, O2.

**Severity.** Medium — release is degraded, not broken. Three of four
platforms can still publish.

**Reversibility.** TWO_WAY at the pipeline level; the failed platform
is simply excluded from the release.

**Steps.**
1. Check the failed job log for the actual failure — signature
   mismatch? runtime error? missing library?
2. If the failure is platform-specific code (rare — phronesis is
   stdlib-only): fix, commit, delete the tag, retag.
3. If the failure is runner-specific (toolchain, network flake):
   re-run only the failed job from the GH Actions UI.
4. If neither: cancel the `release` job, drop the tag, investigate
   before retagging.

**Escalation.** Three failed re-runs → page the owner. Two platforms
failing simultaneously → escalate immediately.

## RB-2: `release` step fails after smoke passed

**Trigger.** All four smoke jobs green, but the goreleaser step errors.

**Related constraint.** TN2 (GitHub Release is atomic root), RT-3,
S4, B1.

**Severity.** High — the release is considered failed; no artifacts
are published on any channel.

**Reversibility.** REVERSIBLE_WITH_COST — the tag exists on remote.
Deleting it is possible but subtly confusing for watchers.

**Steps.**
1. Read the goreleaser log: is it a config error (bad template, schema
   mismatch) or a dependency error (ghcr login, tap token expired)?
2. Config error: fix, delete the tag (`git push :refs/tags/vX.Y.Z`),
   retag.
3. Dependency error: rotate the affected credential
   (`HOMEBREW_TAP_TOKEN`) or wait for upstream (`ghcr.io` 5xx) and
   re-run the job.
4. Check that `goreleaser check` passes on the config before retag:
   ```
   goreleaser check
   ```

**Escalation.** Two consecutive `release` failures with different
root causes → stop retagging, file an incident, review the config
end-to-end.

## RB-3: Docker publish fails; GH Release already green

**Trigger.** `release` job succeeded (GH Release visible, tarballs
uploaded, draft flipped), but `docker_manifests` step logged an
error (ghcr outage, rate-limit, auth glitch).

**Related constraint.** RT-3 (GitHub is atomic root, Docker is a
best-effort mirror), S4.

**Severity.** Low — tarballs are canonical; Docker users can build
from the tarball if they need the image urgently.

**Reversibility.** TWO_WAY — re-run the Docker-only step (or manually
re-push the manifest) once ghcr recovers.

**Steps.**
1. Do NOT delete the tag. GH Release stands.
2. From the GH Actions UI, re-run ONLY the `release` job with
   `--skip=archive,homebrew,nfpm` to retry only Docker. Use
   `make release CHANNEL=docker` locally if GH Actions is also
   degraded.
3. Announce in release notes that the Docker image is delayed.

**Escalation.** Docker missing for > 4 hours → page the owner, post
status update.

## RB-4: Homebrew tap push fails (`brew audit` or token)

**Trigger.** `release` succeeds for GH + Docker; Homebrew formula
push fails OR `audit-brew` job flags the emitted formula.

**Related constraint.** S5, RT-4, RT-10.

**Severity.** Low — Homebrew users can't `brew upgrade phronesis`
yet, but everything else is live.

**Reversibility.** TWO_WAY.

**Steps.**
1. If token error: rotate `HOMEBREW_TAP_TOKEN` (fine-scoped deploy
   key on the tap repo).
2. If audit error: read the audit output. Common post-Homebrew-5.0
   issues include use of `needs` or `conflicts_with formula:` (both
   deprecated). Update the template in `.goreleaser.yaml`'s `brews`
   block.
3. Re-run with `make release CHANNEL=homebrew` (or re-run the release
   job).

**Escalation.** Homebrew lagging for > 24h → announce in release
notes.

## RB-5: Release pipeline wall-clock > 10 min

**Trigger.** O5 breach — release exceeded the 10-minute p95 budget
on a recent tag.

**Related constraint.** O5, RT-7.

**Severity.** Low (on individual breach), Medium (on pattern).

**Reversibility.** Observability issue, not a break.

**Steps.**
1. Inspect the GH Actions run summary: which job consumed the most
   time? (`smoke` vs `release` vs `audit-brew`.)
2. Common culprits:
   - `npm ci` cache miss → ensure `cache-dependency-path` is set.
   - Docker buildx cache miss → add a `setup-buildx-action` cache
     key based on `${{ hashFiles('Dockerfile') }}`.
   - `go mod download` cache miss → verify `actions/setup-go`'s
     `cache: true`.
3. Add parallelism: separate docker-manifest into its own job so it
   doesn't block.
4. Re-measure trailing 10 releases after the fix.

**Escalation.** Three consecutive breaches → review pipeline
architecture.

## RB-6: `ubuntu-24.04-arm` runner unavailable

**Trigger.** Smoke job for linux-arm64 fails to allocate a runner
(GitHub's arm64 runner pool is a newer addition and can saturate).

**Related constraint.** TN6 (resolved; this is the failure_cascade
branch), RT-11, O2.

**Severity.** Medium.

**Reversibility.** TWO_WAY.

**Steps.**
1. In the release workflow, temporarily swap
   `ubuntu-24.04-arm` → `ubuntu-24.04` with QEMU binfmt_misc
   registration.
2. Add an annotation on the release explicitly noting linux-arm64 was
   smoke-tested under QEMU for this release.
3. Open an issue to revert once arm64 runners are back.

**Escalation.** Arm64 unavailable for > 48h → consider a dedicated
self-hosted runner.

## RB-7: Two machines produce different SHA256 for the same tag

**Trigger.** Determinism check fails — the worktree-verify script in
README yields mismatched shasums.

**Related constraint.** T2, RT-2, TN1.

**Severity.** High. Breaks the T2 invariant.

**Reversibility.** Investigate before acting.

**Steps.**
1. Confirm both machines are on the same Go version (`go version`).
   A Go minor version bump silently changes code generation.
2. Confirm `SOURCE_DATE_EPOCH` resolves identically on both:
   `git log -1 --format=%ct <tag>`.
3. Confirm `git status` is clean on both; uncommitted files poison
   `-trimpath` output.
4. If Go versions differ: pin both to the value in `go.mod`'s `go`
   directive. Consider bumping `toolchain` in go.mod if drift is
   chronic.

**Escalation.** Root cause isn't one of the above → open a T2
regression issue and stop advertising reproducibility until resolved.

---

## v2 work items (not in scope for v1)

- Cosign keyless signing (S1 amendment, RT-12 v2) — sign the digest,
  not the tag, per sigstore docs.
- SBOM generation via `goreleaser`'s `sboms:` block.
- Per-platform distro repositories (deb.phronesis.io, rpm.phronesis.io) —
  currently `.deb` / `.rpm` are attached to the GH Release only.
