package crdt

import (
	"context"
	"errors"
	"sync"
	"time"
)

// FlushPolicy enforces O8's bounded-loss guarantee for a room.
//
// Satisfies: O8, RT-2.3, TN1 (bounded-loss definition of B1)
//
// A flush fires when WHICHEVER of the following occurs first:
//   - MaxIdle has elapsed since the last Op was applied (default: 3s)
//   - PendingOps since the last flush has reached MaxOps (default: 100)
//
// These defaults come from O8's threshold in the manifold; changing them here
// changes the edit-loss bound that B1 is contractually satisfied with.
type FlushPolicy struct {
	MaxIdle time.Duration
	MaxOps  int
}

// DefaultFlushPolicy returns the O8 thresholds.
func DefaultFlushPolicy() FlushPolicy {
	return FlushPolicy{
		MaxIdle: 3 * time.Second,
		MaxOps:  100,
	}
}

// Room is a per-document collaborative session. It accepts ops, broadcasts
// them to peers (emitting OpAckedEvent), and flushes them to disk under the
// O8 policy (emitting OpSavedEvent).
//
// Satisfies: RT-2 (binding constraint), T1, T2, U1, O8
//
// Concurrency: all exported methods are safe for concurrent use.
// Shutdown: call Close() during graceful drain (O5) to force a final flush.
type Room struct {
	id          string
	policy      FlushPolicy
	broadcaster Broadcaster
	flusher     Flusher

	// acked/saved receive events after the corresponding transitions; they are
	// the wire-side hooks the server surfaces to clients (via SSE/WebSocket)
	// so the client state machine in RT-2.2 can drive its two-state indicator.
	acked chan OpAckedEvent
	saved chan OpSavedEvent

	mu         sync.Mutex
	nextSeq    int64
	buffered   []Op      // ops since last successful flush
	lastOpAt   time.Time // wall-clock time of most recent Apply
	flushedSeq int64     // highest Seq that is disk-durable

	trigger chan struct{} // buffered(1): non-blocking nudge to flush loop
	done    chan struct{} // closed by Close() to stop the flush loop
	wg      sync.WaitGroup
}

// NewRoom constructs a Room with the given dependencies. The flush loop begins
// immediately; callers must invoke Close() during graceful shutdown (O5).
func NewRoom(id string, policy FlushPolicy, b Broadcaster, f Flusher) *Room {
	r := &Room{
		id:          id,
		policy:      policy,
		broadcaster: b,
		flusher:     f,
		acked:       make(chan OpAckedEvent, 64),
		saved:       make(chan OpSavedEvent, 8),
		trigger:     make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	r.wg.Add(1)
	go r.flushLoop()
	return r
}

// Apply accepts an op, assigns it a sequence number, broadcasts it, and emits
// an OpAckedEvent. The op is buffered and will be flushed per FlushPolicy.
//
// Satisfies: RT-2.1 (two-event emission), T2 (broadcast on hot path)
//
// Precondition for T2 latency target: broadcaster.Broadcast must not block on
// disk I/O. In Wave 1 the broadcaster is a fan-out goroutine; in Wave 2 it's
// the SSE/WebSocket writer.
func (r *Room) Apply(op Op) (OpAckedEvent, error) {
	if op.RoomID != "" && op.RoomID != r.id {
		return OpAckedEvent{}, errors.New("crdt: op room id does not match room")
	}

	r.mu.Lock()
	r.nextSeq++
	op.RoomID = r.id
	op.Seq = r.nextSeq
	if op.At.IsZero() {
		op.At = time.Now().UTC()
	}
	r.buffered = append(r.buffered, op)
	r.lastOpAt = time.Now()
	pendingCount := len(r.buffered)
	r.mu.Unlock()

	// Broadcast OFF the critical mutex; broadcaster.Broadcast must be fast
	// (feeds T2's < 300ms p95 peer-visible latency).
	if err := r.broadcaster.Broadcast(r.id, op); err != nil {
		return OpAckedEvent{}, err
	}

	evt := OpAckedEvent{RoomID: r.id, Seq: op.Seq, At: op.At}
	// Non-blocking send so a backed-up consumer does not stall Apply.
	select {
	case r.acked <- evt:
	default:
		// Best-effort: consumer is slow; m5-verify tests this path.
	}

	// If the op-count trigger has been hit, nudge the flush loop immediately
	// instead of waiting for the MaxIdle timer.
	if pendingCount >= r.policy.MaxOps {
		r.triggerFlush()
	}
	return evt, nil
}

// Acked returns a channel that emits an OpAckedEvent after every successful Apply.
// Downstream (Wave 2) wires this to the SSE/WebSocket writer feeding clients.
//
// Satisfies: RT-2.1
func (r *Room) Acked() <-chan OpAckedEvent { return r.acked }

// Saved returns a channel that emits an OpSavedEvent after every successful flush.
// Downstream (Wave 2) wires this to the client so the two-state indicator can
// upgrade from "synced" to "saved".
//
// Satisfies: RT-2.1, RT-2.3, TN6
func (r *Room) Saved() <-chan OpSavedEvent { return r.saved }

// FlushedSeq returns the highest Seq that is currently disk-durable.
func (r *Room) FlushedSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushedSeq
}

