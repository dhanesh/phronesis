// Multi-workspace e2e: list endpoint, admin CRUD, workspace switcher.
//
// @constraint Shell - admin can create + delete workspaces from the UI
// @constraint Shell - non-admin can switch but not manage
// @constraint Shell - workspace selector lives in the top bar; pages
//                     scope to the selected workspace

import { test, expect } from '../fixtures';

test.describe('multi-workspace API', () => {
  test('GET /api/workspaces returns at least the default workspace', async ({ page }) => {
    const res = await page.request.get('/api/workspaces');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body.workspaces)).toBe(true);
    expect(body.workspaces.some((w: { slug: string }) => w.slug === 'default')).toBe(true);
  });

  test('admin can create + delete a workspace; default is locked', async ({ page }) => {
    const slug = `e2e-${Date.now().toString(36)}`;

    // Create
    const create = await page.request.post('/api/admin/workspaces', {
      data: { slug, name: 'E2E Test' },
    });
    expect(create.status()).toBe(201);
    const created = await create.json();
    expect(created.workspace.slug).toBe(slug);

    // List shows it
    const list = await page.request.get('/api/workspaces');
    const body = await list.json();
    expect(body.workspaces.some((w: { slug: string }) => w.slug === slug)).toBe(true);

    // Page write+read inside the new workspace round-trips
    const write = await page.request.post(`/api/workspaces/${slug}/pages/probe`, {
      data: { content: '# Probe\n', baseVersion: 0 },
    });
    expect(write.ok()).toBeTruthy();
    const read = await page.request.get(`/api/workspaces/${slug}/pages/probe`);
    expect((await read.json()).page.content).toBe('# Probe\n');

    // Default cannot be deleted
    const delDefault = await page.request.delete('/api/admin/workspaces/default');
    expect(delDefault.status()).toBe(403);

    // Created can be deleted
    const del = await page.request.delete(`/api/admin/workspaces/${slug}`);
    expect(del.status()).toBe(204);

    // Subsequent list omits it
    const list2 = await page.request.get('/api/workspaces');
    const body2 = await list2.json();
    expect(body2.workspaces.some((w: { slug: string }) => w.slug === slug)).toBe(false);
  });

  test('invalid slugs are rejected with 400', async ({ page }) => {
    const bad = await page.request.post('/api/admin/workspaces', {
      data: { slug: 'BAD SLUG', name: '' },
    });
    expect(bad.status()).toBe(400);
  });
});

test.describe('workspace switcher in top bar', () => {
  test('selector lists workspaces and switching reloads the page list', async ({ page }) => {
    const slug = `e2e-switch-${Date.now().toString(36)}`;
    await page.request.post('/api/admin/workspaces', {
      data: { slug, name: 'Switch Target' },
    });
    // Seed a unique page in the new workspace.
    await page.request.post(`/api/workspaces/${slug}/pages/only-here`, {
      data: { content: '# Only Here\n', baseVersion: 0 },
    });

    await page.goto('/w/home');
    await expect(page.locator('.workspace-switcher')).toBeVisible();

    // Switch to the new workspace via the dropdown.
    await page.locator('.workspace-switcher').selectOption(slug);

    // The sidebar nav should now list `only-here` from the new workspace.
    await expect(page.locator('.nav-link', { hasText: 'only-here' })).toBeVisible();

    // Cleanup.
    await page.request.delete(`/api/admin/workspaces/${slug}`);
  });
});
