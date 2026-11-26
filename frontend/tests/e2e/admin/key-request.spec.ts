// TN7 request -> approve flow (decision 2026-11-27): an OIDC user asks for
// an API key from /settings/keys/request, the admin approves it and gets
// the plaintext once. Also guards OIDC RBAC: a new OIDC user is a viewer.
import { createHmac } from 'crypto';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { test as base, expect } from '@playwright/test';
import { startPhronesis } from '../fixtures/server';

const PORT = 4210;
const SECRET = 'e2e-key-request-secret';

function oidcToken(sub: string): string {
  const enc = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');
  const now = Math.floor(Date.now() / 1000);
  const head = enc({ alg: 'HS256', typ: 'JWT', kid: 'stub-1' });
  const body = enc({ iss: 'e2e-iss', aud: 'e2e-aud', iat: now, exp: now + 3600, sub, email: `${sub}@example.com`, name: sub });
  const sig = createHmac('sha256', SECRET).update(`${head}.${body}`).digest('base64url');
  return `${head}.${body}.${sig}`;
}

const test = base.extend<{}, { krBaseURL: string }>({
  krBaseURL: [
    async ({}, use) => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'phronesis-key-request-'));
      const baseURL = `http://localhost:${PORT}`;
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
      await use(baseURL);
      await stop();
      fs.rmSync(dir, { recursive: true, force: true });
    },
    { scope: 'worker' },
  ],
});

test('OIDC user requests a key; admin approves and gets the plaintext once', async ({ browser, krBaseURL }) => {
  const user = await browser.newContext({ baseURL: krBaseURL });
  const login = await user.request.post('/api/auth/oidc/login', { data: { id_token: oidcToken('erin') } });
  expect(login.ok()).toBeTruthy();
  const session = await (await user.request.get('/api/session')).json();
  expect(session.role).toBe('viewer');

  const page = await user.newPage();
  await page.goto('/settings/keys/request');
  await expect(page.getByRole('dialog', { name: 'Request an API key' })).toBeVisible();
  await page.getByTestId('kr-scope').selectOption('write');
  await page.getByTestId('kr-label').fill('e2e laptop');
  await page.getByTestId('kr-submit').click();
  await expect(page.getByTestId('kr-info')).toBeVisible();
  await expect(page.getByTestId('kr-row').first()).toContainText('pending');

  const admin = await browser.newContext({ baseURL: krBaseURL });
  expect((await admin.request.post('/api/login', { data: { username: 'admin', password: 'admin123' } })).ok()).toBeTruthy();
  const pending = await (await admin.request.get('/api/admin/keys/requests')).json();
  const req = pending.requests.find((r: { requested_label: string }) => r.requested_label === 'e2e laptop');
  expect(req?.requested_scope).toBe('write');
  const appr = await admin.request.post(`/api/admin/keys/requests/${req.id}/approve`);
  expect(appr.status()).toBe(201);
  expect((await appr.json()).key.plaintext).toMatch(/^phr_live_/);

  await page.reload();
  await expect(page.getByTestId('kr-row').first()).toContainText('approved');
  await user.close();
  await admin.close();
});
