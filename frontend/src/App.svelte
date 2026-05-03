<script>
  import { onMount } from 'svelte';
  import Editor from './lib/Editor.svelte';
  import CommandPalette from './lib/CommandPalette.svelte';
  import TopBar from './lib/TopBar.svelte';
  import WorkspaceManager from './lib/WorkspaceManager.svelte';
  import UsersManager from './lib/UsersManager.svelte';
  import KeysManager from './lib/KeysManager.svelte';
  import { loadTheme } from './lib/theme';

  const defaultPage = 'home';
  let checkingSession = $state(true);
  let authenticated = $state(false);
  let loginError = $state('');
  let bootError = $state('');
  let username = $state('admin');
  let password = $state('');

  let pages = $state([]);
  let pageName = $state(defaultPage);
  let page = $state(emptyPage(defaultPage));
  let draft = $state('');
  let status = $state('Idle');
  let mergedNotice = $state('');
  let sidebarOpen = $state(true);
  // Imperative handles. `editor` is assigned via `bind:this` so it must be
  // a rune for the assignment to land; the others never escape this script
  // so plain lets are fine.
  let source;
  let saveTimer;
  let editor = $state();
  // INT-9 / RT-2.2: durability state driven by autosave lifecycle. Values
  // from DURABILITY_STATES in durability.js: idle | dirty | syncing | synced
  // | saved | disconnected. This is the v1 approximation; once the server
  // emits op_acked/op_saved over SSE the indicator can drive itself.
  let durability = $state('idle');
  let paletteOpen = $state(false);

  // Multi-workspace state. Loaded after auth; persisted across the
  // session via localStorage so the top-bar selector remembers the
  // last active workspace across reloads.
  const WORKSPACE_STORAGE_KEY = 'phronesis.workspace';
  let workspaces = $state([]);
  let currentWorkspace = $state('default');
  let userRole = $state('');
  let workspaceManagerOpen = $state(false);
  let usersManagerOpen = $state(false);
  let keysManagerOpen = $state(false);

  function pagePath(name) {
    const ws = encodeURIComponent(currentWorkspace);
    const n = encodeURIComponent(name);
    return `/api/workspaces/${ws}/pages/${n}`;
  }

  function pagesListPath() {
    return `/api/workspaces/${encodeURIComponent(currentWorkspace)}/pages`;
  }

  function onPaletteSelect(item) {
    if (item.type === 'page' || item.type === 'new-page') {
      loadPage(item.name);
    } else if (item.type === 'switch-workspace') {
      switchWorkspace(item.slug);
    } else if (item.type === 'open-workspace-manager') {
      workspaceManagerOpen = true;
    } else if (item.type === 'open-users-manager') {
      usersManagerOpen = true;
    } else if (item.type === 'open-keys-manager') {
      keysManagerOpen = true;
    }
  }

  async function switchWorkspace(slug) {
    if (!slug || slug === currentWorkspace) return;
    if (!workspaces.some((w) => w.slug === slug)) return;
    currentWorkspace = slug;
    try { localStorage.setItem(WORKSPACE_STORAGE_KEY, slug); } catch {}
    pageName = defaultPage;
    await Promise.all([loadPages(), loadPage(defaultPage)]);
  }

  async function loadWorkspaces() {
    try {
      const res = await fetch('/api/workspaces');
      if (!res.ok) return;
      const data = await res.json();
      workspaces = data.workspaces ?? [];
      let stored = '';
      try { stored = localStorage.getItem(WORKSPACE_STORAGE_KEY) || ''; } catch {}
      if (stored && workspaces.some((w) => w.slug === stored)) {
        currentWorkspace = stored;
      } else if (!workspaces.some((w) => w.slug === currentWorkspace)) {
        currentWorkspace = workspaces[0]?.slug || 'default';
      }
    } catch {
      // Best-effort; leave defaults.
    }
  }

  onMount(async () => {
    loadTheme();
    // Cmd-K / Ctrl-K opens the command palette globally. Escape and
    // arrow keys are handled inside the palette while it's open.
    const onGlobalKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        paletteOpen = !paletteOpen;
      }
    };
    window.addEventListener('keydown', onGlobalKey);

    await loadSession();
    if (authenticated) {
      await loadWorkspaces();
      const match = window.location.pathname.match(/^\/w\/(.+)$/);
      const initialPage = match ? decodeURIComponent(match[1]) : defaultPage;
      await Promise.all([loadPages(), loadPage(initialPage)]);
      // loadPage calls reconnectStream() internally; no extra connectStream() needed.
    }
  });

  function emptyPage(name) {
    return {
      name,
      content: '',
      version: 0,
      updatedAt: '',
      render: {
        html: '<p>Start writing.</p>',
        tags: [],
        links: [],
        backlinks: []
      },
      tagged: []
    };
  }

  async function loadSession() {
    checkingSession = true;
    bootError = '';
    try {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 5000);
      const res = await fetch('/api/session', {
        signal: controller.signal,
        headers: {
          Accept: 'application/json'
        }
      });
      clearTimeout(timeout);

      if (!res.ok) {
        throw new Error(`session check failed (${res.status})`);
      }

      const contentType = res.headers.get('content-type') || '';
      if (!contentType.includes('application/json')) {
        throw new Error('session endpoint returned unexpected content');
      }

      const data = await res.json();
      authenticated = Boolean(data.authenticated);
      userRole = data.role || '';
    } catch (error) {
      authenticated = false;
      bootError = error?.name === 'AbortError'
        ? 'The server did not respond to the session check in time.'
        : error?.message || 'Unable to contact the server.';
    } finally {
      checkingSession = false;
    }
  }

  async function login() {
    loginError = '';
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password })
    });
    if (!res.ok) {
      const data = await res.json();
      loginError = data.error ?? 'Login failed';
      return;
    }
    authenticated = true;
    password = '';
    // Re-pull session to capture role for the freshly-logged-in user.
    await loadSession();
    await loadWorkspaces();
    await Promise.all([loadPages(), loadPage(pageName)]);
    // loadPage calls reconnectStream() internally; no extra connectStream() needed.
  }

  async function logout() {
    await fetch('/api/logout', { method: 'POST' });
    source?.close();
    authenticated = false;
    page = emptyPage(defaultPage);
    draft = '';
  }

  async function loadPages() {
    const res = await fetch(pagesListPath());
    const data = await res.json();
    pages = data.pages ?? [];
  }

  async function loadPage(name) {
    pageName = (name || defaultPage).toLowerCase();
    const res = await fetch(pagePath(pageName));
    const data = await res.json();
    page = data.page ?? emptyPage(pageName);
    draft = page.content;
    mergedNotice = '';
    status = page.version ? `Loaded ${page.name}` : `New page ${page.name}`;
    reconnectStream();
    queueMicrotask(() => editor?.focus());
  }

  function reconnectStream() {
    if (!authenticated) return;
    source?.close();
    connectStream();
  }

  function connectStream() {
    source = new EventSource(`${pagePath(pageName)}/events`);
    source.addEventListener('snapshot', (event) => {
      const payload = JSON.parse(event.data);
      page = payload.page;
      if (!draft) {
        draft = page.content;
      }
    });
    source.addEventListener('update', (event) => {
      const payload = JSON.parse(event.data);
      const incoming = payload.page;
      const localDirty = draft !== page.content;
      page = incoming;
      if (!localDirty || payload.author === username) {
        draft = incoming.content;
      }
      mergedNotice = payload.merged ? 'Concurrent edits were merged on the server.' : '';
      status = `Live update ${new Date(payload.timestamp).toLocaleTimeString()}`;
    });
  }

  function scheduleSave() {
    status = 'Saving…';
    durability = 'syncing';
    clearTimeout(saveTimer);
    saveTimer = setTimeout(saveDraft, 600);
  }

  async function saveDraft() {
    const currentDraft = draft;
    const res = await fetch(pagePath(pageName), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        content: currentDraft,
        baseVersion: page.version
      })
    });
    const data = await res.json();
    if (!res.ok) {
      status = data.error ?? 'Save failed';
      // INT-9: failed save -> treat as still-dirty so the indicator nudges
      // the user. 'disconnected' is reserved for true heartbeat loss.
      durability = 'dirty';
      return;
    }
    page = data.page;
    draft = data.page.content;
    mergedNotice = data.merged ? 'Saved with merge adjustments.' : '';
    status = `Saved ${new Date().toLocaleTimeString()}`;
    // INT-9: two-state indicator. Without server op_saved events we can't
    // distinguish synced from saved, so on success we go straight to saved.
    // When Wave-Future wires CRDT op_acked/op_saved over SSE, this becomes
    // a proper synced → saved transition.
    durability = 'saved';
    await loadPages();
  }

  function onEditorChange(detail) {
    draft = detail.value;
    durability = 'dirty';
    scheduleSave();
  }

  function onNavigate(detail) {
    loadPage(detail.page);
  }
