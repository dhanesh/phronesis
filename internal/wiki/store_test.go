package wiki

import (
	"slices"
	"testing"
)

func TestBacklinksReflectIncomingLinksAndUpdateOnPut(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if _, err := store.Put("alpha", "see [[beta]] and [[gamma]]"); err != nil {
		t.Fatalf("Put alpha: %v", err)
	}
	if _, err := store.Put("beta", "back to [[alpha]]"); err != nil {
		t.Fatalf("Put beta: %v", err)
	}

	betaBacklinks, err := store.Backlinks("beta")
	if err != nil {
		t.Fatalf("Backlinks beta: %v", err)
	}
	if !slices.Equal(betaBacklinks, []string{"alpha"}) {
		t.Errorf("beta backlinks = %v; want [alpha]", betaBacklinks)
	}

	// Rewrite alpha so it no longer mentions beta — the cache must reflect that.
	if _, err := store.Put("alpha", "see [[gamma]] only"); err != nil {
		t.Fatalf("Put alpha rewrite: %v", err)
	}
	betaBacklinks, err = store.Backlinks("beta")
	if err != nil {
		t.Fatalf("Backlinks beta after rewrite: %v", err)
	}
	if len(betaBacklinks) != 0 {
		t.Errorf("beta backlinks after rewrite = %v; want empty", betaBacklinks)
	}
}

func TestNewStoreRebuildsLinkGraphFromExistingFiles(t *testing.T) {
	dir := t.TempDir()

	first, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := first.Put("alpha", "see [[beta]]"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Re-open the store. The backlink graph should be rebuilt from disk
	// without any further Puts.
	second, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore re-open: %v", err)
	}
	got, err := second.Backlinks("beta")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("backlinks after re-open = %v; want [alpha]", got)
	}
}

func TestGetMissingPageReturnsEmpty(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	page, err := store.Get("does-not-exist")
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if page.Content != "" || page.Version != 0 {
		t.Errorf("missing page = %+v; want zero", page)
	}
}
