package crdt

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeBroadcaster records every op handed to Broadcast.
type fakeBroadcaster struct {
	mu  sync.Mutex
	ops []Op
}

func (f *fakeBroadcaster) Broadcast(roomID string, op Op) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
	return nil
}

func (f *fakeBroadcaster) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ops)
}

// fakeFlusher records every batch handed to Flush.
type fakeFlusher struct {
	mu      sync.Mutex
	batches [][]Op
}

func (f *fakeFlusher) Flush(roomID string, ops []Op) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := make([]Op, len(ops))
	copy(batch, ops)
	f.batches = append(f.batches, batch)
	return nil
}

func (f *fakeFlusher) BatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

func (f *fakeFlusher) TotalOps() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

// @constraint RT-2.1 RT-2 T2
// Apply must emit an OpAckedEvent after broadcasting and BEFORE flush.
// This is the "synced" state of the two-state indicator (TN6).
func TestApplyEmitsAckedBeforeSaved(t *testing.T) {
	b := &fakeBroadcaster{}
	f := &fakeFlusher{}
	// Large idle window + high op threshold so the flush loop does not fire
	// during this test; we want to observe acked without saved.
	policy := FlushPolicy{MaxIdle: time.Hour, MaxOps: 10_000}
	r := NewRoom("doc-1", policy, b, f)
	defer r.Close(context.Background())

	if _, err := r.Apply(Op{Actor: "alice", Payload: []byte("hello")}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	select {
	case evt := <-r.Acked():
		if evt.Seq != 1 {
			t.Errorf("Seq: got %d, want 1", evt.Seq)
		}
		if evt.RoomID != "doc-1" {
			t.Errorf("RoomID: got %q, want doc-1", evt.RoomID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no OpAckedEvent within 100ms of Apply")
	}

	select {
	case saved := <-r.Saved():
		t.Errorf("unexpected OpSavedEvent before flush conditions met: %+v", saved)
	case <-time.After(50 * time.Millisecond):
		// Correct: no save event yet.
	}
}

// @constraint O8 RT-2.3 RT-2 TN1
// The op-count trigger must fire a flush when pending ops reach MaxOps,
// independent of the idle timer.
func TestFlushFiresOnOpCountThreshold(t *testing.T) {
	b := &fakeBroadcaster{}
	f := &fakeFlusher{}
	policy := FlushPolicy{MaxIdle: time.Hour, MaxOps: 5}
	r := NewRoom("doc-2", policy, b, f)
	defer r.Close(context.Background())

	for i := 0; i < 5; i++ {
		if _, err := r.Apply(Op{Payload: []byte{byte(i)}}); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}

	select {
	case saved := <-r.Saved():
		if saved.ThroughSeq < 5 {
			t.Errorf("ThroughSeq: got %d, want >= 5", saved.ThroughSeq)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no OpSavedEvent within 2s despite %d pending ops", policy.MaxOps)
	}

	if got := f.BatchCount(); got == 0 {
		t.Errorf("flusher received %d batches, want >= 1", got)
	}
}

// @constraint O8 RT-2.3 TN1
// The idle trigger must fire a flush after MaxIdle of inactivity.
func TestFlushFiresOnIdleTimer(t *testing.T) {
	b := &fakeBroadcaster{}
	f := &fakeFlusher{}
	// Short idle so the test completes quickly; high ops so only idle fires.
	policy := FlushPolicy{MaxIdle: 150 * time.Millisecond, MaxOps: 10_000}
	r := NewRoom("doc-3", policy, b, f)
	defer r.Close(context.Background())

	if _, err := r.Apply(Op{Payload: []byte("edit")}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	select {
	case saved := <-r.Saved():
		if saved.Seq() != 1 {
			// OpSavedEvent does not expose Seq directly; ThroughSeq should be 1.
			t.Errorf("ThroughSeq: got %d, want 1", saved.ThroughSeq)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("no OpSavedEvent within 1s; idle trigger did not fire")
	}
}

// Helper: allow the "Seq" expectation above to compile while OpSavedEvent
// exposes only ThroughSeq. Keeping this as a conservative guard in case the
// event shape evolves.
func (e OpSavedEvent) Seq() int64 { return e.ThroughSeq }

// @constraint O5 RT-12.2
// Close must trigger a final flush even with pending ops below both thresholds.
// This is the graceful-drain contract from TN10.
func TestCloseForcesFinalFlush(t *testing.T) {
	b := &fakeBroadcaster{}
	f := &fakeFlusher{}
	policy := FlushPolicy{MaxIdle: time.Hour, MaxOps: 10_000}
	r := NewRoom("doc-4", policy, b, f)

	for i := 0; i < 3; i++ {
		if _, err := r.Apply(Op{Payload: []byte{byte(i)}}); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}

	if got := f.TotalOps(); got != 0 {
		t.Fatalf("flusher prematurely invoked: %d ops", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := f.TotalOps(); got != 3 {
		t.Errorf("after Close, flusher has %d ops, want 3", got)
	}
}

// @constraint RT-2.1 T2
// Every op must be broadcast before the acked event is emitted. This ensures
// the "synced" state observed by clients is a true peer-visibility signal.
func TestBroadcastPrecedesAcked(t *testing.T) {
	b := &fakeBroadcaster{}
	f := &fakeFlusher{}
	r := NewRoom("doc-5", DefaultFlushPolicy(), b, f)
	defer r.Close(context.Background())

	if _, err := r.Apply(Op{Payload: []byte("a")}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	select {
	case <-r.Acked():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no acked event")
	}
	if got := b.Count(); got != 1 {
		t.Errorf("broadcaster received %d ops, want 1", got)
	}
}

// @constraint RT-2 B1 TN1
// DefaultFlushPolicy must match the O8 thresholds in the manifold (3s / 100 ops).
// Changing this test requires updating O8 AND re-opening TN1.
func TestDefaultPolicyMatchesO8Thresholds(t *testing.T) {
	p := DefaultFlushPolicy()
	if p.MaxIdle != 3*time.Second {
		t.Errorf("MaxIdle: got %v, want 3s (O8 threshold)", p.MaxIdle)
	}
	if p.MaxOps != 100 {
		t.Errorf("MaxOps: got %d, want 100 (O8 threshold)", p.MaxOps)
	}
}
