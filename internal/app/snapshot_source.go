package app

import (
	"context"
	"fmt"

	"github.com/dhanesh/phronesis/internal/blob"
	"github.com/dhanesh/phronesis/internal/snapshot"
	"github.com/dhanesh/phronesis/internal/wiki"
)

// wikiSource adapts wiki.Workspaces + blob.LocalFSStore into
// snapshot.Source for INT-6. Each workspace is enumerated as a distinct
// snapshot bucket; the existing snapshot subsystem already iterates by
// workspaceID so multi-workspace just means more entries returned by
// Workspaces().
type wikiSource struct {
	workspaces *wiki.Workspaces
	blobs      blob.Store
}

func (w *wikiSource) Workspaces(_ context.Context) ([]string, error) {
	metas := w.workspaces.List()
	out := make([]string, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.Slug)
	}
	return out, nil
}

func (w *wikiSource) SnapshotFor(_ context.Context, workspaceID string) (snapshot.Snapshot, error) {
	store, _, ok := w.workspaces.Get(workspaceID)
	if !ok {
		return snapshot.Snapshot{}, fmt.Errorf("unknown workspace %q", workspaceID)
	}
	summaries, err := store.List()
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	md := make(map[string][]byte, len(summaries))
	for _, sum := range summaries {
		page, err := store.Get(sum.Name)
		if err != nil {
			continue
		}
		md[sum.Name+".md"] = []byte(page.Content)
	}
	// TN4: snapshots include blobs too. Blob enumeration from blob.Store
	// requires per-workspace listing (not currently exposed); v1 skips blob
	// capture here and documents that as a known incompleteness. Future:
	// add blob.Store.Enumerate(workspaceID) + populate snap.Blobs.
	return snapshot.Snapshot{
		WorkspaceID: workspaceID,
		Markdown:    md,
	}, nil
}
