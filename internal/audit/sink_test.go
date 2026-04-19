package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSink struct {
	mu     sync.Mutex
	events []Event
	closed bool
}

func (m *memSink) Write(ctx context.Context, events []Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	batch := make([]Event, len(events))
	copy(batch, events)
	m.events = append(m.events, batch...)
	return nil
}

func (m *memSink) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *memSink) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

// @constraint RT-4.4 S2 S9
// Enqueue is hot-path: must not block. 1000 enqueues must complete in well
// under 100ms even when the sink is slow to drain.
func TestDrainerEnqueueIsHotPath(t *testing.T) {
	sink := &memSink{}
	d := NewBufferedDrainer(sink, DrainerConfig{Capacity: 2000, Batch: 200, Interval: 10 * time.Millisecond})
	defer d.Close(context.Background())

	start := time.Now()
	for i := 0; i < 1000; i++ {
		d.Enqueue(Event{At: time.Now(), Action: "doc.view", PrincipalID: "u1"})
	}
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Errorf("Enqueue 1000 events took %v, expected <50ms (hot-path budget for T3)", elapsed)
	}
}

// @constraint RT-4.4 S9
// Events enqueued before Close must be flushed to the sink.
func TestDrainerFlushesOnClose(t *testing.T) {
	sink := &memSink{}
	d := NewBufferedDrainer(sink, DrainerConfig{Capacity: 200, Batch: 50, Interval: time.Second})

	for i := 0; i < 37; i++ {
		d.Enqueue(Event{At: time.Now(), Action: "doc.edit", PrincipalID: "u1"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := sink.Count(); got != 37 {
		t.Errorf("events persisted: got %d, want 37", got)
	}
}

// @constraint RT-4.4 S9 O1
// Saturation drops oldest with onDrop counter; never blocks, never errors.
func TestDrainerDropsOldestOnSaturation(t *testing.T) {
	var dropped int64
	// Tiny capacity + slow sink = saturation guaranteed.
	sink := &slowSink{delay: 10 * time.Millisecond}
	d := NewBufferedDrainer(sink, DrainerConfig{
		Capacity: 4,
		Batch:    2,
		Interval: 100 * time.Millisecond,
		OnDrop:   func(n int) { atomic.AddInt64(&dropped, int64(n)) },
	})
	defer d.Close(context.Background())

	for i := 0; i < 100; i++ {
		d.Enqueue(Event{At: time.Now(), Action: "doc.view"})
	}

	if atomic.LoadInt64(&dropped) == 0 {
		t.Error("onDrop never fired despite saturation; drop-oldest not working")
	}
}

// @constraint RT-4.4 S2 S3
// FileSink persists events as JSONL and survives a roundtrip read.
func TestFileSinkJSONLRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	s, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	events := []Event{
		{At: time.Now().UTC(), Action: "auth.login", PrincipalID: "u1", PrincipalType: "user"},
		{At: time.Now().UTC(), Action: "doc.edit", PrincipalID: "svc-agent", PrincipalType: "service_account"},
	}
	if err := s.Write(context.Background(), events); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	lines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Errorf("unmarshal line %d: %v", lines, err)
		}
		lines++
	}
	if lines != 2 {
		t.Errorf("JSONL lines: got %d, want 2", lines)
	}
}

// @constraint RT-4.4
// Write after Close returns ErrClosed.
func TestFileSinkWriteAfterCloseErrors(t *testing.T) {
	s, _ := NewFileSink(filepath.Join(t.TempDir(), "a.log"))
	_ = s.Close(context.Background())
	err := s.Write(context.Background(), []Event{{Action: "x"}})
	if err == nil {
		t.Error("Write after Close: want error, got nil")
	}
}

type slowSink struct {
	mu    sync.Mutex
	delay time.Duration
	n     int64
}

func (s *slowSink) Write(ctx context.Context, events []Event) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.n += int64(len(events))
	s.mu.Unlock()
	return nil
}
func (s *slowSink) Close(ctx context.Context) error { return nil }
