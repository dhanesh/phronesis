package wiki

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewWorkspacesMigratesFlatPagesIntoDefault(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed two flat .md files (pre-multi-workspace layout).
	for _, name := range []string{"home.md", "about.md"} {
		if err := os.WriteFile(filepath.Join(pages, name), []byte("# Hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ws, err := NewWorkspaces(pages, filepath.Join(root, "workspaces.json"))
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}

	// Flat files should now live under default/.
	for _, name := range []string{"home.md", "about.md"} {
		if _, err := os.Stat(filepath.Join(pages, "default", name)); err != nil {
			t.Errorf("default/%s missing: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(pages, name)); err == nil {
			t.Errorf("flat %s should have been moved", name)
		}
	}
	// And the default workspace lists them.
	store, _, ok := ws.Get(DefaultWorkspaceSlug)
	if !ok {
		t.Fatal("default workspace missing after migration")
	}
	pagesList, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pagesList) != 2 {
		t.Errorf("default page count = %d; want 2", len(pagesList))
	}
}

func TestNewWorkspacesIdempotentMigration(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	meta := filepath.Join(root, "workspaces.json")

	// Seed default/ already populated — migration should not run.
	if err := os.MkdirAll(filepath.Join(pages, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pages, "default", "home.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewWorkspaces(pages, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWorkspaces(pages, meta); err != nil {
		t.Fatalf("second NewWorkspaces: %v", err)
	}
}

func TestWorkspacesCreateAndDelete(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	meta := filepath.Join(root, "workspaces.json")

	ws, err := NewWorkspaces(pages, meta)
	if err != nil {
		t.Fatal(err)
	}

	created, err := ws.Create("research", "Research")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Slug != "research" || created.Name != "Research" {
		t.Errorf("created = %+v", created)
	}

	// Default + research show up in List.
	list := ws.List()
	if len(list) != 2 {
		t.Fatalf("List len = %d; want 2", len(list))
	}
	if list[0].Slug != "default" {
		t.Errorf("first slug = %q; want default", list[0].Slug)
	}

	// Page directory for research exists on disk.
	if _, err := os.Stat(filepath.Join(pages, "research")); err != nil {
		t.Errorf("research dir missing: %v", err)
	}

	// Reopening picks up the persisted metadata.
	ws2, err := NewWorkspaces(pages, meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws2.List()) != 2 {
		t.Errorf("reopened workspace count != 2")
	}

	// Duplicate slug returns ErrWorkspaceExists.
	if _, err := ws.Create("research", "another"); err != ErrWorkspaceExists {
		t.Errorf("duplicate Create err = %v; want ErrWorkspaceExists", err)
	}

	// Delete removes the workspace + dir.
	if err := ws.Delete("research"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, ok := ws.Get("research"); ok {
		t.Errorf("Get after Delete should be false")
	}
	if _, err := os.Stat(filepath.Join(pages, "research")); err == nil {
		t.Errorf("research dir should have been removed")
	}

	// Delete of default is refused.
	if err := ws.Delete("default"); err != ErrCannotDeleteDefault {
		t.Errorf("delete default err = %v; want ErrCannotDeleteDefault", err)
	}
}

func TestValidateSlug(t *testing.T) {
	good := []string{"default", "research", "team-alpha", "team-1", "x", "0to1"}
	bad := []string{"", "Default", "with spaces", "with/slash", "-leading", "trailing-",
		"way-way-way-way-way-way-way-way-way-way-way-way-way-way-way-way-too-long-slug"}
	for _, s := range good {
		if err := ValidateSlug(s); err != nil {
			t.Errorf("ValidateSlug(%q) errored: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidateSlug(s); err == nil {
			t.Errorf("ValidateSlug(%q) should have errored", s)
		}
	}
}

func TestWorkspacesAutoDiscoversOrphanDirectories(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	meta := filepath.Join(root, "workspaces.json")

	// Create a workspace dir out-of-band (no metadata file yet).
	if err := os.MkdirAll(filepath.Join(pages, "imported"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pages, "imported", "x.md"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspaces(pages, meta)
	if err != nil {
		t.Fatal(err)
	}
	list := ws.List()
	if len(list) != 2 {
		t.Fatalf("auto-discovery count = %d; want 2 (default + imported)", len(list))
	}
	if _, _, ok := ws.Get("imported"); !ok {
		t.Error("imported workspace should have been auto-discovered")
	}
}
