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
import * as crypto from 'node:crypto';
import * as fs from 'node:fs';
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
// OUT_DIR (optional): save screenshots there.
const SHOTS = process.env.OUT_DIR || '';
const shot = async (page, name) => {
  if (SHOTS) await page.screenshot({ path: path.join(SHOTS, name), fullPage: false }).catch(() => {});
};

async function ssoLogin(browser, email, password) {
  const ctx = await browser.newContext({ baseURL: BASE });
  const page = await ctx.newPage();
  await page.goto('/');
  await page.getByTestId('sso-signin').waitFor();
  await shot(page, 'signin-page.png');
  await page.getByTestId('sso-signin').click();
  // Dex local password form.
  await page.locator('#login').waitFor({ timeout: 15000 });
  await shot(page, 'dex-login.png');
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
  await shot(page, 'viewer-after-sso.png');
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

  await shot(page, 'editor-after-promotion.png');

  // 3b. An MCP agent through OAuth: the SSO user authorizes a client in the
  // browser (consent), the agent writes a page; demoting the user takes
  // write away from the token already issued.
  const redirectURI = 'http://127.0.0.1:8976/callback';
  const reg = await (await browser.newContext({ baseURL: BASE })).request.post('/oauth/register', {
    data: { client_name: 'smoke-agent', redirect_uris: [redirectURI] },
  });
  const client = await reg.json();
  check('mcp_client_registered', reg.status() === 201, reg.status());
  const verifier = crypto.randomBytes(48).toString('base64url');
  const challenge = crypto.createHash('sha256').update(verifier).digest('base64url');
  let callback = null;
  page.on('request', (req) => { if (req.url().startsWith(redirectURI)) callback = req.url(); });
  await page.goto(`/oauth/authorize?response_type=code&client_id=${encodeURIComponent(client.client_id)}` +
    `&redirect_uri=${encodeURIComponent(redirectURI)}&state=s1&scope=write` +
    `&code_challenge=${challenge}&code_challenge_method=S256`);
  await page.getByTestId('consent-approve').waitFor({ timeout: 10000 });
  await shot(page, 'consent.png');
  check('consent_shows_client', (await page.getByTestId('consent-client').textContent()) === 'smoke-agent');
  await page.getByTestId('consent-approve').click();
  for (let i = 0; i < 50 && !callback; i++) await page.waitForTimeout(200);
  const code = callback ? new URL(callback).searchParams.get('code') : null;
  check('consent_returns_code', !!code, callback);
  const agent = await browser.newContext({ baseURL: BASE });
  const tokRes = await agent.request.post('/oauth/token', {
    form: { grant_type: 'authorization_code', code: code ?? '', code_verifier: verifier, client_id: client.client_id, redirect_uri: redirectURI },
  });
  const access = (await tokRes.json()).access_token;
  check('oauth_token_issued', tokRes.ok() && !!access, tokRes.status());
  const tool = async (name, args) => {
    const r = await agent.request.post('/mcp', {
      headers: { Authorization: `Bearer ${access}`, Accept: 'application/json, text/event-stream' },
      data: { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } },
    });
    const j = await r.json().catch(() => ({}));
    return j.error ? { error: j.error.message } : JSON.parse(j.result?.content?.[0]?.text ?? '{}');
  };
  const agentPage = `smoke-agent-${Date.now()}`;
  const w1 = await tool('write_page', { name: agentPage, content: '# Written by an MCP agent\n' });
  check('mcp_agent_writes_page', !w1.error && w1.version >= 1, JSON.stringify(w1));
  const r1 = await tool('read_page', { name: agentPage });
  check('mcp_agent_reads_page', (r1.content ?? '').includes('MCP agent'), JSON.stringify(r1).slice(0, 100));
  await admin.request.post(`/api/admin/users/${u?.id}/role`, { data: { role: 'viewer' } });
  const w2 = await tool('write_page', { name: agentPage, content: 'should not land' });
  check('demotion_limits_issued_token', (w2.error ?? '').includes('forbidden'), JSON.stringify(w2));
  await admin.request.post(`/api/admin/users/${u?.id}/role`, { data: { role: 'editor' } });

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
  const p2 = await second.newPage();
  await p2.goto('/');
  await p2.getByTestId('whoami').waitFor();
  await shot(p2, 'whoami-viewer.png');
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
