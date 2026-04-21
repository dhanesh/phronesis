// Satisfies: T3 (prod binary), T4 (per-worker tempdir + unique port), S3 (ephemeral cleanup).
import { test as base } from '@playwright/test';
import * as cp from 'child_process';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { fileURLToPath } from 'url';

const BASE_PORT = 4100;
const MAX_WORKERS_PER_SHARD = 4;

// PHRONESIS_BIN is set in CI to the downloaded binary path; locally defaults to repo-root binary.
// Path: frontend/tests/e2e/fixtures/ → up 4 levels → repo root → phronesis
const BINARY = process.env.PHRONESIS_BIN
  ?? path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', '..', '..', 'phronesis');

async function waitForReady(url: string, timeoutMs = 15_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr: string = 'not started';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(1000) });
      if (res.ok) return;
      lastErr = `HTTP ${res.status}`;
    } catch (e) {
      lastErr = String(e);
    }
    await new Promise(r => setTimeout(r, 200));
  }
  throw new Error(`Server at ${url} not ready after ${timeoutMs}ms: ${lastErr}`);
}

/** Start a phronesis subprocess. Returns baseURL and a cleanup fn. */
export async function startPhronesis(options: {
  port: number;
  pagesDir: string;
  extra?: Record<string, string>;
}): Promise<{ baseURL: string; stop: () => Promise<void> }> {
  const { port, pagesDir, extra = {} } = options;
  const blobDir = path.join(pagesDir, 'blobs');
  const auditLog = path.join(pagesDir, 'audit.log');
  fs.mkdirSync(blobDir, { recursive: true });

  const proc = cp.spawn(BINARY, [], {
    env: {
      ...process.env,
      PHRONESIS_ADDR: `:${port}`,
      PHRONESIS_PAGES_DIR: pagesDir,
      PHRONESIS_BLOB_DIR: blobDir,
      PHRONESIS_AUDIT_LOG: auditLog,
      PHRONESIS_ADMIN_USER: 'admin',
      PHRONESIS_ADMIN_PASSWORD: 'admin123',
      ...extra,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const baseURL = `http://localhost:${port}`;
  await waitForReady(`${baseURL}/api/health`);

  const stop = (): Promise<void> =>
    new Promise(resolve => {
      proc.kill('SIGTERM');
      const forceKill = setTimeout(() => proc.kill('SIGKILL'), 3000);
      proc.on('exit', () => { clearTimeout(forceKill); resolve(); });
    });

  return { baseURL, stop };
}

type ServerFixtures = {
  workerPort: number;
  workerPagesDir: string;
  workerBaseURL: string;
};

export const test = base.extend<Record<string, never>, ServerFixtures>({
  workerPort: [
    async ({ workerIndex }, use) => {
      const shardIndex = parseInt(process.env.SHARD_INDEX ?? '0', 10);
      await use(BASE_PORT + shardIndex * MAX_WORKERS_PER_SHARD + workerIndex);
    },
    { scope: 'worker' },
  ],

  workerPagesDir: [
    async ({}, use) => {
      const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'phronesis-e2e-'));
      await use(dir);
      fs.rmSync(dir, { recursive: true, force: true });
    },
    { scope: 'worker' },
  ],

  workerBaseURL: [
    async ({ workerPort, workerPagesDir }, use) => {
      const { baseURL, stop } = await startPhronesis({ port: workerPort, pagesDir: workerPagesDir });
      await use(baseURL);
      await stop();
    },
    { scope: 'worker' },
  ],
});
