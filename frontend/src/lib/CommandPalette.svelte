<!--
  Cmd-K / Ctrl-K command palette. Modal overlay with fuzzy filter over
  pages (passed in as props) plus a small set of commands that act on
  the host app (theme toggle, new page).
  Keybindings: ArrowUp/Down to move selection, Enter to invoke,
  Escape to close. Mouse hover updates the active index.
-->
<script>
  import { THEMES, applyTheme, getCurrentTheme } from './theme';

  let {
    open = $bindable(false),
    pages = [],
    workspaces = [],
    currentWorkspace = '',
    isAdmin = false,
    onSelect,
  } = $props();

  let query = $state('');
  let activeIndex = $state(0);
  let inputEl;

  // Reset state every time the palette opens.
  $effect(() => {
    if (open) {
      query = '';
      activeIndex = 0;
      // Focus the input on the next frame so the autofocus behaviour
      // doesn't race the open animation / DOM mount.
      queueMicrotask(() => inputEl?.focus());
    }
  });

  function toggleTheme() {
    const cur = getCurrentTheme();
    const idx = THEMES.findIndex((t) => t.id === cur);
    const next = THEMES[(idx + 1) % THEMES.length];
    applyTheme(next.id);
  }

  function buildItems(q, pageList) {
    const trimmed = q.trim().toLowerCase();
    const matches = pageList
      .filter((p) => !trimmed || p.name.toLowerCase().includes(trimmed))
      .slice(0, 20)
      .map((p) => ({
        kind: 'page',
        id: `page:${p.name}`,
        label: p.name,
        hint: 'Open page',
        invoke: () => onSelect?.({ type: 'page', name: p.name }),
      }));
    const commands = [
      {
        kind: 'command',
        id: 'cmd:theme.toggle',
        label: 'Toggle theme',
        hint: THEMES.map((t) => t.label).join(' / '),
        invoke: () => toggleTheme(),
      },
    ];
    if (trimmed && !pageList.some((p) => p.name.toLowerCase() === trimmed)) {
      commands.unshift({
        kind: 'command',
        id: 'cmd:page.new',
        label: `New page: ${q}`,
        hint: 'Create',
        invoke: () => onSelect?.({ type: 'new-page', name: trimmed }),
      });
    }
    // Workspace switching: surface other workspaces as commands so users
    // can jump between them with Cmd-K rather than mousing to the
    // top-bar dropdown.
    const wsCommands = workspaces
      .filter((w) => w.slug !== currentWorkspace)
      .filter((w) => !trimmed || w.slug.toLowerCase().includes(trimmed) || (w.name ?? '').toLowerCase().includes(trimmed))
      .map((w) => ({
        kind: 'command',
        id: `cmd:workspace.switch:${w.slug}`,
        label: `Switch to ${w.name || w.slug}`,
        hint: 'Workspace',
        invoke: () => onSelect?.({ type: 'switch-workspace', slug: w.slug }),
      }));
    if (isAdmin) {
      commands.push({
        kind: 'command',
        id: 'cmd:workspace.manage',
        label: 'Manage workspaces',
        hint: 'Admin',
        invoke: () => onSelect?.({ type: 'open-workspace-manager' }),
      });
      commands.push({
        kind: 'command',
        id: 'cmd:users.manage',
        label: 'Manage users',
        hint: 'Admin',
        invoke: () => onSelect?.({ type: 'open-users-manager' }),
      });
      commands.push({
        kind: 'command',
        id: 'cmd:keys.manage',
        label: 'Manage API keys',
        hint: 'Admin',
        invoke: () => onSelect?.({ type: 'open-keys-manager' }),
      });
      // admin-ui RT-5 / RT-6: MCP setup panel surfaces the discovery
      // + JWKS URLs an admin pastes into Claude Code or other MCP
      // clients. Lives behind the same isAdmin gate as the other
      // admin entries.
      commands.push({
        kind: 'command',
        id: 'cmd:mcp.setup',
        label: 'Connect an MCP client',
        hint: 'Admin',
        invoke: () => onSelect?.({ type: 'open-mcp-setup' }),
      });
    }
    return [...matches, ...wsCommands, ...commands];
  }

  // Re-derive whenever any of the inputs change. Listed explicitly so
  // Svelte's reactive graph picks up workspaces / currentWorkspace /
  // isAdmin changes from the host App.
  let items = $derived.by(() => {
    void workspaces; void currentWorkspace; void isAdmin;
    return buildItems(query, pages);
  });

  // Keep activeIndex within bounds when items shrinks.
  $effect(() => {
    if (activeIndex >= items.length) activeIndex = Math.max(0, items.length - 1);
  });

  function close() {
    open = false;
  }

  function invokeAt(idx) {
    const item = items[idx];
    if (!item) return;
    item.invoke();
    // Close after invoking, but keep open for theme toggles so users
    // can flip back and forth without reopening — close anyway, the
    // common case.
    close();
  }

  function onKeyDown(event) {
    if (event.key === 'ArrowDown') {
      activeIndex = Math.min(activeIndex + 1, items.length - 1);
      event.preventDefault();
    } else if (event.key === 'ArrowUp') {
      activeIndex = Math.max(activeIndex - 1, 0);
      event.preventDefault();
    } else if (event.key === 'Enter') {
      invokeAt(activeIndex);
      event.preventDefault();
    } else if (event.key === 'Escape') {
      close();
      event.preventDefault();
    }
  }
