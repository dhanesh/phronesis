// Satisfies: T5 (storageState — log in once per worker, no per-spec UI login).
// Per-mortem #2 guard: auth fixture uses API login, not UI, to avoid CodeMirror timing traps.
import { request } from '@playwright/test';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { test as serverTest } from './server';

type AuthFixtures = {
  workerAuthFile: string;
  storageState: string;
  baseURL: string;
};

export const test = serverTest.extend<Record<string, never>, AuthFixtures>({
  // Log in once per worker via POST /api/login; save cookie to disk as storageState JSON.
  workerAuthFile: [
    async ({ workerBaseURL, workerPort }, use) => {
      const statePath = path.join(os.tmpdir(), `phronesis-auth-${workerPort}.json`);
      // Use a one-off APIRequestContext (not tied to any page) for worker-level login.
      const ctx = await request.newContext({ baseURL: workerBaseURL });
      const res = await ctx.post('/api/login', {
        data: { username: 'admin', password: 'admin123' },
      });
      if (!res.ok()) {
        await ctx.dispose();
        throw new Error(`Worker auth setup failed: ${res.status()} ${await res.text()}`);
      }
      await ctx.storageState({ path: statePath });
      await ctx.dispose();

      await use(statePath);

      fs.rmSync(statePath, { force: true });
    },
    { scope: 'worker' },
  ],

  // Override Playwright's built-in storageState fixture with our worker-scoped path.
  // This causes every browser context to start authenticated.
  storageState: [
    async ({ workerAuthFile }, use) => {
      await use(workerAuthFile);
    },
    { scope: 'worker' },
  ],

  // Override baseURL so specs can use relative paths (/w/page, /api/...).
  baseURL: [
    async ({ workerBaseURL }, use) => {
      await use(workerBaseURL);
    },
    { scope: 'worker' },
  ],
});
