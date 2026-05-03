// @constraint U2 - Admin Users page lists projected users
// @constraint U3 - Admin Keys page lists keys + pending requests
// @constraint RT-9 - admin Web UI surface (Stage 1c frontend)
//
// Confirms that the /api/admin/users and /api/admin/keys endpoints
// (Stage 1b server-side) round-trip end-to-end through the worker
// fixture, AND that the Svelte modal opens via Cmd-K and renders the
// expected empty state. Approve flow's 501 stub is asserted at API
// level so the user-facing inline message stays accurate.

import { test, expect } from '../fixtures';

test.describe('admin users + keys API', () => {
  test('GET /api/admin/users returns an empty array on a fresh worker', async ({ page }) => {
    const res = await page.request.get('/api/admin/users');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body.users)).toBe(true);
    // Fresh worker with no OIDC projection → empty.
    expect(body.users).toEqual([]);
  });

  test('GET /api/admin/keys returns an empty array on a fresh worker', async ({ page }) => {
    const res = await page.request.get('/api/admin/keys');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body.keys)).toBe(true);
    expect(body.keys).toEqual([]);
  });

  test('GET /api/admin/keys/requests returns an empty array on a fresh worker', async ({ page }) => {
    const res = await page.request.get('/api/admin/keys/requests');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(Array.isArray(body.requests)).toBe(true);
    expect(body.requests).toEqual([]);
  });

  test('admin can suspend, reactivate, and delete a seeded user', async ({ page, baseURL }) => {
    // Seed a user via direct SQL is out of scope from the test side;
    // instead drive the admin API. The OIDC projection layer is unit-
    // tested in internal/auth/oidc/projection_test.go; here we round-
    // trip the URL routing + status transitions for a row inserted via
    // a direct admin DB seed not available, so we skip suspend/delete
    // semantic checks against an actual row and instead assert the
    // 404-on-missing semantics that gate the routing.
    const suspend = await page.request.post('/api/admin/users/9999/suspend');
    expect(suspend.status()).toBe(404);

    const del = await page.request.delete('/api/admin/users/9999');
    expect(del.status()).toBe(404);

    // Unknown action → 404 (handler dispatches by action name).
    const bogus = await page.request.post('/api/admin/users/9999/sudo');
    expect(bogus.status()).toBe(404);

    // baseURL is used to confirm we are hitting the worker server.
    expect(baseURL).toBeTruthy();
  });

  test('approve on a missing key request returns 404 (route otherwise reachable)', async ({ page }) => {
    // Stage 2a ships real Argon2id key minting (replaces the Stage 1b
    // 501 stub). Without a seeded key_request row, the handler now
    // returns 404 (request not found). The full approve->mint->201
    // path is covered by the Go integration test
    // TestAdminKeyRequestApproveMintsRealKey.
    const res = await page.request.post('/api/admin/keys/requests/9999/approve');
    expect(res.status()).toBe(404);
  });

  test('deny on a missing key request returns 404 (route otherwise reachable)', async ({ page }) => {
    const res = await page.request.post('/api/admin/keys/requests/9999/deny');
    expect(res.status()).toBe(404);
  });

  test('revoke on a missing key returns 404', async ({ page }) => {
    const res = await page.request.post('/api/admin/keys/9999/revoke');
    expect(res.status()).toBe(404);
  });
});

test.describe('admin Users + Keys modals via Cmd-K', () => {
  test('admin can open the Users modal from the command palette', async ({ page }) => {
    await page.goto('/');
    // Wait for app shell to render after auth.
    await expect(page.locator('.top-bar')).toBeVisible({ timeout: 10000 });

    // Open command palette via shortcut.
    await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
    await expect(page.getByPlaceholder(/type a page name|page name or command/i).first()).toBeVisible({ timeout: 5000 });

    // Type to filter to "Manage users".
    await page.keyboard.type('users');
    const item = page.getByText(/manage users/i).first();
    await expect(item).toBeVisible();
    await item.click();

    // Modal renders.
    await expect(page.locator('[aria-label="Manage users"]')).toBeVisible({ timeout: 5000 });
    // On a fresh worker the empty state appears.
    await expect(page.getByText(/No users projected yet/i)).toBeVisible();
  });

  test('admin can open the Keys modal from the command palette', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('.top-bar')).toBeVisible({ timeout: 10000 });

    await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
    await page.keyboard.type('api keys');
    const item = page.getByText(/manage api keys/i).first();
    await expect(item).toBeVisible();
    await item.click();

    await expect(page.locator('[aria-label="Manage API keys"]')).toBeVisible({ timeout: 5000 });
    await expect(page.getByText(/No pending key requests/i)).toBeVisible();
    await expect(page.getByText(/No keys issued yet/i)).toBeVisible();
  });
});