</script>

{#if checkingSession}
  <main class="center">Checking session…</main>
{:else if !authenticated}
  <main class="login-shell">
    <form
      class="login-card"
      onsubmit={(e) => {
        e.preventDefault();
        login();
      }}
    >
      <p class="eyebrow">phronesis</p>
      <h1>A knowledge base for humans and AI agents</h1>
      <p class="lede">Share notes, docs, and context in plain Markdown — agent-readable by design, self-hosted by default.</p>
      {#if bootError}<p class="error">{bootError}</p>{/if}
      <label>
        Username
        <input bind:value={username} autocomplete="username" />
      </label>
      <label>
        Password
        <input bind:value={password} type="password" autocomplete="current-password" />
      </label>
      <button type="submit">Sign in</button>
      {#if loginError}<p class="error">{loginError}</p>{/if}
    </form>
  </main>
{:else}
  <main class="app-shell">
    <TopBar
      pageName={page.name}
      {status}
      {mergedNotice}
      {workspaces}
      {currentWorkspace}
      onSwitchWorkspace={switchWorkspace}
      isAdmin={userRole === 'admin'}
      onManageWorkspaces={() => (workspaceManagerOpen = true)}
      onOpenPalette={() => (paletteOpen = true)}
      onLogout={logout}
    />

    <div class="app-body">
      <aside class:closed={!sidebarOpen}>
        <div class="sidebar-head">
          <p class="eyebrow">Pages</p>
          <button
            class="ghost"
            type="button"
            aria-label={sidebarOpen ? 'Hide sidebar' : 'Show sidebar'}
            onclick={() => (sidebarOpen = !sidebarOpen)}
          >{sidebarOpen ? '◀' : '▶'}</button>
        </div>
        {#if sidebarOpen}
          <nav>
            {#each pages as entry (entry.name)}
              <button class:selected={entry.name === page.name} class="nav-link" onclick={() => loadPage(entry.name)}>
                <span>{entry.name}</span>
              </button>
            {/each}
          </nav>
        {/if}
      </aside>

      <section class="workspace">
        <div class="workspace-body">
          <section class="editor-card">
            <div class="editor-frame">
            <Editor
              bind:this={editor}
              value={draft}
              page={page.name}
              {durability}
              onchange={onEditorChange}
              onnavigate={onNavigate}
            />
          </div>
        </section>

        <aside class="context-rail">
          <section class="rail-card">
            <p class="eyebrow">Page State</p>
            <h3>Context</h3>
            <p>{page.version ? `Updated ${new Date(page.updatedAt).toLocaleString()}` : 'Not saved yet'}</p>
          </section>

          <section class="rail-card">
            <p class="eyebrow">Incoming Links</p>
            <h3>
              Backlinks
              {#if page.render.backlinks.length}
                <span class="rail-count">{page.render.backlinks.length}</span>
              {/if}
            </h3>
            {#if page.render.backlinks.length}
              <div class="pill-list">
                {#each page.render.backlinks as link (link)}
                  <button class="pill ghost-pill" onclick={() => loadPage(link)}>{link}</button>
                {/each}
              </div>
            {:else}
              <p>No backlinks yet.</p>
            {/if}
          </section>

          {#if (page.tagged ?? []).length}
            <section class="rail-card">
              <p class="eyebrow">Tagged</p>
              <h3>
                Pages tagged #{page.name}
                <span class="rail-count">{(page.tagged ?? []).length}</span>
              </h3>
              <div class="pill-list">
                {#each (page.tagged ?? []) as link (link)}
                  <button class="pill ghost-pill" onclick={() => loadPage(link)}>{link}</button>
                {/each}
              </div>
            </section>
          {/if}

          <section class="rail-card">
            <p class="eyebrow">Outgoing Links</p>
            <h3>
              Wiki Graph
              {#if page.render.links.length}
                <span class="rail-count">{page.render.links.length}</span>
              {/if}
            </h3>
            {#if page.render.links.length}
              <div class="pill-list">
                {#each page.render.links as link (link)}
                  <button class="pill" onclick={() => loadPage(link)}>{link}</button>
                {/each}
              </div>
            {:else}
              <p>No wiki links yet.</p>
            {/if}
          </section>

          <section class="rail-card">
            <p class="eyebrow">Tags</p>
            <h3>
              Metadata
              {#if page.render.tags.length}
                <span class="rail-count">{page.render.tags.length}</span>
              {/if}
            </h3>
            {#if page.render.tags.length}
              <div class="pill-list">
                {#each page.render.tags as tag (tag)}
                  <button class="tag-chip" onclick={() => loadPage(tag)}>#{tag}</button>
                {/each}
              </div>
            {:else}
              <p>No tags yet.</p>
            {/if}
          </section>
        </aside>
      </div>
    </section>
    </div>
  </main>
{/if}

<CommandPalette
  bind:open={paletteOpen}
  pages={pages}
  workspaces={workspaces}
  currentWorkspace={currentWorkspace}
  isAdmin={userRole === 'admin'}
  onSelect={onPaletteSelect}
/>

<WorkspaceManager
  bind:open={workspaceManagerOpen}
  bind:workspaces
  currentWorkspace={currentWorkspace}
  onChanged={loadWorkspaces}
/>

<UsersManager bind:open={usersManagerOpen} />
<KeysManager bind:open={keysManagerOpen} />

<style>
  /* Tokens come from frontend/src/themes.css (apple-light / apple-dark).
     Every value here references var(--…) so the theme switcher is a
     one-attribute change at the documentElement level. */

  .center {
    display: grid;
    min-height: 100vh;
    place-items: center;
    color: var(--text-primary);
  }

  .login-shell {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: 2rem;
  }

  .login-card {
    width: min(28rem, 100%);
    padding: 2rem;
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    border: 1px solid var(--border-subtle);
    box-shadow: var(--shadow-lg);
    color: var(--text-primary);
  }

  .eyebrow {
    margin: 0 0 0.4rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    font-size: 0.72rem;
    color: var(--text-secondary);
  }

  h1, h2, h3, p {
    margin-top: 0;
  }

  .lede {
    color: var(--text-secondary);
    margin-bottom: 1.2rem;
  }

  .lede.small {
    margin-bottom: 0;
    max-width: 34rem;
  }

  label {
    display: grid;
    gap: 0.35rem;
    margin-bottom: 1rem;
    color: var(--text-primary);
  }

  input {
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    padding: 0.7rem 0.9rem;
    background: var(--bg-control);
    color: var(--text-primary);
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  input:focus {
    outline: none;
    border-color: var(--border-focus);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }

  button {
    border: 0;
    border-radius: var(--radius-md);
    padding: 0.6rem 1rem;
    background: var(--accent);
    color: var(--text-on-accent);
    cursor: pointer;
    font-weight: 500;
    transition: background 0.15s;
  }
  button:hover {
    background: var(--accent-hover);
  }

  .error {
    color: var(--danger);
    margin-top: 1rem;
  }

  .app-shell {
    display: grid;
    min-height: 100vh;
    grid-template-rows: auto 1fr;
  }

  .app-body {
    display: grid;
    grid-template-columns: 14rem minmax(0, 1fr);
    min-height: 0;
  }

  aside {
    padding: 0.85rem 0.65rem;
    background: var(--bg-elevated);
    border-right: 1px solid var(--border-subtle);
    color: var(--text-primary);
    display: grid;
    grid-template-rows: auto 1fr;
    gap: 0.5rem;
    overflow: hidden;
  }

  aside.closed {
    grid-template-columns: auto;
    grid-template-rows: auto;
  }

  .sidebar-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0 0.4rem;
  }
  .sidebar-head .eyebrow {
    margin: 0;
    font-size: 0.68rem;
    color: var(--text-tertiary);
  }

  nav {
    display: grid;
    gap: 0.15rem;
    align-content: start;
    overflow: auto;
  }

  .nav-link,
  .ghost {
    justify-content: flex-start;
    background: transparent;
    color: var(--text-primary);
    border: 0;
    border-radius: var(--radius-md);
    padding: 0.4rem 0.6rem;
    text-align: left;
    font-weight: 400;
    font-size: 0.92rem;
    cursor: pointer;
  }
  .nav-link:hover,
  .ghost:hover {
    background: var(--bg-hover);
  }
  .ghost {
    padding: 0.25rem 0.45rem;
    color: var(--text-tertiary);
    font-size: 0.78rem;
  }

  .nav-link.selected {
    background: var(--bg-selected);
    color: var(--accent);
    font-weight: 500;
  }

  .workspace {
    padding: 1rem 1.25rem 1.25rem;
    display: grid;
    grid-template-rows: 1fr;
    min-height: 0;
  }

  .workspace-body {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 18rem;
    gap: 1rem;
    min-height: 0;
  }

  .editor-card,
  .rail-card {
    background: var(--bg-elevated);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-sm);
  }

  .editor-card {
    display: grid;
    grid-template-rows: 1fr;
    min-height: 0;
  }

  .rail-card {
    padding: 0.9rem 1.1rem;
  }

  .editor-frame {
    min-height: 68vh;
    padding: 0.5rem 1rem 0.25rem 1.2rem;
  }

  .context-rail {
    display: grid;
    align-content: start;
    gap: 0.85rem;
    background: transparent;
    color: var(--text-primary);
    grid-template-rows: none;
    padding: 0;
  }

  .rail-card h3 {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 1rem;
    font-weight: 600;
  }
  .rail-card p:last-child {
    margin-bottom: 0;
    color: var(--text-secondary);
    font-size: 0.88rem;
  }

  .pill-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
  }

  .pill,
  .tag-chip,
  .ghost-pill {
    font-size: 0.85rem;
    border: 0;
    cursor: pointer;
    font-family: inherit;
    transition: background 0.15s;
  }

  .pill {
    padding: 0.35rem 0.7rem;
    border-radius: var(--radius-pill);
    background: var(--accent-bg);
    color: var(--accent);
  }
  .pill:hover { background: color-mix(in oklab, var(--accent) 18%, transparent); }

  .ghost-pill {
    padding: 0.35rem 0.7rem;
    border-radius: var(--radius-pill);
    background: var(--bg-control);
    color: var(--text-primary);
  }
  .ghost-pill:hover { background: var(--bg-hover); }

  .tag-chip {
    display: inline-flex;
    padding: 0.35rem 0.7rem;
    border-radius: var(--radius-pill);
    background: var(--bg-control);
    color: var(--text-primary);
  }
  .tag-chip:hover {
    background: var(--bg-hover);
  }

  .rail-count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 1.3rem;
    height: 1.3rem;
    padding: 0 0.4rem;
    border-radius: var(--radius-pill);
    background: var(--bg-control);
    color: var(--text-secondary);
    font-size: 0.75rem;
    font-weight: 600;
  }

  @media (max-width: 1100px) {
    .workspace-body {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 900px) {
    .app-body {
      grid-template-columns: 1fr;
    }
    aside {
      order: 2;
      border-right: 0;
      border-top: 1px solid var(--border-subtle);
    }
  }
</style>
