package models

import (
	"testing"

	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
)

func TestEnsureSitesAssignsIDs(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com"},
		{Key: "b", SiteURL: "b.example.com"},
	}
	if err := EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}
	if sites[0].ID == 0 || sites[1].ID == 0 {
		t.Fatalf("expected non-zero IDs, got %+v", sites)
	}
	if sites[0].ID == sites[1].ID {
		t.Fatal("expected distinct IDs")
	}
}

func TestEnsureSitesReconcilesHostname(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	first := []config.Site{{Key: "default", SiteURL: "old-host.example.com"}}
	if err := EnsureSites(database, first); err != nil {
		t.Fatal(err)
	}
	firstID := first[0].ID

	second := []config.Site{{Key: "default", SiteURL: "new-host.example.com"}}
	if err := EnsureSites(database, second); err != nil {
		t.Fatal(err)
	}
	if second[0].ID != firstID {
		t.Errorf("expected same ID across reconciliation, got %d then %d", firstID, second[0].ID)
	}

	var r dbSite
	if err := database.Where("key = ?", "default").First(&r).Error; err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "new-host.example.com" {
		t.Errorf("hostname not reconciled: got %q", r.Hostname)
	}
}
