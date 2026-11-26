// Regression: a signed-out user who starts the OAuth authorize flow is
// bounced to /?next=<authorize url> by handleOAuthAuthorize. After signing
// in through the form the SPA must send them back to `next`, so the MCP
// client receives its authorization code. Before the fix the SPA ignored
// `next` and the flow dead-ended on the wiki.
import * as crypto from 'crypto';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { test as base, expect } from '@playwright/test';
import { startPhronesis } from '../fixtures/server';

// Not 4190: that port is on the WHATWG fetch "bad ports" list, so Node fetch refuses it.
const PORT = 4180;

const test = base.extend<{}, { oauthBaseURL: string }>({
  oauthBaseURL: [
    async ({}, use) => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'phronesis-oauth-next-'));
      const baseURL = `http://localhost:${PORT}`;
      const { stop } = await startPhronesis({
        port: PORT,
        pagesDir: dir,
        extra: {
          PHRONESIS_OAUTH_ENABLED: '1',
          PHRONESIS_OAUTH_ISSUER: baseURL,
          PHRONESIS_OAUTH_KEY_PATH: path.join(dir, 'oauth-key.pem'),
        },
      });
      await use(baseURL);
      await stop();
      fs.rmSync(dir, { recursive: true, force: true });
    },
    { scope: 'worker' },
  ],
});

test.describe('login honours ?next', () => {
  test('signed-out OAuth authorize returns to the client with a code after login', async ({ browser, oauthBaseURL }) => {
    const ctx = await browser.newContext({ baseURL: oauthBaseURL });
    const redirectURI = 'http://127.0.0.1:8976/callback';
    const reg = await ctx.request.post('/oauth/register', {
      data: { client_name: 'e2e', redirect_uris: [redirectURI] },
    });
    expect(reg.status()).toBe(201);
    const { client_id } = await reg.json();
    const verifier = crypto.randomBytes(48).toString('base64url');
    const challenge = crypto.createHash('sha256').update(verifier).digest('base64url');

    const page = await ctx.newPage();
    let callback: string | null = null;
    // page.route does not intercept the target of a server 302, so watch
    // requests instead; nothing listens on the callback port.
    page.on('request', (req) => {
      if (req.url().startsWith(redirectURI)) callback = req.url();
    });
    await page.goto(
      `/oauth/authorize?response_type=code&client_id=${encodeURIComponent(client_id)}` +
        `&redirect_uri=${encodeURIComponent(redirectURI)}&state=xyz` +
        `&code_challenge=${challenge}&code_challenge_method=S256`,
    );
    await page.getByLabel('Username').fill('admin');
    await page.getByLabel('Password').fill('admin123');
    await page.getByRole('button', { name: 'Sign in' }).click();

    // Consent step (finding mcp-oauth): no code until the user approves.
    const consent = page.getByRole('dialog', { name: 'Authorize application' });
    await expect(consent).toBeVisible();
    await expect(page.getByTestId('consent-client')).toHaveText('e2e');
    await expect(page.getByTestId('consent-redirect')).toContainText('127.0.0.1:8976');
    expect(callback).toBeNull();
    await page.getByTestId('consent-approve').click();

    await expect.poll(() => callback, { timeout: 10_000 }).not.toBeNull();
    const url = new URL(callback!);
    expect(url.searchParams.get('code')).toMatch(/^phr_code_/);
    expect(url.searchParams.get('state')).toBe('xyz');
    await ctx.close();
  });

  test('deny at the consent step returns access_denied and no code', async ({ browser, oauthBaseURL }) => {
    const ctx = await browser.newContext({ baseURL: oauthBaseURL });
    const redirectURI = 'http://127.0.0.1:8976/callback';
    const { client_id } = await (
      await ctx.request.post('/oauth/register', { data: { client_name: 'e2e-deny', redirect_uris: [redirectURI] } })
    ).json();
    expect((await ctx.request.post('/api/login', { data: { username: 'admin', password: 'admin123' } })).ok()).toBeTruthy();
    const page = await ctx.newPage();
    let callback: string | null = null;
    page.on('request', (req) => {
      if (req.url().startsWith(redirectURI)) callback = req.url();
    });
    await page.goto(
      `/oauth/authorize?response_type=code&client_id=${encodeURIComponent(client_id)}` +
        `&redirect_uri=${encodeURIComponent(redirectURI)}&state=d1` +
        `&code_challenge=${'a'.repeat(43)}&code_challenge_method=S256`,
    );
    await page.getByTestId('consent-deny').click();
    await expect.poll(() => callback, { timeout: 10_000 }).not.toBeNull();
    const url = new URL(callback!);
    expect(url.searchParams.get('error')).toBe('access_denied');
    expect(url.searchParams.get('code')).toBeNull();
    await ctx.close();
  });

  test('an off-site next is ignored (no open redirect)', async ({ browser, oauthBaseURL }) => {
    const ctx = await browser.newContext({ baseURL: oauthBaseURL });
    const page = await ctx.newPage();
    let leaked = false;
    await page.route('https://evil.example/**', (route) => {
      leaked = true;
      return route.fulfill({ status: 200, body: 'evil' });
    });
    await page.goto('/?next=' + encodeURIComponent('//evil.example/steal'));
    await page.getByLabel('Username').fill('admin');
    await page.getByLabel('Password').fill('admin123');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.locator('.cm-content')).toBeVisible();
    expect(leaked).toBe(false);
    expect(new URL(page.url()).origin).toBe(oauthBaseURL);
    await ctx.close();
  });
});
