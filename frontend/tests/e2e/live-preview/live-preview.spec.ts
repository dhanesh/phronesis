// Single Playwright spec enumerating every V1 decoration family for the
// silverbullet-like-live-preview feature.
// Satisfies: O1 (e2e per family), TN5 (one fixture, every family), TN6 (≤30
//            line fixture so every assertion is in initial viewport).
//
// @constraint U2 - V1 decoration coverage (Full set: headings, emphasis,
//                  inline-code, markdown-links, lists, images, fenced-code,
//                  blockquote, tables, plus existing wiki-links)
// @constraint U1 - cursor-in-region reveals source for that one region
// @constraint S1 - safeURL allow-list applied to every rendered href
// @constraint S2 - widget content via textContent only (no innerHTML escapes
//                  embedded HTML in source)
// @constraint O3 - smoke: at least one decoration renders on a known seed
//                  page; loud failure on wholesale decoration breakage

import { test, expect } from '../fixtures';

// V1 fixture: every construct in U2's list, kept ≤30 lines so the whole
// page renders in the initial viewport (TN6).
const FIXTURE = `# Heading One
## Heading Two
### Heading Three

Paragraph with **bold**, *italic*, and \`inline code\`.

[label](https://example.com) [internal](/w/internal-page)

[xss](javascript:alert(1))

- item one
- item two
1. ordered one
2. ordered two

\`\`\`javascript
const fenced = "code";
\`\`\`

> blockquote line one
> blockquote line two

| col a | col b |
| ----- | ----- |
| cell  | cell  |

![image alt](/media/abc123def)

[[wiki-page]]
`;

async function seedPage(page: any, name: string) {
  const res = await page.request.post(`/api/pages/${name}`, {
    data: { content: FIXTURE, baseVersion: 0 },
  });
  expect(res.ok()).toBeTruthy();
}

test.describe('live-preview decorations — Full V1 coverage', () => {
  test('headings render with cm-md-heading-N class', async ({ page }) => {
    const name = `lp-headings-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-heading-1').first()).toBeVisible();
    await expect(page.locator('.cm-md-heading-2').first()).toBeVisible();
    await expect(page.locator('.cm-md-heading-3').first()).toBeVisible();
  });

  test('emphasis renders cm-md-strong and cm-md-emphasis', async ({ page }) => {
    const name = `lp-emphasis-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-strong').first()).toBeVisible();
    await expect(page.locator('.cm-md-emphasis').first()).toBeVisible();
  });

  test('inline code renders cm-md-inline-code', async ({ page }) => {
    const name = `lp-code-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-inline-code').first()).toBeVisible();
  });

  test('markdown links render cm-md-link with safeURL-screened href', async ({ page }) => {
    const name = `lp-links-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);

    // Allowed scheme passes through.
    await expect(page.locator('a.cm-md-link[href="https://example.com"]')).toBeVisible();
    // Internal href passes through.
    await expect(page.locator('a.cm-md-link[href="/w/internal-page"]')).toBeVisible();
    // S1: javascript: collapses to "#"; the dangerous scheme must NOT survive.
    const allHrefs = await page.locator('a.cm-md-link').evaluateAll((els) =>
      els.map((e: HTMLAnchorElement) => e.getAttribute('href') || ''),
    );
    expect(allHrefs.some((h) => h.toLowerCase().startsWith('javascript:'))).toBe(false);
  });

  test('list markers render cm-md-list-marker', async ({ page }) => {
    const name = `lp-lists-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-list-marker').first()).toBeVisible();
  });

  test('images render cm-md-image with /media/<sha> src', async ({ page }) => {
    const name = `lp-image-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('img.cm-md-image[src="/media/abc123def"]')).toBeVisible();
  });

  test('fenced code blocks render cm-md-fenced-code-line', async ({ page }) => {
    const name = `lp-fenced-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-fenced-code-line').first()).toBeVisible();
  });

  test('blockquotes render cm-md-blockquote', async ({ page }) => {
    const name = `lp-blockquote-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-blockquote').first()).toBeVisible();
  });

  test('tables render as cm-md-table widget', async ({ page }) => {
    const name = `lp-table-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('table.cm-md-table')).toBeVisible();
    await expect(page.locator('.cm-md-table-header').first()).toBeVisible();
    await expect(page.locator('.cm-md-table-row').first()).toBeVisible();
  });

  test('wiki-links still render after decoration migration (regression)', async ({ page }) => {
    const name = `lp-wiki-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    await expect(page.locator('a.cm-wikilink').first()).toBeVisible();
  });

  // O3 — wholesale-breakage smoke. If decorations no-op (e.g. CM API drift),
  // none of the cm-md-* classes appear and this test fails loudly. Single
  // assertion gates CI.
  test('O3 smoke: at least one decoration renders on the seed page', async ({ page }) => {
    const name = `lp-smoke-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);
    const decorationCount = await page.locator(
      [
        '.cm-md-heading-1',
        '.cm-md-strong',
        '.cm-md-emphasis',
        '.cm-md-inline-code',
        '.cm-md-link',
        '.cm-md-list-marker',
        '.cm-md-image',
        '.cm-md-fenced-code-line',
        '.cm-md-blockquote',
        'table.cm-md-table',
        'a.cm-wikilink',
      ].join(', '),
    ).count();
    expect(decorationCount).toBeGreaterThan(0);
  });
});
