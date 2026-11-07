package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"gorm.io/gorm"
)

func openMem(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s, _ := d.DB()
		s.Close()
	})
	return d
}

func exec(t *testing.T, d *gorm.DB, q string, args ...any) error {
	t.Helper()
	return d.Exec(q, args...).Error
}

func count(t *testing.T, d *gorm.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := d.Raw("SELECT COUNT(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFreshSchemaHasAllTables(t *testing.T) {
	d := openMem(t)
	for _, table := range []string{
		"users", "identities", "sites", "memberships", "invites", "sessions",
		"posts", "post_revisions", "tags", "post_tags", "sections", "section_pages",
	} {
		var n int64
		if err := d.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %q missing from the fresh schema", table)
		}
	}
}

// A database migrated by an earlier ntx has goose versions 1-3 recorded. A
// rewritten schema would be skipped silently on it, so Open must refuse.
func TestRefusesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY, version_id INTEGER, is_applied INTEGER, tstamp TEXT)",
		"INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (3, 1, '2026-01-01')",
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	d, err := Open("sqlite://" + path)
	if err == nil {
		s, _ := d.DB()
		s.Close()
		t.Fatal("expected Open to refuse a pre-100 database")
	}
	if !errors.Is(err, ErrLegacySchema) || !strings.Contains(err.Error(), "delete") {
		t.Errorf("expected ErrLegacySchema telling the operator to delete the database, got: %v", err)
	}
}

// Foreign keys must be on for every connection, or deleting a site would
// leave its posts and sessions behind.
func TestDeletingSiteCascades(t *testing.T) {
	d := openMem(t)
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(t, d, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	must("INSERT INTO users (id, name, is_root, is_platform_admin, created_at) VALUES (1, 'root', 1, 0, CURRENT_TIMESTAMP)")
	must("INSERT INTO sites (id, key, hostname, name, description, about_slug, theme, created_at, updated_at) VALUES (1, 'a', 'a.example.com', 'A', '', 'about', 'default', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)")
	must("INSERT INTO posts (site_id, slug, title, body, published, created_at, updated_at) VALUES (1, 'p', 'P', '.', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)")
	must("INSERT INTO sessions (token_hash, user_id, site_id, created_at, expires_at, last_seen_at) VALUES ('h', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)")
	must("DELETE FROM sites WHERE id = 1")
	if n := count(t, d, "posts"); n != 0 {
		t.Errorf("posts should cascade with their site, %d left", n)
	}
	if n := count(t, d, "sessions"); n != 0 {
		t.Errorf("sessions should cascade with their site, %d left", n)
	}
}

func TestOnlyOneRootUser(t *testing.T) {
	d := openMem(t)
	if err := exec(t, d, "INSERT INTO users (name, is_root, is_platform_admin, created_at) VALUES ('root', 1, 0, CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	if err := exec(t, d, "INSERT INTO users (name, is_root, is_platform_admin, created_at) VALUES ('root2', 1, 0, CURRENT_TIMESTAMP)"); err == nil {
		t.Error("a second is_root user must be rejected")
	}
	if err := exec(t, d, "INSERT INTO users (email, name, is_root, is_platform_admin, created_at) VALUES ('a@x.com', 'a', 0, 0, CURRENT_TIMESTAMP)"); err != nil {
		t.Errorf("ordinary users must be allowed: %v", err)
	}
}

func TestOnePendingInvitePerSiteEmail(t *testing.T) {
	d := openMem(t)
	if err := exec(t, d, "INSERT INTO sites (id, key, hostname, name, description, about_slug, theme, created_at, updated_at) VALUES (1, 'a', 'a.example.com', 'A', '', 'about', 'default', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	ins := func(hash string, usedAt any) error {
		return exec(t, d, "INSERT INTO invites (site_id, email, role, can_publish, token_hash, expires_at, used_at, created_at) VALUES (1, 'b@x.com', 'contributor', 0, ?, CURRENT_TIMESTAMP, ?, CURRENT_TIMESTAMP)", hash, usedAt)
	}
	if err := ins("t1", nil); err != nil {
		t.Fatal(err)
	}
	if err := ins("t2", nil); err == nil {
		t.Error("a second pending invite for the same site and email must be rejected")
	}
	if err := ins("t3", "2026-01-01 00:00:00"); err != nil {
		t.Errorf("a used invite alongside a pending one must be allowed: %v", err)
	}
}
