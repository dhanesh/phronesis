---
name: verify-phronesis
description: >-
  Prove that a change to phronesis (the Go + Svelte Markdown wiki in this repo) works by
  driving the real running server the way a user does: the browser editor (sign-in,
  autosave, live SSE, live preview, workspaces), the HTTP API (pages, media, admin keys)
  and the OAuth 2.1 + MCP transport. Use after changing anything under cmd/, internal/
  or frontend/, before calling a phronesis feature done, or when asked to verify, smoke
  test or prove a phronesis feature. Tests passing is not verification; this is.
---

# verify-phronesis

phronesis is a self-hosted Markdown wiki: a Go `net/http` server that embeds a Svelte +
CodeMirror frontend and stores pages as `.md` files on disk. This skill launches isolated
instances of the production binary, drives each user-facing feature with a Playwright
harness (`scripts/drive.mjs`, JSON out), and records SHA-bound evidence with
`scripts/verify_evidence.py`. The feature map is in `features/README.md`.

## Session variables

Set these once per session, from the repo root:

```sh
SKILL="$(git rev-parse --show-toplevel)/.claude/skills/verify-phronesis"
INSTANCE="<verifier>-a"      # unique per session: prefix with your verifier id so you
INSTANCE_OAUTH="<verifier>-b" # never reuse another verifier's .verify/<instance>/ dir
INSTANCE_OIDC="<verifier>-c"  # launched with --oidc (oidc-login)
INSTANCE_KEYS="<verifier>-d"  # launched with --oidc --oauth (api-keys, mcp-wiki-tools)
INSTANCE_COMPOSE="<verifier>-compose"  # compose project for deploy-compose
INSTANCE_HELM="<verifier>-helm"        # k8s namespace for deploy-helm
VERIFIER="<your id>"         # under factory-conductor: the id from the verifier brief;
                             # working alone: a stable name for this session, never the
                             # author of the change being proven
FORGE="$HOME/.claude/skills/verification-skill-forge"   # the forge that maintains this skill
```

Prerequisites, once per checkout: `cd frontend && npm ci && npx playwright install chromium`.

## Launch

Build the production binary from HEAD, then start one instance per name. Launch refuses a
binary whose `--version` commit is not HEAD, so always rebuild after a commit. It also
refuses uncommitted changes under `cmd/`, `internal/`, `frontend/src/`, `go.mod` or
`go.sum`, because evidence is recorded against HEAD and must describe HEAD. Commit first
(on a branch), then build, then drive.

```sh
make build                                       # embeds the frontend, stamps commit=HEAD
"$SKILL/scripts/launch.sh" "$INSTANCE"           # plain instance
"$SKILL/scripts/launch.sh" "$INSTANCE_OAUTH" --oauth   # OAuth 2.1 + /mcp enabled
"$SKILL/scripts/launch.sh" "$INSTANCE_OIDC" --oidc     # OIDC sign-in, HMAC stub verifier
"$SKILL/scripts/launch.sh" "$INSTANCE_KEYS" --oidc --oauth   # flags combine
```

What launch does for each instance:

- Claims a port with `verify_evidence.py port --instance "$INSTANCE"` (range 4300-4499,
  O_EXCL lock under `.verify-run/ports/`), so two owners never share one.
- Points every data path at `.verify-run/$INSTANCE/data/` (pages, blobs, audit log,
  SQLite store, OAuth signing key, journal, snapshots). Nothing touches `./data/`.
- Signs in once as `admin` / `verify-admin-pw` into the per-instance cookie jar
  `.verify-run/$INSTANCE/cookies.txt`.
- Readiness: `GET /api/health` answers `{"ok":true}` AND `lsof` shows the port is held by
  the pid this launch started (health does not identify the process). Timeout: 15 s.
  If the process exits first, launch releases the port claim and exits 1; read
  `.verify-run/$INSTANCE/server.log`, then launch again.

Exit 0 prints one JSON line: `{"instance","port","base_url","pid","data","cookies","oauth"}`.

### Container instances (deploy-compose, deploy-helm)

All container testing runs in Colima. The deploy features test the release image, not the
local binary, so build it from HEAD first; `container.sh` refuses an image whose
`org.opencontainers.image.revision` label is not HEAD (exit 2).

```sh
make image                                            # Docker context colima
make image DOCKER_CONTEXT=colima-phronesis-k8s        # for Helm: k3s uses this Docker
colima status phronesis-k8s || colima start phronesis-k8s --kubernetes --cpu 2 --memory 4
```

`colima start` switches the global docker and kubectl contexts; switch them back
(`docker context use <previous>`, `kubectl config use-context <previous>`). Never restart
the user's default Colima profile. `container.sh` claims two ports per instance
(`<instance>` for phronesis, `<instance>-idp` for Dex), uses the instance name as the compose
project or namespace, and launches, drives and tears down in one run; readiness is
`/api/readyz` through the forwarded port (timeout 60 s).

## Doctor

One read-only check per instance, before the first drive and after any failure:

```sh
"$SKILL/scripts/doctor.sh" "$INSTANCE" "$VERIFIER"
```

