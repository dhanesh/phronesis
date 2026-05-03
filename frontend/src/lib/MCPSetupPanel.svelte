<!--
  Admin-only modal that surfaces the OAuth/MCP discovery + JWKS URLs
  with one-click copy buttons. The whole point: an admin can hand
  Claude Code (or any MCP client) the discovery URL and the client
  takes it from there — RFC 7591 dynamic registration runs, the
  OAuth flow runs, the client connects.

  When OAuth is not configured (server returns 503 from
  /.well-known/oauth-authorization-server), the panel renders a
  configuration helper listing the env vars to set instead of
  broken URLs. This makes the failure mode legible.

  Self-consistency: the panel fetches the same discovery endpoint a
  real MCP client would. Whatever it shows is what the client sees.
  No new server endpoint is needed.

  Satisfies:
    admin-ui RT-5 (MCP setup panel surfaces discovery + JWKS URLs).
    admin-ui U3 (one-click copy with visual feedback).
    admin-ui B1 (full UI parity for the OAuth/MCP surface).
-->
<script>
  let {
    open = $bindable(false),
  } = $props();

  let discovery = $state(null);
  let issuer = $state('');
  let discoveryURL = $state('');
  let jwksURL = $state('');
  let configured = $state(false);
  let error = $state('');
  let loaded = $state(false);
  let copyFeedbackFor = $state('');

  $effect(() => {
    if (open) {
      error = '';
      loaded = false;
      copyFeedbackFor = '';
      void loadDiscovery();
    }
  });

  async function loadDiscovery() {
    try {
      // Same URL real MCP clients hit — self-consistent.
      const url = `${window.location.origin}/.well-known/oauth-authorization-server`;
      const res = await fetch(url);
      if (res.status === 503) {
        // OAuth substrate disabled; not a hard error, surface a helper.
        configured = false;
        loaded = true;
        return;
      }
      if (!res.ok) {
        error = `Discovery fetch failed (${res.status})`;
        loaded = true;
        return;
      }
      const data = await res.json();
      discovery = data;
      issuer = typeof data.issuer === 'string' ? data.issuer : '';
      discoveryURL = url;
      jwksURL = typeof data.jwks_uri === 'string' ? data.jwks_uri : '';
      configured = !!issuer;
      loaded = true;
    } catch (e) {
      error = `Discovery fetch failed: ${e?.message || 'network error'}`;
      loaded = true;
    }
  }

  async function copy(value, key) {
    try {
      await navigator.clipboard.writeText(value);
      copyFeedbackFor = key;
      setTimeout(() => {
        if (copyFeedbackFor === key) copyFeedbackFor = '';
      }, 1500);
    } catch {
      copyFeedbackFor = `${key}-failed`;
      setTimeout(() => (copyFeedbackFor = ''), 2500);
    }
  }
</script>

