// End-to-end smoke test for a deployed phronesis + Dex (Compose or Helm).
// Drives the real user path in a browser: SSO through Dex, RBAC, editing,
// API keys on the HTTP API. Prints one JSON object; exit 0 = all passed.
//
//   node deploy/smoke/smoke.mjs <base-url> <admin-password> [phase]
//
// phase "full" (default) runs everything and writes a marker page;
// phase "persist" checks the marker page, the promoted role and the key
// survived a restart (run it after recreating the pod/container).
import { createRequire } from 'node:module';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.join(here, '..', '..', 'frontend', 'package.json'));
const { chromium } = require('@playwright/test');

const [BASE, ADMIN_PW, PHASE = 'full'] = process.argv.slice(2);
if (!BASE || !ADMIN_PW) {
  console.error('usage: smoke.mjs <base-url> <admin-password> [full|persist]');
  process.exit(2);
}
const checks = [];
const check = (name, ok, detail = '') => checks.push({ name, ok: !!ok, detail: String(detail ?? '') });
const MARKER = 'smoke-persist';

async function ssoLogin(browser, email, password) {
  const ctx = await browser.newContext({ baseURL: BASE });
  const page = await ctx.newPage();
  await page.goto('/');
  await page.getByTestId('sso-signin').click();
  // Dex local password form.
  await page.locator('#login').waitFor({ timeout: 15000 });
  await page.locator('#login').fill(email);
  await page.locator('#password').fill(password);
  await page.locator('#submit-login').click();
  await page.waitForURL((u) => u.toString().startsWith(BASE), { timeout: 15000 });
  await page.locator('.cm-content').waitFor({ timeout: 15000 });
  return { ctx, page };
}

async function adminCtx(browser) {
  const ctx = await browser.newContext({ baseURL: BASE });
  const r = await ctx.request.post('/api/login', { data: { username: 'admin', password: ADMIN_PW } });
  if (!r.ok()) throw new Error(`admin login ${r.status()}`);
  return ctx;
}

async function full(browser) {
  const anon = await browser.newContext({ baseURL: BASE });
  const sess0 = await (await anon.request.get('/api/session')).json();
  check('sso_advertised', sess0.sso === true, JSON.stringify(sess0));
  const health = await anon.request.get('/api/readyz');
  check('readyz_ok', health.ok(), health.status());

  // 1. A new SSO user is a viewer with a read-only editor.
  const { ctx: viewer, page } = await ssoLogin(browser, 'viewer@example.com', 'viewer-pass');
  const s1 = await (await viewer.request.get('/api/session')).json();
  check('sso_login_authenticated', s1.authenticated === true, JSON.stringify(s1));
  check('sso_user_is_viewer', s1.role === 'viewer', s1.role);
  check('display_name_not_opaque_sub', s1.display_name === 'viewer', s1.display_name);
  check('whoami_shows_name', (await page.getByTestId('whoami').textContent())?.includes('viewer'));
  check('readonly_banner_for_viewer', await page.getByTestId('readonly-banner').isVisible());
  const denied = await viewer.request.post('/api/pages/smoke-viewer', { data: { content: 'x', baseVersion: 0 } });
  check('viewer_write_403', denied.status() === 403, denied.status());

  // 2. The password admin promotes the SSO user to editor.
  const admin = await adminCtx(browser);
  const users = (await (await admin.request.get('/api/admin/users')).json()).users ?? [];
  const u = users.find((x) => x.email === 'viewer@example.com');
  check('sso_user_projected', !!u, users.length);
  const promote = await admin.request.post(`/api/admin/users/${u?.id}/role`, { data: { role: 'editor' } });
  check('promote_to_editor', promote.ok(), promote.status());

  // 3. After reload the user can edit; autosave persists.
  await page.reload();
  await page.locator('.cm-content').waitFor();
  check('editor_no_banner', (await page.getByTestId('readonly-banner').count()) === 0);
  const name = `smoke-edit-${Date.now()}`;
  await page.goto(`/w/${name}`);
  await page.locator('.cm-content').click();
  await page.keyboard.type('Edited through SSO');
  let saved = '';
  for (let i = 0; i < 40 && !saved.includes('Edited through SSO'); i++) {
    await page.waitForTimeout(250);
    saved = (await (await viewer.request.get(`/api/pages/${name}`)).json()).page?.content ?? '';
  }
  check('editor_autosave_persists', saved.includes('Edited through SSO'), saved.slice(0, 80));

  // 4. API keys work on the HTTP API (request -> approve).
  const req = await viewer.request.post('/api/keys/requests', { data: { scope: 'write', label: 'smoke' } });
  check('key_request_201', req.status() === 201, req.status());
  const reqId = (await req.json()).request?.id;
  const appr = await admin.request.post(`/api/admin/keys/requests/${reqId}/approve`);
  const key = (await appr.json()).key?.plaintext;
  check('key_approved', appr.status() === 201 && key?.startsWith('phr_live_'), appr.status());
  const bare = await browser.newContext({ baseURL: BASE });
  const H = { Authorization: `Bearer ${key}` };
  check('key_reads_api', (await bare.request.get('/api/pages', { headers: H })).ok());
  const kw = await bare.request.post(`/api/pages/${MARKER}`, { headers: H, data: { content: '# persisted by smoke\n', baseVersion: 0 } });
  check('key_writes_marker_page', kw.ok(), kw.status());

  // 5. A second SSO user also starts as viewer (no auto-admin).
  const { ctx: second } = await ssoLogin(browser, 'admin@example.com', 'admin-pass');
  const s2 = await (await second.request.get('/api/session')).json();
  check('second_sso_user_is_viewer', s2.role === 'viewer', s2.role);
  return { key };
}

async function persist(browser) {
  const admin = await adminCtx(browser);
  const page = await (await admin.request.get(`/api/pages/${MARKER}`)).json();
  check('marker_page_survives_restart', page.page?.content?.includes('persisted by smoke'), page.page?.content);
  const users = (await (await admin.request.get('/api/admin/users')).json()).users ?? [];
  const u = users.find((x) => x.email === 'viewer@example.com');
  check('promoted_role_survives_restart', u?.role === 'editor', u?.role);
  check('api_key_survives_restart', (u?.active_key_count ?? 0) >= 1, u?.active_key_count);
  const { ctx } = await ssoLogin(browser, 'viewer@example.com', 'viewer-pass');
  const s = await (await ctx.request.get('/api/session')).json();
  check('sso_after_restart_keeps_editor', s.role === 'editor', s.role);
}

const browser = await chromium.launch();
try {
  if (PHASE === 'persist') await persist(browser);
  else await full(browser);
} catch (e) {
  check('no_exception', false, e?.message ?? e);
} finally {
  await browser.close();
}
const ok = checks.every((c) => c.ok);
console.log(JSON.stringify({ base: BASE, phase: PHASE, ok, checks }, null, 1));
process.exit(ok ? 0 : 1);
