<!--
  Admin-only modal that lists workspace API keys + pending key
  requests. Backed by:
    GET    /api/admin/keys
    POST   /api/admin/keys/:id/revoke
    GET    /api/admin/keys/requests
    POST   /api/admin/keys/requests/:id/deny
    POST   /api/admin/keys/requests/:id/approve  (501 in Stage 1b)

  Satisfies: U3 (Admin Keys page lists keys with one-click revoke +
                  pending key-request approval surface),
             RT-9 (admin Web UI surface — Stage 1c frontend),
             TN7 (request->approve flow surfaced in UI; approve
                   inlines a Stage-2 stub message).
-->
<script>
  let {
    open = $bindable(false),
    onChanged,
  } = $props();

  let keys = $state([]);
  let requests = $state([]);
  let error = $state('');
  let info = $state('');
  let busy = $state(false);
  let loaded = $state(false);

  $effect(() => {
    if (open) {
      error = '';
      info = '';
      void refresh();
    }
  });

  async function refresh() {
    busy = true;
    try {
      const [kRes, rRes] = await Promise.all([
        fetch('/api/admin/keys'),
        fetch('/api/admin/keys/requests'),
      ]);
      if (!kRes.ok) {
        const d = await kRes.json().catch(() => ({}));
        error = d.error || `Load keys failed (${kRes.status})`;
        return;
      }
      if (!rRes.ok) {
        const d = await rRes.json().catch(() => ({}));
        error = d.error || `Load requests failed (${rRes.status})`;
        return;
      }
      keys = (await kRes.json()).keys ?? [];
      requests = (await rRes.json()).requests ?? [];
      loaded = true;
      onChanged?.();
    } finally {
      busy = false;
    }
  }

  async function revoke(key) {
    const label = key.label || key.key_prefix || `key #${key.id}`;
    if (!confirm(`Revoke key "${label}"? Any clients using it will start receiving 401.`)) return;
    busy = true;
    error = '';
    info = '';
    try {
      const res = await fetch(`/api/admin/keys/${key.id}/revoke`, { method: 'POST' });
      if (!res.ok && res.status !== 204) {
        const d = await res.json().catch(() => ({}));
        error = d.error || `Revoke failed (${res.status})`;
        return;
      }
      await refresh();
    } finally {
      busy = false;
    }
  }

  async function denyRequest(req) {
    busy = true;
    error = '';
    info = '';
    try {
      const res = await fetch(`/api/admin/keys/requests/${req.id}/deny`, { method: 'POST' });
      if (!res.ok && res.status !== 204) {
        const d = await res.json().catch(() => ({}));
        error = d.error || `Deny failed (${res.status})`;
        return;
      }
      await refresh();
    } finally {
      busy = false;
    }
  }

  async function approveRequest(req) {
    busy = true;
    error = '';
    info = '';
    try {
      const res = await fetch(`/api/admin/keys/requests/${req.id}/approve`, { method: 'POST' });
      if (res.status === 501) {
        // Stage 2 lands real Argon2id minting; surface the structured
        // message inline rather than as a generic error.
        const d = await res.json().catch(() => ({}));
        info = `${d.error || 'Approve flow not implemented yet.'} ${d.workaround || ''}`.trim();
        return;
      }
      if (!res.ok && res.status !== 204) {
        const d = await res.json().catch(() => ({}));
        error = d.error || `Approve failed (${res.status})`;
        return;
      }
      await refresh();
    } finally {
      busy = false;
    }
  }

  function formatTime(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString();
  }
</script>

