<!--
  Application top bar. Renders the product brand, the active page
  title, the status block, and the chrome controls (⌘K launcher,
  theme switcher, sign out). Replaces the previous in-sidebar chrome.

  Editable rename UX is intentionally not wired here — phronesis has
  no backend rename endpoint yet, so the page title stays a display
  heading and the rename path is "Cmd-K, New page: <name>" which
  navigates to the new name.
-->
<script>
  import ThemeSwitcher from './ThemeSwitcher.svelte';

  let {
    pageName = '',
    status = '',
    mergedNotice = '',
    onOpenPalette,
    onLogout,
  } = $props();
</script>

<header class="top-bar">
  <div class="brand">
    <span class="brand-mark" aria-hidden="true">◆</span>
    <span class="brand-name">phronesis</span>
  </div>

  <div class="title-block" aria-live="polite">
    <p class="path">/{pageName}</p>
    <h1 class="page-name">{pageName}</h1>
  </div>

  <div class="status-block" aria-live="polite">
    <span>{status}</span>
    {#if mergedNotice}
      <strong>{mergedNotice}</strong>
    {/if}
  </div>

  <div class="actions">
    <button
      class="cmdk-launcher"
      type="button"
      onclick={() => onOpenPalette?.()}
      title="Open command palette (⌘K)"
    >
      <span class="cmdk-icon" aria-hidden="true">⌘K</span>
      <span class="cmdk-label">Quick open</span>
    </button>
    <ThemeSwitcher />
    <button class="logout" type="button" onclick={() => onLogout?.()}>Sign out</button>
  </div>
</header>

<style>
  .top-bar {
    display: grid;
    grid-template-columns: auto 1fr auto auto;
    align-items: center;
    gap: 1.25rem;
    padding: 0.55rem 1.25rem;
    background: var(--bg-elevated);
    border-bottom: 1px solid var(--border-subtle);
  }

  .brand {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--text-primary);
    font-weight: 600;
  }
  .brand-mark {
    color: var(--accent);
    font-size: 1.1rem;
  }
  .brand-name {
    font-size: 0.95rem;
    letter-spacing: -0.01em;
  }

  .title-block {
    display: grid;
    align-items: center;
    line-height: 1.1;
  }
  .title-block .path {
    margin: 0;
    font-size: 0.7rem;
    color: var(--text-tertiary);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .title-block .page-name {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text-primary);
  }

  .status-block {
    text-align: right;
    color: var(--text-secondary);
    font-size: 0.82rem;
    line-height: 1.2;
  }
  .status-block strong {
    display: block;
    color: var(--warning);
    font-weight: 500;
  }

  .actions {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
  }

  .cmdk-launcher {
    display: inline-flex;
    align-items: center;
    gap: 0.55rem;
    padding: 0.4rem 0.65rem 0.4rem 0.5rem;
    background: var(--bg-control);
    color: var(--text-secondary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    font-size: 0.85rem;
    cursor: pointer;
  }
  .cmdk-launcher:hover {
    background: var(--bg-hover);
    color: var(--text-primary);
  }
  .cmdk-icon {
    font-size: 0.72rem;
    background: var(--bg-elevated);
    color: var(--text-secondary);
    padding: 0.05rem 0.35rem;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
  }

  .logout {
    background: transparent;
    color: var(--text-secondary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    padding: 0.4rem 0.85rem;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .logout:hover {
    background: var(--bg-hover);
    color: var(--text-primary);
  }

  @media (max-width: 900px) {
    .top-bar {
      grid-template-columns: auto 1fr auto;
      gap: 0.7rem;
    }
    .status-block {
      display: none;
    }
    .cmdk-label {
      display: none;
    }
  }
</style>
