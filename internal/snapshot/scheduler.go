package snapshot

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Source supplies the content to snapshot. The snapshot package is decoupled
// from the wiki store; callers wire their own Source (typically over
// internal/wiki + internal/blob) so this package stays testable.
//
// Satisfies: RT-6.3 (server-level snapshot wiring)
type Source interface {
	// Workspaces enumerates all workspace ids currently known to the server.
	Workspaces(ctx context.Context) ([]string, error)
	// SnapshotFor returns the current content of workspaceID. Implementations
	// MUST include both markdown AND blob bytes per TN4.
	SnapshotFor(ctx context.Context, workspaceID string) (Snapshot, error)
}

// Scheduler periodically invokes Source.SnapshotFor for every workspace and
// stores the result via Target.Store. It is a background goroutine with
// graceful-drain semantics (O5).
//
// Satisfies: RT-6.3, O7 (periodic snapshot), O5 (drain on SIGTERM)
type Scheduler struct {
	target   Target
	src      Source
	interval time.Duration
	log      *slog.Logger

	started int32
	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex // serializes access to started via Start/Stop
}

// NewScheduler constructs a scheduler. If interval <= 0, it defaults to 1h per O7.
// log may be nil; a discard logger is used instead.
func NewScheduler(target Target, src Source, interval time.Duration, log *slog.Logger) *Scheduler {
	if interval <= 0 {
		interval = time.Hour
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io_Discard{}, nil))
	}
	return &Scheduler{
		target:   target,
		src:      src,
		interval: interval,
		log:      log,
		done:     make(chan struct{}),
	}
}

// Start begins the periodic snapshot loop in a background goroutine.
// Calling Start more than once is a programmer error; returns an error.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started == 1 {
		return errors.New("snapshot: scheduler already started")
	}
	s.started = 1
	s.wg.Add(1)
	go s.loop()
	return nil
}

// Stop signals the loop to exit and waits up to ctx for the goroutine to
// finish. Any in-flight snapshot completes before exit.
//
// Satisfies: O5 (graceful drain)
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.started == 0 {
		s.mu.Unlock()
		return nil
	}
	close(s.done)
	s.started = 0
	s.mu.Unlock()

	finished := make(chan struct{})
	go func() { s.wg.Wait(); close(finished) }()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunOnce performs a single snapshot cycle across all workspaces. Exported so
// callers can trigger snapshots manually (admin action) and so tests can drive
// the scheduling logic deterministically without time.Sleep.
func (s *Scheduler) RunOnce(ctx context.Context) (stored int, err error) {
	ws, err := s.src.Workspaces(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ws {
		snap, err := s.src.SnapshotFor(ctx, id)
		if err != nil {
			s.log.Warn("snapshot: source error", "workspace", id, "err", err.Error())
			continue
		}
		snap.At = time.Now().UTC()
		if _, err := s.target.Store(ctx, snap); err != nil {
			s.log.Warn("snapshot: store error", "workspace", id, "err", err.Error())
			continue
		}
		stored++
	}
	return stored, nil
}

func (s *Scheduler) loop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), s.interval/2)
			if _, err := s.RunOnce(ctx); err != nil {
				s.log.Warn("snapshot: cycle error", "err", err.Error())
			}
			cancel()
		}
	}
}

// io_Discard is a tiny local shim to keep this package self-contained; the
// slog handler just needs an io.Writer for discard output and we avoid
// importing io purely for io.Discard.
type io_Discard struct{}

func (io_Discard) Write(p []byte) (int, error) { return len(p), nil }
