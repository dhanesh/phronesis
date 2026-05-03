<!--
  Admin-only modal that lists projected users and lets the admin
  suspend / reactivate / delete them. Backed by /api/admin/users
  (GET + POST/:id/{suspend,reactivate} + DELETE/:id).

  Satisfies: U2 (Admin Users page lists users with status, last-seen,
                  active-key count, pending-request count),
             RT-9 (admin Web UI surface — Stage 1c frontend).
-->
<script>
  let {
    open = $bindable(false),
    onChanged,
  } = $props();

  let users = $state([]);
  let error = $state('');
  let busy = $state(false);
  let loaded = $state(false);

  $effect(() => {
    if (open) {
      error = '';
      void refresh();
    }
  });

  async function refresh() {
    busy = true;
    try {
      const res = await fetch('/api/admin/users');
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        error = data.error || `Load failed (${res.status})`;
        return;
      }
      const data = await res.json();
      users = data.users ?? [];
      loaded = true;
      onChanged?.();
    } finally {
      busy = false;
    }
  }

  async function setStatus(id, action) {
    busy = true;
    error = '';
    try {
      const res = await fetch(`/api/admin/users/${id}/${action}`, { method: 'POST' });
      if (!res.ok && res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        error = data.error || `${action} failed (${res.status})`;
        return;
      }
      await refresh();
    } finally {
      busy = false;
    }
  }

  async function remove(user) {
    const label = user.display_name || user.email || user.oidc_sub;
    if (!confirm(`Delete user "${label}"? Their API keys will be revoked.`)) return;
    busy = true;
    error = '';
    try {
      const res = await fetch(`/api/admin/users/${user.id}`, { method: 'DELETE' });
      if (!res.ok && res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        error = data.error || `Delete failed (${res.status})`;
        return;
      }
      await refresh();
    } finally {
      busy = false;
    }
  }

  function formatLastSeen(iso) {
    if (!iso) return 'never';
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString();
  }
</script>

{#if open}
  <div
    class="users-backdrop"
    role="presentation"
    onclick={() => (open = false)}
    onkeydown={(e) => e.key === 'Escape' && (open = false)}
  >
    <div
      class="users-modal"
      role="dialog"
      aria-modal="true"
      aria-label="Manage users"
      onclick={(e) => e.stopPropagation()}
    >
      <header class="users-head">
        <h2>Manage users</h2>
        <button class="users-close" onclick={() => (open = false)} aria-label="Close">×</button>
      </header>

      {#if error}<p class="users-error">{error}</p>{/if}

      {#if !loaded && busy}
        <p class="users-empty">Loading…</p>
      {:else if loaded && users.length === 0}
        <p class="users-empty">
          No users projected yet. Users appear here after they sign in via OIDC.
        </p>
      {:else}
        <div class="users-table-wrap">
          <table class="users-table">
            <thead>
              <tr>
                <th>Name / Email</th>
                <th>Role</th>
                <th>Status</th>
                <th>Keys</th>
                <th>Pending</th>
                <th>Last seen</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {#each users as u (u.id)}
                <tr class:suspended={u.status === 'suspended'} data-testid="users-row">
                  <td>
                    <div class="users-name">{u.display_name || u.email || u.oidc_sub}</div>
                    {#if u.email && u.email !== u.display_name}
                      <div class="users-sub">{u.email}</div>
                    {/if}
                  </td>
                  <td><span class="users-role">{u.role}</span></td>
                  <td>
                    <span class="users-status users-status-{u.status}">{u.status}</span>
                  </td>
                  <td class="users-num">{u.active_key_count ?? 0}</td>
                  <td class="users-num">{u.pending_request_count ?? 0}</td>
                  <td class="users-time">{formatLastSeen(u.last_seen_at)}</td>
                  <td class="users-actions">
                    {#if u.status === 'active'}
                      <button class="users-btn ghost" disabled={busy}
                        onclick={() => setStatus(u.id, 'suspend')}
                        data-testid="users-suspend">Suspend</button>
                    {:else}
                      <button class="users-btn ghost" disabled={busy}
                        onclick={() => setStatus(u.id, 'reactivate')}
                        data-testid="users-reactivate">Reactivate</button>
                    {/if}
                    <button class="users-btn danger" disabled={busy}
                      onclick={() => remove(u)}
                      data-testid="users-delete">Delete</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .users-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklab, black 38%, transparent);
    backdrop-filter: blur(6px);
    z-index: 90;
    display: grid;
    place-items: start center;
    padding-top: 8vh;
  }
  .users-modal {
    width: min(60rem, 96vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
    display: flex;
    flex-direction: column;
    max-height: 80vh;
  }
  .users-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border-subtle);
  }
  .users-head h2 { margin: 0; font-size: 1rem; font-weight: 600; }
  .users-close {
    background: transparent;
    border: 0;
    color: var(--text-secondary);
    font-size: 1.2rem;
    line-height: 1;
    padding: 0.2rem 0.5rem;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .users-close:hover { background: var(--bg-hover); }

  .users-error {
    color: var(--danger);
    margin: 0.5rem 1.1rem 0;
    font-size: 0.86rem;
  }
  .users-empty {
    margin: 1.5rem 1.1rem;
    color: var(--text-tertiary);
    font-size: 0.92rem;
  }

  .users-table-wrap { overflow: auto; }
  .users-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.88rem;
  }
  .users-table th, .users-table td {
    padding: 0.55rem 0.85rem;
    text-align: left;
    border-bottom: 1px solid var(--border-subtle);
    vertical-align: top;
  }
  .users-table thead th {
    position: sticky;
    top: 0;
    background: var(--bg-elevated);
    color: var(--text-secondary);
    font-weight: 500;
    font-size: 0.78rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .users-table tr.suspended .users-name { color: var(--text-tertiary); }
  .users-name { font-weight: 500; }
  .users-sub { color: var(--text-tertiary); font-size: 0.78rem; }
  .users-role {
    text-transform: capitalize;
    color: var(--text-secondary);
  }
  .users-status {
    display: inline-block;
    font-size: 0.74rem;
    padding: 0.05rem 0.5rem;
    border-radius: var(--radius-pill);
  }
  .users-status-active {
    color: var(--accent);
    background: var(--accent-bg);
  }
  .users-status-suspended {
    color: var(--danger);
    background: color-mix(in oklab, var(--danger) 12%, transparent);
  }
  .users-num { font-variant-numeric: tabular-nums; color: var(--text-secondary); }
  .users-time { color: var(--text-tertiary); white-space: nowrap; }
  .users-actions {
    display: flex;
    gap: 0.4rem;
    justify-content: flex-end;
    flex-wrap: wrap;
  }
  .users-btn {
    background: transparent;
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.2rem 0.55rem;
    font-size: 0.8rem;
    cursor: pointer;
  }
  .users-btn:hover:not(:disabled) { background: var(--bg-hover); }
  .users-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .users-btn.danger { color: var(--danger); }
  .users-btn.danger:hover:not(:disabled) {
    background: color-mix(in oklab, var(--danger) 14%, transparent);
  }
</style>
