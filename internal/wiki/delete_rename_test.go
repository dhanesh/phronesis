package wiki

import (
	"errors"
	"testing"
)

func TestHubDeleteAndRename(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(store)
	if _, _, err := hub.Apply("a", 0, "# A links [[b]] #tag", "u"); err != nil {
		t.Fatal(err)
	}
	ch, cancel, _ := hub.Subscribe("a")
	defer func() { defer func() { _ = recover() }(); cancel() }()
	<-ch // snapshot

	p, err := hub.Rename("a", "Renamed Page")
	if err != nil || p.Name != "renamed-page" || p.Content != "# A links [[b]] #tag" {
		t.Fatalf("rename: %+v %v", p, err)
	}
	if ev := <-ch; ev.Type != "renamed" || ev.Page.Name != "renamed-page" {
		t.Errorf("event = %+v", ev)
	}
	if got, _ := store.Backlinks("b"); len(got) != 1 || got[0] != "renamed-page" {
		t.Errorf("backlinks after rename = %v", got)
	}
	if _, _, err := hub.Apply("c", 0, "c", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Rename("c", "renamed-page"); !errors.Is(err, ErrPageExists) {
		t.Errorf("rename onto existing = %v", err)
	}
	if err := hub.Delete("renamed-page"); err != nil {
		t.Fatal(err)
	}
	if err := hub.Delete("renamed-page"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if got, _ := store.Backlinks("b"); len(got) != 0 {
		t.Errorf("backlinks after delete = %v", got)
	}
	list, _ := store.List()
	if len(list) != 1 || list[0].Name != "c" {
		t.Errorf("list = %+v", list)
	}
}
