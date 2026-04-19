package crdt

import (
	"context"
	"testing"
	"time"
)

// @constraint RT-4.2 T2
// An in-process broadcaster delivers a broadcast op to every subscribed
// channel within a short window.
func TestInProcBroadcasterFansOutToAllSubscribers(t *testing.T) {
	b := NewInProcBroadcaster(8)

	chA, cancelA := b.Subscribe("doc-1")
	defer cancelA()
	chB, cancelB := b.Subscribe("doc-1")
	defer cancelB()

	op := Op{RoomID: "doc-1", Seq: 1, Payload: []byte("hello")}
	if err := b.Broadcast("doc-1", op); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	seen := 0
	deadline := time.After(100 * time.Millisecond)
	for seen < 2 {
		select {
		case got := <-chA:
			if got.Seq != 1 {
				t.Errorf("chA Seq: got %d, want 1", got.Seq)
			}
			chA = nil // consumed; stop reselecting
			seen++
		case got := <-chB:
			if got.Seq != 1 {
				t.Errorf("chB Seq: got %d, want 1", got.Seq)
			}
			chB = nil
			seen++
		case <-deadline:
			t.Fatalf("only %d subscribers received within 100ms", seen)
		}
	}
}

// @constraint RT-4.2 T2
// Broadcast to a room with no subscribers must not error and must not block.
func TestInProcBroadcasterEmptyRoomNoBlock(t *testing.T) {
	b := NewInProcBroadcaster(8)
	done := make(chan struct{})
	go func() {
		_ = b.Broadcast("empty", Op{RoomID: "empty", Seq: 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Broadcast to empty room blocked")
	}
}

// @constraint RT-4.2 T2 O1
// A slow subscriber whose buffer is saturated drops its ops but does not
// stall the broadcaster or other subscribers, and the drop counter increments.
func TestInProcBroadcasterSlowSubscriberDoesNotStall(t *testing.T) {
	b := NewInProcBroadcaster(2) // small buffer to force saturation

	_, cancelSlow := b.Subscribe("doc-2") // never drain
	defer cancelSlow()
	chFast, cancelFast := b.Subscribe("doc-2")
	defer cancelFast()

	for i := 0; i < 10; i++ {
		if err := b.Broadcast("doc-2", Op{RoomID: "doc-2", Seq: int64(i)}); err != nil {
			t.Fatalf("broadcast %d: %v", i, err)
		}
	}

	// Fast subscriber should receive the first few; we only care it did not block.
	received := 0
	drainDeadline := time.After(100 * time.Millisecond)
	for {
		select {
		case <-chFast:
			received++
		case <-drainDeadline:
			goto done
		}
	}
done:
	if received == 0 {
		t.Error("fast subscriber received nothing; broadcaster likely stalled")
	}
	if got := b.DroppedFor("doc-2"); got == 0 {
		t.Error("DroppedFor: expected > 0 after saturating slow subscriber")
	}
}

// @constraint RT-4.2
// Subscribe + cancel must clean up: after cancel, Broadcast should no longer
// send to the cancelled channel, and the room entry should be reclaimed.
func TestInProcBroadcasterSubscribeCancel(t *testing.T) {
	b := NewInProcBroadcaster(8)

	ch, cancel := b.Subscribe("doc-3")
	cancel()

	// Reading from a closed channel returns zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel yielded a value after cancel")
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("cancelled channel not closed within 50ms")
	}

	if err := b.Broadcast("doc-3", Op{RoomID: "doc-3", Seq: 1}); err != nil {
		t.Errorf("Broadcast after cancel: %v", err)
	}
}

// @constraint RT-4.2 RT-2 T2
// Integration with Room: a Room wired to InProcBroadcaster delivers Apply'd
// ops to a subscriber's channel.
func TestRoomThroughInProcBroadcaster(t *testing.T) {
	b := NewInProcBroadcaster(16)
	f := &stubFlusher{}
	r := NewRoom("doc-int", DefaultFlushPolicy(), b, f)
	defer r.Close(context.Background())

	ch, cancel := b.Subscribe("doc-int")
	defer cancel()

	if _, err := r.Apply(Op{Actor: "alice", Payload: []byte("hi")}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	select {
	case got := <-ch:
		if got.Seq != 1 {
			t.Errorf("Seq: got %d, want 1", got.Seq)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber did not receive op within 100ms")
	}
}

// stubFlusher is a no-op flusher for tests that only exercise broadcast paths.
type stubFlusher struct{}

func (s *stubFlusher) Flush(roomID string, ops []Op) error { return nil }
