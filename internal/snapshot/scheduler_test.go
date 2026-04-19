package snapshot

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memSource is a simple Source for tests.
type memSource struct {
	mu         sync.Mutex
	workspaces []string
	snaps      map[string]Snapshot
	calls      atomic.Int64
}

func (m *memSource) Workspaces(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := make([]string, len(m.workspaces))
	copy(ws, m.workspaces)
	return ws, nil
}

func (m *memSource) SnapshotFor(_ context.Context, id string) (Snapshot, error) {
	m.calls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snaps[id]
	if !ok {
		return Snapshot{WorkspaceID: id, At: time.Now().UTC()}, nil
	}
	return s, nil
}

// memTarget is a Target for tests.
type memTarget struct {
	mu    sync.Mutex
	saved []Snapshot
}

func (t *memTarget) Store(_ context.Context, s Snapshot) (Info, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.saved = append(t.saved, s)
	return Info{ID: "mem", WorkspaceID: s.WorkspaceID, At: s.At}, nil
}
func (t *memTarget) List(_ context.Context, _ string) ([]Info, error)      { return nil, nil }
func (t *memTarget) Restore(_ context.Context, _ string) (Snapshot, error) { return Snapshot{}, nil }
func (t *memTarget) Delete(_ context.Context, _ string) error              { return nil }

func (t *memTarget) Saved() []Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Snapshot, len(t.saved))
	copy(out, t.saved)
	return out
}

// @constraint RT-6.3 O7 TN4
// RunOnce iterates every known workspace and persists each via the Target.
func TestSchedulerRunOnceIteratesWorkspaces(t *testing.T) {
	src := &memSource{
		workspaces: []string{"ws-1", "ws-2", "ws-3"},
		snaps: map[string]Snapshot{
			"ws-1": {WorkspaceID: "ws-1", Markdown: map[string][]byte{"a.md": []byte("a")}},
			"ws-2": {WorkspaceID: "ws-2", Blobs: map[string][]byte{"ff": []byte("x")}},
			"ws-3": {WorkspaceID: "ws-3"},
		},
	}
	tgt := &memTarget{}
	s := NewScheduler(tgt, src, time.Hour, nil)

	stored, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stored != 3 {
		t.Errorf("stored: got %d, want 3", stored)
	}
	saved := tgt.Saved()
	if len(saved) != 3 {
		t.Errorf("target stored: got %d, want 3", len(saved))
	}
	// At should be set by scheduler, not left zero.
	for _, s := range saved {
		if s.At.IsZero() {
			t.Errorf("WorkspaceID=%s: At not set by scheduler", s.WorkspaceID)
		}
	}
}

// @constraint RT-6.3 O5
// Start + Stop: the loop fires at least once on a short interval, Stop drains cleanly.
func TestSchedulerStartFiresThenStopsCleanly(t *testing.T) {
	src := &memSource{workspaces: []string{"ws-1"}}
	tgt := &memTarget{}
	s := NewScheduler(tgt, src, 50*time.Millisecond, nil)

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait long enough for ~2 ticks.
	time.Sleep(130 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if got := src.calls.Load(); got < 1 {
		t.Errorf("source Call count: got %d, want >= 1", got)
	}
}

// @constraint RT-6.3
// Start twice should error rather than leak a second goroutine.
func TestSchedulerStartTwiceErrors(t *testing.T) {
	s := NewScheduler(&memTarget{}, &memSource{}, time.Hour, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer s.Stop(context.Background())

	if err := s.Start(); err == nil {
		t.Error("second Start: want error")
	}
}

// @constraint RT-6.3 O5
// Stop on a scheduler that was never started returns nil.
func TestSchedulerStopWithoutStartIsNoop(t *testing.T) {
	s := NewScheduler(&memTarget{}, &memSource{}, time.Hour, nil)
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop without Start: got %v, want nil", err)
	}
}
