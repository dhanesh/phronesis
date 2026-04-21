// @constraint B1 - OIDC surface covered via stub (TN5 resolution)
// @constraint S2 - OIDC tests use HMAC stub verifier, no real IdP
// @constraint RT-6 - spec mints token via same path as internal/auth/oidc/stub.go
import { test as base, expect } from '@playwright/test';
import { createHmac } from 'crypto';
import * as fs from 'fs';
import * as os from 'os';
import { startPhronesis } from '../fixtures/server';

const OIDC_SECRET = 'e2e-oidc-test-secret-not-for-production';
const OIDC_ISSUER = 'e2e-test';
const OIDC_AUDIENCE = 'e2e-test';
const OIDC_PORT = 4200;
const NO_OIDC_PORT = 4201; // second server with OIDC disabled

function mintOIDCToken(claims: Record<string, unknown>): string {
  const enc = (obj: unknown) => Buffer.from(JSON.stringify(obj)).toString('base64url');
  const now = Math.floor(Date.now() / 1000);
  const header = enc({ alg: 'HS256', typ: 'JWT', kid: 'stub-1' });
  const payload = enc({ iss: OIDC_ISSUER, aud: OIDC_AUDIENCE, iat: now, exp: now + 3600, ...claims });
  const signingInput = `${header}.${payload}`;
  const sig = createHmac('sha256', OIDC_SECRET).update(signingInput).digest('base64url');
  return `${signingInput}.${sig}`;
}

base.describe.configure({ mode: 'serial' });

let oidcBaseURL: string;
let noOidcBaseURL: string;
let stopOIDC: () => Promise<void>;
let stopNoOIDC: () => Promise<void>;

base.beforeAll(async () => {
  const oidcDir = fs.mkdtempSync(os.tmpdir() + '/phronesis-oidc-');
  const noOidcDir = fs.mkdtempSync(os.tmpdir() + '/phronesis-nooidc-');

  const [oidcServer, noOidcServer] = await Promise.all([
    startPhronesis({
      port: OIDC_PORT,
      pagesDir: oidcDir,
      extra: {
        PHRONESIS_OIDC_ENABLED: '1',
        PHRONESIS_OIDC_SECRET: OIDC_SECRET,
        PHRONESIS_OIDC_ISSUER: OIDC_ISSUER,
        PHRONESIS_OIDC_AUDIENCE: OIDC_AUDIENCE,
      },
    }),
    startPhronesis({ port: NO_OIDC_PORT, pagesDir: noOidcDir }),
  ]);

  oidcBaseURL = oidcServer.baseURL;
  stopOIDC = oidcServer.stop;
  noOidcBaseURL = noOidcServer.baseURL;
  stopNoOIDC = noOidcServer.stop;
});

base.afterAll(async () => {
  await Promise.all([stopOIDC?.(), stopNoOIDC?.()]);
  // cleanup tempdirs
  try { fs.rmSync(os.tmpdir() + '/phronesis-oidc-*', { recursive: true, force: true }); } catch {}
});

base.describe('OIDC stub authentication', () => {
  base('user can authenticate with a valid OIDC stub token and access a protected route', async ({ request }) => {
    const token = mintOIDCToken({ sub: 'oidc-test-user', email: 'oidc@example.com' });
    const loginRes = await request.post(`${oidcBaseURL}/api/auth/oidc/login`, {
      data: { id_token: token },
    });
    expect(loginRes.ok()).toBeTruthy();
    const sessionCookie = loginRes.headers()['set-cookie']?.split(',')[0]?.split(';')[0] ?? '';
    expect(sessionCookie).not.toBe('');

    // Verify the session is authenticated with the OIDC-issued cookie.
    const sessionRes = await request.get(`${oidcBaseURL}/api/session`, {
      headers: { Cookie: sessionCookie },
    });
    const body = await sessionRes.json();
    expect(body.authenticated).toBe(true);
  });

  base('OIDC login with a tampered token is rejected with 401', async ({ request }) => {
    const token = mintOIDCToken({ sub: 'oidc-test-user' });
    const tampered = token.slice(0, -5) + 'TAMPR';
    const res = await request.post(`${oidcBaseURL}/api/auth/oidc/login`, {
      data: { id_token: tampered },
    });
    expect(res.status()).toBe(401);
  });

  base('OIDC endpoint returns 404 when OIDC is disabled on the server', async ({ request }) => {
    const token = mintOIDCToken({ sub: 'test-user' });
    const res = await request.post(`${noOidcBaseURL}/api/auth/oidc/login`, {
      data: { id_token: token },
    });
    expect(res.status()).toBe(404);
  });
});