</script>

{#if open}
  <div
    class="palette-backdrop"
    role="presentation"
    onclick={close}
    onkeydown={(e) => e.key === 'Escape' && close()}
  >
    <div
      class="palette"
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
    >
      <input
        class="palette-input"
        type="text"
        placeholder="Type a page name or command…"
        bind:value={query}
        bind:this={inputEl}
        onkeydown={onKeyDown}
        autocomplete="off"
        spellcheck="false"
      />
      {#if items.length === 0}
        <p class="palette-empty">No matches.</p>
      {:else}
        <ul class="palette-list" role="listbox">
          {#each items as item, i (item.id)}
            <li
              class="palette-item"
              class:active={i === activeIndex}
              role="option"
              aria-selected={i === activeIndex}
              onmousemove={() => (activeIndex = i)}
              onclick={() => invokeAt(i)}
            >
              <span class="palette-kind" data-kind={item.kind} aria-hidden="true">
                {item.kind === 'page' ? '📄' : '⌘'}
              </span>
              <span class="palette-label">{item.label}</span>
              <span class="palette-hint">{item.hint}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </div>
{/if}

<style>
  .palette-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in oklab, black 38%, transparent);
    backdrop-filter: blur(6px);
    z-index: 100;
    display: grid;
    place-items: start center;
    padding-top: 16vh;
  }

  .palette {
    width: min(36rem, 92vw);
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
    display: grid;
    grid-template-rows: auto 1fr;
  }

  .palette-input {
    width: 100%;
    border: 0;
    border-bottom: 1px solid var(--border-subtle);
    padding: 0.95rem 1.1rem;
    font-size: 1rem;
    background: transparent;
    color: var(--text-primary);
    box-sizing: border-box;
  }
  .palette-input:focus {
    outline: none;
  }
  .palette-input::placeholder {
    color: var(--text-tertiary);
  }

  .palette-list {
    list-style: none;
    margin: 0;
    padding: 0.4rem 0;
    max-height: 50vh;
    overflow-y: auto;
  }

  .palette-item {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 0.75rem;
    padding: 0.55rem 1.1rem;
    cursor: pointer;
    color: var(--text-primary);
    font-size: 0.95rem;
  }
  .palette-item.active {
    background: var(--bg-selected);
  }
  .palette-item.active .palette-label {
    color: var(--accent);
    font-weight: 500;
  }

  .palette-kind {
    width: 1.4rem;
    text-align: center;
    color: var(--text-secondary);
  }

  .palette-hint {
    font-size: 0.8rem;
    color: var(--text-tertiary);
  }

  .palette-empty {
    padding: 1.1rem;
    margin: 0;
    color: var(--text-secondary);
    font-size: 0.92rem;
    text-align: center;
  }
</style>
