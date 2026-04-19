// Durability state machine for the two-state "synced -> saved" indicator.
//
// Satisfies: RT-2.2, TN6, U3
//
// Event sources (from server SSE):
//   - { type: 'op_acked', seq: N }   -> peer broadcast complete
//   - { type: 'op_saved', throughSeq: N } -> disk flush complete (O8)
//   - { type: 'heartbeat' }          -> liveness signal (DRT-6)
//
// Derived UI state machine (from highestAcked vs highestSaved vs outbox):
//   idle         -- no local ops since last saved; no remote activity
//   dirty        -- local op typed, not yet sent (outbox > 0)
//   syncing      -- op sent to server, awaiting op_acked
//   synced       -- latest op acked but not yet saved  (U3 "saved" indicator lights at this point per TN6)
//   saved        -- latest op is disk-durable          (true durability)
//   disconnected -- no heartbeat within HEARTBEAT_TIMEOUT_MS (U2: editor goes read-only)
//
// The two-state indicator displays:
//   - "Synced" label when state is 'syncing' or 'synced'
//   - "Saved"  label (bolder) when state is 'saved'
//   - "Offline" label when 'disconnected'
//
// Why a separate module: RT-2 is the binding constraint. Keeping durability
// logic outside Editor.svelte lets RT-2 be tested and reviewed independently,
// and lets Wave 2 swap SSE for WebSocket without touching editor glue.

export const DURABILITY_STATES = Object.freeze({
  IDLE: 'idle',
  DIRTY: 'dirty',
  SYNCING: 'syncing',
  SYNCED: 'synced',
  SAVED: 'saved',
  DISCONNECTED: 'disconnected',
});

// DRT-6: heartbeat must be distinct from "slow response" so slow networks are
// not misclassified as disconnected. The heartbeat interval is decided
// server-side; the client only checks freshness.
export const HEARTBEAT_TIMEOUT_MS = 5_000;

/**
 * Create a durability tracker for a single document/room.
 *
 * @param {object} opts
 * @param {(state: string) => void} opts.onState  called on every state transition
 * @param {() => number} [opts.now]               injectable clock for tests
 * @returns {{ onSent: (seq:number)=>void,
 *             onAcked: (seq:number)=>void,
 *             onSaved: (throughSeq:number)=>void,
 *             onHeartbeat: ()=>void,
 *             tick: ()=>void,
 *             state: ()=>string,
 *             counters: ()=>object }}
 */
export function createDurabilityTracker({ onState, now = () => Date.now() } = {}) {
  let highestSent = 0;
  let highestAcked = 0;
  let highestSaved = 0;
  let lastHeartbeatAt = now();

  let current = DURABILITY_STATES.IDLE;

  function derive() {
    if (now() - lastHeartbeatAt > HEARTBEAT_TIMEOUT_MS) {
      return DURABILITY_STATES.DISCONNECTED;
    }
    if (highestSent === 0 && highestAcked === 0 && highestSaved === 0) {
      return DURABILITY_STATES.IDLE;
    }
    if (highestSent > highestAcked) {
      return highestAcked === 0 ? DURABILITY_STATES.DIRTY : DURABILITY_STATES.SYNCING;
    }
    if (highestAcked > highestSaved) {
      return DURABILITY_STATES.SYNCED;
    }
    return DURABILITY_STATES.SAVED;
  }

  function publish() {
    const next = derive();
    if (next !== current) {
      current = next;
      if (onState) onState(current);
    }
  }

  return {
    onSent(seq) {
      if (seq > highestSent) {
        highestSent = seq;
        publish();
      }
    },
    onAcked(seq) {
      if (seq > highestAcked) {
        highestAcked = seq;
        publish();
      }
    },
    onSaved(throughSeq) {
      if (throughSeq > highestSaved) {
        highestSaved = throughSeq;
        publish();
      }
    },
    onHeartbeat() {
      lastHeartbeatAt = now();
      // Re-evaluate in case we were DISCONNECTED and just came back.
      publish();
    },
    tick() {
      // Call periodically from the UI (e.g., every 1s) so DISCONNECTED is
      // reached without an explicit event.
      publish();
    },
    state() {
      return current;
    },
    counters() {
      return { highestSent, highestAcked, highestSaved, lastHeartbeatAt };
    },
  };
}