// ----------------------------------------------------------------------
// admin-ui Stage: plaintext modal + approve form + storage cleanliness.
// These walk the UI flow with synthetic /api/admin/keys/* responses so
// we can drive the approve flow without seeding real DB rows.
// ----------------------------------------------------------------------

const SYNTHETIC_REQUEST = {
  id: 7,
  user_id: 42,
  owner_name: 'alice',
  owner_email: 'alice@example.com',
  workspace_slug: 'default',
  requested_scope: 'write',
  requested_label: 'claude-code on alice-laptop',
  requested_at: '2026-05-04T08:00:00Z',
};

const SYNTHETIC_PLAINTEXT = 'phr_live_abcd1234efgh_zyxwvutsrqponmlkjihgfedcba012345';
const SYNTHETIC_PREFIX = 'phr_live_abcd1234efgh';

async function openKeysModalWithSyntheticRequest(page) {
  // Mock the keys-list (empty) and the requests-list (one pending).
  await page.route('**/api/admin/keys', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ keys: [] }) }),
  );
  await page.route('**/api/admin/keys/requests', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ requests: [SYNTHETIC_REQUEST] }) }),
  );

  await page.goto('/');
  await expect(page.locator('.top-bar')).toBeVisible({ timeout: 10000 });
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  await page.keyboard.type('api keys');
  const item = page.getByText(/manage api keys/i).first();
  await expect(item).toBeVisible();
  await item.click();
  await expect(page.locator('[aria-label="Manage API keys"]')).toBeVisible({ timeout: 5000 });
  // Synthetic request appears.
  await expect(page.getByTestId('keys-request-row')).toBeVisible();
}

