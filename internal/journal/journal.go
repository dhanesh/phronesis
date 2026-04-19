// Package journal implements the persistent push-spillover journal that
// backs RT-7 (TN10 resolution). When SIGTERM graceful drain exceeds its
// bounded window with commits still queued, those commits are appended to
// a JSONL file on disk so the next startup can replay them before declaring
// readiness.
//
// Satisfies: RT-7, RT-7.1, RT-7.2, RT-12.3, O5
//
// Design notes:
//   - Append is the hot-path entry. It writes one JSONL line with an
//     fsync so durability is bounded to "before Append returns".
//   - Replay is the cold-path entry run once per process startup. It
//     reads all entries, calls a user-supplied replayer for each, and
//     removes the journal file on full success.
//   - Replayer MUST be idempotent. If Replay is interrupted (crash,
//     context cancel) and re-run later, already-applied entries may be
//     presented again.
//   - Payload bytes are opaque to this package; callers encode their own
//     commit format (git push entries, audit spill batches, etc.) so the
//     journal is reusable.
package journal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is a single journaled item.
//
// Satisfies: RT-7 (durable record), RT-12.3 (payload is opaque, so a git-push
// commit or an audit batch can be spilled using the same journal).
type Entry struct {
	ID          string    `json:"id"`           // caller-supplied unique id
	At          time.Time `json:"at"`           // enqueue time (UTC)
	WorkspaceID string    `json:"workspace_id"` // scope (multi-workspace aware)
	Kind        string    `json:"kind"`         // caller taxonomy: "git.push", "audit.batch", etc.
	Payload     []byte    `json:"payload"`      // opaque to this package
}

// Journal owns an append-only file on disk. Safe for concurrent Append.
type Journal struct {
	path   string
	mu     sync.Mutex
	f      *os.File
	closed bool
}

// Open opens (creating if necessary) the journal at path. The parent
// directory is created with 0755 if missing.
func Open(path string) (*Journal, error) {
	if path == "" {
		return nil, errors.New("journal: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("journal: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("journal: open: %w", err)
	}
	return &Journal{path: path, f: f}, nil
}

// Append adds a single entry and fsyncs before returning, so when Append
// returns nil the entry is durable.
//
// Satisfies: RT-7.1 (durable before SIGTERM exit)
func (j *Journal) Append(ctx context.Context, e Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.ID == "" {
		return errors.New("journal: Entry.ID is required")
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("journal: marshal: %w", err)
	}
	line = append(line, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClosed
	}
	if _, err := j.f.Write(line); err != nil {
		return fmt.Errorf("journal: write: %w", err)
	}
	if err := j.f.Sync(); err != nil {
		return fmt.Errorf("journal: fsync: %w", err)
	}
	return nil
}

// Close closes the underlying file. Safe to call multiple times.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	if err := j.f.Sync(); err != nil && !errors.Is(err, os.ErrClosed) {
		_ = j.f.Close()
		return err
	}
	if err := j.f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

// ErrClosed is returned by Append on a closed Journal.
var ErrClosed = errors.New("journal: closed")

// Replayer is invoked for each entry during Replay. If it returns a non-nil
// error, Replay stops, the journal is NOT removed, and the error is returned
// so callers can decide to retry or escalate (RT-7.2 fail-safe contract).
type Replayer func(ctx context.Context, e Entry) error

// Replay reads the journal file at path and invokes replayer for each entry
// in append order. On successful completion (all replayer calls return nil),
// the journal file is removed atomically so startup does not re-replay.
//
// If the journal does not exist, Replay returns (0, nil) — the caller can
// proceed to declare readiness.
//
// Satisfies: RT-7.2 (replay before /readyz), RT-12.3 (recovery path)
//
// Contract: replayer MUST be idempotent. Partial replay + restart will
// present already-applied entries again.
func Replay(ctx context.Context, path string, replayer Replayer) (int, error) {
	if replayer == nil {
		return 0, errors.New("journal: replayer is required")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("journal: open for replay: %w", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	// Allow larger lines (default is 64KB). Journal entries may include
	// multi-KB payloads (e.g., a git push batch).
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return count, fmt.Errorf("journal: unmarshal line %d: %w", count+1, err)
		}
		if err := replayer(ctx, e); err != nil {
			return count, fmt.Errorf("journal: replayer error at entry %d (%s): %w", count+1, e.ID, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("journal: scan: %w", err)
	}

	// All entries replayed successfully; remove the journal so we don't
	// re-replay them next startup.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return count, fmt.Errorf("journal: remove after replay: %w", err)
	}
	return count, nil
}

// Exists reports whether a journal file is present AND non-empty. Used by
// /readyz probes to block "ready" until Replay has run.
//
// Satisfies: RT-7.2 (readiness gating)
func Exists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.Size() > 0, nil
}
