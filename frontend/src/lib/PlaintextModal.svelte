<!--
  Modal that displays a freshly-minted API key plaintext exactly once.

  The plaintext is rendered with CSS filter: blur(...) by default;
  an explicit Reveal click toggles the blur off. Copy-to-clipboard
  works through the blur — the clipboard receives the unobscured
  string, the DOM stays blurred so a colleague behind the admin can't
  read it ambient. Industry convention: 1Password, AWS console
  credentials, GitHub PATs.

  Dismiss is gated on an acknowledgment checkbox — "I have copied
  this token; I understand it cannot be retrieved." Until checked:
  the dismiss button is disabled, Escape-key is suppressed, and
  backdrop clicks are suppressed. This prevents fat-finger loss
  (pre-mortem A2: admin Esc-dismisses thinking it's a confirmation,
  loses the token forever).

  On unmount, the locally-bound state (revealed flag, copy-feedback
  string) is reset so a re-mount with new plaintext starts clean.
  The plaintext value itself is owned by the parent — the parent
  MUST clear the bound prop after dismiss; this component does not
  retain it past the close.

  Satisfies:
    RT-1 (BINDING — plaintext modal with blur/reveal + ack gate +
          on-unmount clear).
    Resolves TN2 (visible plaintext vs shoulder-surfing): blur until
                  reveal.
    Underpins S1 (no client storage of plaintext) — bound state is
                  scoped to this component instance, not persisted
                  anywhere.
-->
<script>
  let {
    open = $bindable(false),
    plaintext = '',
    prefix = '',
    warning = '',
    onDismiss,
  } = $props();

  // Local UX state. Reset on every (re)open via the $effect below.
  let acked = $state(false);
  let revealed = $state(false);
  let copyFeedback = $state('');

  // RT-1: when the modal opens or closes, reset the local state so a
  // subsequent mint cannot inherit the previous flow's reveal/ack.
  $effect(() => {
    if (open) {
      acked = false;
      revealed = false;
      copyFeedback = '';
    }
  });

  async function copy() {
    if (!plaintext) return;
    try {
      // RT-1 / TN2: clipboard receives the underlying string value,
      // independent of the rendered blur state. So an admin can
      // copy without ever revealing the token to ambient observers.
      await navigator.clipboard.writeText(plaintext);
      copyFeedback = 'Copied!';
      setTimeout(() => (copyFeedback = ''), 1500);
    } catch {
      copyFeedback = 'Copy failed — select & ⌘C manually';
      setTimeout(() => (copyFeedback = ''), 2500);
    }
  }

  function toggleReveal() {
    revealed = !revealed;
  }

  function dismiss() {
    if (!acked) return; // RT-1: ack-gate
    open = false;
    onDismiss?.();
  }

  function onKeydown(e) {
    // RT-1: suppress Escape until acked — fat-finger protection.
    if (e.key === 'Escape' && acked) {
      dismiss();
    }
  }

  function onBackdrop() {
    // RT-1: suppress backdrop dismiss until acked.
    if (acked) dismiss();
  }
</script>

{#if open}
  <div
    class="pt-backdrop"
    role="presentation"
    onclick={onBackdrop}
    onkeydown={onKeydown}
  >
    <div
      class="pt-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="pt-title"
      onclick={(e) => e.stopPropagation()}
    >
      <header class="pt-head">
        <h2 id="pt-title">API key created</h2>
      </header>

      <p class="pt-warning" data-testid="plaintext-warning">
        This token is shown <strong>once</strong>. Copy it now —
        it cannot be retrieved later. Treat it like a password:
        anyone who reads this screen can use it.
      </p>

      {#if prefix}
        <p class="pt-prefix">
          Display ID: <code>{prefix}</code>
        </p>
      {/if}

      <div class="pt-token-row">
        <span
          class="pt-token"
          class:pt-token-blurred={!revealed}
          data-testid="plaintext-token"
          data-revealed={revealed}
          onclick={toggleReveal}
          onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && toggleReveal()}
          role="button"
          tabindex="0"
          aria-label={revealed ? 'Hide token' : 'Reveal token'}
        >{plaintext}</span>
        <button
          type="button"
          class="pt-btn pt-btn-secondary"
          onclick={toggleReveal}
          data-testid="plaintext-reveal"
        >{revealed ? 'Hide' : 'Reveal'}</button>
        <button
          type="button"
          class="pt-btn pt-btn-primary"
          onclick={copy}
          data-testid="plaintext-copy"
        >Copy</button>
      </div>

      {#if copyFeedback}
        <p class="pt-feedback" data-testid="plaintext-feedback">{copyFeedback}</p>
      {/if}

      {#if warning}
        <p class="pt-server-note">{warning}</p>
      {/if}

      <label class="pt-ack">
        <input
          type="checkbox"
          bind:checked={acked}
          data-testid="plaintext-ack"
        />
        I have copied this token; I understand it cannot be retrieved.
      </label>

      <footer class="pt-foot">
        <button
          type="button"
          class="pt-btn pt-btn-primary"
          disabled={!acked}
          onclick={dismiss}
          data-testid="plaintext-dismiss"
        >Done</button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .pt-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1100;
  }
  .pt-modal {
    background: var(--bg-surface, #fff);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
    max-width: 520px;
    width: 90vw;
    padding: 1rem 1.1rem 1rem;
  }
  .pt-head h2 { margin: 0 0 0.6rem; font-size: 1.05rem; font-weight: 600; }
  .pt-warning {
    color: var(--danger);
    background: color-mix(in oklab, var(--danger) 8%, transparent);
    border-radius: var(--radius-md);
    padding: 0.55rem 0.75rem;
    margin: 0 0 0.7rem;
    font-size: 0.86rem;
  }
  .pt-prefix {
    margin: 0 0 0.4rem;
    color: var(--text-secondary);
    font-size: 0.82rem;
  }
  .pt-prefix code { font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace); }

  .pt-token-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0 0 0.4rem;
  }
  .pt-token {
    flex: 1 1 auto;
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace);
    font-size: 0.86rem;
    background: var(--bg-control);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.4rem 0.55rem;
    cursor: pointer;
    user-select: all;
    transition: filter 80ms ease;
    word-break: break-all;
  }
  .pt-token-blurred {
    /* TN2 resolution: blurred plaintext defends against shoulder-
       surfing while the modal is open. Click toggles via toggleReveal. */
    filter: blur(7px);
  }
  .pt-feedback {
    color: var(--accent);
    font-size: 0.8rem;
    margin: 0 0 0.5rem;
  }
  .pt-server-note {
    color: var(--text-tertiary);
    font-size: 0.78rem;
    margin: 0 0 0.6rem;
  }

  .pt-ack {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    font-size: 0.86rem;
    color: var(--text-secondary);
    margin: 0.4rem 0 0.8rem;
    cursor: pointer;
    user-select: none;
  }
  .pt-ack input[type="checkbox"] {
    margin-top: 0.18rem;
  }

  .pt-foot {
    display: flex;
    justify-content: flex-end;
    gap: 0.4rem;
  }

  .pt-btn {
    background: transparent;
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 0.3rem 0.7rem;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .pt-btn:hover:not(:disabled) { background: var(--bg-hover); }
  .pt-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .pt-btn-primary { color: var(--accent); border-color: var(--accent); }
  .pt-btn-primary:hover:not(:disabled) { background: var(--accent-bg); }
  .pt-btn-secondary { color: var(--text-secondary); }
</style>
