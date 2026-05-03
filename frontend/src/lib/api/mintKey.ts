// Typed wrapper around POST /api/admin/keys/requests/{id}/approve.
//
// Localises the discipline that closes TN3 (error rendering vs
// plaintext leak): the success branch reads response.json() exactly
// once and immediately destructures key_plaintext into the typed
// return value. The error branch never accesses fields outside the
// `error` / `error_description` allow-list, so a malicious or
// confused server response cannot leak credentials through the error
// path.
//
// Discriminated-union return type forces every caller to handle
// success and failure explicitly — TypeScript will not let a caller
// accidentally read `plaintext` on an error result.
//
// Satisfies:
//   RT-2 (typed wrapper, single-response-read, filtered errors)
//   Resolves TN3 (error states vs plaintext leak)
//   Underpins S3 (no plaintext in console / DOM beyond the modal)

export interface MintParams {
  /** Granted scope. The server validates against the allow-list too. */
  scope: 'read' | 'write' | 'admin';
  /** Human-readable label (defaults to the request's requested_label). */
  label: string;
  /** Optional ISO-8601 expiry timestamp. Empty/undefined = no expiry. */
  expiresAt?: string;
}

export type MintResult =
  | { ok: true; plaintext: string; prefix: string; warning?: string }
  | { ok: false; status: number; message: string };

export async function mintApiKey(
  requestId: number,
  params: MintParams,
): Promise<MintResult> {
  const body: Record<string, string> = {
    scope: params.scope,
    label: params.label,
  };
  if (params.expiresAt && params.expiresAt.trim() !== '') {
    body.expires_at = params.expiresAt;
  }

  let response: Response;
  try {
    response = await fetch(
      `/api/admin/keys/requests/${requestId}/approve`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      },
    );
  } catch (e) {
    // Network failure (offline, DNS, etc.). No response body to read,
    // no leakage risk. Status 0 follows the convention for "no
    // response received."
    return { ok: false, status: 0, message: 'Network error' };
  }

  if (response.ok) {
    // RT-2 / TN3: single read of the success body. Plaintext is
    // extracted into the typed return value immediately; the
    // response object never escapes this branch. If a future
    // server-contract change adds new fields to the body, they go
    // unread — typed-by-construction privacy.
    const parsed = await safeJSON(response);
    const plaintext = strField(parsed, 'key_plaintext');
    const prefix = strField(parsed, 'key_prefix');
    if (!plaintext) {
      // The server claimed success but didn't return a plaintext
      // (shouldn't happen, but defend). Return ok=false so the UI
      // surfaces an error instead of mounting an empty modal.
      return {
        ok: false,
        status: response.status,
        message: 'Server returned no key',
      };
    }
    const warning = strField(parsed, 'warning');
    return warning
      ? { ok: true, plaintext, prefix, warning }
      : { ok: true, plaintext, prefix };
  }

  // RT-2 / TN3: error branch. Read body text once; parse defensively.
  // Only the named error fields cross the boundary back to callers —
  // never the raw body, never any field that could in theory contain
  // credentials. If parsing fails, fall back to status alone.
  let message = `Request failed (${response.status})`;
  try {
    const text = await response.text();
    const parsed = JSON.parse(text);
    const desc = strField(parsed, 'error_description');
    const err = strField(parsed, 'error');
    if (desc) {
      message = desc;
    } else if (err) {
      message = err;
    }
  } catch {
    // Body wasn't JSON or couldn't be read; fall back to generic.
  }
  return { ok: false, status: response.status, message };
}

// Defensive helpers — kept tiny so the wrapper file stays auditable
// at a glance.

async function safeJSON(r: Response): Promise<unknown> {
  try {
    return await r.json();
  } catch {
    return {};
  }
}

function strField(obj: unknown, name: string): string {
  if (obj && typeof obj === 'object' && name in obj) {
    const v = (obj as Record<string, unknown>)[name];
    if (typeof v === 'string') return v;
  }
  return '';
}
