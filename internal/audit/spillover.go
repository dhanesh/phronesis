package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// SpilloverSink wraps an inner Sink with an fsync'd JSONL spillover
// journal. Every Write batch is appended to the journal BEFORE the
// inner Sink is invoked; on success the journal is truncated. This
// bounds loss-on-crash to one batch interval — if the process is
// killed between append and inner-Write, the next NewServer
// startup's ReplaySpillover drains the journal into the sink.
//
// Satisfies: TN6 failure-cascade ("crash before drain" -> bounded
//
//	to 1 batch interval),
//	RT-10 (audit drainer + retention + bounded loss-on-
//	       crash — the bounded-loss leaf).
//
// Cost: one fsync per batch (default 1s tick or batch-full). At
// human-traffic rates this is negligible; at peak AI-agent burst
// (e.g. 100 keys × 10 req/s) the drainer batches them so the per-
// batch fsync cost amortises.
type SpilloverSink struct {
	inner   Sink
	journal *spilloverJournal
}

// NewSpilloverSink wraps inner with a spillover journal at the
// given path. The journal file is opened immediately; failure
// returns an error and the inner Sink is left untouched.
func NewSpilloverSink(inner Sink, journalPath string) (*SpilloverSink, error) {
	j, err := openSpilloverJournal(journalPath)
	if err != nil {
		return nil, err
	}
	return &SpilloverSink{inner: inner, journal: j}, nil
}

// Write appends the batch to the spillover journal, calls the
// inner Sink, and truncates the journal on success.
//
// Failure semantics:
//   - Append-then-inner-fail leaves the batch in the journal so
//     ReplaySpillover on next startup picks it up.
//   - Append-fail returns immediately without invoking inner. The
//     drainer will retry on its next tick; the dropped events are
//     surfaced via the BufferedDrainer's drop counter.
func (s *SpilloverSink) Write(ctx context.Context, events []Event) error {
	if err := s.journal.appendBatch(events); err != nil {
		return fmt.Errorf("audit spillover: append: %w", err)
	}
	if err := s.inner.Write(ctx, events); err != nil {
		// Journal retains the batch; ReplaySpillover or a future
		// successful inner-Write will eventually drain it.
		return fmt.Errorf("audit spillover: inner write: %w", err)
	}
	if err := s.journal.truncate(); err != nil {
		// Couldn't clear the journal. Subsequent successful Writes
		// will retry truncate, and ReplaySpillover would replay
		// already-written events — but the audit_events INSERT path
		// is not idempotent (no UNIQUE on the natural key), so a
		// double-replay would produce duplicate rows. Surface the
		// error so the operator can intervene.
		return fmt.Errorf("audit spillover: truncate after inner write: %w", err)
	}
	return nil
}

// Close closes the inner Sink and the journal file. The journal is
// NOT truncated — pending events stay on disk for the next startup
// to replay.
func (s *SpilloverSink) Close(ctx context.Context) error {
	innerErr := s.inner.Close(ctx)
	jErr := s.journal.close()
	if innerErr != nil {
		return innerErr
	}
	return jErr
}

// ReplaySpillover drains any events left in the spillover journal
// at journalPath into sink, then truncates the journal. Called by
// NewServer at startup before any new events are accepted.
//
// If journalPath does not exist, returns (0, nil) — the common
// case for a clean shutdown's previous run.
func ReplaySpillover(ctx context.Context, journalPath string, sink Sink) (int, error) {
	f, err := os.OpenFile(journalPath, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("audit spillover: open for replay: %w", err)
	}
	defer f.Close()

	var events []Event
	dec := json.NewDecoder(bufio.NewReader(f))
	for {
		var e Event
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, fmt.Errorf("audit spillover: decode event: %w", err)
		}
		events = append(events, e)
	}

	if len(events) == 0 {
		// File exists but empty — clean shutdown left an empty
		// journal. Truncate to canonicalise and we're done.
		_ = os.Remove(journalPath)
		return 0, nil
	}
	if err := sink.Write(ctx, events); err != nil {
		return 0, fmt.Errorf("audit spillover: replay sink.Write: %w", err)
	}
	// Successfully drained — remove the file so it's clean for the
	// next process incarnation.
	_ = f.Close()
	if err := os.Remove(journalPath); err != nil {
		return len(events), fmt.Errorf("audit spillover: remove after replay: %w", err)
	}
	return len(events), nil
}

// spilloverJournal is the on-disk JSONL file backing SpilloverSink.
// One JSON-encoded Event per line, fsync'd per batch.
type spilloverJournal struct {
	path string

	mu sync.Mutex
	f  *os.File
}

func openSpilloverJournal(path string) (*spilloverJournal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit spillover: open %q: %w", path, err)
	}
	return &spilloverJournal{path: path, f: f}, nil
}

func (j *spilloverJournal) appendBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	w := bufio.NewWriter(j.f)
	enc := json.NewEncoder(w)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return j.f.Sync()
}

// truncate empties the journal file. Called after a successful
// inner Sink.Write so the next batch starts from an empty journal.
func (j *spilloverJournal) truncate() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.f.Truncate(0); err != nil {
		return err
	}
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return nil
}

func (j *spilloverJournal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}
