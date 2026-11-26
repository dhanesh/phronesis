# Decisions

Decisions made by the project owner, newest last. Agents re-read this after a
resume or context compaction.

## 2026-11-26

- **Verify, then fix broken features before anything else.** Asked for a check
  of what works, a `verify-phronesis` skill built with verification-skill-forge,
  and fixes for the current feature set before any new work.
  - Done: the login `?next` bug that broke the MCP OAuth flow for signed-out
    users (commit c88c9ca).
  - Open, waiting on the owner's design input: API key requests (no create
    endpoint) and OIDC RBAC (every session is admin; users are never projected).

## 2026-11-27

- **OIDC default role is `viewer`.** A new OIDC user whose claims map to no
  role is projected as `viewer`; admins promote. Group-claim mapping (S8) is
  deferred. Every session's role comes from the projected users row, never a
  hardcoded admin.
- **API keys follow the spec request -> approve flow (TN7).** Signed-in users
  with a users row submit `POST /api/keys/requests` from a small request form;
  the admin approves in the existing Keys panel and sees the plaintext once.
