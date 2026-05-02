// Cmd-K command palette + theme switcher tests.
//
// @constraint Shell - palette opens via keystroke, fuzzy-filters pages,
//                     navigates on Enter, closes on Escape
// @constraint Shell - ThemeSwitcher persists choice and updates the
//                     documentElement dataset.theme attribute

import { test, expect } from '../fixtures';

test.describe('app shell — command palette', () => {
  test('Cmd-K (or Ctrl-K) opens the palette and Escape closes it', async ({
    page,
  }, testInfo) => {
    // Seed at least one page so the palette has something to list.
    await page.request.post('/api/pages/palette-seed', {
      data: { content: '# Seed\n', baseVersion: 0 },
    });
    await page.goto('/w/palette-seed');
    await expect(page.locator('.cm-md-line-heading-1').first()).toBeVisible();

    const isMac = testInfo.project.use?.userAgent?.toLowerCase().includes('mac')
      ?? process.platform === 'darwin';
    const meta = isMac ? 'Meta' : 'Control';

    await page.keyboard.press(`${meta}+k`);
    await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeHidden();
  });

  test('typing into the palette filters pages and Enter navigates', async ({
    page,
  }, testInfo) => {
    const target = `palette-target-${Date.now()}`;
    await page.request.post(`/api/pages/${target}`, {
      data: { content: '# Target\n', baseVersion: 0 },
    });
    await page.request.post('/api/pages/palette-other', {
      data: { content: '# Other\n', baseVersion: 0 },
    });

    await page.goto('/w/palette-other');
    await expect(page.locator('.cm-md-line-heading-1').first()).toBeVisible();

    const isMac = testInfo.project.use?.userAgent?.toLowerCase().includes('mac')
      ?? process.platform === 'darwin';
    const meta = isMac ? 'Meta' : 'Control';

    await page.keyboard.press(`${meta}+k`);
    await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeVisible();

    // Filter — typing the target's prefix should narrow the list.
    await page.keyboard.type('palette-target');

    // The palette list should include the target page as the first
    // page-kind entry; press Enter to navigate.
    await page.keyboard.press('Enter');

    // Navigation: the workspace header reflects the new page name.
    await expect(page.getByRole('heading', { level: 1, name: target })).toBeVisible();
  });
});

test.describe('app shell — theme switcher', () => {
  test('changing theme updates dataset.theme and persists', async ({ page }) => {
    await page.goto('/w/home');
    // Wait for app shell.
    await expect(page.locator('.theme-switcher')).toBeVisible();

    // Initial theme: whatever default loadTheme picked. Should be one
    // of the registered ones.
    const initial = await page.evaluate(() => document.documentElement.dataset.theme);
    expect(['apple-light', 'apple-dark']).toContain(initial);

    // Switch to dark explicitly.
    await page.locator('.theme-switcher').selectOption('apple-dark');
    const after = await page.evaluate(() => document.documentElement.dataset.theme);
    expect(after).toBe('apple-dark');

    // Persisted to localStorage so a reload keeps the choice.
    await page.reload();
    await expect(page.locator('.theme-switcher')).toBeVisible();
    const reloaded = await page.evaluate(() => document.documentElement.dataset.theme);
    expect(reloaded).toBe('apple-dark');

    // Reset for any subsequent tests (workers share storageState).
    await page.locator('.theme-switcher').selectOption('apple-light');
  });
});
