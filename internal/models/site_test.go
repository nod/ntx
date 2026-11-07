package models

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/db"
)

const testPlatformHost = "admin.example.com"

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

func mustCreateSite(t *testing.T, s *SiteStore, key, host string) *Site {
	t.Helper()
	site := &Site{Key: key, Hostname: host, Theme: "default"}
	if err := s.Create(site, testPlatformHost); err != nil {
		t.Fatalf("create site %q: %v", key, err)
	}
	return site
}

func isValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

func countRows(t *testing.T, d *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := d.Raw("SELECT COUNT(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateAndList(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	b := mustCreateSite(t, s, "b", "B.Example.com")
	mustCreateSite(t, s, "a", "a.example.com")

	sites, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].Key != "a" || sites[1].Key != "b" {
		t.Fatalf("expected sites ordered a, b; got %+v", sites)
	}
	if b.ID == 0 {
		t.Error("Create must set the ID")
	}
	got := sites[1]
	if got.Hostname != "b.example.com" {
		t.Errorf("hostname should be stored lowercase, got %q", got.Hostname)
	}
	if got.AboutSlug != "about" || got.Name != "b" {
		t.Errorf("expected defaults about_slug=about, name=key; got %q, %q", got.AboutSlug, got.Name)
	}
}

func TestCreateValidates(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	mustCreateSite(t, s, "a", "a.example.com")

	for name, site := range map[string]*Site{
		"bad key":            {Key: "../x", Hostname: "x.example.com", Theme: "default"},
		"bad hostname":       {Key: "x", Hostname: "https://x.com", Theme: "default"},
		"platform hostname":  {Key: "x", Hostname: "Admin.Example.com", Theme: "default"},
		"duplicate key":      {Key: "a", Hostname: "other.example.com", Theme: "default"},
		"duplicate hostname": {Key: "x", Hostname: "A.example.com", Theme: "default"},
		"bad theme":          {Key: "x", Hostname: "x.example.com", Theme: "../t"},
	} {
		if err := s.Create(site, testPlatformHost); !isValidation(err) {
			t.Errorf("%s: expected *ValidationError, got %v", name, err)
		}
	}
	if n := countRows(t, d, "sites"); n != 1 {
		t.Errorf("rejected creates must insert nothing; %d sites", n)
	}
}

func TestUpdateRejectsHostnameConflicts(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	a := mustCreateSite(t, s, "a", "a.example.com")
	mustCreateSite(t, s, "b", "b.example.com")

	for _, host := range []string{"b.example.com", "B.EXAMPLE.COM", testPlatformHost} {
		edit := *a
		edit.Hostname = host
		if err := s.Update(&edit, testPlatformHost); !isValidation(err) {
			t.Errorf("hostname %q: expected *ValidationError, got %v", host, err)
		}
	}
	sites, _ := s.List()
	if sites[0].Hostname != "a.example.com" {
		t.Errorf("rejected update must not change the hostname, got %q", sites[0].Hostname)
	}
}

func TestUpdateKeepsKey(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	a := mustCreateSite(t, s, "a", "a.example.com")
	edit := *a
	edit.Key = "renamed"
	edit.Name = "New Name"
	if err := s.Update(&edit, testPlatformHost); err != nil {
		t.Fatal(err)
	}
	sites, _ := s.List()
	if sites[0].Key != "a" || sites[0].Name != "New Name" {
		t.Errorf("key must be immutable while other fields update; got key %q name %q", sites[0].Key, sites[0].Name)
	}
}

func TestHostnameChangeDeletesSessions(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	a := mustCreateSite(t, s, "a", "a.example.com")
	if err := d.Exec("INSERT INTO users (id, name, is_root, is_platform_admin, created_at) VALUES (1, 'root', 1, 0, CURRENT_TIMESTAMP)").Error; err != nil {
		t.Fatal(err)
	}
	addSession := func() {
		t.Helper()
		if err := d.Exec("INSERT INTO sessions (token_hash, user_id, site_id, created_at, expires_at, last_seen_at) VALUES (?, 1, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", "h"+a.Name, a.ID).Error; err != nil {
			t.Fatal(err)
		}
	}
	addSession()

	same := *a
	same.Name = "Renamed"
	if err := s.Update(&same, testPlatformHost); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d, "sessions"); n != 1 {
		t.Errorf("an update that keeps the hostname must keep sessions; %d left", n)
	}

	moved := same
	moved.Hostname = "new.example.com"
	if err := s.Update(&moved, testPlatformHost); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d, "sessions"); n != 0 {
		t.Errorf("changing the hostname must delete the site's sessions; %d left", n)
	}
}

func TestSetSectionsValidatesAndReplaces(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	a := mustCreateSite(t, s, "a", "a.example.com")

	orig := []Section{{Slug: "projects", Title: "Projects", Pages: []SectionPage{{Slug: "one", Title: "One"}}}}
	if err := s.SetSections(a.ID, orig); err != nil {
		t.Fatal(err)
	}

	for name, secs := range map[string][]Section{
		"reserved slug":  {{Slug: "blog"}},
		"bad page slug":  {{Slug: "x", Pages: []SectionPage{{Slug: "../x"}}}},
		"duplicate slug": {{Slug: "x"}, {Slug: "x"}},
		"dup page slug":  {{Slug: "x", Pages: []SectionPage{{Slug: "p"}, {Slug: "p"}}}},
	} {
		if err := s.SetSections(a.ID, secs); !isValidation(err) {
			t.Errorf("%s: expected *ValidationError, got %v", name, err)
		}
	}
	sites, _ := s.List()
	if len(sites[0].Sections) != 1 || sites[0].Sections[0].Slug != "projects" {
		t.Fatalf("rejected sections must leave the old ones intact, got %+v", sites[0].Sections)
	}

	next := []Section{
		{Slug: "writing", Title: "Writing", Vars: map[string]any{"year": "2026"}, Pages: []SectionPage{
			{Slug: "b", Title: "B", Vars: map[string]any{"n": float64(2)}},
			{Slug: "a", Title: "A"},
		}},
	}
	if err := s.SetSections(a.ID, next); err != nil {
		t.Fatal(err)
	}
	sites, _ = s.List()
	got := sites[0].Sections
	if len(got) != 1 || got[0].Slug != "writing" || got[0].Vars["year"] != "2026" {
		t.Fatalf("sections not replaced as expected: %+v", got)
	}
	if len(got[0].Pages) != 2 || got[0].Pages[0].Slug != "b" || got[0].Pages[0].Vars["n"] != float64(2) {
		t.Errorf("pages must keep their given order and vars: %+v", got[0].Pages)
	}
}

func TestDeleteCascadesSections(t *testing.T) {
	d := newTestSiteDB(t)
	s := NewSiteStore(d)
	a := mustCreateSite(t, s, "a", "a.example.com")
	if err := s.SetSections(a.ID, []Section{{Slug: "x", Pages: []SectionPage{{Slug: "p"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d, "sections") + countRows(t, d, "section_pages"); n != 0 {
		t.Errorf("deleting a site must delete its sections; %d rows left", n)
	}
}
