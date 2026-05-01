// TypeScript port of internal/render/markdown.go::safeURL.
// Satisfies: S1 (RT-5)
//
// Output parity with the Go reference is enforced via Playwright e2e
// fixtures asserting rendered <a href> values for every scheme class.

const ALLOWED_PREFIXES = ['http://', 'https://', 'mailto:'];

export function safeURL(raw: string | null | undefined): string {
  const trimmed = (raw ?? '').trim();
  if (trimmed === '') return '#';
  // Relative paths and fragment / query links are safe by construction.
  if (trimmed.startsWith('/') || trimmed.startsWith('#') || trimmed.startsWith('?')) {
    return trimmed;
  }
  // Scheme-relative URLs (//host/path) inherit the page scheme — safe.
  if (trimmed.startsWith('//')) return trimmed;
  const lower = trimmed.toLowerCase();
  for (const prefix of ALLOWED_PREFIXES) {
    if (lower.startsWith(prefix)) return trimmed;
  }
  // No scheme separator at all — treat as relative path.
  if (!trimmed.includes(':')) return trimmed;
  return '#';
}
