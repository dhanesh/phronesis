// @constraint B1 - SSE live-update surface coverage
// @constraint T8 - web-first assertions (no manual wait delays)
import { test, expect } from '../fixtures';

test.describe('SSE live updates', () => {
  test('page editor updates via SSE when another session edits the same page', async ({ page }) => {
    const name = 'sse-test-' + Date.now();

    // Create the page.
    await page.request.post(`/api/pages/${name}`, {
      data: { content: '# Initial Content\n', baseVersion: 0 },
    });

    // Navigate to the page — this establishes an SSE connection.
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-content')).toContainText('Initial Content');

    // POST an update via the authenticated request context.
    // The server's Hub.Apply broadcasts the update via SSE to all subscribers.
    const update = await page.request.post(`/api/pages/${name}`, {
      data: { content: '# Updated via SSE\n\nContent updated by external edit.\n', baseVersion: 0 },
    });
    expect(update.ok()).toBeTruthy();

    // Web-first assertion: wait for the SSE snapshot to arrive and the editor to update.
    await expect(page.locator('.cm-content')).toContainText('Updated via SSE');
  });

  test('SSE connection is established when page is loaded', async ({ page }) => {
    const name = 'sse-connect-' + Date.now();
    await page.request.post(`/api/pages/${name}`, {
      data: { content: '# SSE Connection Test\n', baseVersion: 0 },
    });

    // Intercept the SSE request to verify it's made.
    const sseRequest = page.waitForRequest((req) =>
      req.url().includes(`/api/pages/${name}/events`),
    );
    await page.goto(`/w/${name}`);
    const req = await sseRequest;
    expect(req.method()).toBe('GET');
  });
});
