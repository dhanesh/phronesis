<script>
  import { onMount } from 'svelte';
  import Editor from './lib/Editor.svelte';

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

  onMount(async () => {
    await loadSession();
    if (authenticated) {
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
    const res = await fetch('/api/pages');
    const data = await res.json();
    pages = data.pages ?? [];
  }

  async function loadPage(name) {
    pageName = (name || defaultPage).toLowerCase();
    const res = await fetch(`/api/pages/${encodeURIComponent(pageName)}`);
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
    source = new EventSource(`/api/pages/${encodeURIComponent(pageName)}/events`);
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
    const res = await fetch(`/api/pages/${encodeURIComponent(pageName)}`, {
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
    <section class="login-card">
      <p class="eyebrow">phronesis</p>
      <h1>Project knowledge without the ticket sprawl</h1>
      <p class="lede">Sign in to browse and edit Markdown pages with autosave and a single document surface.</p>
      {#if bootError}<p class="error">{bootError}</p>{/if}
      <label>
        Username
        <input bind:value={username} autocomplete="username" />
      </label>
      <label>
        Password
        <input bind:value={password} type="password" autocomplete="current-password" />
      </label>
      <button onclick={login}>Sign in</button>
      {#if loginError}<p class="error">{loginError}</p>{/if}
    </section>
  </main>
{:else}
  <main class="app-shell">
    <aside class:closed={!sidebarOpen}>
      <div class="sidebar-head">
        <div>
          <p class="eyebrow">Workspace</p>
          <h2>Pages</h2>
        </div>
        <button class="ghost" onclick={() => (sidebarOpen = !sidebarOpen)}>{sidebarOpen ? 'Hide' : 'Show'}</button>
      </div>
      <div class="page-jump">
        <input bind:value={pageName} placeholder="wiki/url" />
        <button onclick={() => loadPage(pageName)}>Open</button>
      </div>
      <nav>
        {#each pages as entry (entry.name)}
          <button class:selected={entry.name === page.name} class="nav-link" onclick={() => loadPage(entry.name)}>
            <span>{entry.name}</span>
          </button>
        {/each}
      </nav>
      <button class="logout" onclick={logout}>Sign out</button>
    </aside>

    <section class="workspace">
      <header class="workspace-head">
        <div>
          <p class="eyebrow">/{page.name}</p>
          <h1>{page.name}</h1>
          <p class="lede small">Single-surface editor. Click into the page to edit; click wiki links to navigate.</p>
        </div>
        <div class="status-block">
          <span>{status}</span>
          {#if mergedNotice}<strong>{mergedNotice}</strong>{/if}
        </div>
      </header>

      <div class="workspace-body">
        <section class="editor-card">
          <div class="editor-head">
            <div>
              <p class="eyebrow">Document</p>
              <h2>Markdown Live Surface</h2>
            </div>
            <p class="editor-hint">Wiki links render inline until the cursor moves inside them.</p>
          </div>
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
  </main>
{/if}

<style>
  .center {
    display: grid;
    min-height: 100vh;
    place-items: center;
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
    border-radius: 24px;
    background: rgba(255, 252, 244, 0.92);
    border: 1px solid rgba(110, 97, 69, 0.18);
    box-shadow: 0 24px 80px rgba(71, 58, 27, 0.12);
  }

  .eyebrow {
    margin: 0 0 0.4rem;
    text-transform: uppercase;
    letter-spacing: 0.18em;
    font-size: 0.72rem;
    color: #6d644f;
  }

  h1, h2, h3, p {
    margin-top: 0;
  }

  .lede {
    color: #504835;
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
    color: #403724;
  }

  input {
    border: 1px solid #c8b895;
    border-radius: 14px;
    padding: 0.85rem 1rem;
    background: rgba(255,255,255,0.8);
  }

  button {
    border: 0;
    border-radius: 999px;
    padding: 0.75rem 1rem;
    background: #2d5b46;
    color: white;
    cursor: pointer;
  }

  .error {
    color: #8a2e2e;
    margin-top: 1rem;
  }

  .app-shell {
    display: grid;
    min-height: 100vh;
    grid-template-columns: 18rem minmax(0, 1fr);
  }

  aside {
    padding: 1.2rem;
    background: rgba(38, 49, 41, 0.96);
    color: #f4eee2;
    display: grid;
    grid-template-rows: auto auto 1fr auto;
    gap: 1rem;
  }

  aside.closed {
    grid-template-rows: auto;
  }

  .sidebar-head,
  .page-jump,
  .workspace-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }

  .page-jump input {
    flex: 1;
  }

  nav {
    display: grid;
    gap: 0.35rem;
    align-content: start;
    overflow: auto;
  }

  .nav-link,
  .ghost,
  .logout {
    justify-content: flex-start;
    background: transparent;
    color: inherit;
    border: 1px solid rgba(255,255,255,0.16);
  }

  .nav-link.selected {
    background: rgba(255,255,255,0.12);
  }

  .workspace {
    padding: 1.5rem;
    display: grid;
    grid-template-rows: auto 1fr;
    gap: 1rem;
  }

  .status-block {
    text-align: right;
    color: #5d5847;
  }

  .status-block strong {
    display: block;
    color: #854f1c;
  }

  .workspace-body {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 20rem;
    gap: 1rem;
    min-height: 0;
  }

  .editor-card,
  .rail-card {
    background: rgba(255,252,244,0.88);
    border: 1px solid rgba(110, 97, 69, 0.12);
    border-radius: 20px;
    box-shadow: 0 18px 48px rgba(71, 58, 27, 0.08);
  }

  .editor-card {
    display: grid;
    grid-template-rows: auto 1fr;
    min-height: 0;
  }

  .editor-head,
  .rail-card {
    padding: 1rem 1.1rem;
  }

  .editor-head {
    display: flex;
    justify-content: space-between;
    align-items: end;
    gap: 1rem;
    border-bottom: 1px solid rgba(110, 97, 69, 0.12);
  }

  .editor-hint {
    margin-bottom: 0;
    color: #5d5847;
    text-align: right;
    max-width: 18rem;
  }

  .editor-frame {
    min-height: 68vh;
    padding: 0 1rem 0 1.2rem;
  }

  .context-rail {
    display: grid;
    align-content: start;
    gap: 0.9rem;
    background: transparent;
    color: inherit;
    grid-template-rows: none;
    padding: 0;
  }

  .rail-card p:last-child {
    margin-bottom: 0;
    color: #5d5847;
  }

  .pill-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.55rem;
  }

  .pill,
  .tag-chip,
  .ghost-pill {
    font-size: 0.92rem;
  }

  .pill {
    padding: 0.5rem 0.8rem;
    background: rgba(31, 92, 70, 0.1);
    color: #1f5c46;
  }

  .ghost-pill {
    background: rgba(133, 79, 28, 0.08);
    color: #854f1c;
  }

  .tag-chip {
    display: inline-flex;
    padding: 0.45rem 0.7rem;
    border-radius: 999px;
    background: rgba(71, 58, 27, 0.08);
    color: #4f4635;
    border: 0;
    cursor: pointer;
    font: inherit;
    font-size: 0.92rem;
  }
  .tag-chip:hover {
    background: rgba(71, 58, 27, 0.16);
  }
  .rail-card h3 {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .rail-count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 1.4rem;
    height: 1.4rem;
    padding: 0 0.4rem;
    border-radius: 999px;
    background: rgba(110, 97, 69, 0.14);
    color: #5d5847;
    font-size: 0.78rem;
    font-weight: 600;
  }

  @media (max-width: 1100px) {
    .workspace-body {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 900px) {
    .app-shell {
      grid-template-columns: 1fr;
    }

    aside {
      order: 2;
    }

    .workspace-head {
      align-items: start;
      flex-direction: column;
    }
  }
</style>
