// @constraint T7 - verifies that non-localhost requests are blocked.
// The block can come from either layer: phronesis's CSP
// (`connect-src 'self'`) catches fetches at the renderer before they
// reach the network stack, OR the network fixture's
// route.abort('blockedbyclient') catches anything the CSP missed.
// This test asserts the *outcome* (the fetch fails) without depending
// on which layer caught it. Earlier the test waited for a
// `requestfailed` event, but CSP-blocked fetches don't emit one.
import { expect } from '@playwright/test';
import { test } from '../fixtures';

test.describe('network interception fixture', () => {
  test('non-localhost fetches are blocked by CSP and/or the route fixture', async ({ page, workerBaseURL }) => {
    await page.goto(workerBaseURL + '/w/InterceptionCheck');

    // Run the fetch in the browser and report whether it threw.
    const result = await page.evaluate(async () => {
      try {
        const res = await fetch('https://example.com/', { mode: 'no-cors' });
        return { ok: true, status: res.status };
      } catch (e) {
        return { ok: false, error: String(e) };
      }
    });

    // The fetch must NOT have succeeded — either CSP blocked it (the
    // browser throws TypeError "Failed to fetch") or the route handler
    // aborted it (also TypeError).
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/(failed to fetch|blocked|networkerror)/i);
  });

  test('localhost requests to the worker server are not blocked', async ({ page, workerBaseURL }) => {
    // If the fixture incorrectly blocked localhost, every other spec would break.
    // This makes the dependency explicit and documents the expected behavior.
    const response = await page.goto(workerBaseURL + '/w/InterceptionCheck');
    expect(response?.status()).toBeLessThan(500);
  });
});
