// Satisfies: T1 (Playwright), T2 (Chromium only), O2 (≤5min p95 via sharding),
//            O3 (retries=2 CI / 0 local), O4 (trace+screenshot+video).
// Run via: cd frontend && npx playwright test
import { defineConfig, devices } from '@playwright/test';
import * as path from 'path';
import { fileURLToPath } from 'url';

// Config lives in frontend/ (ESM scope — no __dirname) so @playwright/test resolves
// via frontend/node_modules. All artifacts anchored to the repo root for CI paths.
const REPO_ROOT = path.resolve(fileURLToPath(import.meta.url), '..', '..');

export default defineConfig({
  testDir: './tests/e2e',
  outputDir: path.join(REPO_ROOT, 'test-results'),

  // O3: CI retries=2 to absorb flakes; local=0 for speed
  retries: process.env.CI ? 2 : 0,

  // O2: 4 workers per shard; locally 2 workers (fewer server instances)
  workers: process.env.CI ? 4 : 2,

  // O4: failure artifacts — trace on first retry, screenshot + video on failure
  use: {
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  // RT-7 + RT-9: blob reporter for shard merging; JSON for flake-monitor; list for CI output
  reporter: process.env.CI
    ? [
        ['blob', { outputFolder: path.join(REPO_ROOT, 'blob-report') }],
        ['list'],
        ['json', { outputFile: path.join(REPO_ROOT, 'test-results', 'results.json') }],
      ]
    : [
        ['html', { open: 'on-failure', outputFolder: path.join(REPO_ROOT, 'playwright-report') }],
        ['list'],
      ],

  // T2: Chromium only for v1. WebKit + Firefox deferred post-stabilisation.
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],

  // No webServer block — per-worker servers are managed via worker-scoped fixtures (T3, T4).
});