It checks `process`, `port_owned` (port held by this instance's pid), `build_is_head`
(binary commit = HEAD), `health` and `auth` (`GET /api/session` with the cookie jar says
`authenticated: true`), and records them with `verify_evidence.py doctor`. Output:
`DOCTOR: ok <path>` and exit 0, or `DOCTOR: red <path>` and exit 1. On red, run cleanup
and launch again; do not drive a red instance.

## Drive

Each feature has a recipe in `features/<id>.md`. All of them run through one harness:

```sh
FEATURE=page-edit-autosave        # any id from features/README.md
node "$SKILL/scripts/drive.mjs" "$FEATURE" "$INSTANCE"            # headless
node "$SKILL/scripts/drive.mjs" "$FEATURE" "$INSTANCE" --headed   # watch it
node "$SKILL/scripts/drive.mjs" mcp-oauth "$INSTANCE_OAUTH"         # needs --oauth
"$SKILL/scripts/container.sh" deploy-compose "$INSTANCE_COMPOSE"   # release image + Dex, Compose
"$SKILL/scripts/container.sh" deploy-helm "$INSTANCE_HELM"         # release image + Dex, k3s
```

The harness drives the browser through stable handles only: ARIA labels and roles
(`getByLabel('Username')`, `getByRole('button', { name: 'Sign in' })`,
`getByRole('combobox', { name: 'Switch workspace' })`), CodeMirror classes (`.cm-content`,
`.cm-md-*`), `data-testid` attributes and route paths (`/w/<page>`, `/api/...`, `/mcp`).
It prints one JSON object:
`{"feature","instance","base_url","ok","checks":[{"name","ok","detail"}],"artifacts":[...]}`
and saves the same object as `result.json`, which is the last entry in `artifacts`.
Exit 0 = all checks passed, 1 = a check failed, 2 = usage or instance not running.

To add a feature: add a function to the `features` table in `scripts/drive.mjs`, check the
on-disk side effect under `.verify-run/$INSTANCE/data/`, and add `features/<id>.md` plus
its index line.

## Evidence

- Exercise the real user path. Never internal setters, never test-only endpoints.
- Capture the action *and* the resulting state, not just a final screenshot.
- Verify side effects (rows inserted, files written, messages sent, webhooks fired) alongside what is visible.
- Mocks only where a production boundary already isolates the external system.
- Where the safe path is a dry-run or test mode, verify what it *actually* skips by observing files, network and git refs, not by trusting its name. Some dry-runs still touch the network or open a browser.
- Evidence goes to `.verify/<instance>/<feature-id>/<sha>/`, one directory per feature per head, recorded with `scripts/verify_evidence.py record`.
- Harness output is JSON wherever the harness can emit it, so a verifier can read it without a human.
- Cleanup removes instances, never evidence.

Record each drive. Pass the harness JSON and every artifact it lists; `--side-effect`
comes from the feature file's Proof section:

```sh
OUT=$(node "$SKILL/scripts/drive.mjs" page-edit-autosave "$INSTANCE"); RC=$?
ART=(); for a in $(printf "%s" "$OUT" | python3 -c 'import sys,json;print("\n".join(json.load(sys.stdin)["artifacts"]))'); do ART+=(--artifact "$a"); done
python3 "$SKILL/scripts/verify_evidence.py" record --instance "$INSTANCE" \
  --feature page-edit-autosave --verifier "$VERIFIER" \
  --result "$([ $RC = 0 ] && echo pass || echo fail)" \
  --action "typed into .cm-content on a new /w/ page, waited for autosave, reloaded" \
  --observed "$(printf "%s" "$OUT" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(", ".join(c["name"]+"="+("ok" if c["ok"] else "FAIL") for c in d["checks"]))')" \
  --side-effect "file written: data/pages/default/verify-edit-*.md" "${ART[@]}"
```

The recorder reads the commit from git and pins each artifact by sha256 under
`.verify/$INSTANCE/<feature-id>/<sha>/`. Under factory-conductor, export
`VERIFY_EVIDENCE_DIR=<run root>/.verify` first. When a drive shows behaviour that
contradicts the feature file, record it as a product bug with
`python3 "$FORGE/assets/forge.py" finding "$SKILL" --feature "$FEATURE" --expected "..." --observed "..."`.
Never edit the feature file to match the bug.

The `- proven:` line in a feature file names the commit a passing drive was recorded on.
The verifier who recorded the pass updates it, in a separate commit that touches only
`features/`. A pass recorded with an uncommitted change to `scripts/` does not count:
commit the harness change, rebuild, drive again, then set `proven`. After editing the
skill, run `python3 "$FORGE/assets/forge.py" lint "$SKILL"`.

## Cleanup

Stop each instance by its own pid file and release its port. Run it for every instance
you launched:

```sh
"$SKILL/scripts/cleanup.sh" "$INSTANCE"
"$SKILL/scripts/cleanup.sh" "$INSTANCE_OAUTH"
"$SKILL/scripts/cleanup.sh" "$INSTANCE_OIDC"
"$SKILL/scripts/cleanup.sh" "$INSTANCE_KEYS"
```

Container instances clean up inside `container.sh`: the compose smoke runs
`docker compose -p "$INSTANCE_COMPOSE" down -v` and the Helm smoke deletes namespace
`$INSTANCE_HELM`, then the port claims are released, and the teardown line appears in
`smoke.log`. Then remove the run directory with `"$SKILL/scripts/cleanup.sh" "$INSTANCE_COMPOSE"`
(and `$INSTANCE_HELM`); with no pid file it only removes `.verify-run/<instance>/`. With
`KEEP=1` the stack stays up; remove it with the same two commands. Neither touches
`.verify/` or any other project.

Cleanup sends SIGTERM to the pid in `.verify-run/$INSTANCE/pid` (SIGKILL after 3 s),
releases the port claim and removes `.verify-run/$INSTANCE/`. It never kills by process
name and never touches `.verify/`, so the evidence survives for the gate.
