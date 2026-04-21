// @constraint T7 - verifies that the network fixture actually blocks non-localhost requests.
// Without this spec, the route handler fires but no test confirms the block.
import { expect } from '@playwright/test';
import { test } from '../fixtures';

test.describe('network interception fixture', () => {
  test('non-localhost requests are blocked with ERR_BLOCKED_BY_CLIENT', async ({ page, workerBaseURL }) => {
    await page.goto(workerBaseURL + '/wiki/InterceptionCheck');

    // Set up listener before triggering the external request.
    const blockedReq = page.waitForEvent('requestfailed', req =>
      req.url().startsWith('https://example.com')
    );

    // Make a fetch to an external origin from within the browser context.
    // The route handler in network.ts will abort('blockedbyclient') before it leaves the runner.
    await page.evaluate(() => fetch('https://example.com/').catch(() => {}));

    const req = await blockedReq;
    expect(req.url()).toContain('example.com');
    expect(req.failure()?.errorText).toMatch(/ERR_BLOCKED_BY_CLIENT/i);
  });

  test('localhost requests to the worker server are not blocked', async ({ page, workerBaseURL }) => {
    // If the fixture incorrectly blocked localhost, every other spec would break.
    // This makes the dependency explicit and documents the expected behavior.
    const response = await page.goto(workerBaseURL + '/wiki/InterceptionCheck');
    expect(response?.status()).toBeLessThan(500);
  });
});
