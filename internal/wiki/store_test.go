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
	if page.Tagged == nil {
		t.Errorf("missing page Tagged should be non-nil empty slice")
	}
}

func TestPagesByTagReflectsIncomingHashtags(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.Put("alpha", "tagged with #urgent and #review"); err != nil {
		t.Fatalf("Put alpha: %v", err)
	}
	if _, err := store.Put("beta", "also #urgent here"); err != nil {
		t.Fatalf("Put beta: %v", err)
	}
	if _, err := store.Put("gamma", "no tags"); err != nil {
		t.Fatalf("Put gamma: %v", err)
	}

	urgent, err := store.PagesByTag("urgent")
	if err != nil {
		t.Fatalf("PagesByTag: %v", err)
	}
	if !slices.Equal(urgent, []string{"alpha", "beta"}) {
		t.Errorf("PagesByTag(urgent) = %v; want [alpha beta]", urgent)
	}

	review, err := store.PagesByTag("review")
	if err != nil {
		t.Fatalf("PagesByTag review: %v", err)
	}
	if !slices.Equal(review, []string{"alpha"}) {
		t.Errorf("PagesByTag(review) = %v; want [alpha]", review)
	}

	// Re-Put alpha to drop the urgent tag — the index must reflect.
	if _, err := store.Put("alpha", "now plain text"); err != nil {
		t.Fatalf("Put alpha rewrite: %v", err)
	}
	urgent, err = store.PagesByTag("urgent")
	if err != nil {
		t.Fatalf("PagesByTag after rewrite: %v", err)
	}
	if !slices.Equal(urgent, []string{"beta"}) {
		t.Errorf("PagesByTag(urgent) after rewrite = %v; want [beta]", urgent)
	}
}

func TestGetExposesTaggedField(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.Put("alpha", "first page #urgent"); err != nil {
		t.Fatalf("Put alpha: %v", err)
	}

	// Visiting `urgent` (a page that doesn't exist on disk) returns
	// alpha as a tagged page so the frontend can render a synthetic
	// tag-index view.
	page, err := store.Get("urgent")
	if err != nil {
		t.Fatalf("Get urgent: %v", err)
	}
	if !slices.Equal(page.Tagged, []string{"alpha"}) {
		t.Errorf("Tagged = %v; want [alpha]", page.Tagged)
	}
}
