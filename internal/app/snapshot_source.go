package app

import (
	"context"
	"fmt"

	"github.com/dhanesh/phronesis/internal/blob"
	"github.com/dhanesh/phronesis/internal/snapshot"
	"github.com/dhanesh/phronesis/internal/wiki"
)

// wikiSource adapts wiki.Store + blob.LocalFSStore into snapshot.Source for
// INT-6. V1 enumerates a single "default" workspace; multi-workspace
// enumeration is a future-wave extension.
type wikiSource struct {
	store *wiki.Store
	blobs blob.Store
}

func (w *wikiSource) Workspaces(_ context.Context) ([]string, error) {
	return []string{defaultWorkspaceID}, nil
}

func (w *wikiSource) SnapshotFor(_ context.Context, workspaceID string) (snapshot.Snapshot, error) {
	if workspaceID != defaultWorkspaceID {
		return snapshot.Snapshot{}, fmt.Errorf("unknown workspace %q", workspaceID)
	}
	summaries, err := w.store.List()
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	md := make(map[string][]byte, len(summaries))
	for _, sum := range summaries {
		page, err := w.store.Get(sum.Name)
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
