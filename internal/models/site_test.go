package models

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
)

func newTestSiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})
	return database
}

// captureLog redirects the standard logger into a buffer for the rest of
// the test, so warnings can be asserted on.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestEnsureSitesAssignsIDs(t *testing.T) {
	database := newTestSiteDB(t)

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
	database := newTestSiteDB(t)

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

// TestEnsureSitesWarnsAboutOrphanedContent covers the single-site upgrade
// trap: migration 002 backfills all pre-existing posts and tags onto a
// placeholder site with key = "default", and gen-config hands the operator
// key = "mysite". With the wrong key they get a new site row, an empty blog,
// and no error anywhere -- so EnsureSites must at least say so out loud.
func TestEnsureSitesWarnsAboutOrphanedContent(t *testing.T) {
	database := newTestSiteDB(t)

	// The "default" row is created by migration 002; give it content, the way
	// an upgrade from a single-site install would.
	store := NewPostStore(database).ForSite(1)
	if err := store.Create(&Post{Slug: "legacy-post", Title: "Legacy", Body: ".", Tags: []string{"legacy"}}); err != nil {
		t.Fatal(err)
	}

	buf := captureLog(t)

	sites := []config.Site{{Key: "mysite", SiteURL: "mysite.example.com"}}
	if err := EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, `site "default"`) {
		t.Errorf("warning should name the orphaned site key, got: %q", out)
	}
	if !strings.Contains(out, "1 posts / 1 tags") {
		t.Errorf("warning should report the orphaned row counts, got: %q", out)
	}
	if !strings.Contains(out, `key = "default"`) {
		t.Errorf("warning should tell the operator which key to set, got: %q", out)
	}
}

// TestEnsureSitesQuietAboutEmptyRemovedSite is the control for the test
// above: deliberately removing an empty site from the config is legitimate
// and must not produce noise.
func TestEnsureSitesQuietAboutEmptyRemovedSite(t *testing.T) {
	database := newTestSiteDB(t)

	buf := captureLog(t)

	sites := []config.Site{{Key: "mysite", SiteURL: "mysite.example.com"}}
	if err := EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	if out := buf.String(); out != "" {
		t.Errorf("expected no warning for an orphaned site with no content, got: %q", out)
	}
}

// TestEnsureSitesSurvivesKeyRename and the swap test below guard the bug
// migration 003 fixes: sites.hostname used to be NOT NULL UNIQUE, so
// renaming a site's key while keeping its site_url -- or swapping site_url
// between two sites -- aborted startup with an opaque "UNIQUE constraint
// failed: sites.hostname". site_url uniqueness is validated in config.Load,
// which is the enforcement point that gives a clear error.
func TestEnsureSitesSurvivesKeyRename(t *testing.T) {
	database := newTestSiteDB(t)

	before := []config.Site{{Key: "old-key", SiteURL: "mysite.example.com"}}
	if err := EnsureSites(database, before); err != nil {
		t.Fatal(err)
	}

	after := []config.Site{{Key: "new-key", SiteURL: "mysite.example.com"}}
	if err := EnsureSites(database, after); err != nil {
		t.Fatalf("renaming a site's key while keeping its site_url should succeed, got: %v", err)
	}
	if after[0].ID == 0 {
		t.Error("expected the renamed site to get a database id")
	}
	if after[0].ID == before[0].ID {
		t.Error("a new key is a new site row, so it should get a new id")
	}
}

func TestEnsureSitesSurvivesURLSwap(t *testing.T) {
	database := newTestSiteDB(t)

	before := []config.Site{
		{Key: "a", SiteURL: "a.example.com"},
		{Key: "b", SiteURL: "b.example.com"},
	}
	if err := EnsureSites(database, before); err != nil {
		t.Fatal(err)
	}

	swapped := []config.Site{
		{Key: "a", SiteURL: "b.example.com"},
		{Key: "b", SiteURL: "a.example.com"},
	}
	if err := EnsureSites(database, swapped); err != nil {
		t.Fatalf("swapping site_url between two sites should succeed, got: %v", err)
	}
	if swapped[0].ID != before[0].ID || swapped[1].ID != before[1].ID {
		t.Errorf("ids should follow the key, not the url: got %d/%d, want %d/%d",
			swapped[0].ID, swapped[1].ID, before[0].ID, before[1].ID)
	}

	var rows []dbSite
	if err := database.Order("key").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	hostnames := map[string]string{}
	for _, r := range rows {
		hostnames[r.Key] = r.Hostname
	}
	if hostnames["a"] != "b.example.com" || hostnames["b"] != "a.example.com" {
		t.Errorf("hostnames not reconciled after swap: %v", hostnames)
	}
}
