// Package audit implements the S2 audit log with an S9 async-buffered drainer.
//
// Satisfies: RT-4.4, S2, S9 (async off hot path)
//
// Shape:
//   - Sink  : persistence interface (swap for Postgres/ClickHouse later).
//   - BufferedDrainer : fronts a Sink with a bounded-capacity queue, drops
//     oldest events on saturation with a metric callback, and drains to the
//     Sink from a background goroutine.
//
// Hot-path rule: callers invoke Drainer.Enqueue which is O(1) and never
// blocks on disk. If the buffer is full, the oldest event is dropped and the
// onDrop callback fires; this preserves T3 read-latency budget even under
// audit-write pressure.
package audit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Event is one audit record.
//
// Satisfies: S2 (records principal + workspace + action + resource), TN8
// propagation (principal_type field distinguishes user vs service_account).
type Event struct {
	At            time.Time
	Action        string            // e.g., "auth.login", "doc.edit", "admin.role_change", "doc.view"
	PrincipalID   string
	PrincipalType string            // "user" | "service_account" (RT-5)
	WorkspaceID   string
	ResourceID    string            // optional: document id, user id, etc.
	Metadata      map[string]string // free-form attributes (ip, ua, request_id)
}

// Sink is the persistent backend for audit events.
//
// Implementations MUST be safe for concurrent use.
type Sink interface {
	Write(ctx context.Context, events []Event) error
	Close(ctx context.Context) error
}

// BufferedDrainer is the async pipeline between hot-path enqueue and the Sink.
//
// Satisfies: S9, RT-4.4
type BufferedDrainer struct {
	sink     Sink
	buf      chan Event
	batch    int                  // max batch size handed to sink.Write
	interval time.Duration        // max wait before flushing a partial batch
	onDrop   func(dropped int)    // observability hook (O1 counter)
	done     chan struct{}
	wg       sync.WaitGroup
	closed   chan struct{}
}

// DrainerConfig tunes the drainer. Defaults suit T3 (p95 < 100ms) at 1K rps.
type DrainerConfig struct {
	Capacity int           // max events in flight; default 10_000
	Batch    int           // max events per Sink.Write; default 200
	Interval time.Duration // max buffering delay; default 500ms
	OnDrop   func(dropped int)
}

// NewBufferedDrainer wraps sink with a bounded async queue and starts the
// drain goroutine. Close() MUST be called during graceful shutdown (O5) so
// the buffer drains and the sink closes cleanly.
func NewBufferedDrainer(sink Sink, cfg DrainerConfig) *BufferedDrainer {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 10_000
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 200
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.OnDrop == nil {
		cfg.OnDrop = func(int) {}
	}
	d := &BufferedDrainer{
		sink:     sink,
		buf:      make(chan Event, cfg.Capacity),
		batch:    cfg.Batch,
		interval: cfg.Interval,
		onDrop:   cfg.OnDrop,
		done:     make(chan struct{}),
		closed:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.drainLoop()
	return d
}

// Enqueue is the hot-path entry point.
//
// Satisfies: S9 (non-blocking), T3 (O(1), no mutex, no disk)
//
// If the buffer is full, the oldest event is dropped (drop-oldest semantic
// implemented as a single Enqueue retry after a non-blocking receive) and
// onDrop(1) fires. Returns nil always from the caller's perspective.
func (d *BufferedDrainer) Enqueue(evt Event) {
	select {
	case <-d.closed:
		return // ignore after Close
	default:
	}
	select {
	case d.buf <- evt:
	default:
		// Full: drop oldest, try again; on double-failure just drop.
		select {
		case <-d.buf:
			d.onDrop(1)
		default:
		}
		select {
		case d.buf <- evt:
		default:
			d.onDrop(1)
		}
	}
}

// Close drains the remaining buffer (bounded by ctx) and closes the sink.
//
// Satisfies: O5, RT-12 adjacent (drain within bounded window)
func (d *BufferedDrainer) Close(ctx context.Context) error {
	select {
	case <-d.closed:
		return nil
	default:
		close(d.closed)
	}
	close(d.done)

	doneCh := make(chan struct{})
	go func() { d.wg.Wait(); close(doneCh) }()

	select {
	case <-doneCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	return d.sink.Close(ctx)
}

func (d *BufferedDrainer) drainLoop() {
	defer d.wg.Done()

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	batch := make([]Event, 0, d.batch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = d.sink.Write(ctx, batch) // errors surfaced by Sink impl; S9 tolerates bounded loss
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case <-d.done:
			// Drain whatever is left.
			for {
				select {
				case e := <-d.buf:
					batch = append(batch, e)
					if len(batch) >= d.batch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case e := <-d.buf:
			batch = append(batch, e)
			if len(batch) >= d.batch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// ErrClosed is returned by a Sink's Write after Close.
var ErrClosed = errors.New("audit: sink closed")
