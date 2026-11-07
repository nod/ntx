package sitereg

import (
	"testing"

	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/models"
)

func newStore(t *testing.T) *models.SiteStore {
	t.Helper()
	d, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s, _ := d.DB(); s.Close() })
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
