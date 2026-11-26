#!/usr/bin/env node
// verify-phronesis drive harness. Drives a running instance (started by
// launch.sh) the way a user does, and prints one JSON object on stdout.
//
//   node drive.mjs <feature> <instance> [--headed]
//
// Exit 0 = every check passed; 1 = a check failed (see "checks");
// 2 = usage / instance not running.
//
// Screenshots and captured bodies land in .verify-run/<instance>/artifacts/
// and are listed in "artifacts" so the caller can pass them to
// verify_evidence.py record --artifact.
import { createRequire } from 'node:module';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as crypto from 'node:crypto';

const ROOT = execFileSync('git', ['rev-parse', '--show-toplevel']).toString().trim();
// Resolve Playwright from the repo's frontend/node_modules (npm ci there).
const { chromium } = createRequire(path.join(ROOT, 'frontend', 'package.json'))('@playwright/test');

const [feature, instance, ...rest] = process.argv.slice(2);
const headed = rest.includes('--headed');
if (!feature || !instance) {
  console.error('usage: node drive.mjs <feature> <instance> [--headed]');
  process.exit(2);
}
const RUN = path.join(ROOT, '.verify-run', instance);
if (!fs.existsSync(path.join(RUN, 'port'))) {
  console.error(`instance ${instance} is not running; run launch.sh ${instance} first`);
  process.exit(2);
}
const PORT = fs.readFileSync(path.join(RUN, 'port'), 'utf8').trim();
const BASE = `http://127.0.0.1:${PORT}`;
const DATA = path.join(RUN, 'data');
const ART = path.join(RUN, 'artifacts', feature);
fs.mkdirSync(ART, { recursive: true });
const USER = 'admin';
const PASS = 'verify-admin-pw';
const stamp = Date.now().toString(36);

const checks = [];
const artifacts = [];
const check = (name, ok, detail = '') => checks.push({ name, ok: !!ok, detail: String(detail).slice(0, 400) });
const save = (name, body) => {
  const p = path.join(ART, name);
  fs.writeFileSync(p, typeof body === 'string' ? body : JSON.stringify(body, null, 2));
  artifacts.push(p);
  return p;
};
const shot = async (page, name) => {
  const p = path.join(ART, name);
  await page.screenshot({ path: p, fullPage: true });
  artifacts.push(p);
};

// UI login through the real form (labels from App.svelte).
async function uiLogin(page) {
  await page.getByLabel('Username').fill(USER);
  await page.getByLabel('Password').fill(PASS);
  await page.getByRole('button', { name: 'Sign in' }).click();
}

async function apiLoginContext(browser) {
  const ctx = await browser.newContext({ baseURL: BASE });
  const res = await ctx.request.post('/api/login', { data: { username: USER, password: PASS } });
  if (!res.ok()) throw new Error(`api login failed: ${res.status()}`);
  return ctx;
}

// OIDC sign-in with the HMAC stub (instance launched with --oidc).
function oidcToken(sub) {
  const enc = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
  const now = Math.floor(Date.now() / 1000);
  const head = enc({ alg: 'HS256', typ: 'JWT', kid: 'stub-1' });
  const body = enc({ iss: 'verify-iss', aud: 'verify-aud', iat: now, exp: now + 3600, sub, email: `${sub}@example.com`, name: sub });
  const sig = crypto.createHmac('sha256', 'verify-oidc-secret').update(`${head}.${body}`).digest('base64url');
  return `${head}.${body}.${sig}`;
}

async function oidcContext(browser, sub) {
  const ctx = await browser.newContext({ baseURL: BASE });
  const res = await ctx.request.post('/api/auth/oidc/login', { data: { id_token: oidcToken(sub) } });
  if (!res.ok()) throw new Error(`oidc login failed: ${res.status()} ${await res.text()}`);
  return ctx;
}

