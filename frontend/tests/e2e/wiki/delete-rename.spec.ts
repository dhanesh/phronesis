// Beta scope (decision 2026-11-27): editors rename and delete pages from the
// command palette; the change shows in the page list.
import { test, expect } from '../fixtures';

async function palette(page) {
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  await expect(page.getByPlaceholder(/type a page name|page name or command/i).first()).toBeVisible();
}

test('rename then delete a page from the command palette', async ({ page }) => {
  const name = `dr-${Date.now()}`;
  expect((await page.request.post(`/api/pages/${name}`, { data: { content: '# Doomed page\n', baseVersion: 0 } })).ok()).toBeTruthy();
  await page.goto(`/w/${name}`);
  await expect(page.locator('.cm-content')).toContainText('Doomed page');

  await palette(page);
  await page.keyboard.type(`${name}-renamed`);
  await page.getByText(`Rename "${name}" to "${name}-renamed"`).click();
  await expect(page.locator('.cm-content')).toContainText('Doomed page');
  expect((await page.request.get(`/api/pages/${name}-renamed`)).ok()).toBeTruthy();
  const list = await (await page.request.get('/api/pages')).json();
  expect(list.pages.map((p) => p.name)).toContain(`${name}-renamed`);
  expect(list.pages.map((p) => p.name)).not.toContain(name);

  page.once('dialog', (d) => d.accept());
  await palette(page);
  await page.keyboard.type('delete');
  await page.getByText(`Delete page "${name}-renamed"`).click();
  await expect.poll(async () => (await (await page.request.get('/api/pages')).json()).pages.map((p) => p.name)).not.toContain(`${name}-renamed`);
});
