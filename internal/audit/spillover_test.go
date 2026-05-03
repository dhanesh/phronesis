package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// failingSink simulates a sink whose Write fails — letting tests
// exercise the "batch stays in journal" leg of SpilloverSink.
type failingSink struct{}

func (failingSink) Write(_ context.Context, _ []Event) error {
	return errors.New("simulated sink failure")
}
func (failingSink) Close(_ context.Context) error { return nil }

// @constraint TN6 / RT-10 — happy path: SpilloverSink appends to the
// journal, calls inner sink, truncates on success. No file content
// remains after a successful write.
func TestSpilloverHappyPathTruncatesJournal(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "spillover.jsonl")
	mem := &memSink{}
	s, err := NewSpilloverSink(mem, journalPath)
	if err != nil {
		t.Fatalf("NewSpilloverSink: %v", err)
	}

	evts := []Event{
		{At: time.Now(), Action: "page.write", PrincipalID: "alice", PrincipalType: "user"},
		{At: time.Now(), Action: "key.use", PrincipalID: "phr_live_xyz", PrincipalType: "service_account"},
	}
	if err := s.Write(context.Background(), evts); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Inner sink got the batch.
	if got := mem.Count(); got != 2 {
		t.Fatalf("inner sink got %d events, want 2", got)
	}

	// Journal file is empty after the successful round-trip.
	info, err := os.Stat(journalPath)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("journal should be empty after successful write, got size=%d", info.Size())
	}

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// @constraint TN6 — when inner sink fails, the batch stays in the
// journal so the next startup's ReplaySpillover can drain it.
func TestSpilloverInnerFailureRetainsBatchInJournal(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "spillover.jsonl")
	s, err := NewSpilloverSink(failingSink{}, journalPath)
	if err != nil {
		t.Fatalf("NewSpilloverSink: %v", err)
	}

	evts := []Event{
		{At: time.Now(), Action: "lost.event", PrincipalID: "alice", PrincipalType: "user"},
	}
	err = s.Write(context.Background(), evts)
	if err == nil {
		t.Fatal("expected error from failing inner sink")
	}

	info, err := os.Stat(journalPath)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("journal should retain the batch after inner-sink failure")
	}

	// ReplaySpillover with a working sink drains the batch.
	mem := &memSink{}
	s.journal.close() // close the journal so Replay can re-open it
	n, err := ReplaySpillover(context.Background(), journalPath, mem)
	if err != nil {
		t.Fatalf("ReplaySpillover: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 replayed event, got %d", n)
	}
	if got := mem.Count(); got != 1 {
		t.Errorf("memSink got %d events after replay, want 1", got)
	}

	// Journal file is removed after successful replay.
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("expected journal removed after replay, stat err=%v", err)
	}
}

// @constraint TN6 — calling ReplaySpillover when no journal file
// exists is a no-op. (Common case after a clean shutdown.)
func TestReplaySpilloverNoFileIsNoop(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	mem := &memSink{}
	n, err := ReplaySpillover(context.Background(), missing, mem)
	if err != nil {
		t.Fatalf("ReplaySpillover: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 events, got %d", n)
	}
	if got := mem.Count(); got != 0 {
		t.Errorf("memSink should be untouched, got %d events", got)
	}
}

// @constraint TN6 — empty journal (created but nothing written) is
// removed on Replay rather than left behind.
func TestReplaySpilloverEmptyFileIsRemoved(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "empty.jsonl")
	f, err := os.Create(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	mem := &memSink{}
	n, err := ReplaySpillover(context.Background(), journalPath, mem)
	if err != nil {
		t.Fatalf("ReplaySpillover: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 events, got %d", n)
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatal("expected empty journal removed")
	}
}

func TestSpilloverEmptyBatchIsNoop(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "spillover.jsonl")
	s, err := NewSpilloverSink(&memSink{}, journalPath)
	if err != nil {
		t.Fatalf("NewSpilloverSink: %v", err)
	}
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("nil batch: %v", err)
	}
	if err := s.Write(context.Background(), []Event{}); err != nil {
		t.Fatalf("empty slice: %v", err)
	}
	_ = s.Close(context.Background())
}

// @constraint TN6 — a clean Close leaves the journal file in place
// (empty after the last successful Write); the next NewServer
// startup's ReplaySpillover handles it as the empty-file case.
func TestSpilloverCleanCloseRetainsFile(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "spillover.jsonl")
	mem := &memSink{}
	s, err := NewSpilloverSink(mem, journalPath)
	if err != nil {
		t.Fatalf("NewSpilloverSink: %v", err)
	}
	_ = s.Write(context.Background(), []Event{
		{At: time.Now(), Action: "x", PrincipalID: "y", PrincipalType: "user"},
	})
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("file should still exist after Close: %v", err)
	}
}
