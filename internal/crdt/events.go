// Package crdt implements the durability event model that is the binding
// constraint (RT-2) for the collab-wiki feature. The package is intentionally
// CRDT-library-agnostic: Op.Payload is opaque here and will be filled in when
// a concrete CRDT engine is selected in Wave 2.
//
// RT-2.1: Server emits two distinct events per op (OpAckedEvent, OpSavedEvent).
//   - OpAckedEvent is emitted after a received op has been broadcast to peers.
//   - OpSavedEvent is emitted after a batch of ops is disk-durable per the O8
//     flush policy.
//
// These two events feed the client's two-state durability indicator (RT-2.2)
// and are the observability hook for B1's bounded-loss definition (TN1, O8).
package crdt

import "time"

// Op is a single edit operation in a collaborative session.
//
// Satisfies: RT-2 (binding), T1
//
// The concrete Payload format is deliberately opaque to this package. A future
// CRDT engine (Yjs/Automerge/Y-CRDT via FFI) will define Payload semantics; the
// event shape and flush policy defined here are independent of that choice.
type Op struct {
	RoomID  string
	Seq     int64     // monotonic per-room sequence; assigned on Apply
	Actor   string    // principal id; Wave 3 (RT-5) supplies a Principal type
	At      time.Time // wall-clock receipt time
	Payload []byte    // opaque to this layer
}

// OpAckedEvent fires after an op has been applied to the room and broadcast to
// peers. This is the "synced" state of RT-2.2's client state machine.
//
// Satisfies: RT-2.1, TN6 (two-state indicator)
type OpAckedEvent struct {
	RoomID string
	Seq    int64
	At     time.Time
}

// OpSavedEvent fires after a batch of ops has been flushed to disk under the
// O8 policy. ThroughSeq is the highest sequence number that is now disk-durable.
// This is the "saved" state of RT-2.2.
//
// Satisfies: RT-2.1, RT-2.3, O8
type OpSavedEvent struct {
	RoomID     string
	ThroughSeq int64 // all ops with Seq <= ThroughSeq are disk-durable
	FlushedAt  time.Time
	FlushDurMs int64 // observed flush duration; feeds O1 metrics + RT-2.4
}

// Broadcaster is how a Room fans an op out to peer clients.
//
// Satisfies: RT-4.2 (CRDT broadcast interface, with in-process default in Wave 2)
//
// The spike supplies a test double; Wave 2 supplies the real SSE/WebSocket impl.
type Broadcaster interface {
	Broadcast(roomID string, op Op) error
}

// Flusher persists a batch of ops to the authoritative .md file on disk.
//
// Satisfies: RT-4.3 adjacent (snapshot/flush boundary), O6 (atomic write), T1
//
// The spike supplies a test double; Wave 2 wires this through internal/wiki's
// storage layer with atomic write-tempfile + fsync + rename per O6.
type Flusher interface {
	Flush(roomID string, ops []Op) error
}