{#if open}
  <div
    class="mcp-backdrop"
    role="presentation"
    onclick={() => (open = false)}
    onkeydown={(e) => e.key === 'Escape' && (open = false)}
  >
    <div
      class="mcp-modal"
      role="dialog"
      aria-modal="true"
      aria-label="Connect an MCP client"
      onclick={(e) => e.stopPropagation()}
    >
      <header class="mcp-head">
        <h2>Connect an MCP client</h2>
        <button class="mcp-close" onclick={() => (open = false)} aria-label="Close">×</button>
      </header>

      {#if !loaded}
        <p class="mcp-loading">Loading discovery…</p>
      {:else if error}
        <p class="mcp-error" data-testid="mcp-error">{error}</p>
      {:else if !configured}
        <section class="mcp-helper" data-testid="mcp-not-configured">
          <p><strong>OAuth is not configured on this instance.</strong></p>
          <p>To enable Claude Code and other MCP clients to connect,
             set these environment variables on the phronesis server
             and restart:</p>
          <pre class="mcp-env">PHRONESIS_OAUTH_ENABLED=1
PHRONESIS_OAUTH_ISSUER=https://your-public-host.example
# Optional — RSA key auto-generated on first start:
# PHRONESIS_OAUTH_KEY_PATH=./data/oauth-key.pem</pre>
          <p class="mcp-doc-link">
            See <code>docs/admin-guide.md</code> for the full setup walkthrough.
          </p>
        </section>
      {:else}
        <section class="mcp-section">
          <p class="mcp-intro">
            Paste the <strong>Discovery URL</strong> into your MCP client's
            server configuration. The client will run RFC 7591 dynamic
            client registration automatically; you complete the OAuth
            flow once in your browser. After that, the client connects
            without further setup.
          </p>

          <div class="mcp-row">
            <label class="mcp-label">Discovery URL</label>
            <div class="mcp-url-row">
              <code class="mcp-url" data-testid="mcp-discovery-url">{discoveryURL}</code>
              <button
                class="mcp-btn"
                onclick={() => copy(discoveryURL, 'discovery')}
                data-testid="mcp-copy-discovery"
              >Copy</button>
              {#if copyFeedbackFor === 'discovery'}
                <span class="mcp-feedback" data-testid="mcp-feedback-discovery">Copied!</span>
              {:else if copyFeedbackFor === 'discovery-failed'}
                <span class="mcp-feedback mcp-feedback-error">Copy failed</span>
              {/if}
            </div>
          </div>

          <div class="mcp-row">
            <label class="mcp-label">JWKS URL</label>
            <div class="mcp-url-row">
              <code class="mcp-url" data-testid="mcp-jwks-url">{jwksURL}</code>
              <button
                class="mcp-btn"
                onclick={() => copy(jwksURL, 'jwks')}
                data-testid="mcp-copy-jwks"
              >Copy</button>
              {#if copyFeedbackFor === 'jwks'}
                <span class="mcp-feedback" data-testid="mcp-feedback-jwks">Copied!</span>
              {:else if copyFeedbackFor === 'jwks-failed'}
                <span class="mcp-feedback mcp-feedback-error">Copy failed</span>
              {/if}
            </div>
          </div>

          <div class="mcp-row">
            <label class="mcp-label">Issuer</label>
            <code class="mcp-url" data-testid="mcp-issuer">{issuer}</code>
          </div>

          <p class="mcp-doc-link">
            Full setup walkthrough in <code>docs/admin-guide.md</code> §
            "OAuth + MCP setup".
          </p>
        </section>
      {/if}
    </div>
  </div>
{/if}

<style>
  .mcp-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklab, black 38%, transparent);
    backdrop-filter: blur(6px);
    z-index: 90;
    display: grid;
    place-items: start center;
    padding-top: 12vh;
  }
  .mcp-modal {
    width: min(36rem, 92vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  .mcp-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border-subtle);
  }
  .mcp-head h2 { margin: 0; font-size: 1rem; font-weight: 600; }
  .mcp-close {
    background: transparent;
    border: 0;
    color: var(--text-secondary);
    font-size: 1.2rem;
    line-height: 1;
    padding: 0.2rem 0.5rem;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .mcp-close:hover { background: var(--bg-hover); }

  .mcp-loading,
  .mcp-error {
    padding: 1.1rem;
    margin: 0;
    color: var(--text-secondary);
    font-size: 0.9rem;
  }
  .mcp-error { color: var(--danger); }

  .mcp-section,
  .mcp-helper {
    padding: 0.9rem 1.1rem 1rem;
  }
  .mcp-intro {
    margin: 0 0 0.9rem;
    font-size: 0.9rem;
    color: var(--text-primary);
    line-height: 1.45;
  }
  .mcp-row { margin: 0 0 0.7rem; }
  .mcp-label {
    display: block;
    font-size: 0.74rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-secondary);
    margin: 0 0 0.25rem;
  }
  .mcp-url-row {
    display: flex;
    align-items: center;
    gap: 0.45rem;
  }
  .mcp-url {
    flex: 1 1 auto;
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace);
    font-size: 0.82rem;
    background: var(--bg-control);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.4rem 0.55rem;
    overflow-wrap: anywhere;
    user-select: all;
  }
  .mcp-btn {
    background: transparent;
    color: var(--accent);
    border: 1px solid var(--accent);
    border-radius: var(--radius-sm);
    padding: 0.3rem 0.7rem;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .mcp-btn:hover { background: var(--accent-bg); }

  .mcp-feedback {
    color: var(--accent);
    font-size: 0.8rem;
    white-space: nowrap;
    transition: opacity 200ms ease;
  }
  .mcp-feedback-error { color: var(--danger); }

  .mcp-helper p { margin: 0 0 0.6rem; font-size: 0.9rem; line-height: 1.45; }
  .mcp-env {
    background: var(--bg-control);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.6rem 0.75rem;
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace);
    font-size: 0.82rem;
    overflow-x: auto;
    white-space: pre-wrap;
    margin: 0 0 0.7rem;
  }
  .mcp-doc-link {
    color: var(--text-secondary);
    font-size: 0.82rem;
    margin: 0.4rem 0 0;
  }
  .mcp-doc-link code {
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace);
  }
</style>
