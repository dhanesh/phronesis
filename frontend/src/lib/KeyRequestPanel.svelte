<!--
  User side of the API key request -> admin approve flow (TN7, decision
  2026-11-27). Reached at /settings/keys/request or from the command
  palette ("Request an API key"). Backed by:
    GET  /api/keys/requests   (the caller's own requests)
    POST /api/keys/requests   {scope: read|write, label}
  The admin approves in the Manage API keys dialog and hands the
  one-time plaintext to the requester.
-->
<script>
  let { open = $bindable(false) } = $props();

  let scope = $state('read');
  let label = $state('');
  let requests = $state([]);
  let error = $state('');
  let info = $state('');
  let busy = $state(false);

  $effect(() => {
    if (open) {
      info = '';
      error = '';
      load();
    }
  });

  async function load() {
    busy = true;
    try {
      const res = await fetch('/api/keys/requests');
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        error = data.error || `Could not load requests (${res.status})`;
        requests = [];
        return;
      }
      requests = data.requests ?? [];
    } catch (e) {
      error = e?.message || 'Could not load requests';
    } finally {
      busy = false;
    }
  }

  async function submit() {
    error = '';
    info = '';
    busy = true;
    try {
      const res = await fetch('/api/keys/requests', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scope, label }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        error = data.error || `Request failed (${res.status})`;
        return;
      }
      info = 'Request sent. An admin will approve it and give you the key.';
      label = '';
      await load();
    } catch (e) {
      error = e?.message || 'Request failed';
    } finally {
      busy = false;
    }
  }

  function status(r) {
    return r.decision || 'pending';
  }
</script>

{#if open}
  <div
    class="kr-backdrop"
    role="presentation"
    onclick={() => (open = false)}
    onkeydown={(e) => e.key === 'Escape' && (open = false)}
  >
    <div
      class="kr-modal"
      role="dialog"
      aria-modal="true"
      aria-label="Request an API key"
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.key === 'Escape' && (open = false)}
    >
      <header class="kr-head">
        <h2>Request an API key</h2>
        <button class="kr-close" onclick={() => (open = false)} aria-label="Close">×</button>
      </header>

      <form
        class="kr-form"
        onsubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <label>
          Scope
          <select bind:value={scope} data-testid="kr-scope">
            <option value="read">read</option>
            <option value="write">write</option>
          </select>
        </label>
        <label>
          Label
          <input bind:value={label} maxlength="100" placeholder="e.g. laptop CLI" data-testid="kr-label" />
        </label>
        <button type="submit" disabled={busy || !label.trim()} data-testid="kr-submit">Send request</button>
      </form>

      {#if error}<p class="kr-error" data-testid="kr-error">{error}</p>{/if}
      {#if info}<p class="kr-info" data-testid="kr-info">{info}</p>{/if}

      <h3>Your requests</h3>
      {#if busy && requests.length === 0}
        <p class="kr-empty">Loading…</p>
      {:else if requests.length === 0}
        <p class="kr-empty">No requests yet.</p>
      {:else}
        <ul class="kr-list">
          {#each requests as r (r.id)}
            <li data-testid="kr-row">
              <span class="kr-label">{r.requested_label}</span>
              <span class="kr-scope">{r.requested_scope}</span>
              <span class="kr-status kr-status-{status(r)}">{status(r)}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>
{/if}

<style>
  .kr-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklab, black 38%, transparent);
    backdrop-filter: blur(6px);
    z-index: 90;
    display: grid;
    place-items: start center;
    padding-top: 8vh;
  }
  .kr-modal {
    width: min(32rem, 96vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    padding-bottom: 1rem;
  }
  .kr-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border-subtle);
  }
  .kr-head h2 { margin: 0; font-size: 1rem; font-weight: 600; }
  .kr-close {
    background: transparent;
    border: 0;
    color: var(--text-secondary);
    font-size: 1.2rem;
    line-height: 1;
    padding: 0.2rem 0.5rem;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .kr-form {
    display: grid;
    grid-template-columns: 7rem 1fr auto;
    gap: 0.6rem;
    align-items: end;
    padding: 1rem 1.1rem 0.4rem;
  }
  .kr-form label { display: grid; gap: 0.25rem; font-size: 0.8rem; color: var(--text-secondary); }
  h3 { margin: 0.8rem 1.1rem 0.4rem; font-size: 0.85rem; font-weight: 600; }
  .kr-error, .kr-info, .kr-empty { margin: 0.4rem 1.1rem; font-size: 0.85rem; }
  .kr-error { color: var(--danger, #c0392b); }
  .kr-empty { color: var(--text-secondary); }
  .kr-list { list-style: none; margin: 0; padding: 0 1.1rem; display: grid; gap: 0.35rem; }
  .kr-list li { display: flex; gap: 0.75rem; align-items: center; font-size: 0.85rem; }
  .kr-label { flex: 1; }
  .kr-scope, .kr-status { color: var(--text-secondary); }
  .kr-status-approved { color: var(--success, #2e7d32); }
  .kr-status-denied { color: var(--danger, #c0392b); }
</style>
