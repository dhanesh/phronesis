// @constraint B1 - wiki CRUD surface coverage
// @constraint T8 - web-first assertions throughout
import { test, expect } from '../fixtures';

test.describe('wiki page CRUD', () => {
  test('user can create a new wiki page and retrieve its content', async ({ page }) => {
    const name = 'wiki-create-' + Date.now();
    const content = '# Hello Wiki\n\nThis is a test page.\n';

    const res = await page.request.post(`/api/pages/${name}`, {
      data: { content, baseVersion: 0 },
    });
    expect(res.ok()).toBeTruthy();

    const get = await page.request.get(`/api/pages/${name}`);
    const body = await get.json();
    expect(body.page.name).toBe(name);
    expect(body.page.content).toBe(content);
  });

  test('user can update a wiki page and see new content', async ({ page }) => {
    const name = 'wiki-update-' + Date.now();
    await page.request.post(`/api/pages/${name}`, {
      data: { content: 'original content', baseVersion: 0 },
    });

    const update = await page.request.post(`/api/pages/${name}`, {
      data: { content: 'updated content', baseVersion: 0 },
    });
    expect(update.ok()).toBeTruthy();

    const get = await page.request.get(`/api/pages/${name}`);
    expect((await get.json()).page.content).toBe('updated content');
  });

  test('created page appears in the pages list', async ({ page }) => {
    const name = 'wiki-list-' + Date.now();
    await page.request.post(`/api/pages/${name}`, {
      data: { content: 'list test', baseVersion: 0 },
    });

    const list = await page.request.get('/api/pages');
    const body = await list.json();
    expect(body.pages.some((p: { name: string }) => p.name === name)).toBe(true);
  });

  test('wiki links in page content are extracted as backlinks on the target page', async ({ page }) => {
    const source = 'source-' + Date.now();
    const target = 'target-' + Date.now();

    // Create target page first.
    await page.request.post(`/api/pages/${target}`, {
      data: { content: '# Target Page\n', baseVersion: 0 },
    });

    // Create source page with a wiki link to target.
    await page.request.post(`/api/pages/${source}`, {
      data: { content: `See [[${target}]] for details.\n`, baseVersion: 0 },
    });

    // Target page should now have a backlink from source.
    const get = await page.request.get(`/api/pages/${target}`);
    const body = await get.json();
    expect(body.page.render.backlinks ?? []).toContain(source);
  });

  test('tags in page content are extracted and visible on the page', async ({ page }) => {
    const name = 'wiki-tags-' + Date.now();
    await page.request.post(`/api/pages/${name}`, {
      data: { content: 'A page with #golang and #testing tags.\n', baseVersion: 0 },
    });

    const get = await page.request.get(`/api/pages/${name}`);
    const body = await get.json();
    const tags: string[] = body.page.render.tags ?? [];
    expect(tags).toContain('golang');
    expect(tags).toContain('testing');
  });

  test('task list items in page content are extracted', async ({ page }) => {
    const name = 'wiki-tasks-' + Date.now();
    await page.request.post(`/api/pages/${name}`, {
      data: {
        content: '## Tasks\n- [ ] write tests\n- [x] deploy CI\n',
        baseVersion: 0,
      },
    });

    const get = await page.request.get(`/api/pages/${name}`);
    const body = await get.json();
    const tasks = body.page.render.tasks ?? [];
    expect(tasks.length).toBeGreaterThan(0);
  });

  test('navigating to a wiki page URL renders the page in the browser', async ({ page }) => {
    const name = 'wiki-nav-' + Date.now();
    await page.request.post(`/api/pages/${name}`, {
      data: { content: '# Navigation Test\n\nWelcome to the page.\n', baseVersion: 0 },
    });

    await page.goto(`/w/${name}`);
    // The Svelte app loads and CodeMirror renders the content.
    // Web-first assertion: wait for editor content to appear.
    await expect(page.locator('.cm-content')).toContainText('Navigation Test');
  });
});
