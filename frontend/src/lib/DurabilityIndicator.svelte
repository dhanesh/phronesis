<!--
  Two-state durability indicator for the collab-wiki editor.

  Satisfies: RT-2.2, TN6, U3, U2
  Depends on: ./durability.js state machine

  Visual contract (TN6):
    - "Synced"   : latest op broadcast to peers, not yet disk-durable
    - "Saved"    : latest op disk-durable per O8
    - "Syncing..." : op sent, awaiting peer ack
    - "Offline"  : no heartbeat (U2: editor transitions to read-only)

  The indicator is deliberately minimal - a single label with a dot. Richer
  affordances (last-saved-at, retry status, peer count) are out of scope for
  this binding-constraint spike and belong to a later UX iteration.
-->
<script>
  import { DURABILITY_STATES } from './durability.js';

  export let state = DURABILITY_STATES.IDLE;

  const LABELS = {
    idle:         { text: 'Ready',    tone: 'idle'   },
    dirty:        { text: 'Editing',  tone: 'dirty'  },
    syncing:      { text: 'Syncing',  tone: 'syncing'},
    synced:       { text: 'Synced',   tone: 'synced' },
    saved:        { text: 'Saved',    tone: 'saved'  },
    disconnected: { text: 'Offline',  tone: 'offline'},
  };

  $: label = LABELS[state] || LABELS.idle;
</script>

<div
  class="durability"
  data-state={state}
  aria-live="polite"
  aria-label="Document {label.text}"
  role="status"
>
  <span class="dot tone-{label.tone}" aria-hidden="true"></span>
  <span class="text">{label.text}</span>
</div>

<style>
  .durability {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.82rem;
    color: var(--durability-fg, #4a5568);
    padding: 0.15rem 0.5rem;
    border-radius: 999px;
    background: var(--durability-bg, rgba(0, 0, 0, 0.04));
    user-select: none;
  }

  .dot {
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    display: inline-block;
    flex-shrink: 0;
  }

  .tone-idle    { background: #a0aec0; }
  .tone-dirty   { background: #ed8936; }
  .tone-syncing { background: #4299e1; animation: pulse 1.2s ease-in-out infinite; }
  .tone-synced  { background: #38b2ac; }
  .tone-saved   { background: #38a169; }
  .tone-offline { background: #e53e3e; }

  .text {
    font-weight: 500;
    letter-spacing: 0.01em;
  }

  /* The "saved" state carries extra weight because it is the true durability
     signal (O8 flush complete). "Synced" is intentionally lighter so users
     can distinguish the two per TN6. */
  .durability[data-state="saved"] .text {
    font-weight: 600;
  }

  .durability[data-state="disconnected"] {
    color: #c53030;
    background: rgba(229, 62, 62, 0.1);
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50%      { opacity: 0.45; }
  }
</style>
