// Package snapshot provides periodic corpus backups for collab-wiki workspaces.
//
// Satisfies: RT-4.3 adjacent, O7, TN4 propagation (snapshot covers markdown AND
// blob store together).
//
// The Target interface abstracts the storage destination so v1 can ship a local
// filesystem default (single-binary mode) and drop in S3-compatible or restic
// adapters later without touching the snapshotting logic.
package snapshot

import (
	"context"
	"errors"
	"time"
)

// Snapshot is a point-in-time capture of a single workspace's content.
//
// TN4 propagation: Blobs MUST be populated alongside Markdown; a snapshot that
// omits media would silently lose data on restore.
type Snapshot struct {
	WorkspaceID string
	At          time.Time
	Markdown    map[string][]byte // path -> content (relative to workspace root)
	Blobs       map[string][]byte // sha256-hex -> bytes
}

// Info is a lightweight summary returned by Target.List.
type Info struct {
	ID          string // opaque, target-specific
	WorkspaceID string
	At          time.Time
	Size        int64 // bytes on target; -1 if unknown
}

// ErrNotFound is returned when Restore or stat operations cannot locate the id.
var ErrNotFound = errors.New("snapshot: not found")

// Target is the persistence backend for snapshots.
//
// Implementations MUST be safe for concurrent use across workspaces. Operations
// on the same workspace SHOULD be serialized by callers (the Snapshotter engine
// does this).
type Target interface {
	Store(ctx context.Context, s Snapshot) (Info, error)
	List(ctx context.Context, workspaceID string) ([]Info, error)
	Restore(ctx context.Context, id string) (Snapshot, error)
	Delete(ctx context.Context, id string) error
}
