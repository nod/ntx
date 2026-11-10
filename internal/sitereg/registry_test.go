package sitereg

import (
	"testing"

	"github.com/nod/ntx/internal/db/dbtest"
	"github.com/nod/ntx/internal/models"
)

func newStore(t *testing.T) *models.SiteStore {
	t.Helper()
	d := dbtest.Open(t)
	return models.NewSiteStore(d)
}

func create(t *testing.T, s *models.SiteStore, key string) *models.Site {
	t.Helper()
	site := &models.Site{Key: key, Hostname: key + ".example.com", Theme: "default"}
	if err := s.Create(site, "admin.example.com"); err != nil {
		t.Fatal(err)
	}
	return site
}

func TestRegistryReload(t *testing.T) {
	store := newStore(t)
	a := create(t, store, "a")
	reg, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.ByHost("a.example.com"); got == nil || got.ID != a.ID {
		t.Fatalf("ByHost(a) = %+v", got)
	}
	if got := reg.ByID(a.ID); got == nil || got.Key != "a" {
		t.Fatalf("ByID(a) = %+v", got)
	}

	create(t, store, "b")
	if reg.ByHost("b.example.com") != nil {
		t.Fatal("a new site must not appear before Reload")
	}
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	if reg.ByHost("b.example.com") == nil {
		t.Fatal("Reload must pick up new sites")
	}
	if all := reg.All(); len(all) != 2 || all[0].Key != "a" || all[1].Key != "b" {
		t.Fatalf("All() should list a, b in key order: %+v", all)
	}

	if err := store.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	reg.Reload()
	if reg.ByHost("a.example.com") != nil || reg.ByID(a.ID) != nil {
		t.Error("a deleted site must disappear after Reload")
	}
}

func TestRegistryReturnsCopies(t *testing.T) {
	store := newStore(t)
	create(t, store, "a")
	reg, _ := New(store)
	got := reg.ByHost("a.example.com")
	got.Name = "mutated"
	got.Sections = append(got.Sections, models.Section{Slug: "x"})
	again := reg.ByHost("a.example.com")
	if again.Name == "mutated" || len(again.Sections) != 0 {
		t.Error("callers must not be able to change the registry's copy")
	}
}

// Vars are maps (with nested JSON maps and lists): writing to a copy's
// vars, at any depth, must not reach the registry's.
func TestRegistryCopiesVars(t *testing.T) {
	store := newStore(t)
	a := create(t, store, "a")
	vars := func() map[string]any {
		return map[string]any{"k": "v", "nested": map[string]any{"n": "1"}, "list": []any{"x"}}
	}
	if err := store.SetSections(a.ID, []models.Section{{Slug: "s", Vars: vars(), Pages: []models.SectionPage{{Slug: "p", Vars: vars()}}}}); err != nil {
		t.Fatal(err)
	}
	reg, _ := New(store)
	scribble := func(m map[string]any) {
		m["k"] = "changed"
		m["added"] = true
		m["nested"].(map[string]any)["n"] = "changed"
		m["list"].([]any)[0] = "changed"
	}
	got := reg.ByID(a.ID)
	scribble(got.Sections[0].Vars)
	scribble(got.Sections[0].Pages[0].Vars)
	all := reg.All()
	scribble(all[0].Sections[0].Vars)

	again := reg.ByHost("a.example.com")
	for name, m := range map[string]map[string]any{"section": again.Sections[0].Vars, "page": again.Sections[0].Pages[0].Vars} {
		if m["k"] != "v" || m["added"] != nil || m["nested"].(map[string]any)["n"] != "1" || m["list"].([]any)[0] != "x" {
			t.Errorf("%s vars changed through a copy: %v", name, m)
		}
	}
}
