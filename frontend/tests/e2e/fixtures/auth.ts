// Satisfies: T5 (storageState — log in once per worker, no per-spec UI login).
// Per-mortem #2 guard: auth fixture uses API login, not UI, to avoid CodeMirror timing traps.
import { request } from '@playwright/test';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { test as serverTest } from './server';

// Only workerAuthFile is worker-scoped (computed once per worker process).
// storageState and baseURL are test-scoped Playwright built-ins — we override their
// values but cannot change their scope.
type AuthWorkerFixtures = {
  workerAuthFile: string;
};

export const test = serverTest.extend<{}, AuthWorkerFixtures>({
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

  // Override Playwright's built-in storageState (test-scoped) to point to worker auth file.
  // This causes every browser context created in specs to start authenticated.
  storageState: async ({ workerAuthFile }, use) => {
    await use(workerAuthFile);
  },

  // Override Playwright's built-in baseURL (test-scoped) so specs can use relative paths.
  baseURL: async ({ workerBaseURL }, use) => {
    await use(workerBaseURL);
  },
});