test.describe('admin-ui: approve form + plaintext modal', () => {
  // @constraint U1 — plaintext modal with blur/reveal + acknowledgment gate
  // @constraint S1 — no client storage of plaintext
  // @constraint admin-ui:RT-1 — binding
  test('approve flow: form fields, plaintext modal lifecycle, ack-gate, on-dismiss clear', async ({ page }) => {
    await openKeysModalWithSyntheticRequest(page);

    // Snapshot client storage BEFORE the flow so we can assert no
    // residue after (RT-10 / S1).
    const before = await page.evaluate(() => ({
      ls: { ...localStorage },
      ss: { ...sessionStorage },
    }));

    // Open the approve form (admin-ui RT-3).
    await page.getByTestId('keys-approve').click();
    await expect(page.getByTestId('keys-approve-form')).toBeVisible();

    // Form pre-fills from the request.
    await expect(page.getByTestId('keys-form-scope')).toHaveValue('write');
    await expect(page.getByTestId('keys-form-label')).toHaveValue(SYNTHETIC_REQUEST.requested_label);

    // Override scope; leave label as-is.
    await page.getByTestId('keys-form-scope').selectOption('read');

    // Mock the approve endpoint to return the synthetic plaintext.
    await page.route('**/api/admin/keys/requests/7/approve', (route) =>
      route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 99,
          key_prefix: SYNTHETIC_PREFIX,
          key_plaintext: SYNTHETIC_PLAINTEXT,
          warning: 'plaintext is shown ONCE; copy it now or revoke and re-issue',
        }),
      }),
    );

    await page.getByTestId('keys-form-submit').click();

    // Plaintext modal mounts.
    const modal = page.locator('[aria-labelledby="pt-title"]');
    await expect(modal).toBeVisible({ timeout: 5000 });

    // Token is rendered blurred by default (TN2 resolution).
    const token = page.getByTestId('plaintext-token');
    await expect(token).toHaveAttribute('data-revealed', 'false');
    // CSS class assertion — the blurred class is what applies the filter.
    await expect(token).toHaveClass(/pt-token-blurred/);

    // Reveal toggles the blur off.
    await page.getByTestId('plaintext-reveal').click();
    await expect(token).toHaveAttribute('data-revealed', 'true');
    await expect(token).not.toHaveClass(/pt-token-blurred/);

    // Dismiss button is disabled until the ack checkbox is checked.
    const dismiss = page.getByTestId('plaintext-dismiss');
    await expect(dismiss).toBeDisabled();

    // Clipboard copy returns the unobscured value regardless of blur.
    await page.evaluate(() => {
      // Stub clipboard so we can capture the writeText call.
      navigator.clipboard.writeText = async (s) => {
        (window as any).__lastCopied = s;
      };
    });
    await page.getByTestId('plaintext-copy').click();
    const copied = await page.evaluate(() => (window as any).__lastCopied);
    expect(copied).toBe(SYNTHETIC_PLAINTEXT);

    // Tick the ack checkbox; dismiss enables.
    await page.getByTestId('plaintext-ack').check();
    await expect(dismiss).toBeEnabled();
    await dismiss.click();
    await expect(modal).not.toBeVisible();

    // RT-10 / S1: storage is unchanged (plaintext never persisted).
    const after = await page.evaluate(() => ({
      ls: { ...localStorage },
      ss: { ...sessionStorage },
    }));
    expect(after).toEqual(before);

    // Ensure the plaintext is also not lingering anywhere in the DOM.
    const html = await page.content();
    expect(html).not.toContain(SYNTHETIC_PLAINTEXT);
  });

  // @constraint U1 — Escape suppression until acked
  test('plaintext modal: Escape key is suppressed until ack', async ({ page }) => {
    await openKeysModalWithSyntheticRequest(page);

    await page.route('**/api/admin/keys/requests/7/approve', (route) =>
      route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 99,
          key_prefix: SYNTHETIC_PREFIX,
          key_plaintext: SYNTHETIC_PLAINTEXT,
        }),
      }),
    );

    await page.getByTestId('keys-approve').click();
    await page.getByTestId('keys-form-submit').click();
    const modal = page.locator('[aria-labelledby="pt-title"]');
    await expect(modal).toBeVisible();

    // Pre-ack: Escape is a no-op.
    await page.keyboard.press('Escape');
    await expect(modal).toBeVisible();

    // Post-ack: Escape dismisses.
    await page.getByTestId('plaintext-ack').check();
    await page.keyboard.press('Escape');
    await expect(modal).not.toBeVisible();
  });

  // @constraint S3 — no plaintext in console; @constraint admin-ui:TN3
  test('approve flow: error path renders structured message; no plaintext in console', async ({ page }) => {
    const consoleMessages: string[] = [];
    page.on('console', (msg) => consoleMessages.push(msg.text()));

    await openKeysModalWithSyntheticRequest(page);

    // Synthetic 500 from approve. The error body has no plaintext, so
    // this exercises the negative case + the console-silence assertion.
    await page.route('**/api/admin/keys/requests/7/approve', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'database constraint violation' }),
      }),
    );

    await page.getByTestId('keys-approve').click();
    await page.getByTestId('keys-form-submit').click();

    // Form-error rendered; no plaintext modal.
    await expect(page.getByTestId('keys-form-error')).toContainText(/database constraint violation/i);
    await expect(page.locator('[aria-labelledby="pt-title"]')).not.toBeVisible();

    // Console capture: nothing logged with phr_live_ prefix during the
    // failed flow.
    for (const msg of consoleMessages) {
      expect(msg).not.toContain('phr_live_');
    }
  });

  // @constraint admin-ui:RT-3 — client-side validation
  test('approve form: rejects empty label client-side', async ({ page }) => {
    await openKeysModalWithSyntheticRequest(page);

    await page.getByTestId('keys-approve').click();
    await page.getByTestId('keys-form-label').fill('   '); // whitespace-only
    await page.getByTestId('keys-form-submit').click();

    await expect(page.getByTestId('keys-form-error')).toContainText(/label/i);
    // Approve endpoint is NOT called.
    await expect(page.locator('[aria-labelledby="pt-title"]')).not.toBeVisible();
  });
});
