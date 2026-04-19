# phronesis — distribution & packaging

This directory documents how phronesis is built, tested, and released.
Derived directly from `.manifold/dist-packaging.md` (constraints) and
`.manifold/dist-packaging.json` (structure).

## Quick start

```bash
# Local dev (no frontend build needed — stub FS is served)
make test                    # Runs ./... under -race, <30s on fresh clone
make lint                    # go vet + staticcheck (staticcheck pinned via go.mod)

# Production build (embeds the real Svelte UI)
make build                   # -> ./phronesis with -tags=prod
./phronesis --version        # -> phronesis version=... commit=... buildTime=... go=go1.24.5

# All-channel release (tag-driven; do this in CI, not locally)
git tag v0.1.0 && git push --tags
# → triggers .github/workflows/release.yml
```

## Makefile surface (7 targets)

| Target | What it does |
|--------|--------------|
| `help` | Prints the auto-extracted target list (this table stays in sync by regex; see RT-6). |
| `build` | Builds `frontend/dist/`, stages `internal/webfs/dist/`, then `go build -tags=prod -trimpath -ldflags=...`. |
| `test` | `go test -race -timeout=90s ./...`. No frontend dependency (dev stub). |
| `lint` | `go vet` + `go tool staticcheck` (staticcheck pinned via `go.mod` tool directive). |
| `release` | Publishes to all 4 channels via goreleaser. `CHANNEL=<name>` scopes to one. |
| `docker` | Local `goreleaser release --snapshot` — builds multi-arch images without publishing. |
| `clean` | Removes `phronesis`, `dist/`, `internal/webfs/dist/`, `frontend/dist/`. |

## Distribution channels

Four channels per release (B1), with independence enforced at the
pipeline level (RT-3 / S4 / TN2):

1. **GitHub Releases** (`SHA256SUMS` + tarballs + `.deb`/`.rpm`) — the
   **atomic root**. If this stage fails, the release is considered
   failed and no downstream publishes attempt.
2. **Docker** (`ghcr.io/dhanesh/phronesis:<tag>` + `:latest`,
   multi-arch manifest) — best-effort mirror.
3. **Homebrew** (`brew tap dhanesh/phronesis && brew install phronesis`) —
   formula downloads the GH tarball and verifies SHA256; never rebuilds
   from source (S2 / RT-4).
4. **Linux packages** (`.deb`, `.rpm`) — built by goreleaser's `nfpm`
   integration and attached to the GH Release.

## Target platforms

Exactly four binaries per release (T1):
`darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`.
Windows is deliberately out of scope for v1.

## Build reproducibility

Two machines building the same tag produce byte-identical binaries (T2).
This is achieved by:
- `-trimpath` — strips local filesystem paths.
- `-ldflags "-s -w"` — strips symbol table + DWARF.
- `-X main.buildTime=<commit time>` — uses `git log -1 --format=%cI`
  against the tag, NOT wall-clock. See RT-2 / TN1.
- `SOURCE_DATE_EPOCH` set from the same commit time.
- Pinned Go version (`go.mod` `go 1.24.5`) — the toolchain itself is the
  last variable.

Verify locally with:
```bash
make build && shasum -a 256 phronesis
git worktree add /tmp/phronesis-verify <tag>
cd /tmp/phronesis-verify && make build && shasum -a 256 phronesis
# Both shasums must match.
```

## Tool pinning

- **staticcheck**: pinned in `go.mod` via the Go 1.24 `tool` directive.
  Invoke with `go tool staticcheck`. Version update:
  `go get -tool honnef.co/go/tools/cmd/staticcheck@<version>`.
- **goreleaser**: pinned by major version in
  `.github/workflows/release.yml` via
  `goreleaser/goreleaser-action@v6` with `version: "~> v2"`. Not
  embedded in `go.mod` because goreleaser's transitive deps would
  force the module's Go floor past 1.24.5, tightening T6 beyond intent.

## Time budgets

| Concern | Budget | Measurement |
|---------|--------|-------------|
| `make test` (local, fresh clone) | p95 ≤ 30s (T5) | `time make test` |
| Full release on CI (`git push --tags` → all channels published) | p95 ≤ 10 min (O5 / RT-7) | GH Actions run summary, trailing 10 releases |

## Further reading

- [`DECISIONS.md`](DECISIONS.md) — why each choice was made; traces back to constraints and tensions.
- [`RUNBOOK.md`](RUNBOOK.md) — what to do when a release fails.
- [`../../.manifold/dist-packaging.md`](../../.manifold/dist-packaging.md) — raw constraint manifold.
