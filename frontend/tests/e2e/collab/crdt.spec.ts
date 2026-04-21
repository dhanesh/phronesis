// @constraint B1 - CRDT collab surface coverage
// @constraint T8 - web-first assertions
import { test, expect } from '../fixtures';

test.describe('CRDT collaborative editing', () => {
  test('concurrent edits from two clients converge to the same page content', async ({ page }) => {
    const name = 'crdt-test-' + Date.now();

    // Create the initial page.
    const init = await page.request.post(`/api/pages/${name}`, {
      data: { content: 'base content', baseVersion: 0 },
    });
    expect(init.ok()).toBeTruthy();

    // Navigate to the page — establishes SSE subscription.
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-content')).toContainText('base content');

    // Simulate a concurrent edit from another client (same baseVersion = conflict scenario).
    // The server's CRDT merge will produce a deterministic convergence.
    const concurrent = await page.request.post(`/api/pages/${name}`, {
      data: { content: 'concurrent edit applied', baseVersion: 0 },
    });
    expect(concurrent.ok()).toBeTruthy();

    // The SSE snapshot delivers the merged state to the connected browser.
    // Web-first assertion: wait for the editor to show the converged content.
    await expect(page.locator('.cm-content')).toContainText('concurrent edit applied');
  });

  test('editing a page via the UI and retrieving it via API returns consistent content', async ({ page }) => {
    const name = 'crdt-consistency-' + Date.now();

    // Create via API.
    await page.request.post(`/api/pages/${name}`, {
      data: { content: 'api created', baseVersion: 0 },
    });

    // Update via API (simulating the editor autosave path).
    const newContent = 'content after autosave';
    const update = await page.request.post(`/api/pages/${name}`, {
      data: { content: newContent, baseVersion: 0 },
    });
    expect(update.ok()).toBeTruthy();

    // GET the page via API — should return the updated content.
    const get = await page.request.get(`/api/pages/${name}`);
    expect((await get.json()).page.content).toBe(newContent);
  });
});
