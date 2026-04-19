package crdt

import (
	"sync"
)

// InProcBroadcaster is the default Broadcaster: fans out ops to subscribers
// via buffered channels within the same process.
//
// Satisfies: RT-4.2, T2
//
// Hot-path budget: Broadcast must not hold the room's mutex or touch disk.
// Fan-out to N subscribers is O(N) with non-blocking sends; a slow subscriber
// drops ops for that subscriber only (dropped count exposed via DroppedFor).
// This is acceptable at Wave-2 scope; k8s multi-replica deployments (deferred
// per TN3) will replace this with a Redis pubsub-backed implementation.
type InProcBroadcaster struct {
	mu        sync.RWMutex
	rooms     map[string][]*subscriber // subs keyed by room id
	bufSize   int                      // per-subscriber channel buffer
	droppedMu sync.Mutex
	dropped   map[string]int64 // roomID -> dropped-op count (observability hook)
}

type subscriber struct {
	ch     chan Op
	cancel chan struct{}
}

// NewInProcBroadcaster returns a new broadcaster. bufSize is the per-subscriber
// channel buffer depth; a reasonable default is 64. Slow subscribers that
// exceed this depth drop newer ops.
func NewInProcBroadcaster(bufSize int) *InProcBroadcaster {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &InProcBroadcaster{
		rooms:   make(map[string][]*subscriber),
		bufSize: bufSize,
		dropped: make(map[string]int64),
	}
}

// Broadcast delivers op to all subscribers of roomID. Non-blocking per
// subscriber: if a subscriber's buffer is full, the op is dropped for that
// subscriber only and the dropped counter for the room is incremented.
//
// Satisfies: T2 (non-blocking, off-mutex-of-Room)
func (b *InProcBroadcaster) Broadcast(roomID string, op Op) error {
	b.mu.RLock()
	subs := b.rooms[roomID]
	b.mu.RUnlock()

	var drops int64
	for _, s := range subs {
		select {
		case s.ch <- op:
		case <-s.cancel:
			// Subscriber is shutting down; skip.
		default:
			drops++
		}
	}
	if drops > 0 {
		b.droppedMu.Lock()
		b.dropped[roomID] += drops
		b.droppedMu.Unlock()
	}
	return nil
}

// Subscribe registers a new subscriber for roomID. Returns the receive channel
// and a cancel func that must be called to unregister (also closes the channel).
func (b *InProcBroadcaster) Subscribe(roomID string) (<-chan Op, func()) {
	s := &subscriber{
		ch:     make(chan Op, b.bufSize),
		cancel: make(chan struct{}),
	}

	b.mu.Lock()
	b.rooms[roomID] = append(b.rooms[roomID], s)
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		subs := b.rooms[roomID]
		for i, cur := range subs {
			if cur == s {
				b.rooms[roomID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(b.rooms[roomID]) == 0 {
			delete(b.rooms, roomID)
		}
		b.mu.Unlock()

		close(s.cancel)
		close(s.ch)
	}
	return s.ch, cancel
}

// DroppedFor returns the cumulative count of ops dropped for roomID due to
// subscriber backpressure. Used by O1 metrics export.
//
// Satisfies: O1 (observable backpressure)
func (b *InProcBroadcaster) DroppedFor(roomID string) int64 {
	b.droppedMu.Lock()
	defer b.droppedMu.Unlock()
	return b.dropped[roomID]
}
