// @constraint B1 - blob upload surface coverage
import { test, expect } from '../fixtures';

test.describe('media blob upload', () => {
  test('authenticated user can upload a PNG file and retrieve it by URL', async ({ page }) => {
    // Create a minimal 1x1 PNG (89 bytes -- valid PNG header).
    const pngBytes = Buffer.from(
      '89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489' +
      '0000000a49444154789c6260000000020001e221bc330000000049454e44ae426082',
      'hex',
    );

    const res = await page.request.post('/media', {
      multipart: {
        file: {
          name: 'test.png',
          mimeType: 'image/png',
          buffer: pngBytes,
        },
      },
    });
    expect(res.ok()).toBeTruthy();

    const body = await res.json();
    expect(body.url).toMatch(/^\/media\//);
    expect(body.contentType).toBe('image/png');

    // Retrieve the uploaded blob.
    const get = await page.request.get(body.url);
    expect(get.ok()).toBeTruthy();
    expect(get.headers()['content-type']).toContain('image/png');
  });

  test('upload with disallowed content type is rejected', async ({ page }) => {
    const res = await page.request.post('/media', {
      multipart: {
        file: {
          name: 'test.exe',
          mimeType: 'application/octet-stream',
          buffer: Buffer.from('MZ'), // fake exe header
        },
      },
    });
    expect(res.ok()).toBeFalsy();
    // Server rejects disallowed content types with 4xx.
    expect(res.status()).toBeGreaterThanOrEqual(400);
  });

  test('unauthenticated media upload is rejected with 401', async ({ browser }) => {
    const ctx = await browser.newContext(); // no storageState
    const res = await ctx.request.post('/media', {
      multipart: {
        file: {
          name: 'test.png',
          mimeType: 'image/png',
          buffer: Buffer.from('fake'),
        },
      },
    });
    expect(res.status()).toBe(401);
    await ctx.close();
  });
});
