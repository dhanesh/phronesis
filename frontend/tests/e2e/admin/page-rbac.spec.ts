// Decision 2026-11-27: RBAC is enforced on pages; API keys work on the HTTP
// API like sessions. A new OIDC user is a viewer: the editor is read-only
// with a banner until an admin promotes them in the Users panel.
import { createHmac } from 'crypto';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { test as base, expect, type Page } from '@playwright/test';
import { startPhronesis } from '../fixtures/server';

const PORT = 4230;
const SECRET = 'e2e-page-rbac-secret';

function oidcToken(sub: string): string {
  const enc = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');
  const now = Math.floor(Date.now() / 1000);
  const head = enc({ alg: 'HS256', typ: 'JWT', kid: 'stub-1' });
  const body = enc({ iss: 'e2e-iss', aud: 'e2e-aud', iat: now, exp: now + 3600, sub, email: `${sub}@example.com`, name: sub });
  const sig = createHmac('sha256', SECRET).update(`${head}.${body}`).digest('base64url');
  return `${head}.${body}.${sig}`;
}

const test = base.extend<{}, { rbacURL: string }>({
  rbacURL: [
    async ({}, use) => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'phronesis-page-rbac-'));
      const { stop } = await startPhronesis({
        port: PORT,
        pagesDir: dir,
        extra: {
          PHRONESIS_OIDC_ENABLED: '1',
          PHRONESIS_OIDC_SECRET: SECRET,
          PHRONESIS_OIDC_ISSUER: 'e2e-iss',
          PHRONESIS_OIDC_AUDIENCE: 'e2e-aud',
        },
      });
      await use(`http://localhost:${PORT}`);
      await stop();
      fs.rmSync(dir, { recursive: true, force: true });
    },
    { scope: 'worker' },
  ],
});

async function openUsers(page: Page) {
  await expect(page.locator('.top-bar')).toBeVisible({ timeout: 10000 });
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  await expect(page.getByPlaceholder(/type a page name|page name or command/i).first()).toBeVisible();
  await page.keyboard.type('users');
  await page.getByText(/manage users/i).first().click();
  await expect(page.locator('[aria-label="Manage users"]')).toBeVisible();
}

test('viewer is read-only until an admin promotes them; keys work on /api/pages', async ({ browser, rbacURL }) => {
  const user = await browser.newContext({ baseURL: rbacURL });
  expect((await user.request.post('/api/auth/oidc/login', { data: { id_token: oidcToken('rita') } })).ok()).toBeTruthy();

  const page = await user.newPage();
  await page.goto('/');
  await expect(page.getByTestId('readonly-banner')).toContainText('viewer');
  await expect(page.locator('.cm-content')).toHaveAttribute('contenteditable', 'false');
  const denied = await user.request.post('/api/pages/home', { data: { content: 'x', baseVersion: 0 } });
  expect(denied.status()).toBe(403);

  // Admin promotes rita to editor from the Users panel.
  const admin = await browser.newContext({ baseURL: rbacURL });
  const adminPage = await admin.newPage();
  await adminPage.goto('/');
  await adminPage.getByLabel('Username').fill('admin');
  await adminPage.getByLabel('Password').fill('admin123');
  await adminPage.getByRole('button', { name: 'Sign in' }).click();
  await expect(adminPage.locator('.cm-content')).toBeVisible();
  await openUsers(adminPage);
  const row = adminPage.getByTestId('users-row').filter({ hasText: 'rita' });
  await row.getByTestId('users-role').selectOption('editor');
  await expect(row.getByTestId('users-role')).toHaveValue('editor');

  // Rita reloads: editable, no banner, and an edit autosaves.
  await page.reload();
  await expect(page.getByTestId('readonly-banner')).toHaveCount(0);
  await expect(page.locator('.cm-content')).toHaveAttribute('contenteditable', 'true');
  await page.locator('.cm-content').click();
  await page.keyboard.type(' rbac-edit');
  await expect.poll(async () => (await (await user.request.get('/api/pages/home')).json()).page.content, { timeout: 10_000 }).toContain('rbac-edit');

  // Keys on the HTTP API: request read + write, admin approves.
  const keys: Record<string, string> = {};
  for (const scope of ['read', 'write']) {
    const r = await user.request.post('/api/keys/requests', { data: { scope, label: `e2e-${scope}` } });
    expect(r.status()).toBe(201);
    const id = (await r.json()).request.id;
    const a = await admin.request.post(`/api/admin/keys/requests/${id}/approve`);
    expect(a.status()).toBe(201);
    keys[scope] = (await a.json()).key.plaintext;
  }
  const bare = await browser.newContext({ baseURL: rbacURL, storageState: { cookies: [], origins: [] } });
  const auth = (k: string) => ({ Authorization: `Bearer ${k}` });
  expect((await bare.request.get('/api/pages', { headers: auth(keys.read) })).status()).toBe(200);
  expect((await bare.request.post('/api/pages/home', { headers: auth(keys.read), data: { content: 'r', baseVersion: 0 } })).status()).toBe(403);
  const page0 = await (await bare.request.get('/api/pages/home', { headers: auth(keys.write) })).json();
  const wr = await bare.request.post('/api/pages/home', {
    headers: auth(keys.write),
    data: { content: page0.page.content + '\nfrom-key', baseVersion: page0.page.version },
  });
  expect(wr.status()).toBe(200);

  await Promise.all([user.close(), admin.close(), bare.close()]);
});
