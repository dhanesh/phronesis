// Theme registry and switcher state.
//
// Each theme is a token set (defined in `themes.css`) keyed by
// `[data-theme="<id>"]` on the documentElement. Themes are pure CSS
// variables — no JS work happens at switch time beyond setting the
// dataset attribute, and the cascade re-applies instantly.
//
// Adding a new theme:
//   1. Append a new block to themes.css with the same variable names.
//   2. Add an entry below in `THEMES`.
//   3. (Optional) update OS-preference fallback in loadTheme() if the
//      new theme is the natural light/dark default.

export interface ThemeMeta {
  id: string;
  label: string;
  scheme: 'light' | 'dark';
}

export const THEMES: ReadonlyArray<ThemeMeta> = [
  { id: 'apple-light', label: 'Apple Light', scheme: 'light' },
  { id: 'apple-dark', label: 'Apple Dark', scheme: 'dark' },
];

const STORAGE_KEY = 'phronesis.theme';
const DEFAULT_LIGHT = 'apple-light';
const DEFAULT_DARK = 'apple-dark';

export function applyTheme(id: string): void {
  document.documentElement.dataset.theme = id;
  // Hint UA controls (scrollbar, form autofill) so they match the
  // current scheme. The dataset.theme attribute alone wouldn't tell
  // the browser whether we're in light or dark mode.
  const meta = THEMES.find((t) => t.id === id);
  document.documentElement.style.colorScheme = meta?.scheme ?? 'light';
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // localStorage may be unavailable (private mode, file://); ignore.
  }
}

export function loadTheme(): string {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(STORAGE_KEY);
  } catch {
    // Same as above.
  }
  if (stored && THEMES.some((t) => t.id === stored)) {
    applyTheme(stored);
    return stored;
  }
  // Respect the OS preference for first-time visitors.
  const prefersDark =
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches;
  const fallback = prefersDark ? DEFAULT_DARK : DEFAULT_LIGHT;
  applyTheme(fallback);
  return fallback;
}

export function getCurrentTheme(): string {
  return document.documentElement.dataset.theme ?? DEFAULT_LIGHT;
}