const features = {
  // auth-login: sign in through the form, see the editor, sign out.
  async 'auth-login'(browser) {
    const ctx = await browser.newContext({ baseURL: BASE });
    const page = await ctx.newPage();
    await page.goto('/');
    await page.getByRole('button', { name: 'Sign in' }).waitFor();
    // Wrong password first: must show an error and stay on the form.
    await page.getByLabel('Username').fill(USER);
    await page.getByLabel('Password').fill('wrong-password');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.locator('.login-card .error').waitFor({ timeout: 5000 }).catch(() => {});
    check('bad_password_rejected', await page.locator('.login-card .error').isVisible());
    await page.getByLabel('Password').fill(PASS);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.locator('.cm-content').waitFor({ timeout: 10000 });
    check('editor_visible_after_login', await page.locator('.cm-content').isVisible());
    const sess = await (await ctx.request.get('/api/session')).json();
    save('session-after-login.json', sess);
    check('session_authenticated', sess.authenticated === true, JSON.stringify(sess));
    await shot(page, 'logged-in.png');
    const out = await ctx.request.post('/api/logout');
    const sess2 = await (await ctx.request.get('/api/session')).json();
    check('logout_clears_session', out.ok() && sess2.authenticated === false, JSON.stringify(sess2));
    const denied = await ctx.request.get('/api/pages');
    check('api_rejects_after_logout', denied.status() === 401, denied.status());
  },

  // page-edit-autosave: type into the editor, autosave writes the .md file.
  async 'page-edit-autosave'(browser) {
    const ctx = await apiLoginContext(browser);
    const page = await ctx.newPage();
    const name = `verify-edit-${stamp}`;
    await page.goto(`/w/${name}`);
    const editor = page.locator('.cm-content');
    await editor.waitFor();
    await editor.click();
    const text = `Autosave proof ${stamp}`;
    await page.keyboard.type(text);
    const file = path.join(DATA, 'pages', 'default', `${name}.md`);
    let disk = '';
    for (let i = 0; i < 50 && !disk.includes(text); i++) {
      await page.waitForTimeout(200);
      disk = fs.existsSync(file) ? fs.readFileSync(file, 'utf8') : '';
    }
    check('md_file_written', disk.includes(text), file);
    save('disk-content.md', disk);
    const api = await (await ctx.request.get(`/api/pages/${name}`)).json();
    check('api_returns_typed_text', api.page?.content?.includes(text));
    await page.reload();
    await editor.waitFor();
    // The editor mounts before the page body loads; wait for the text.
    let shown = '';
    for (let i = 0; i < 25 && !shown.includes(text); i++) {
      shown = await editor.innerText();
      if (!shown.includes(text)) await page.waitForTimeout(200);
    }
    check('text_survives_reload', shown.includes(text));
    await shot(page, 'after-reload.png');
  },

  // live-sse: a second browser sees an edit made in the first without reload.
  async 'live-sse'(browser) {
    const name = `verify-sse-${stamp}`;
    const c1 = await apiLoginContext(browser);
    const c2 = await apiLoginContext(browser);
    await c1.request.post(`/api/pages/${name}`, { data: { content: 'start\n', baseVersion: 0 } });
    const p1 = await c1.newPage();
    const p2 = await c2.newPage();
    await p1.goto(`/w/${name}`);
    await p2.goto(`/w/${name}`);
    await p1.locator('.cm-content').waitFor();
    await p2.locator('.cm-content').waitFor();
    await p1.locator('.cm-content').click();
    await p1.keyboard.press('End');
    const text = ` live-${stamp}`;
    await p1.keyboard.type(text);
    let seen = false;
    for (let i = 0; i < 50 && !seen; i++) {
      await p2.waitForTimeout(200);
      seen = (await p2.locator('.cm-content').innerText()).includes(text);
    }
    check('second_client_sees_edit', seen);
    await shot(p2, 'second-client.png');
    const disk = fs.readFileSync(path.join(DATA, 'pages', 'default', `${name}.md`), 'utf8');
    save('disk-content.md', disk);
    check('edit_persisted_to_disk', disk.includes(text.trim()));
  },

  // wiki-metadata: links, backlinks, tags and tasks are extracted.
  async 'wiki-metadata'(browser) {
    const ctx = await apiLoginContext(browser);
    const target = `verify-target-${stamp}`;
    const source = `verify-source-${stamp}`;
    await ctx.request.post(`/api/pages/${target}`, { data: { content: '# Target\n', baseVersion: 0 } });
    await ctx.request.post(`/api/pages/${source}`, {
      data: { content: `See [[${target}]] #verifytag\n\n- [ ] open task\n- [x] done task\n`, baseVersion: 0 },
    });
    const src = (await (await ctx.request.get(`/api/pages/${source}`)).json()).page;
    const tgt = (await (await ctx.request.get(`/api/pages/${target}`)).json()).page;
    save('source-render.json', src.render);
    save('target-render.json', tgt.render);
    check('links_extracted', (src.render.links ?? []).includes(target), JSON.stringify(src.render.links));
    check('tags_extracted', (src.render.tags ?? []).includes('verifytag'), JSON.stringify(src.render.tags));
    const tasks = src.render.tasks ?? [];
    check('tasks_extracted',
      tasks.length === 2
        && tasks[0].text === 'open task' && tasks[0].checked === false
        && tasks[1].text === 'done task' && tasks[1].checked === true,
      JSON.stringify(tasks));
    check('backlink_on_target', (tgt.render.backlinks ?? []).includes(source), JSON.stringify(tgt.render.backlinks));
    // Side effect: both pages are Markdown files on disk with the typed syntax.
    const pagesDir = path.join(DATA, 'pages', 'default');
    const srcFile = path.join(pagesDir, `${source}.md`);
    const srcDisk = fs.existsSync(srcFile) ? fs.readFileSync(srcFile, 'utf8') : '';
    save('source-disk.md', srcDisk);
    check('pages_on_disk',
      fs.existsSync(path.join(pagesDir, `${target}.md`))
        && srcDisk.includes('#verifytag') && srcDisk.includes('- [ ] open task'),
      pagesDir);
    // Shown in the browser on the source page: the tag chip in the rail
    // (rendered from render.tags) and the task checkboxes in the editor
    // (CodeMirror live preview; the UI does not render render.tasks).
    const srcPage = await ctx.newPage();
    await srcPage.goto(`/w/${source}`);
    await srcPage.locator('.cm-content').waitFor();
    const chip = srcPage.locator('button.tag-chip', { hasText: '#verifytag' });
    await chip.first().waitFor({ timeout: 5000 }).catch(() => {});
    check('tag_chip_in_ui', await chip.first().isVisible());
    const boxes = srcPage.locator('input.cm-md-task-checkbox');
    await boxes.nth(1).waitFor({ timeout: 5000 }).catch(() => {});
    const boxStates = await boxes.evaluateAll((els) => els.map((e) => e.checked));
    check('task_checkboxes_in_ui', JSON.stringify(boxStates) === '[false,true]', JSON.stringify(boxStates));
    await shot(srcPage, 'source-tags-tasks.png');
    const page = await ctx.newPage();
    await page.goto(`/w/${target}`);
    await page.locator('.cm-content').waitFor();
    const pill = page.locator('button.pill', { hasText: source });
    await pill.first().waitFor({ timeout: 5000 }).catch(() => {});
    check('backlink_pill_in_ui', await pill.first().isVisible());
    await shot(page, 'target-backlinks.png');
  },

  // live-preview: decorations render while the document text stays raw.
  async 'live-preview'(browser) {
    const ctx = await apiLoginContext(browser);
    const name = `verify-preview-${stamp}`;
    const content = '# Heading One\n\nSome **bold** and *em* and `code`.\n\n> quoted\n\n- item\n';
    await ctx.request.post(`/api/pages/${name}`, { data: { content, baseVersion: 0 } });
    const page = await ctx.newPage();
    await page.goto(`/w/${name}`);
    await page.locator('.cm-content').waitFor();
    for (const cls of ['cm-md-line-heading-1', 'cm-md-strong', 'cm-md-emphasis', 'cm-md-inline-code', 'cm-md-blockquote', 'cm-md-list-marker']) {
      const loc = page.locator(`.${cls}`).first();
      await loc.waitFor({ timeout: 5000 }).catch(() => {});
      check(`decoration_${cls}`, await loc.isVisible());
    }
    await shot(page, 'preview.png');
    const disk = fs.readFileSync(path.join(DATA, 'pages', 'default', `${name}.md`), 'utf8');
    check('document_text_not_mutated', disk === content);
  },

  // media-upload: upload an image, fetch it back, blob is on disk.
  async 'media-upload'(browser) {
    const ctx = await apiLoginContext(browser);
    // 1x1 transparent PNG.
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64');
    const res = await ctx.request.post('/media', { headers: { 'Content-Type': 'image/png' }, data: png });
    const body = await res.json().catch(() => ({}));
    save('upload-response.json', body);
    check('upload_accepted', res.ok(), res.status());
    const get = await ctx.request.get(body.url ?? '/media/missing');
    const bytes = await get.body();
    check('download_matches', get.ok() && Buffer.compare(bytes, png) === 0, get.status());
    const sha = crypto.createHash('sha256').update(png).digest('hex');
    const onDisk = execFileSync('find', [path.join(DATA, 'blobs'), '-type', 'f']).toString();
    save('blob-files.txt', onDisk);
    check('blob_on_disk', onDisk.includes(sha.slice(0, 16)), sha);
    const bad = await ctx.request.post('/media', { headers: { 'Content-Type': 'application/octet-stream' }, data: Buffer.from('MZ') });
    check('disallowed_type_rejected', !bad.ok(), bad.status());
  },

  // workspaces: admin creates a workspace, writes a page in it, deletes it.
  async 'workspaces'(browser) {
    const ctx = await apiLoginContext(browser);
    const slug = `verify-ws-${stamp}`;
    const create = await ctx.request.post('/api/admin/workspaces', { data: { slug, name: 'Verify WS' } });
    check('create_201', create.status() === 201, create.status());
    const write = await ctx.request.post(`/api/workspaces/${slug}/pages/probe`, { data: { content: '# In WS\n', baseVersion: 0 } });
    check('write_in_workspace', write.ok(), write.status());
    const file = path.join(DATA, 'pages', slug, 'probe.md');
    check('file_under_workspace_dir', fs.existsSync(file), file);
    const def = await ctx.request.get('/api/pages/probe');
    const defBody = await def.json();
    check('default_workspace_isolated', !defBody.page?.content?.includes('In WS'));
    const page = await ctx.newPage();
    await page.goto('/');
    await page.getByRole('combobox', { name: 'Switch workspace' }).waitFor({ timeout: 10000 }).catch(() => {});
    check('workspace_switcher_visible', await page.getByRole('combobox', { name: 'Switch workspace' }).isVisible().catch(() => false));
    await shot(page, 'shell.png');
    const delDefault = await ctx.request.delete('/api/admin/workspaces/default');
    check('default_not_deletable', delDefault.status() === 403, delDefault.status());
    const del = await ctx.request.delete(`/api/admin/workspaces/${slug}`);
    check('delete_204', del.status() === 204, del.status());
    const list = await (await ctx.request.get('/api/workspaces')).json();
    save('workspaces-after-delete.json', list);
    check('gone_from_list', !list.workspaces.some((w) => w.slug === slug));
  },

  // api-keys: an OIDC user requests a key through the form at
  // /settings/keys/request, the admin approves it and gets the plaintext
  // once, the key works as a bearer token, and revoking it stops it.
  // Needs an instance launched with --oidc --oauth (/mcp is the
  // service-account surface the key is used on).
  async 'api-keys'(browser) {
    const user = await oidcContext(browser, `keyuser-${stamp}`);
    const page = await user.newPage();
    await page.goto('/settings/keys/request');
    const dialog = page.getByRole('dialog', { name: 'Request an API key' });
    await dialog.waitFor({ timeout: 10000 });
    await page.getByTestId('kr-scope').selectOption('read');
    await page.getByTestId('kr-label').fill(`verify-${stamp}`);
    await page.getByTestId('kr-submit').click();
    await page.getByTestId('kr-info').waitFor({ timeout: 5000 }).catch(() => {});
    check('request_form_submitted', await page.getByTestId('kr-info').isVisible(),
      await page.getByTestId('kr-error').textContent().catch(() => ''));
    await page.getByTestId('kr-row').first().waitFor({ timeout: 5000 }).catch(() => {});
    check('own_request_listed_pending', (await page.getByTestId('kr-row').first().textContent().catch(() => '')).includes('pending'));
    await shot(page, 'request-form.png');

    const admin = await apiLoginContext(browser);
    const list = await (await admin.request.get('/api/admin/keys/requests')).json();
    save('admin-pending.json', list);
    const req = list.requests?.find((r) => r.requested_label === `verify-${stamp}`);
    check('admin_sees_pending_request', !!req, JSON.stringify(list).slice(0, 200));
    const appr = await admin.request.post(`/api/admin/keys/requests/${req?.id}/approve`);
    const apprBody = await appr.json().catch(() => ({}));
    const token = apprBody.key?.plaintext;
    check('approve_returns_plaintext_once', appr.status() === 201 && typeof token === 'string' && token.startsWith('phr_live_'), appr.status());
    const keys = await (await admin.request.get('/api/admin/keys')).json();
    save('keys-list.json', keys);
    check('plaintext_not_in_listing', !JSON.stringify(keys).includes(token ?? '<none>'));
    const keyId = apprBody.key?.id;

    const client = await browser.newContext({ baseURL: BASE });
    const call = () => client.request.post('/mcp', {
      headers: { Authorization: `Bearer ${token}`, Accept: 'application/json, text/event-stream' },
      data: { jsonrpc: '2.0', id: 1, method: 'tools/list' },
    });
    const use = await call();
    check('bearer_key_authenticates', use.ok(), use.status());
    // Keys work on the HTTP API like sessions (decision 2026-11-27),
    // bounded by scope (read) and workspace pin.
    const bearer = { Authorization: `Bearer ${token}` };
    const pagesWithKey = await client.request.get('/api/pages', { headers: bearer });
    save('pages-with-key.txt', `${pagesWithKey.status()} ${(await pagesWithKey.text()).slice(0, 200)}`);
    check('read_key_lists_pages_on_http_api', pagesWithKey.status() === 200, pagesWithKey.status());
    const writeWithRead = await client.request.post(`/api/pages/key-${stamp}`, { headers: bearer, data: { content: 'x', baseVersion: 0 } });
    check('read_key_cannot_write_pages', writeWithRead.status() === 403, writeWithRead.status());
    const otherWs = `keyws-${stamp}`;
    await admin.request.post('/api/admin/workspaces', { data: { slug: otherWs, name: 'Key WS' } });
    const crossWs = await client.request.get(`/api/workspaces/${otherWs}/pages`, { headers: bearer });
    check('key_pinned_to_its_workspace', crossWs.status() === 403, crossWs.status());
    const rev = await admin.request.post(`/api/admin/keys/${keyId}/revoke`);
    check('revoke_ok', rev.ok(), rev.status());
    let after = 0;
    for (let i = 0; i < 120; i++) {
      after = (await call()).status();
      if (after === 401) break;
      await new Promise((r) => setTimeout(r, 500));
    }
    check('revoked_key_rejected_within_60s', after === 401, after);
  },

  // oidc-login: an OIDC user signs in, is projected into users, and gets
  // its projected role (not admin). Needs an instance launched with --oidc.
  async 'oidc-login'(browser) {
    const ctx = await browser.newContext({ baseURL: BASE });
    const login = await ctx.request.post('/api/auth/oidc/login', { data: { id_token: oidcToken(`oidc-viewer-${stamp}`) } });
    check('oidc_login_200', login.ok(), login.status());
    const sess = await (await ctx.request.get('/api/session')).json();
    save('session.json', sess);
    check('session_authenticated', sess.authenticated === true);
    check('role_not_admin_without_claim', sess.role !== 'admin', `role=${sess.role}`);
    const ws = await ctx.request.post('/api/admin/workspaces', { data: { slug: `oidc-ws-${stamp}`, name: 'x' } });
    save('admin-post.txt', `${ws.status()} ${await ws.text()}`);
    check('admin_endpoint_forbidden', ws.status() === 403, ws.status());
    const admin = await apiLoginContext(browser);
    const users = await (await admin.request.get('/api/admin/users')).json();
    save('users.json', users);
    check('user_projected_into_users', (users.users ?? []).some((u) => u.email === `oidc-viewer-${stamp}@example.com`),
      `users=${(users.users ?? []).length}`);

    // Page RBAC (decision 2026-11-27): a viewer reads but cannot write.
    check('new_oidc_user_is_viewer', sess.role === 'viewer', `role=${sess.role}`);
    const page = await ctx.newPage();
    await page.goto('/');
    await page.getByTestId('readonly-banner').waitFor({ timeout: 10000 }).catch(() => {});
    check('viewer_sees_readonly_banner', await page.getByTestId('readonly-banner').isVisible());
    check('viewer_editor_not_editable', (await page.locator('.cm-content').getAttribute('contenteditable')) === 'false');
    await shot(page, 'viewer-readonly.png');
    const vw = await ctx.request.post(`/api/pages/rbac-${stamp}`, { data: { content: 'x', baseVersion: 0 } });
    check('viewer_write_forbidden', vw.status() === 403, vw.status());

    // The admin promotes the user to editor in the Users panel.
    const adminPage = await admin.newPage();
    await adminPage.goto('/');
    await adminPage.locator('.top-bar').waitFor({ timeout: 10000 });
    await adminPage.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
    await adminPage.keyboard.type('users');
    await adminPage.getByText(/manage users/i).first().click();
    const row = adminPage.getByTestId('users-row').filter({ hasText: `oidc-viewer-${stamp}` });
    await row.getByTestId('users-role').selectOption('editor');
    await adminPage.waitForTimeout(500);
    await shot(adminPage, 'users-role-picker.png');
    check('admin_promotes_to_editor_in_ui', (await row.getByTestId('users-role').inputValue()) === 'editor');
    await page.reload();
    await page.locator('.cm-content').waitFor({ timeout: 10000 });
    check('editor_editable_after_promotion', (await page.locator('.cm-content').getAttribute('contenteditable')) === 'true');
    const ew = await ctx.request.post(`/api/pages/rbac-${stamp}`, { data: { content: '# promoted', baseVersion: 0 } });
    check('editor_write_ok', ew.ok(), ew.status());
  },

  // page-delete-rename: an editor renames then deletes a page from the
  // command palette; the Markdown file moves, then disappears.
  async 'page-delete-rename'(browser) {
    const ctx = await apiLoginContext(browser);
    const name = `verify-dr-${stamp}`;
    const w = await ctx.request.post(`/api/pages/${name}`, { data: { content: '# Rename me\n', baseVersion: 0 } });
    check('page_created', w.ok(), w.status());
    const page = await ctx.newPage();
    await page.goto(`/w/${name}`);
    await page.locator('.cm-content').waitFor();
    const palette = async () => {
      await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
      await page.getByPlaceholder(/type a page name|page name or command/i).first().waitFor();
    };
    await palette();
    await page.keyboard.type(`${name}-renamed`);
    await shot(page, 'rename-command.png');
    await page.getByText(`Rename "${name}" to "${name}-renamed"`).click();
    const dir = path.join(DATA, 'pages', 'default');
    let moved = false;
    for (let i = 0; i < 25 && !moved; i++) {
      moved = fs.existsSync(path.join(dir, `${name}-renamed.md`)) && !fs.existsSync(path.join(dir, `${name}.md`));
      if (!moved) await page.waitForTimeout(200);
    }
    check('file_moved_on_disk', moved, dir);
    check('renamed_page_opened', page.url().includes(`${name}-renamed`) || (await page.locator('.cm-content').innerText()).includes('Rename me'));
    page.once('dialog', (d) => d.accept());
    await palette();
    await page.keyboard.type('delete');
    await page.getByText(`Delete page "${name}-renamed"`).click();
    let gone = false;
    for (let i = 0; i < 25 && !gone; i++) {
      gone = !fs.existsSync(path.join(dir, `${name}-renamed.md`));
      if (!gone) await page.waitForTimeout(200);
    }
    check('file_deleted_on_disk', gone);
    const list = await (await ctx.request.get('/api/pages')).json();
    save('pages-after.json', list);
    check('page_gone_from_list', !list.pages.some((p) => p.name.startsWith(name)));
  },

  // mcp-wiki-tools: an agent with an API key lists, reads, writes and
  // searches pages over /mcp, within its scope. Needs --oidc --oauth.
  async 'mcp-wiki-tools'(browser) {
    const admin = await apiLoginContext(browser);
    const user = await oidcContext(browser, `agent-owner-${stamp}`);
    const users = (await (await admin.request.get('/api/admin/users')).json()).users ?? [];
    const u = users.find((x) => x.email === `agent-owner-${stamp}@example.com`);
    await admin.request.post(`/api/admin/users/${u?.id}/role`, { data: { role: 'editor' } });
    const mint = async (scope) => {
      const r = await user.request.post('/api/keys/requests', { data: { scope, label: `mcp-${scope}-${stamp}` } });
      const id = (await r.json()).request?.id;
      const a = await admin.request.post(`/api/admin/keys/requests/${id}/approve`);
      return (await a.json()).key?.plaintext;
    };
    const readKey = await mint('read');
    const writeKey = await mint('write');
    const agent = await browser.newContext({ baseURL: BASE });
    const call = async (key, name, args, id = 1) => {
      const r = await agent.request.post('/mcp', {
        headers: { Authorization: `Bearer ${key}`, Accept: 'application/json, text/event-stream' },
        data: { jsonrpc: '2.0', id, method: 'tools/call', params: { name, arguments: args } },
      });
      const j = await r.json();
      return j.error ? { error: j.error.message } : JSON.parse(j.result.content[0].text);
    };
    const list = await agent.request.post('/mcp', {
      headers: { Authorization: `Bearer ${readKey}`, Accept: 'application/json, text/event-stream' },
      data: { jsonrpc: '2.0', id: 1, method: 'tools/list' },
    });
    const tools = ((await list.json()).result?.tools ?? []).map((t) => t.name);
    check('tools_listed', ['list_pages', 'read_page', 'write_page', 'search_pages'].every((t) => tools.includes(t)), tools.join(','));
    const name = `agent-note-${stamp}`;
    const wrote = await call(writeKey, 'write_page', { name, content: `# Agent note\nCodename ZEPHYR-${stamp}` });
    check('write_page_with_write_key', !wrote.error && wrote.version >= 1, JSON.stringify(wrote));
    const file = path.join(DATA, 'pages', 'default', `${name}.md`);
    check('markdown_file_written_by_agent', fs.existsSync(file) && fs.readFileSync(file, 'utf8').includes(`ZEPHYR-${stamp}`), file);
    const denied = await call(readKey, 'write_page', { name, content: 'x' });
    check('read_key_cannot_write', (denied.error ?? '').includes('forbidden'), JSON.stringify(denied));
    const read = await call(readKey, 'read_page', { name });
    check('read_page', (read.content ?? '').includes(`ZEPHYR-${stamp}`), JSON.stringify(read).slice(0, 120));
    const listed = await call(readKey, 'list_pages', {});
    check('list_pages', JSON.stringify(listed).includes(name));
    const found = await call(readKey, 'search_pages', { query: `zephyr-${stamp}` });
    check('search_pages', JSON.stringify(found).includes(name), JSON.stringify(found).slice(0, 160));
    save('mcp-tools-transcript.json', { tools, wrote, denied, read, listed, found });
    const ui = await admin.newPage();
    await ui.goto(`/w/${name}`);
    await ui.locator('.cm-content').waitFor();
    let txt = '';
    for (let i = 0; i < 25 && !txt.includes('ZEPHYR'); i++) { txt = await ui.locator('.cm-content').innerText(); if (!txt.includes('ZEPHYR')) await ui.waitForTimeout(200); }
    check('agent_edit_visible_in_ui', txt.includes(`ZEPHYR-${stamp}`));
    await shot(ui, 'agent-page-in-ui.png');
  },

  // mcp-oauth: a signed-out user completes OAuth (DCR + PKCE) in the
  // browser, the client exchanges the code, then calls /mcp.
  // Needs an instance launched with --oauth.
  async 'mcp-oauth'(browser) {
    const anon = await browser.newContext({ baseURL: BASE });
    const meta = await (await anon.request.get('/.well-known/oauth-authorization-server')).json();
    save('oauth-metadata.json', meta);
    check('discovery_metadata', meta.issuer === BASE && meta.code_challenge_methods_supported?.includes('S256'), meta.issuer);
    const redirectURI = 'http://127.0.0.1:8976/callback';
    const reg = await anon.request.post('/oauth/register', {
      data: { client_name: 'verify-phronesis', redirect_uris: [redirectURI] },
    });
    const client = await reg.json();
    check('dynamic_registration_201', reg.status() === 201, reg.status());
    const offsite = await anon.request.post('/oauth/register', {
      data: { client_name: 'offsite', redirect_uris: ['https://evil.example/cb'] },
    });
    check('anonymous_non_loopback_registration_refused', offsite.status() === 403, offsite.status());
    const verifier = crypto.randomBytes(48).toString('base64url');
    const challenge = crypto.createHash('sha256').update(verifier).digest('base64url');
    const authURL = `/oauth/authorize?response_type=code&client_id=${encodeURIComponent(client.client_id)}` +
      `&redirect_uri=${encodeURIComponent(redirectURI)}&state=st-${stamp}&scope=read` +
      `&code_challenge=${challenge}&code_challenge_method=S256`;
    // Signed-out browser: authorize must bounce to login, then come back.
    const page = await anon.newPage();
    let callback = null;
    // page.route does not intercept the target of a server 302; watch requests.
    page.on('request', (req) => { if (req.url().startsWith(redirectURI)) callback = req.url(); });
    await page.goto(authURL);
    await page.getByRole('button', { name: 'Sign in' }).waitFor({ timeout: 10000 });
    await uiLogin(page);
    // Consent step (finding mcp-oauth): nothing reaches the client until
    // the user approves on the consent page.
    const consent = page.getByRole('dialog', { name: 'Authorize application' });
    await consent.waitFor({ timeout: 10000 }).catch(() => {});
    check('consent_page_shown', await consent.isVisible(), page.url());
    check('no_code_before_consent', callback === null, callback);
    check('consent_names_client', (await page.getByTestId('consent-client').textContent().catch(() => '')) === 'verify-phronesis');
    await shot(page, 'consent.png');
    await page.getByTestId('consent-approve').click();
    for (let i = 0; i < 50 && !callback; i++) await page.waitForTimeout(200);
    save('callback-url.txt', callback ?? `no callback; page at ${page.url()}`);
    check('login_returns_to_client_with_code', !!callback && callback.includes('code='), callback ?? page.url());
    let code = callback ? new URL(callback).searchParams.get('code') : null;
    if (!code) {
      // Fallback so the remaining MCP checks still run: approve directly
      // now that the browser is signed in.
      const form = Object.fromEntries(new URL(authURL, BASE).searchParams);
      const r = await anon.request.post('/oauth/authorize', {
        form: { ...form, decision: 'approve' }, headers: { Accept: 'application/json', Origin: BASE },
      });
      const to = (await r.json().catch(() => ({}))).redirect_to;
      code = to ? new URL(to).searchParams.get('code') : null;
    }
    // The MCP client is a separate process with no browser cookies: a
    // session cookie outranks a bearer token in principalFromRequest.
    const mcpClient = await browser.newContext({ baseURL: BASE });
    const tok = await mcpClient.request.post('/oauth/token', {
      form: { grant_type: 'authorization_code', code, code_verifier: verifier, client_id: client.client_id, redirect_uri: redirectURI },
    });
    const tokBody = await tok.json();
    check('token_exchange', tok.ok() && !!tokBody.access_token, tok.status());
    const rpc = async (id, method, params) => {
      const r = await mcpClient.request.post('/mcp', {
        headers: { Authorization: `Bearer ${tokBody.access_token}`, Accept: 'application/json, text/event-stream' },
        data: { jsonrpc: '2.0', id, method, ...(params ? { params } : {}) },
      });
      return r.json();
    };
    const init = await rpc(1, 'initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'verify', version: '0' } });
    const tools = await rpc(2, 'tools/list');
    const echo = await rpc(3, 'tools/call', { name: 'echo', arguments: { message: `hi-${stamp}` } });
    save('mcp-transcript.json', { init, tools, echo });
    check('mcp_initialize', init.result?.serverInfo?.name === 'phronesis', JSON.stringify(init).slice(0, 200));
    check('mcp_tools_list', tools.result?.tools?.some((t) => t.name === 'echo'));
    check('mcp_echo_call', JSON.stringify(echo).includes(`hi-${stamp}`), JSON.stringify(echo).slice(0, 200));
    const noTok = await mcpClient.request.post('/mcp', { data: { jsonrpc: '2.0', id: 9, method: 'tools/list' } });
    check('mcp_rejects_without_token', noTok.status() === 401, noTok.status());
  },
};

const fn = features[feature];
if (!fn) {
  console.error(`unknown feature ${feature}; known: ${Object.keys(features).join(', ')}`);
  process.exit(2);
}
const browser = await chromium.launch({ headless: !headed });
let error = null;
try {
  await fn(browser);
} catch (e) {
  error = String(e?.stack ?? e).slice(0, 800);
  check('no_exception', false, error);
} finally {
  await browser.close();
}
const ok = checks.length > 0 && checks.every((c) => c.ok);
const result = { feature, instance, base_url: BASE, ok, checks, artifacts };
save('result.json', result);
console.log(JSON.stringify(result, null, 2));
process.exit(ok ? 0 : 1);
