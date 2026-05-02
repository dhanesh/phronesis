<!--
  Admin-only modal that lists workspaces and lets the admin create
  new ones (slug + name) or delete non-default ones. Backed by
  /api/admin/workspaces (POST + DELETE) and /api/workspaces (GET).
-->
<script>
  let {
    open = $bindable(false),
    workspaces = $bindable([]),
    currentWorkspace = '',
    onChanged,
  } = $props();

  let slug = $state('');
  let name = $state('');
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      slug = '';
      name = '';
      error = '';
    }
  });

  async function refresh() {
    const res = await fetch('/api/workspaces');
    if (!res.ok) return;
    const data = await res.json();
    workspaces = data.workspaces ?? [];
    onChanged?.();
  }

  async function create() {
    if (!slug.trim()) {
      error = 'Slug is required.';
      return;
    }
    busy = true;
    error = '';
    try {
      const res = await fetch('/api/admin/workspaces', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ slug: slug.trim(), name: name.trim() || slug.trim() }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        error = data.error || `Create failed (${res.status})`;
        return;
      }
      slug = '';
      name = '';
      await refresh();
    } finally {
      busy = false;
    }
  }

  async function remove(target) {
    if (target === 'default') return;
    if (!confirm(`Delete workspace "${target}"? Pages inside will be removed.`)) return;
    busy = true;
    error = '';
    try {
      const res = await fetch(`/api/admin/workspaces/${encodeURIComponent(target)}`, {
        method: 'DELETE',
      });
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
</script>

{#if open}
  <div
    class="ws-backdrop"
    role="presentation"
    onclick={() => (open = false)}
    onkeydown={(e) => e.key === 'Escape' && (open = false)}
  >
    <div
      class="ws-modal"
      role="dialog"
      aria-modal="true"
      aria-label="Manage workspaces"
      onclick={(e) => e.stopPropagation()}
    >
      <header class="ws-head">
        <h2>Manage workspaces</h2>
        <button class="ws-close" onclick={() => (open = false)} aria-label="Close">×</button>
      </header>

      <ul class="ws-list">
        {#each workspaces as w (w.slug)}
          <li class="ws-row">
            <div class="ws-meta">
              <span class="ws-slug">{w.slug}</span>
              {#if w.name && w.name !== w.slug}<span class="ws-name">{w.name}</span>{/if}
              {#if w.slug === currentWorkspace}<span class="ws-current">active</span>{/if}
            </div>
            {#if w.slug !== 'default'}
              <button class="ws-delete" disabled={busy} onclick={() => remove(w.slug)}>Delete</button>
            {:else}
              <span class="ws-locked" title="Default workspace cannot be deleted">locked</span>
            {/if}
          </li>
        {/each}
      </ul>

      <form class="ws-form" onsubmit={(e) => { e.preventDefault(); create(); }}>
        <h3>New workspace</h3>
        <label>
          Slug
          <input
            bind:value={slug}
            placeholder="research"
            autocomplete="off"
            spellcheck="false"
            disabled={busy}
          />
          <small>Lowercase letters, digits, hyphens. 1–63 chars.</small>
        </label>
        <label>
          Name (optional)
          <input bind:value={name} placeholder="Research" autocomplete="off" disabled={busy} />
        </label>
        {#if error}<p class="ws-error">{error}</p>{/if}
        <button type="submit" disabled={busy}>{busy ? 'Working…' : 'Create'}</button>
      </form>
    </div>
  </div>
{/if}

<style>
  .ws-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklab, black 38%, transparent);
    backdrop-filter: blur(6px);
    z-index: 90;
    display: grid;
    place-items: start center;
    padding-top: 12vh;
  }
  .ws-modal {
    width: min(32rem, 92vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  .ws-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border-subtle);
  }
  .ws-head h2 {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
  }
  .ws-close {
    background: transparent;
    border: 0;
    color: var(--text-secondary);
    font-size: 1.2rem;
    line-height: 1;
    padding: 0.2rem 0.5rem;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .ws-close:hover { background: var(--bg-hover); }

  .ws-list {
    list-style: none;
    margin: 0;
    padding: 0.5rem 0;
    max-height: 30vh;
    overflow-y: auto;
  }
  .ws-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.4rem 1.1rem;
  }
  .ws-row:hover { background: var(--bg-hover); }
  .ws-meta {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
  }
  .ws-slug { font-weight: 500; }
  .ws-name { color: var(--text-secondary); font-size: 0.88rem; }
  .ws-current {
    font-size: 0.72rem;
    color: var(--accent);
    background: var(--accent-bg);
    padding: 0.05rem 0.45rem;
    border-radius: var(--radius-pill);
  }
  .ws-delete {
    background: transparent;
    color: var(--danger);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.2rem 0.55rem;
    font-size: 0.82rem;
    cursor: pointer;
  }
  .ws-delete:hover { background: color-mix(in oklab, var(--danger) 14%, transparent); }
  .ws-locked {
    color: var(--text-tertiary);
    font-size: 0.78rem;
    font-style: italic;
  }

  .ws-form {
    border-top: 1px solid var(--border-subtle);
    padding: 0.85rem 1.1rem 1.1rem;
    display: grid;
    gap: 0.6rem;
  }
  .ws-form h3 {
    margin: 0;
    font-size: 0.92rem;
    font-weight: 600;
    color: var(--text-secondary);
  }
  .ws-form label {
    display: grid;
    gap: 0.25rem;
    font-size: 0.86rem;
    color: var(--text-secondary);
    margin-bottom: 0;
  }
  .ws-form input {
    background: var(--bg-control);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    padding: 0.45rem 0.7rem;
  }
  .ws-form input:focus {
    outline: none;
    border-color: var(--border-focus);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
  .ws-form small { color: var(--text-tertiary); font-size: 0.78rem; }
  .ws-form button {
    justify-self: start;
    background: var(--accent);
    color: var(--text-on-accent);
    border: 0;
    border-radius: var(--radius-md);
    padding: 0.45rem 1rem;
    font-weight: 500;
    cursor: pointer;
  }
  .ws-form button:disabled { opacity: 0.6; cursor: not-allowed; }
  .ws-error { color: var(--danger); margin: 0; font-size: 0.86rem; }
</style>
