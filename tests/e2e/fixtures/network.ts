// Satisfies: T7 (third-party network call interception), T8 (no external requests in CI).
// Defensive invariant: blocks any request not targeting the worker's localhost port.
// Protects against future frontend changes accidentally adding external dependencies.
import { type Page } from '@playwright/test';
import { test as authTest } from './auth';

export const test = authTest.extend<{ page: Page }>({
  page: async ({ page, workerPort }, use) => {
    // Block all requests not targeting our worker's localhost port.
    await page.route('**/*', (route) => {
      const url = new URL(route.request().url());
      if (url.hostname === 'localhost' && url.port === String(workerPort)) {
        route.continue();
      } else {
        // Abort external requests — they would flake on real network conditions.
        route.abort('blockedbyclient');
      }
    });
    await use(page);
  },
});
