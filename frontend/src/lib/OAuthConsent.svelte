<!--
  OAuth consent step (finding mcp-oauth, decision 2026-11-27).
  /oauth/authorize redirects a signed-in user here with its query. The page
  shows who is asking and what they get, then POSTs decision=approve|deny
  to /oauth/authorize and navigates to the redirect_to the server returns.
-->
<script>
  const params = new URLSearchParams(window.location.search);
  const fields = [...params.entries()].filter(([k]) => k !== 'decision');

  let info = $state(null);
  let error = $state('');
  let busy = $state(false);

  // POST the decision with fetch and navigate to the returned redirect_to.
  // A plain form POST cannot follow the server's redirect to the client's
  // callback: the CSP's form-action 'self' blocks it.
  async function decide(decision) {
    busy = true;
    error = '';
    try {
      const body = new URLSearchParams(fields);
      body.set('decision', decision);
      const res = await fetch('/oauth/authorize', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded', Accept: 'application/json' },
        body,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data.redirect_to) {
        error = data.error || data.error_description || `Authorization failed (${res.status})`;
        return;
      }
      window.location.assign(data.redirect_to);
    } finally {
      busy = false;
    }
  }

  async function load() {
    const q = new URLSearchParams({
      client_id: params.get('client_id') || '',
      redirect_uri: params.get('redirect_uri') || '',
      scope: params.get('scope') || '',
    });
    const res = await fetch(`/api/oauth/consent-info?${q}`);
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      error = data.error || `This authorization request is not valid (${res.status}).`;
      return;
    }
    info = data;
  }
  load();

  const scopeText = {
    read: 'Read pages in this wiki',
    write: 'Read and edit pages in this wiki',
    admin: 'Full admin access, including users and keys',
  };
</script>

<div class="consent-backdrop">
  <div class="consent-card" role="dialog" aria-modal="true" aria-label="Authorize application">
    <h2>Authorize application</h2>
    {#if error}
      <p class="consent-error" data-testid="consent-error">{error}</p>
    {:else if !info}
      <p>Loading…</p>
    {:else}
      <p class="consent-lead">
        <strong data-testid="consent-client">{info.client_name || 'Unnamed client'}</strong>
        <span class="consent-tag">unverified</span>
        wants to access phronesis as you.
      </p>
      <dl>
        <dt>Sends you back to</dt>
        <dd data-testid="consent-redirect">{info.redirect_host}{#if info.loopback} (this computer){/if}</dd>
        <dt>Access granted</dt>
        <dd data-testid="consent-scope">{scopeText[info.granted_scope] || info.granted_scope}</dd>
      </dl>
      {#if info.requested_scope && !info.requested_scope.split(' ').includes(info.granted_scope)}
        <p class="consent-note">It asked for more ({info.requested_scope}); your role limits it to {info.granted_scope}.</p>
      {/if}
      <p class="consent-note">Only approve if you started this sign-in from that application.</p>
      <div class="consent-actions">
        <button type="button" class="ghost" disabled={busy} onclick={() => decide('deny')} data-testid="consent-deny">Deny</button>
        <button type="button" disabled={busy} onclick={() => decide('approve')} data-testid="consent-approve">Approve</button>
      </div>
    {/if}
  </div>
</div>

<style>
  .consent-backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: var(--bg-base, #111);
    z-index: 100;
  }
  .consent-card {
    width: min(28rem, 92vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    padding: 1.4rem 1.6rem;
  }
  h2 { margin: 0 0 0.8rem; font-size: 1.1rem; }
  .consent-tag {
    font-size: 0.7rem;
    padding: 0.05rem 0.4rem;
    border-radius: 999px;
    border: 1px solid var(--border-subtle);
    color: var(--text-secondary);
    margin-right: 0.25rem;
  }
  dl { display: grid; grid-template-columns: auto 1fr; gap: 0.35rem 0.9rem; font-size: 0.9rem; }
  dt { color: var(--text-secondary); }
  dd { margin: 0; }
  .consent-note { font-size: 0.8rem; color: var(--text-secondary); }
  .consent-error { color: var(--danger, #c0392b); }
  .consent-actions { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 1rem; }
  button { padding: 0.45rem 1rem; border-radius: var(--radius-sm, 6px); cursor: pointer; }
  button.ghost { background: transparent; border: 1px solid var(--border-subtle); color: var(--text-primary); }
</style>
