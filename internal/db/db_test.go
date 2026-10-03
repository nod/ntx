package db

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestSQLiteUpgradePreservesData proves the 001 -> 002 migration preserves
// pre-existing posts/tags/post_tags rows instead of silently losing them.
// Under SQLite, a DROP TABLE performs an implicit DELETE first, and with
// foreign_keys=ON that DELETE cascades through any ON DELETE CASCADE
// references -- dropping posts/tags while post_tags still points at them
// would wipe every post<->tag association. This test seeds 001-era data,
// applies 002, and asserts nothing was lost.
func TestSQLiteUpgradePreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enabling foreign_keys: %v", err)
	}

	sub, err := fs.Sub(sqliteMigrations, "migrations/sqlite")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, sub)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// Migrate to the 001 schema only (no site_id yet).
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatalf("migrating to 001: %v", err)
	}

	// Seed 001-era data: two posts, two tags, three post_tags associations.
	now := "2026-01-01 00:00:00"
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO posts (id, slug, title, body, published, created_at, updated_at)
		 VALUES (1, 'post-one', 'Post One', 'body one', 1, ?, ?),
		        (2, 'post-two', 'Post Two', 'body two', 0, ?, ?)`,
		now, now, now, now); err != nil {
		t.Fatalf("seeding posts: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO tags (id, name) VALUES (1, 'go'), (2, 'testing')`); err != nil {
		t.Fatalf("seeding tags: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO post_tags (post_id, tag_id) VALUES (1, 1), (1, 2), (2, 1)`); err != nil {
		t.Fatalf("seeding post_tags: %v", err)
	}

	// Apply 002 (and any later migrations).
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrating to latest: %v", err)
	}

	// The assertion that catches the cascade bug: post_tags must still have
	// all 3 associations. Under the buggy migration (DROP TABLE posts/tags
	// before post_tags is handled), this drops to 0.
	var postTagsCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM post_tags").Scan(&postTagsCount); err != nil {
		t.Fatalf("counting post_tags: %v", err)
	}
	if postTagsCount != 3 {
		t.Errorf("post_tags: expected 3 associations to survive the migration, got %d", postTagsCount)
	}

	var postsCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM posts").Scan(&postsCount); err != nil {
		t.Fatalf("counting posts: %v", err)
	}
	if postsCount != 2 {
		t.Errorf("posts: expected 2 rows to survive, got %d", postsCount)
	}

	var tagsCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM tags").Scan(&tagsCount); err != nil {
		t.Fatalf("counting tags: %v", err)
	}
	if tagsCount != 2 {
		t.Errorf("tags: expected 2 rows to survive, got %d", tagsCount)
	}

	var defaultSiteID int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM sites WHERE key = 'default'").Scan(&defaultSiteID); err != nil {
		t.Fatalf("looking up default site: %v", err)
	}

	rows, err := sqlDB.QueryContext(ctx, "SELECT site_id FROM posts")
	if err != nil {
		t.Fatalf("querying posts.site_id: %v", err)
	}
	for rows.Next() {
		var siteID int64
		if err := rows.Scan(&siteID); err != nil {
			rows.Close()
			t.Fatalf("scanning posts.site_id: %v", err)
		}
		if siteID != defaultSiteID {
			t.Errorf("posts.site_id: expected %d (default site), got %d", defaultSiteID, siteID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating posts rows: %v", err)
	}

	rows, err = sqlDB.QueryContext(ctx, "SELECT site_id FROM tags")
	if err != nil {
		t.Fatalf("querying tags.site_id: %v", err)
	}
	for rows.Next() {
		var siteID int64
		if err := rows.Scan(&siteID); err != nil {
			rows.Close()
			t.Fatalf("scanning tags.site_id: %v", err)
		}
		if siteID != defaultSiteID {
			t.Errorf("tags.site_id: expected %d (default site), got %d", defaultSiteID, siteID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating tags rows: %v", err)
	}

	// PRAGMA foreign_key_check must come back clean.
	fkRows, err := sqlDB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	var violations int
	for fkRows.Next() {
		violations++
	}
	fkRows.Close()
	if err := fkRows.Err(); err != nil {
		t.Fatalf("iterating foreign_key_check rows: %v", err)
	}
	if violations != 0 {
		t.Errorf("foreign_key_check: expected no violations, got %d", violations)
	}

	// Per-site uniqueness: same slug under a different site_id must succeed,
	// a duplicate (site_id, slug) must fail.
	if _, err := sqlDB.ExecContext(ctx,
		"INSERT INTO sites (key, hostname) VALUES ('second', 'second.example.com')"); err != nil {
		t.Fatalf("inserting second site: %v", err)
	}
	var secondSiteID int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM sites WHERE key = 'second'").Scan(&secondSiteID); err != nil {
		t.Fatalf("looking up second site: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO posts (site_id, slug, title, body, published, created_at, updated_at)
		 VALUES (?, 'post-one', 'Post One (site 2)', 'body', 0, ?, ?)`,
		secondSiteID, now, now); err != nil {
		t.Errorf("expected same slug under a different site_id to succeed, got: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO posts (site_id, slug, title, body, published, created_at, updated_at)
		 VALUES (?, 'post-one', 'Duplicate', 'body', 0, ?, ?)`,
		defaultSiteID, now, now); err == nil {
		t.Error("expected duplicate (site_id, slug) insert to fail")
	}
}
