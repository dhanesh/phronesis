// @constraint B1 - Coverage spans every currently-shipping surface (auth surface)
// @constraint S1 - No real production credentials in test code (uses fixture defaults)
// @constraint T8 - Web-first assertions throughout (no waitForSelector + toBe)
import { test, expect } from '../fixtures';

test.describe('password authentication', () => {
  test('user can log in with valid credentials and reach the wiki', async ({ page }) => {
    // page fixture already carries storageState (logged in). Verify session is active.
    const res = await page.request.get('/api/session');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.authenticated).toBe(true);
    expect(body.username).toBe('admin');
  });

  test('unauthenticated request to protected API is rejected with 401', async ({ browser }) => {
    // Use a fresh context WITHOUT storageState to test unauthenticated access.
    const ctx = await browser.newContext();
    const req = await ctx.request.get('/api/pages');
    expect(req.status()).toBe(401);
    await ctx.close();
  });

  test('user can log out and subsequent requests are rejected', async ({ page }) => {
    // Confirm we are logged in.
    let session = await page.request.get('/api/session');
    expect((await session.json()).authenticated).toBe(true);

    // Log out.
    const logout = await page.request.post('/api/logout');
    expect(logout.ok()).toBeTruthy();

    // After logout, protected API returns 401.
    const after = await page.request.get('/api/pages');
    expect(after.status()).toBe(401);
  });

  test('login with wrong password is rejected', async ({ browser, workerBaseURL }) => {
    const ctx = await browser.newContext();
    const res = await ctx.request.post(`${workerBaseURL}/api/login`, {
      data: { username: 'admin', password: 'wrong-password' },
    });
    expect(res.ok()).toBeFalsy();
    await ctx.close();
  });

  test('admin user can list pages and create a wiki page', async ({ page }) => {
    // Create a page.
    const name = 'auth-test-' + Date.now();
    const create = await page.request.post(`/api/pages/${name}`, {
      data: { content: '# Auth Test Page\n', baseVersion: 0 },
    });
    expect(create.ok()).toBeTruthy();

    // Verify it appears in the pages list.
    const list = await page.request.get('/api/pages');
    const body = await list.json();
    expect(body.pages.some((p: { name: string }) => p.name === name)).toBe(true);
  });
});
