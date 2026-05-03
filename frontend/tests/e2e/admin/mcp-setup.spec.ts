// @constraint admin-ui:RT-5 — MCP setup panel surfaces discovery + JWKS URLs
// @constraint admin-ui:U3 — one-click copy with visual feedback
// @constraint admin-ui:RT-6 — palette entry gated by isAdmin
//
// Walks the admin flow that helps an admin paste-and-go an MCP client at
// phronesis: open the panel via Cmd-K, see the URLs, copy them, get
// feedback, see the not-configured helper when OAuth is disabled.

import { test, expect } from '../fixtures';

const SYNTHETIC_DISCOVERY = {
  issuer: 'https://phronesis.test.example',
  authorization_endpoint: 'https://phronesis.test.example/oauth/authorize',
  token_endpoint: 'https://phronesis.test.example/oauth/token',
  registration_endpoint: 'https://phronesis.test.example/oauth/register',
  jwks_uri: 'https://phronesis.test.example/.well-known/jwks.json',
  response_types_supported: ['code'],
  grant_types_supported: ['authorization_code', 'refresh_token'],
  code_challenge_methods_supported: ['S256'],
  token_endpoint_auth_methods_supported: ['none'],
  scopes_supported: ['read', 'write'],
};

async function openMCPSetupPanel(page) {
  await page.goto('/');
  await expect(page.locator('.top-bar')).toBeVisible({ timeout: 10000 });
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
  // Wait for the palette input to be focused before typing — same
  // race that hit the workspace-rename helper (m5-verify G1).
  await expect(page.getByPlaceholder(/type a page name|page name or command/i).first()).toBeVisible({ timeout: 5000 });
  await page.keyboard.type('mcp');
  // Scope to the palette to avoid any DOM-text collision with topbar
  // controls (defensive — same shape as the workspace-rename helper).
  const item = page.locator('.palette-item').filter({ hasText: /connect an mcp client/i }).first();
  await expect(item).toBeVisible();
  await item.click();
  await expect(page.locator('[aria-label="Connect an MCP client"]')).toBeVisible({ timeout: 5000 });
}

test.describe('admin-ui: MCP setup panel', () => {
  test('discovery + JWKS URLs render with copy buttons; copy fires clipboard with the right value', async ({ page }) => {
    // Mock the discovery endpoint.
    await page.route('**/.well-known/oauth-authorization-server', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(SYNTHETIC_DISCOVERY),
      }),
    );

    await openMCPSetupPanel(page);

    // Both URLs render.
    const discoveryURL = page.getByTestId('mcp-discovery-url');
    const jwksURL = page.getByTestId('mcp-jwks-url');
    const issuer = page.getByTestId('mcp-issuer');

    await expect(discoveryURL).toContainText('/.well-known/oauth-authorization-server');
    await expect(jwksURL).toContainText(SYNTHETIC_DISCOVERY.jwks_uri);
    await expect(issuer).toContainText(SYNTHETIC_DISCOVERY.issuer);

    // Stub clipboard so we can capture the writeText call.
    await page.evaluate(() => {
      navigator.clipboard.writeText = async (s) => {
        (window as any).__lastCopied = s;
      };
    });

    await page.getByTestId('mcp-copy-discovery').click();
    let copied = await page.evaluate(() => (window as any).__lastCopied);
    expect(copied).toContain('/.well-known/oauth-authorization-server');
    await expect(page.getByTestId('mcp-feedback-discovery')).toContainText(/copied/i);

    await page.getByTestId('mcp-copy-jwks').click();
    copied = await page.evaluate(() => (window as any).__lastCopied);
    expect(copied).toBe(SYNTHETIC_DISCOVERY.jwks_uri);
    await expect(page.getByTestId('mcp-feedback-jwks')).toContainText(/copied/i);
  });

  test('OAuth not configured: panel shows the env-var helper instead of broken URLs', async ({ page }) => {
    // Server returns 503 when OAuth is not configured.
    await page.route('**/.well-known/oauth-authorization-server', (route) =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'oauth is not configured' }),
      }),
    );

    await openMCPSetupPanel(page);

    await expect(page.getByTestId('mcp-not-configured')).toBeVisible();
    await expect(page.getByTestId('mcp-not-configured')).toContainText(/PHRONESIS_OAUTH_ENABLED/);
    await expect(page.getByTestId('mcp-not-configured')).toContainText(/PHRONESIS_OAUTH_ISSUER/);
    // No URL elements.
    await expect(page.getByTestId('mcp-discovery-url')).toHaveCount(0);
  });
});
