# Roles and permissions

This is the role catalog for phronesis: the roles a wiki needs when it runs
for one team and when it runs inside an enterprise. Each role is a named
bundle of permissions. Only part of the catalog is built today; the
**Status** column says which part.

Decisions behind this page are in [decisions.md](decisions.md) (2026-11-27):
RBAC is enforced on pages, API keys work on `/mcp` and the HTTP API alike,
and new OIDC users start as `viewer`.

## Principles

1. **Least privilege by default.** A new user is a `viewer`. People get more
   access when an admin (or, later, an IdP group) grants it.
2. **Delegation never escalates.** An API key or OAuth token acts with the
   lesser of its scope and its owner's current role. Demoting a user weakens
   their keys at once.
3. **Roles are checked on every request.** The role comes from the users
   row, not from the session cookie. Promotion, demotion and suspension take
   effect on open sessions.
4. **One authorization path.** Sessions, API keys and OAuth tokens all become
   the same `Principal`; handlers check `Can(action)` and never the auth
   method.
5. **Scope is explicit.** A key is pinned to one workspace. Instance roles
   and workspace roles are separate layers (workspace layer: planned).

## Permissions

| Permission | Meaning |
|---|---|
| `page.read` | Read pages, backlinks, tags, live updates (SSE), media |
| `page.comment` | Comment on pages (planned feature) |
| `page.propose` | Suggest an edit that someone else must accept (planned) |
| `page.write` | Create and edit pages, upload media |
| `page.delete` | Delete and rename pages (built: editor and up; a maintainer-only policy is planned) |
| `page.restrict` | Restrict a page to named people or groups (planned) |
| `history.read` | Read page history and diffs (planned feature) |
| `history.restore` | Restore an older version (planned feature) |
| `workspace.manage` | Create, rename and delete workspaces |
| `members.manage` | Grant workspace roles (planned: today roles are instance-wide) |
| `users.manage` | List, suspend, reactivate, delete users; change roles |
| `keys.request` | Ask for an API key |
| `keys.manage` | Approve, deny, revoke API keys |
| `audit.read` | Read the audit log (planned UI; the log is written today) |
| `settings.manage` | Instance settings: auth, quotas, integrations (planned UI) |
| `breakglass` | Emergency admin login when the IdP is down (built: env-gated) |

## Instance roles (whole server)

| Role | Who | Permissions | Status |
|---|---|---|---|
| **viewer** | Default for every new user | `page.read`, `keys.request` | **Built** |
| **editor** | Writers and maintainers of content | viewer + `page.write`, `page.delete` | **Built** |
| **admin** | Operators of the wiki | everything except `breakglass` | **Built** |
| break-glass admin | One emergency credential, off by default | admin, audited `severity=high` | **Built** (env-gated) |
| owner / super-admin | The person accountable for the instance; the only role that can grant or remove admin | admin + `settings.manage` + admin grants | Proposed |
| user administrator | Helpdesk / identity team | `users.manage`, `keys.manage`, no content write | Proposed |
| security auditor | Compliance and security review | `page.read`, `audit.read`, `history.read`; no writes | Proposed |
| guest | External collaborator (contractor, partner) | `page.read` only on pages shared with them; never listed in search | Proposed |
| anonymous reader | Public, unauthenticated visitors of a workspace marked public | `page.read` on that workspace | Proposed |

## Workspace roles (one workspace)

Planned layer. Today the instance role applies to every workspace. When
this layer exists, a user's effective role in a workspace is the workspace
role if one is set, else the instance role, and an instance admin is always
a workspace admin.

| Role | Permissions | Status |
|---|---|---|
| workspace admin | all workspace permissions + `members.manage` + `workspace.manage` for that workspace | Proposed |
| maintainer / moderator | editor + `page.delete`, `history.restore`, `page.restrict` | Proposed |
| editor | `page.read`, `page.write`, `history.read` | Proposed |
| contributor | `page.read`, `page.propose`, `page.comment`; creates pages but edits to others' pages need review | Proposed |
| commenter | `page.read`, `page.comment` | Proposed |
| viewer | `page.read`, `history.read` | Proposed |

## Page-level controls

| Control | Meaning | Status |
|---|---|---|
| restricted page | Only listed people or groups can read (and optionally edit) the page; inherits to child pages | Proposed |
| page owner | The person accountable for a page; gets review requests and stale-page reminders | Proposed |
| locked page | Readable by all who can read the workspace; only maintainers can edit (policy pages) | Proposed |

## Non-human principals

| Principal | How it is created | Effective role | Status |
|---|---|---|---|
| API key (`phr_live_...`) | User requests, admin approves; plaintext shown once | min(key scope, owner role); pinned to one workspace; works on `/mcp` and `/api/*` | **Built** |
| OAuth client (MCP, e.g. Claude Code) | Dynamic registration (loopback redirect URIs only without an admin) + user consent | min(granted scope, user role); limited to `/mcp` | **Built** |
| legacy single key (`API-KEY` header) | `PHRONESIS_API_KEY` env | editor on the default workspace | **Built** (deprecated) |
| service account (no human owner) | Admin creates; for CI and bots that outlive people | its own role, workspace-pinned, owned by a team | Proposed |
| AI agent acting for a user | OAuth on-behalf-of with a narrower scope per task | min(task scope, user role); every write audited with the agent id | Proposed |

## Mapping today's code

| Code value | Role |
|---|---|
| `users.role = 'viewer' / 'editor' / 'admin'` | viewer / editor / admin (migration 003; legacy `'user'` became `viewer`) |
| key scope `read / write / admin` | viewer / editor / admin, capped at the owner's role |
| OAuth scope `read / write / admin` | same, capped at the user's role |
| password admin (`PHRONESIS_ADMIN_USER`) | admin, no users row |

`Principal.Can(action)` implements the built part: `read` for viewer and up,
`write` for editor and up, `admin` for admin. Page, workspace-page, event and
media routes call it through `authorize()` in `internal/app/authz.go`.

## Recommended next steps

1. Group-to-role mapping from the IdP (`groups` claim), so enterprises manage
   roles in one place. Needs a decision: IdP-authoritative or in-app
   override.
2. Workspace roles (`workspace_members` table), then guest.
3. Security auditor role with an audit-log viewer.
4. Page history; then maintainer (`history.restore`, and move `page.delete` from editor to maintainer).