{#if open}
  <div
    class="keys-backdrop"
    role="presentation"
    onclick={() => (open = false)}
    onkeydown={(e) => e.key === 'Escape' && (open = false)}
  >
    <div
      class="keys-modal"
      role="dialog"
      aria-modal="true"
      aria-label="Manage API keys"
      onclick={(e) => e.stopPropagation()}
    >
      <header class="keys-head">
        <h2>Manage API keys</h2>
        <button class="keys-close" onclick={() => (open = false)} aria-label="Close">×</button>
      </header>

      {#if error}<p class="keys-error">{error}</p>{/if}
      {#if info}<p class="keys-info" data-testid="keys-info">{info}</p>{/if}

      <section class="keys-section">
        <h3>
          Pending requests
          {#if requests.length > 0}
            <span class="keys-badge" data-testid="keys-pending-count">{requests.length}</span>
          {/if}
        </h3>
        {#if !loaded && busy}
          <p class="keys-empty">Loading…</p>
        {:else if requests.length === 0}
          <p class="keys-empty">No pending key requests.</p>
        {:else}
          <table class="keys-table">
            <thead>
              <tr>
                <th>Owner</th>
                <th>Workspace</th>
                <th>Scope</th>
                <th>Label</th>
                <th>Requested</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {#each requests as r (r.id)}
                <tr data-testid="keys-request-row">
                  <td>{r.owner_name || r.owner_email || `user #${r.user_id}`}</td>
                  <td>{r.workspace_slug}</td>
                  <td><span class="keys-scope keys-scope-{r.requested_scope}">{r.requested_scope}</span></td>
                  <td>{r.requested_label}</td>
                  <td class="keys-time">{formatTime(r.requested_at)}</td>
                  <td class="keys-actions">
                    <button class="keys-btn primary" disabled={busy}
                      onclick={() => approveRequest(r)}
                      data-testid="keys-approve">Approve</button>
                    <button class="keys-btn ghost" disabled={busy}
                      onclick={() => denyRequest(r)}
                      data-testid="keys-deny">Deny</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>

      <section class="keys-section">
        <h3>Active keys</h3>
        {#if !loaded && busy}
          <p class="keys-empty">Loading…</p>
        {:else if keys.length === 0}
          <p class="keys-empty">
            No keys issued yet. They appear here after admin approves a key request
            (Stage 2).
          </p>
        {:else}
          <table class="keys-table">
            <thead>
              <tr>
                <th>Owner</th>
                <th>Prefix</th>
                <th>Workspace</th>
                <th>Scope</th>
                <th>Created</th>
                <th>Last used</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {#each keys as k (k.id)}
                <tr class:revoked={!!k.revoked_at} data-testid="keys-row">
                  <td>{k.owner_name || k.owner_email}</td>
                  <td class="keys-prefix">{k.key_prefix}</td>
                  <td>{k.workspace_slug}</td>
                  <td><span class="keys-scope keys-scope-{k.scope}">{k.scope}</span></td>
                  <td class="keys-time">{formatTime(k.created_at)}</td>
                  <td class="keys-time">{k.last_used_at ? formatTime(k.last_used_at) : 'never'}</td>
                  <td>
                    {#if k.revoked_at}
                      <span class="keys-status keys-status-revoked">revoked</span>
                    {:else if k.expires_at}
                      <span class="keys-status keys-status-active">expires {formatTime(k.expires_at)}</span>
                    {:else}
                      <span class="keys-status keys-status-active">active</span>
                    {/if}
                  </td>
                  <td class="keys-actions">
                    {#if !k.revoked_at}
                      <button class="keys-btn danger" disabled={busy}
                        onclick={() => revoke(k)}
                        data-testid="keys-revoke">Revoke</button>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .keys-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklab, black 38%, transparent);
    backdrop-filter: blur(6px);
    z-index: 90;
    display: grid;
    place-items: start center;
    padding-top: 6vh;
  }
  .keys-modal {
    width: min(70rem, 96vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
    display: flex;
    flex-direction: column;
    max-height: 84vh;
  }
  .keys-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border-subtle);
  }
  .keys-head h2 { margin: 0; font-size: 1rem; font-weight: 600; }
  .keys-close {
    background: transparent;
    border: 0;
    color: var(--text-secondary);
    font-size: 1.2rem;
    line-height: 1;
    padding: 0.2rem 0.5rem;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .keys-close:hover { background: var(--bg-hover); }

  .keys-error {
    color: var(--danger);
    margin: 0.5rem 1.1rem 0;
    font-size: 0.86rem;
  }
  .keys-info {
    color: var(--text-secondary);
    background: var(--accent-bg);
    margin: 0.5rem 1.1rem;
    padding: 0.5rem 0.75rem;
    border-radius: var(--radius-md);
    font-size: 0.86rem;
  }

  .keys-section {
    padding: 0.6rem 1.1rem 1rem;
    border-top: 1px solid var(--border-subtle);
    overflow-y: auto;
  }
  .keys-section:first-of-type { border-top: 0; }
  .keys-section h3 {
    margin: 0.4rem 0 0.6rem;
    font-size: 0.92rem;
    font-weight: 600;
    color: var(--text-secondary);
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .keys-badge {
    background: var(--accent);
    color: var(--text-on-accent);
    border-radius: var(--radius-pill);
    padding: 0.05rem 0.5rem;
    font-size: 0.72rem;
    font-weight: 600;
  }
  .keys-empty {
    color: var(--text-tertiary);
    font-size: 0.86rem;
    margin: 0;
  }

  .keys-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.88rem;
  }
  .keys-table th, .keys-table td {
    padding: 0.45rem 0.7rem;
    text-align: left;
    border-bottom: 1px solid var(--border-subtle);
    vertical-align: top;
  }
  .keys-table thead th {
    color: var(--text-secondary);
    font-weight: 500;
    font-size: 0.74rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .keys-table tr.revoked { color: var(--text-tertiary); }
  .keys-prefix {
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace);
    font-size: 0.82rem;
    color: var(--text-secondary);
  }
  .keys-time {
    color: var(--text-tertiary);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .keys-scope {
    display: inline-block;
    font-size: 0.72rem;
    padding: 0.05rem 0.5rem;
    border-radius: var(--radius-pill);
    text-transform: capitalize;
  }
  .keys-scope-read    { color: var(--text-secondary); background: var(--bg-control); }
  .keys-scope-write   { color: var(--accent); background: var(--accent-bg); }
  .keys-scope-admin   { color: var(--danger); background: color-mix(in oklab, var(--danger) 12%, transparent); }
  .keys-status {
    font-size: 0.74rem;
    padding: 0.05rem 0.5rem;
    border-radius: var(--radius-pill);
  }
  .keys-status-active { color: var(--accent); background: var(--accent-bg); }
  .keys-status-revoked { color: var(--text-tertiary); background: var(--bg-control); }

  .keys-actions {
    display: flex;
    gap: 0.4rem;
    justify-content: flex-end;
    flex-wrap: wrap;
  }
  .keys-btn {
    background: transparent;
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.2rem 0.55rem;
    font-size: 0.8rem;
    cursor: pointer;
  }
  .keys-btn:hover:not(:disabled) { background: var(--bg-hover); }
  .keys-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .keys-btn.primary { color: var(--accent); border-color: var(--accent); }
  .keys-btn.primary:hover:not(:disabled) { background: var(--accent-bg); }
  .keys-btn.danger { color: var(--danger); }
  .keys-btn.danger:hover:not(:disabled) {
    background: color-mix(in oklab, var(--danger) 14%, transparent);
  }
</style>
