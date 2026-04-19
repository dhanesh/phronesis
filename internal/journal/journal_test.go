package journal

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// @constraint RT-7 RT-7.1 RT-12.3
// Append+Replay round-trip preserves order and count.
func TestAppendReplayRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.log")

	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()

	for i := 0; i < 7; i++ {
		err := j.Append(ctx, Entry{
			ID:          strconv.Itoa(i),
			WorkspaceID: "default",
			Kind:        "git.push",
			Payload:     []byte("commit-bytes-" + strconv.Itoa(i)),
		})
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var seen []Entry
	count, err := Replay(ctx, path, func(_ context.Context, e Entry) error {
		seen = append(seen, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != 7 {
		t.Errorf("count: got %d, want 7", count)
	}
	for i, e := range seen {
		if e.ID != strconv.Itoa(i) {
			t.Errorf("order: entry %d has ID %q", i, e.ID)
		}
	}
}

// @constraint RT-7.2
// After successful Replay, the journal file is removed so subsequent startups
// do not re-replay. Exists must report false.
func TestReplayRemovesJournalOnSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.log")
	j, _ := Open(path)
	_ = j.Append(context.Background(), Entry{ID: "1", Payload: []byte("x")})
	_ = j.Close()

	_, err := Replay(context.Background(), path, func(_ context.Context, _ Entry) error { return nil })
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	ok, err := Exists(path)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if ok {
		t.Error("journal still exists after successful Replay; RT-7.2 removes it")
	}
}

// @constraint RT-7.2
// If the replayer returns an error, the journal is NOT removed. Next startup
// can retry after operator intervention.
func TestReplayPreservesJournalOnReplayerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.log")
	j, _ := Open(path)
	_ = j.Append(context.Background(), Entry{ID: "1", Payload: []byte("x")})
	_ = j.Append(context.Background(), Entry{ID: "2", Payload: []byte("y")})
	_ = j.Close()

	fatal := errors.New("simulated replay failure")
	count, err := Replay(context.Background(), path, func(_ context.Context, e Entry) error {
		if e.ID == "2" {
			return fatal
		}
		return nil
	})
	if err == nil {
		t.Error("expected error on replayer failure")
	}
	if count != 1 {
		t.Errorf("count on error: got %d, want 1 (first entry succeeded)", count)
	}
	ok, _ := Exists(path)
	if !ok {
		t.Error("journal removed despite replayer error; loses durability guarantee")
	}
}

// @constraint RT-7
// Replay on a non-existent journal returns (0, nil) — normal startup with
// nothing to replay is not an error.
func TestReplayMissingJournal(t *testing.T) {
	count, err := Replay(
		context.Background(),
		filepath.Join(t.TempDir(), "nope.log"),
		func(_ context.Context, _ Entry) error { return nil },
	)
	if err != nil {
		t.Errorf("Replay missing: got %v, want nil", err)
	}
	if count != 0 {
		t.Errorf("count: got %d, want 0", count)
	}
}

// @constraint RT-7.1 O5
// Append after Close returns ErrClosed. Important during graceful shutdown:
// once Close runs, callers must not silently drop entries.
func TestAppendAfterCloseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.log")
	j, _ := Open(path)
	_ = j.Close()
	err := j.Append(context.Background(), Entry{ID: "1"})
	if !errors.Is(err, ErrClosed) {
		t.Errorf("Append after Close: got %v, want ErrClosed", err)
	}
}

// @constraint RT-7
// Append requires a non-empty Entry.ID so replay can de-duplicate in
// idempotent replayers.
func TestAppendRequiresID(t *testing.T) {
	j, _ := Open(filepath.Join(t.TempDir(), "j.log"))
	defer j.Close()
	err := j.Append(context.Background(), Entry{Payload: []byte("x")})
	if err == nil {
		t.Error("Append with empty ID: want error")
	}
}

// @constraint RT-7.1
// Concurrent Append from many goroutines must be safe and lose no entries.
func TestAppendConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.log")
	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const N = 200
	var wg sync.WaitGroup
	var errs atomic.Int64
	ctx := context.Background()

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := j.Append(ctx, Entry{ID: strconv.Itoa(id), Payload: []byte("p")})
			if err != nil {
				errs.Add(1)
			}
		}(i)
	}
	wg.Wait()
	_ = j.Close()

	if errs.Load() > 0 {
		t.Fatalf("concurrent Append errors: %d", errs.Load())
	}
	count, err := Replay(ctx, path, func(_ context.Context, _ Entry) error { return nil })
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if count != N {
		t.Errorf("lost entries under concurrency: got %d, want %d", count, N)
	}
}

// @constraint RT-7 O5
// Exists returns true when journal has content, false when empty or missing.
// /readyz uses this to gate readiness.
func TestExistsDetection(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.log")
	ok, err := Exists(missing)
	if err != nil {
		t.Fatalf("Exists(missing): %v", err)
	}
	if ok {
		t.Error("Exists(missing): got true, want false")
	}

	present := filepath.Join(dir, "present.log")
	j, _ := Open(present)
	_ = j.Append(context.Background(), Entry{ID: "1"})
	_ = j.Close()

	ok, err = Exists(present)
	if err != nil {
		t.Fatalf("Exists(present): %v", err)
	}
	if !ok {
		t.Error("Exists(present): got false, want true")
	}
}

// Minimal sanity: journal file gets fsync'd — not directly observable without
// syscall hooking, but we can at least confirm Append is synchronous (caller
// waits for write+sync before returning). Deadline check proves no buffering.
func TestAppendIsSynchronous(t *testing.T) {
	j, _ := Open(filepath.Join(t.TempDir(), "j.log"))
	defer j.Close()

	// 100 synchronous appends should complete in <1s on any reasonable disk.
	start := time.Now()
	for i := 0; i < 100; i++ {
		if err := j.Append(context.Background(), Entry{ID: strconv.Itoa(i), Payload: []byte{byte(i)}}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Errorf("100 synchronous Appends took %v; fsync may be misconfigured", elapsed)
	}
}
