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

// ───────────────────────────────────────────────────────────────────────────
// Safety-property tests (close m5-verify gaps G2, G4, G7, G8). Each one
// guards a load-bearing INVARIANT that the surface-rendering tests above
// don't directly exercise.
// ───────────────────────────────────────────────────────────────────────────

test.describe('live-preview safety properties', () => {
  // G2 / T1 — decorations are visual-only; autosave POST carries raw
  // markdown source byte-for-byte, never rendered HTML. If a decoration
  // ever mutates view.state.doc, this test catches it before the wiki on
  // disk gets corrupted.
  // @constraint T1 - decorations are visual-only; document text never mutated
  test('T1: autosave POST contains raw markdown, not rendered HTML', async ({ page }) => {
    const name = `lp-roundtrip-${Date.now()}`;
    await seedPage(page, name);

    // Capture every POST to /api/pages/<name> after the editor mounts.
    const autosaveBodies: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'POST' && req.url().includes(`/api/pages/${name}`)) {
        const body = req.postData();
        if (body) autosaveBodies.push(body);
      }
    });

    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-heading-1').first()).toBeVisible();

    // Trigger autosave by typing one character at end of doc.
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type(' ');
    // Wait for debounced autosave (App.svelte uses ~600ms debounce).
    await page.waitForTimeout(900);

    expect(autosaveBodies.length).toBeGreaterThan(0);
    const body = JSON.parse(autosaveBodies[autosaveBodies.length - 1]);
    // Raw markdown markers must be present in the saved content.
    // If a decoration mutated the doc, these would be missing.
    expect(body.content).toContain('# Heading One');
    expect(body.content).toContain('**bold**');
    expect(body.content).toContain('| col a | col b |');
    // And no rendered HTML should have leaked into persistence.
    expect(body.content).not.toContain('<table');
    expect(body.content).not.toContain('<strong>');
    expect(body.content).not.toContain('<h1>');
  });

  // G4 / U1 — cursor entering a decorated region reveals the raw source
  // for that one region. Verifies decorations suppress when cursor is
  // inside, by asserting the literal markdown markers appear in the
  // rendered .cm-line text only when the cursor sits in the line.
  // @constraint U1 - cursor inside region reveals source for that one region
  test('U1: cursor entering a heading line reveals the # marker', async ({ page }) => {
    const name = `lp-cursor-heading-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);

    const headingLine = page.locator('.cm-line').filter({ hasText: 'Heading One' });
    await expect(headingLine).toBeVisible();

    // With the cursor far away (default after load), the `#` should be
    // hidden by the heading family's Decoration.replace on HeaderMark.
    const textCursorOutside = await headingLine.textContent();
    expect(textCursorOutside).not.toContain('#');

    // Click into the heading line. CodeMirror sets selection at the
    // click position; isCursorInRange now suppresses the replace.
    await headingLine.click();
    const textCursorInside = await headingLine.textContent();
    expect(textCursorInside).toContain('#');
  });

  // @constraint U1 - cursor inside reveals emphasis source markers
  test('U1: cursor entering a bold/italic span reveals * markers', async ({ page }) => {
    const name = `lp-cursor-emphasis-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);

    const paraLine = page.locator('.cm-line').filter({ hasText: 'Paragraph with' });
    await expect(paraLine).toBeVisible();

    // Cursor outside: ** and * should be hidden.
    const textOutside = await paraLine.textContent();
    expect(textOutside).not.toContain('**');
    // Note: a single * inside other text is harder to assert non-presence
    // because the literal asterisk-character could appear elsewhere; the
    // ** check above is the load-bearing assertion.

    // Click into the line; markers should reveal.
    await paraLine.click();
    const textInside = await paraLine.textContent();
    expect(textInside).toContain('**');
  });

  // @constraint U1 - cursor entering inline code reveals backticks
  test('U1: cursor entering inline code reveals backticks', async ({ page }) => {
    const name = `lp-cursor-code-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);

    const paraLine = page.locator('.cm-line').filter({ hasText: 'Paragraph with' });
    await expect(paraLine).toBeVisible();

    const textOutside = await paraLine.textContent();
    expect(textOutside).not.toContain('`');

    await paraLine.click();
    const textInside = await paraLine.textContent();
    expect(textInside).toContain('`');
  });

  // @constraint U1 - cursor entering a markdown link reveals brackets
  test('U1: cursor entering a markdown link reveals [label](url) source', async ({ page }) => {
    const name = `lp-cursor-link-${Date.now()}`;
    await seedPage(page, name);
    await page.goto(`/w/${name}`);

    // The line `[label](https://example.com) [internal](/w/internal-page)`.
    const linkLine = page.locator('.cm-line').filter({ hasText: 'label' });
    await expect(linkLine).toBeVisible();

    // Cursor outside: rendered as chip widget; brackets and URL hidden.
    const textOutside = await linkLine.textContent();
    expect(textOutside).not.toContain('](https://');
    expect(textOutside).not.toContain('[label]');

    // Click on the line (avoid the chip itself — click at line start where
    // there's no widget). After click, the `Link` node's source becomes
    // visible because isCursorInRange returns true.
    await linkLine.click({ position: { x: 2, y: 4 } });
    const textInside = await linkLine.textContent();
    expect(textInside).toContain('[label]');
  });

  // G7 / S1 — every dangerous URL scheme collapses to `#`. The original
  // surface test only covered `javascript:`; this extends to data:,
  // vbscript:, file:, about: for parity with the Go-side allow-list.
  // @constraint S1 - safeURL allow-list rejects all dangerous schemes
  test('S1: data:/vbscript:/file:/about: all collapse to href="#"', async ({ page }) => {
    const name = `lp-safeurl-${Date.now()}`;
    const fixture = `# Schemes

[xss-js](javascript:alert(1))

[xss-data](data:text/html,<script>alert(1)</script>)

[xss-vbs](vbscript:msgbox(1))

[xss-file](file:///etc/passwd)

[xss-about](about:blank)

[ok-http](http://example.com)

[ok-https](https://example.com)

[ok-mailto](mailto:a@b.com)

[ok-relative](/some/path)
`;
    const res = await page.request.post(`/api/pages/${name}`, {
      data: { content: fixture, baseVersion: 0 },
    });
    expect(res.ok()).toBeTruthy();

    await page.goto(`/w/${name}`);
    await expect(page.locator('a.cm-md-link').first()).toBeVisible();

    // Every rendered link's href should either be on the allow-list or
    // exactly "#" — never a live dangerous scheme.
    const allHrefs: string[] = await page.locator('a.cm-md-link').evaluateAll((els) =>
      els.map((e) => (e as HTMLAnchorElement).getAttribute('href') ?? ''),
    );

    expect(allHrefs.length).toBeGreaterThan(0);

    const dangerous = ['javascript:', 'data:', 'vbscript:', 'file:', 'about:'];
    for (const href of allHrefs) {
      const lower = href.toLowerCase();
      for (const scheme of dangerous) {
        expect(lower.startsWith(scheme), `dangerous scheme leaked: ${href}`).toBe(false);
      }
    }

    // Allow-listed URLs must survive untouched.
    expect(allHrefs).toContain('http://example.com');
    expect(allHrefs).toContain('https://example.com');
    expect(allHrefs).toContain('mailto:a@b.com');
    expect(allHrefs).toContain('/some/path');

    // Dangerous scheme labels render but their href is "#".
    const dangerousLabels = ['xss-js', 'xss-data', 'xss-vbs', 'xss-file', 'xss-about'];
    for (const label of dangerousLabels) {
      const href = await page.locator(`a.cm-md-link`, { hasText: label }).getAttribute('href');
      expect(href, `${label} href`).toBe('#');
    }
  });

  // G8 / S2 — widget DOM uses textContent only; embedded HTML / script
  // in markdown source must remain inert. If any widget regresses to
  // innerHTML, this test fires window.__pwned and we catch it.
  // @constraint S2 - widget content via textContent only; no innerHTML
  test('S2: embedded <script> and onerror handlers stay inert', async ({ page }) => {
    const name = `lp-injection-${Date.now()}`;
    const fixture = `# Injection probe

Paragraph **<script>window.__pwned=1</script>** with [link](http://x.example)<img src=x onerror="window.__pwned=2">.

Inline \`<script>window.__pwned=3</script>\` code.

| col | <script>window.__pwned=4</script> |
| --- | --- |
| <img src=x onerror=window.__pwned=5> | cell |

> blockquote with <script>window.__pwned=6</script>
`;
    const res = await page.request.post(`/api/pages/${name}`, {
      data: { content: fixture, baseVersion: 0 },
    });
    expect(res.ok()).toBeTruthy();

    await page.goto(`/w/${name}`);
    await expect(page.locator('.cm-md-heading-1').first()).toBeVisible();

    // No <script> elements should exist anywhere inside the editor.
    const scriptCount = await page.locator('.cm-editor script').count();
    expect(scriptCount).toBe(0);

    // No img element with an onerror attribute should have been
    // constructed by widget DOM.
    const imgWithOnerror = await page.locator('.cm-editor img[onerror]').count();
    expect(imgWithOnerror).toBe(0);

    // Give any (would-be) script a generous tick to fire.
    await page.waitForTimeout(150);

    // The script payloads, if executed, would set window.__pwned. Must be undefined.
    const pwned = await page.evaluate(() => (window as Window & { __pwned?: unknown }).__pwned);
    expect(pwned).toBeUndefined();
  });
});