// Close performs a final flush and stops the flush loop. Must be called during
// graceful shutdown (O5). Returns any error from the final flush.
//
// Satisfies: O5, RT-12.2 (CRDT flush always completes)
func (r *Room) Close(ctx context.Context) error {
	close(r.done)
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.flushNow()
}

// triggerFlush is a non-blocking hint to the flush loop that the op-count
// threshold has been reached, so it can flush immediately without waiting for
// the next idle-tick.
func (r *Room) triggerFlush() {
	select {
	case r.trigger <- struct{}{}:
	default:
		// Already a pending nudge; the loop will pick it up on its next select.
	}
}

func (r *Room) flushLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.policy.MaxIdle / 4)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-r.trigger:
			r.maybeFlush()
		case <-ticker.C:
			r.maybeFlush()
		}
	}
}

// maybeFlush evaluates the O8 policy and flushes if either trigger has fired.
func (r *Room) maybeFlush() {
	r.mu.Lock()
	pending := len(r.buffered)
	idleFor := time.Since(r.lastOpAt)
	r.mu.Unlock()

	if pending == 0 {
		return
	}
	idleTriggered := idleFor >= r.policy.MaxIdle
	opsTriggered := pending >= r.policy.MaxOps
	if !idleTriggered && !opsTriggered {
		return
	}
	_ = r.flushNow()
}

// flushNow unconditionally flushes any buffered ops. Callers: maybeFlush, Close.
// On flusher error, the buffer is retained so a future flush can retry; the
// error is surfaced via RT-2.4 instrumentation (logs + metrics) once Wave 2
// wires them up.
func (r *Room) flushNow() error {
	r.mu.Lock()
	if len(r.buffered) == 0 {
		r.mu.Unlock()
		return nil
	}
	batch := make([]Op, len(r.buffered))
	copy(batch, r.buffered)
	r.mu.Unlock()

	start := time.Now()
	if err := r.flusher.Flush(r.id, batch); err != nil {
		return err
	}
	elapsed := time.Since(start)

	r.mu.Lock()
	// Only pop what we actually wrote; additional ops may have arrived.
	if len(batch) <= len(r.buffered) {
		r.buffered = r.buffered[len(batch):]
	} else {
		r.buffered = nil
	}
	through := batch[len(batch)-1].Seq
	if through > r.flushedSeq {
		r.flushedSeq = through
	}
	r.mu.Unlock()

	evt := OpSavedEvent{
		RoomID:     r.id,
		ThroughSeq: through,
		FlushedAt:  time.Now().UTC(),
		FlushDurMs: elapsed.Milliseconds(),
	}
	select {
	case r.saved <- evt:
	default:
		// Non-blocking; a slow consumer must not stall a flush.
	}
	return nil
}
