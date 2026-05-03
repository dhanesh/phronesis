// @constraint admin-ui:RT-4 — workspace rename via PATCH; no slug edit
// @constraint admin-ui:B3 — slug is immutable in the UI (pre-mortem A1)
//
// Walks the rename flow: edit display-name, save, refresh. Asserts that
// the slug field is read-only in the rename row (B3 — slug rename would
// orphan every page URL and external bookmark, even though the server's
// PATCH endpoint accepts the field).

import { test, expect } from '../fixtures';

async function openWorkspaceManager(page) {
  await page.goto('/');
  await expect(page.locator('.top-bar')).toBeVisible({ timeout: 10000 });
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  // Wait for the palette input to be focused before typing — without
  // this, the keystrokes can race the modal mount and land in the
  // background (closing the gap surfaced by m5-verify G1).
  await expect(page.getByPlaceholder(/type a page name|page name or command/i).first()).toBeVisible({ timeout: 5000 });
  await page.keyboard.type('workspaces');
  // Scope to the palette: the TopBar workspace switcher has a
  // `<option>Manage workspaces…</option>` whose text would otherwise
  // win the .first() match (and is hidden inside a closed <select>,
  // so .toBeVisible() never resolves).
  const item = page.locator('.palette-item').filter({ hasText: /manage workspaces/i }).first();
  await expect(item).toBeVisible();
  await item.click();
  await expect(page.locator('[aria-label="Manage workspaces"]')).toBeVisible({ timeout: 5000 });
}

test.describe('admin-ui: workspace rename', () => {
  test('default workspace can be renamed; slug stays read-only', async ({ page }) => {
    let patchedBody: string | null = null;

    // Mock the workspace listing — the worker's fresh server has the
    // default workspace; we're asserting against the UI flow, not the
    // server's PATCH semantics (those have their own integration test).
    await page.route('**/api/workspaces', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [{ slug: 'default', name: 'Default' }] }),
      }),
    );

    // Capture PATCH body to assert what the UI sends.
    await page.route('**/api/admin/workspaces/default', async (route) => {
      const req = route.request();
      if (req.method() === 'PATCH') {
        patchedBody = req.postData();
        await route.fulfill({
          status: 204,
          contentType: 'application/json',
          body: '',
        });
      } else {
        await route.continue();
      }
    });

    await openWorkspaceManager(page);

    // Click Rename on the default row.
    await page.getByTestId('ws-rename').first().click();

    // Slug element renders as locked (no input).
    const slugLocked = page.getByTestId('ws-slug-locked').first();
    await expect(slugLocked).toBeVisible();
    await expect(slugLocked).toHaveText('default');

    // Display-name input is editable.
    const input = page.getByTestId('ws-rename-input').first();
    await expect(input).toBeVisible();
    await input.fill('Default Workspace');

    // After workspaces refresh on save, the new name should appear.
    await page.route('**/api/workspaces', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [{ slug: 'default', name: 'Default Workspace' }] }),
      }),
    );

    await page.getByTestId('ws-rename-save').first().click();

    // Wait for the rename row to disappear (back to display mode).
    await expect(page.getByTestId('ws-rename-input')).toHaveCount(0);

    // PATCH was sent with only the name field — never the slug.
    expect(patchedBody).not.toBeNull();
    const parsed = JSON.parse(patchedBody!);
    expect(parsed).toHaveProperty('name', 'Default Workspace');
    expect(parsed).not.toHaveProperty('slug');
  });

  test('cancel rename leaves the workspace untouched', async ({ page }) => {
    let patchCalled = false;
    await page.route('**/api/workspaces', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [{ slug: 'default', name: 'Default' }] }),
      }),
    );
    await page.route('**/api/admin/workspaces/default', async (route) => {
      if (route.request().method() === 'PATCH') {
        patchCalled = true;
      }
      await route.continue();
    });

    await openWorkspaceManager(page);
    await page.getByTestId('ws-rename').first().click();
    await page.getByTestId('ws-rename-input').first().fill('Some Other Name');
    await page.getByTestId('ws-rename-cancel').first().click();
    await expect(page.getByTestId('ws-rename-input')).toHaveCount(0);

    expect(patchCalled).toBe(false);
  });
});
