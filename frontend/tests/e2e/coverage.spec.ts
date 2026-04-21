// @constraint B1 - coverage meta-test: enforces every shipping surface has a spec
// @constraint RT-3 - meta-test enumerates required directories; fails if any are absent
// This spec is intentionally a Node.js-level check (no page/browser used).
import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const E2E_ROOT = __dirname; // tests/e2e/

const REQUIRED_DIRS = ['auth', 'wiki', 'collab', 'blob', 'admin', 'rate-limit'];

const REQUIRED_SPEC_FILES = [
  'auth/auth.spec.ts',
  'auth/oidc.spec.ts',
  'wiki/crud.spec.ts',
  'collab/sse.spec.ts',
  'collab/crdt.spec.ts',
  'blob/upload.spec.ts',
  'admin/roles.spec.ts',
  'rate-limit/rate-limit.spec.ts',
];

test.describe('e2e coverage meta-test', () => {
  test('all required feature directories exist under tests/e2e/', () => {
    for (const dir of REQUIRED_DIRS) {
      const dirPath = path.join(E2E_ROOT, dir);
      expect(
        fs.existsSync(dirPath),
        `Missing required e2e feature directory: tests/e2e/${dir}/\n` +
        `A new shipping feature must have a paired spec directory.`,
      ).toBe(true);
    }
  });

  test('all required spec files exist', () => {
    for (const specFile of REQUIRED_SPEC_FILES) {
      const filePath = path.join(E2E_ROOT, specFile);
      expect(
        fs.existsSync(filePath),
        `Missing required spec file: tests/e2e/${specFile}\n` +
        `Add a spec file before merging the feature.`,
      ).toBe(true);
    }
  });

  test('fixtures directory contains all required fixture files', () => {
    const fixtureFiles = ['server.ts', 'auth.ts', 'network.ts', 'index.ts'];
    for (const file of fixtureFiles) {
      const filePath = path.join(E2E_ROOT, 'fixtures', file);
      expect(
        fs.existsSync(filePath),
        `Missing fixture file: tests/e2e/fixtures/${file}`,
      ).toBe(true);
    }
  });
});
