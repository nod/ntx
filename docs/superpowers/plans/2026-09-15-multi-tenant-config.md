# Multi-tenant config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make one ntx process serve multiple independent websites, routed by HTTP Host header, each defined by a `[[site]]` block in one `ntx.toml`.

**Architecture:** `config.Config` gains a `Sites []Site` slice; a boot-time `EnsureSites` upsert gives each site a stable DB id; `posts`/`tags` gain a `site_id` column scoped by that id; a router-level middleware resolves the request's Host to a `*config.Site` and stashes it in context via a small `sitectx` package; `handlers.Handler` holds one `siteHandler` (scoped post store + template cache) per site and reads the current one from context; sessions carry a `site_id` claim so admin logins are scoped per-site, with an optional global admin hash that authenticates everywhere.

**Tech Stack:** Go 1.26, chi v5, GORM (sqlite/postgres), goose migrations, viper/TOML config, bcrypt.

**Spec:** `docs/superpowers/specs/2026-09-15-multi-tenant-config-design.md`

## Global Constraints

- Module path is `github.com/nod/ntx` — all imports use this.
- Existing single-site installs upgrade by adding exactly one `[[site]]` block with `key = "default"` — the schema migration backfills existing `posts`/`tags` rows to a site pre-seeded with that key, and `EnsureSites` reconciles its `hostname` to the configured `site_url` on next boot. No separate migration CLI command.
- `session_secret` stays a single global HMAC key (validated: required, min 32 chars) — unchanged from current behavior.
- Every existing test file (`config_test.go`, `auth_test.go`, `server_test.go`, `post_test.go`) must keep passing (rewritten where its target API changed) — no regressions.
- Follow existing error-wrapping style: `fmt.Errorf("doing thing: %w", err)`.
- Run `go build ./...` and `go test ./...` at the end of every task.

---

### Task 1: Site-scoped database schema

**Files:**
- Create: `internal/db/migrations/sqlite/002_multi_tenant.sql`
- Create: `internal/db/migrations/postgres/002_multi_tenant.sql`
- Test: `internal/models/post_test.go` (extend `newTestDB` usage — no new test file; verified indirectly by Task 3's site-scoping tests, which fail to compile/pass without this schema)

**Interfaces:**
- Produces: a `sites` table (`id`, `key` UNIQUE, `hostname` UNIQUE); `posts.site_id` and `tags.site_id` columns, both `NOT NULL REFERENCES sites(id)`; uniqueness moved from `posts.slug` / `tags.name` (global) to `(site_id, slug)` / `(site_id, name)` (per-site). A pre-existing single-tenant DB gets one backfilled site row: `key='default', hostname='default'`.

- [ ] **Step 1: Write the sqlite migration**

```sql
-- +goose Up

CREATE TABLE IF NOT EXISTS sites (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    key      TEXT    NOT NULL UNIQUE,
    hostname TEXT    NOT NULL UNIQUE
);

-- Upgrade path: any pre-existing posts/tags rows (from before multi-tenancy)
-- get attached to this placeholder site. An upgrading operator's ntx.toml
-- must define a [[site]] block with key = "default" so EnsureSites (see
-- internal/models/site.go) reconciles this row's hostname to their real
-- site_url on next boot.
INSERT INTO sites (key, hostname) VALUES ('default', 'default');

CREATE TABLE posts_new (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    site_id     INTEGER  NOT NULL REFERENCES sites(id),
    slug        TEXT     NOT NULL,
    title       TEXT     NOT NULL,
    body        TEXT     NOT NULL,
    published   INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);
INSERT INTO posts_new (id, site_id, slug, title, body, published, created_at, updated_at)
    SELECT id, (SELECT id FROM sites WHERE key = 'default'), slug, title, body, published, created_at, updated_at
    FROM posts;
DROP TABLE posts;
ALTER TABLE posts_new RENAME TO posts;
CREATE UNIQUE INDEX idx_posts_site_slug ON posts(site_id, slug);

CREATE TABLE tags_new (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER NOT NULL REFERENCES sites(id),
    name    TEXT    NOT NULL
);
INSERT INTO tags_new (id, site_id, name)
    SELECT id, (SELECT id FROM sites WHERE key = 'default'), name
    FROM tags;
DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;
CREATE UNIQUE INDEX idx_tags_site_name ON tags(site_id, name);

-- +goose Down

DROP INDEX IF EXISTS idx_tags_site_name;
DROP INDEX IF EXISTS idx_posts_site_slug;

CREATE TABLE tags_old (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT    NOT NULL UNIQUE
);
INSERT INTO tags_old (id, name) SELECT id, name FROM tags;
DROP TABLE tags;
ALTER TABLE tags_old RENAME TO tags;

CREATE TABLE posts_old (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    slug        TEXT     NOT NULL UNIQUE,
    title       TEXT     NOT NULL,
    body        TEXT     NOT NULL,
    published   INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);
INSERT INTO posts_old (id, slug, title, body, published, created_at, updated_at)
    SELECT id, slug, title, body, published, created_at, updated_at FROM posts;
DROP TABLE posts;
ALTER TABLE posts_old RENAME TO posts;

DROP TABLE IF EXISTS sites;
```

- [ ] **Step 2: Write the postgres migration**

```sql
-- +goose Up

CREATE TABLE IF NOT EXISTS sites (
    id       BIGSERIAL PRIMARY KEY,
    key      TEXT      NOT NULL UNIQUE,
    hostname TEXT      NOT NULL UNIQUE
);

INSERT INTO sites (key, hostname) VALUES ('default', 'default');

ALTER TABLE posts ADD COLUMN site_id BIGINT REFERENCES sites(id);
UPDATE posts SET site_id = (SELECT id FROM sites WHERE key = 'default');
ALTER TABLE posts ALTER COLUMN site_id SET NOT NULL;
ALTER TABLE posts DROP CONSTRAINT posts_slug_key;
CREATE UNIQUE INDEX idx_posts_site_slug ON posts(site_id, slug);

ALTER TABLE tags ADD COLUMN site_id BIGINT REFERENCES sites(id);
UPDATE tags SET site_id = (SELECT id FROM sites WHERE key = 'default');
ALTER TABLE tags ALTER COLUMN site_id SET NOT NULL;
ALTER TABLE tags DROP CONSTRAINT tags_name_key;
CREATE UNIQUE INDEX idx_tags_site_name ON tags(site_id, name);

-- +goose Down

DROP INDEX IF EXISTS idx_tags_site_name;
DROP INDEX IF EXISTS idx_posts_site_slug;

ALTER TABLE tags ADD CONSTRAINT tags_name_key UNIQUE (name);
ALTER TABLE tags DROP COLUMN site_id;

ALTER TABLE posts ADD CONSTRAINT posts_slug_key UNIQUE (slug);
ALTER TABLE posts DROP COLUMN site_id;

DROP TABLE IF EXISTS sites;
```

- [ ] **Step 3: Verify migrations apply cleanly**

Run: `go test ./internal/models/... -run TestCreateAndGetBySlug -v`
Expected: still PASS (this test doesn't reference `site_id` yet, but it exercises `db.Open("sqlite://:memory:")`, which runs this new migration — a syntax error here fails the whole test with a migration error, not a test assertion failure).

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/sqlite/002_multi_tenant.sql internal/db/migrations/postgres/002_multi_tenant.sql
git commit -m "Add sites table and site-scope posts/tags in schema"
```

---

### Task 2: Site model and boot-time upsert

**Files:**
- Create: `internal/models/site.go`
- Test: `internal/models/site_test.go`

**Interfaces:**
- Consumes: `config.Site{ID int64; Key, SiteURL string; ...}` — defined in Task 3 (Multi-site config schema). **Execute Task 3 before this task**; this task's test and implementation both reference `config.Site`, which doesn't exist until Task 3 lands.
- Produces: `models.EnsureSites(db *gorm.DB, sites []config.Site) error` — upserts each site by `Key`, writes the DB-assigned id back onto `sites[i].ID`.

**Note:** Recommended execution order overall: 3, 2, 1, 4, 5, 6, 7, 8, 9, 10 (config schema first since it's the type every other task builds on; DB schema and site model can then land before or after each other; everything else follows the numbering above).

- [ ] **Step 1: Write the failing test**

```go
package models

import (
	"testing"

	"github.com/nod/ntx/internal/config"
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
```

This needs `import "github.com/nod/ntx/internal/db"` too — add it alongside the `config` import.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/models/... -run TestEnsureSites -v`
Expected: FAIL — `EnsureSites` and `dbSite` undefined.

- [ ] **Step 3: Write the implementation**

```go
package models

import (
	"gorm.io/gorm"

	"github.com/nod/ntx/internal/config"
)

// dbSite is the internal GORM model. TableName ensures GORM uses "sites".
type dbSite struct {
	ID       uint   `gorm:"primarykey;autoIncrement"`
	Key      string `gorm:"uniqueIndex;not null"`
	Hostname string `gorm:"uniqueIndex;not null"`
}

func (dbSite) TableName() string { return "sites" }

// EnsureSites upserts each configured site into the sites table by key,
// keeping hostname in sync with config, and writes the DB-assigned ID
// back onto each config.Site so the rest of the app can use it as the
// scoping key for posts/tags/sessions.
func EnsureSites(db *gorm.DB, sites []config.Site) error {
	for i := range sites {
		var r dbSite
		err := db.Where(dbSite{Key: sites[i].Key}).
			Assign(dbSite{Hostname: sites[i].SiteURL}).
			FirstOrCreate(&r).Error
		if err != nil {
			return err
		}
		sites[i].ID = int64(r.ID)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/models/... -run TestEnsureSites -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/models/site.go internal/models/site_test.go
git commit -m "Add site model and boot-time EnsureSites upsert"
```

---

### Task 3: Multi-site config schema

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` (full rewrite)

**Interfaces:**
- Produces: `config.Site{ID int64; Key, SiteURL, SiteName, SiteDescription, AboutSlug, AdminPasswordHash, TemplatePath, StaticPath string; Sections []Section}`; `config.Config{BindAddr string; Port int; DBDSN string; SessionSecret string; Dev bool; TemplatePath, StaticPath string; GlobalAdminPasswordHash string; Sites []Site}`. `config.Load(cfgFile string) (*Config, error)` — same signature as today.

- [ ] **Step 1: Rewrite the test file (failing against current config.go)**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTOML(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ntx.toml")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

const oneSite = `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$placeholder"
`

func TestEmptySessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$placeholder"
session_secret      = ""
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for empty session_secret")
	}
}

func TestShortSessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "tooshort"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$placeholder"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for session_secret shorter than 32 chars")
	}
}

func TestNoSitesErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error when no [[site]] blocks are configured")
	}
}

func TestSiteMissingKeyErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$placeholder"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for site missing key")
	}
}

func TestSiteMissingURLErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "mysite"
admin_password_hash = "$2a$10$placeholder"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for site missing site_url")
	}
}

func TestSiteMissingPasswordHashErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key      = "mysite"
site_url = "mysite.example.com"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for site missing admin_password_hash")
	}
}

func TestDuplicateSiteKeyErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "dup"
site_url            = "a.example.com"
admin_password_hash = "$2a$10$placeholder"

[[site]]
key                 = "dup"
site_url            = "b.example.com"
admin_password_hash = "$2a$10$placeholder"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for duplicate site key")
	}
}

func TestDuplicateSiteURLErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "a"
site_url            = "dup.example.com"
admin_password_hash = "$2a$10$placeholder"

[[site]]
key                 = "b"
site_url            = "dup.example.com"
admin_password_hash = "$2a$10$placeholder"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for duplicate site_url")
	}
}

func TestDefaults(t *testing.T) {
	f := writeTOML(t, oneSite)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 {
		t.Errorf("default port: want 8080, got %d", cfg.Port)
	}
	if cfg.DBDSN != "sqlite://./ntx.db" {
		t.Errorf("default db_dsn: want sqlite://./ntx.db, got %q", cfg.DBDSN)
	}
	if cfg.TemplatePath != "./templates" {
		t.Errorf("default template_path: want ./templates, got %q", cfg.TemplatePath)
	}
	if cfg.Dev {
		t.Error("dev should default to false")
	}
	if len(cfg.Sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(cfg.Sites))
	}
	site := cfg.Sites[0]
	if site.SiteName != "ntx" {
		t.Errorf("default site_name: want ntx, got %q", site.SiteName)
	}
	if site.AboutSlug != "about" {
		t.Errorf("default about_slug: want about, got %q", site.AboutSlug)
	}
	if site.TemplatePath != "./templates" {
		t.Errorf("site should inherit global template_path, got %q", site.TemplatePath)
	}
	if site.StaticPath != "./static" {
		t.Errorf("site should inherit global static_path, got %q", site.StaticPath)
	}
}

func TestSiteTemplatePathOverride(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"
template_path  = "./shared-templates"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$placeholder"
template_path       = "./mysite-templates"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].TemplatePath != "./mysite-templates" {
		t.Errorf("site override ignored: got %q", cfg.Sites[0].TemplatePath)
	}
}

func TestMultipleSites(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "a"
site_url            = "a.example.com"
admin_password_hash = "$2a$10$placeholder"

  [[site.section]]
  slug  = "projects"
  title = "Projects"

    [[site.section.pages]]
    slug  = "one"
    title = "One"

[[site]]
key                 = "b"
site_url            = "b.example.com"
admin_password_hash = "$2a$10$placeholder"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(cfg.Sites))
	}
	if len(cfg.Sites[0].Sections) != 1 || cfg.Sites[0].Sections[0].Slug != "projects" {
		t.Errorf("expected site a to have a projects section, got %+v", cfg.Sites[0].Sections)
	}
	if len(cfg.Sites[1].Sections) != 0 {
		t.Errorf("expected site b to have no sections, got %+v", cfg.Sites[1].Sections)
	}
}

func TestEnvVarOverride(t *testing.T) {
	t.Setenv("NTX_PORT", "9090")

	f := writeTOML(t, oneSite)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9090 {
		t.Errorf("env override: want port 9090, got %d", cfg.Port)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -v`
Expected: FAIL — compile errors (`cfg.Sites` undefined) and/or assertion failures against the old single-site schema.

- [ ] **Step 3: Rewrite config.go**

```go
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type SectionPage struct {
	Slug  string                 `mapstructure:"slug"`
	Title string                 `mapstructure:"title"`
	Vars  map[string]interface{} `mapstructure:",remain"`
}

type Section struct {
	Slug  string                 `mapstructure:"slug"`
	Title string                 `mapstructure:"title"`
	Pages []SectionPage          `mapstructure:"pages"`
	Vars  map[string]interface{} `mapstructure:",remain"`
}

// Site is one tenant's configuration. ID is populated by
// models.EnsureSites at boot, not read from TOML — it's the stable FK
// used to scope posts/tags/sessions.
type Site struct {
	ID                int64     `mapstructure:"-"`
	Key               string    `mapstructure:"key"`
	SiteURL           string    `mapstructure:"site_url"`
	SiteName          string    `mapstructure:"site_name"`
	SiteDescription   string    `mapstructure:"site_description"`
	AboutSlug         string    `mapstructure:"about_slug"`
	AdminPasswordHash string    `mapstructure:"admin_password_hash"`
	TemplatePath      string    `mapstructure:"template_path"`
	StaticPath        string    `mapstructure:"static_path"`
	Sections          []Section `mapstructure:"section"`
}

type Config struct {
	BindAddr                string `mapstructure:"bind_addr"`
	Port                    int    `mapstructure:"port"`
	DBDSN                   string `mapstructure:"db_dsn"`
	SessionSecret           string `mapstructure:"session_secret"`
	Dev                     bool   `mapstructure:"dev"`
	TemplatePath            string `mapstructure:"template_path"`
	StaticPath              string `mapstructure:"static_path"`
	GlobalAdminPasswordHash string `mapstructure:"global_admin_password_hash"`
	Sites                   []Site `mapstructure:"site"`
}

func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	v.SetDefault("static_path", "./static")
	v.SetDefault("db_dsn", "sqlite://./ntx.db")
	v.SetDefault("port", 8080)
	v.SetDefault("template_path", "./templates")
	v.SetDefault("dev", false)

	v.SetEnvPrefix("NTX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("ntx")
		v.SetConfigType("toml")
		v.AddConfigPath(".")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("session_secret must be set (min 32 chars)")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("session_secret must be at least 32 characters")
	}

	if len(cfg.Sites) == 0 {
		return nil, fmt.Errorf("at least one [[site]] block is required")
	}

	keys := make(map[string]bool, len(cfg.Sites))
	urls := make(map[string]bool, len(cfg.Sites))
	for i := range cfg.Sites {
		s := &cfg.Sites[i]
		if s.Key == "" {
			return nil, fmt.Errorf("site %d: key must be set", i)
		}
		if s.SiteURL == "" {
			return nil, fmt.Errorf("site %q: site_url must be set", s.Key)
		}
		if s.AdminPasswordHash == "" {
			return nil, fmt.Errorf("site %q: admin_password_hash must be set (run: ntx hash-password)", s.Key)
		}
		if keys[s.Key] {
			return nil, fmt.Errorf("duplicate site key %q", s.Key)
		}
		keys[s.Key] = true
		if urls[s.SiteURL] {
			return nil, fmt.Errorf("duplicate site_url %q", s.SiteURL)
		}
		urls[s.SiteURL] = true

		if s.SiteName == "" {
			s.SiteName = "ntx"
		}
		if s.AboutSlug == "" {
			s.AboutSlug = "about"
		}
		if s.TemplatePath == "" {
			s.TemplatePath = cfg.TemplatePath
		}
		if s.StaticPath == "" {
			s.StaticPath = cfg.StaticPath
		}
	}

	return &cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "Support multi-site config with per-site parameters"
```

---

### Task 4: Site-scoped PostStore

**Files:**
- Modify: `internal/models/post.go`
- Modify: `internal/models/post_test.go` (full rewrite)

**Interfaces:**
- Consumes: `config.Site.ID int64` (Task 3) — not directly, but `ForSite` takes a bare `int64` so no import needed.
- Produces: `PostStore.ForSite(siteID int64) *PostStore` — all existing methods (`List`, `ListByTag`, `GetBySlug`, `GetByID`, `Create`, `Update`, `Delete`, `AllTags`, `Stats`) keep their current signatures but only operate correctly on a store returned by `ForSite` (a bare `NewPostStore(db)` store has `siteID == 0`, which matches no real site and is used only by `EnsureSites`-adjacent tooling, never by handlers).

- [ ] **Step 1: Rewrite the test file (failing against current post.go)**

```go
package models

import (
	"testing"
)

func newTestStores(t *testing.T) (siteA, siteB *PostStore) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})
	raw := NewPostStore(database)
	return raw.ForSite(1), raw.ForSite(2)
}

func newTestDB(t *testing.T) *PostStore {
	t.Helper()
	s, _ := newTestStores(t)
	return s
}

func TestCreateAndGetBySlug(t *testing.T) {
	s := newTestDB(t)

	p := &Post{
		Slug:      "hello-world",
		Title:     "Hello World",
		Body:      "# Hello\n\ncontent here",
		Published: true,
		Tags:      []string{"go", "testing"},
	}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Error("expected ID to be set")
	}

	got, err := s.GetBySlug("hello-world")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected post, got nil")
	}
	if got.Title != "Hello World" {
		t.Errorf("title: got %q", got.Title)
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(got.Tags), got.Tags)
	}
}

func TestGetBySlugMissing(t *testing.T) {
	s := newTestDB(t)
	got, err := s.GetBySlug("nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for missing post")
	}
}

func TestList(t *testing.T) {
	s := newTestDB(t)

	posts := []Post{
		{Slug: "draft-one", Title: "Draft One", Body: "...", Published: false},
		{Slug: "pub-one", Title: "Pub One", Body: "...", Published: true},
		{Slug: "pub-two", Title: "Pub Two", Body: "...", Published: true},
	}
	for i := range posts {
		if err := s.Create(&posts[i]); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	published, err := s.List(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 {
		t.Errorf("expected 2 published, got %d", len(published))
	}
}

func TestListByTag(t *testing.T) {
	s := newTestDB(t)

	p1 := &Post{Slug: "a", Title: "A", Body: ".", Published: true, Tags: []string{"go"}}
	p2 := &Post{Slug: "b", Title: "B", Body: ".", Published: true, Tags: []string{"rust"}}
	p3 := &Post{Slug: "c", Title: "C", Body: ".", Published: true, Tags: []string{"go", "rust"}}
	for _, p := range []*Post{p1, p2, p3} {
		if err := s.Create(p); err != nil {
			t.Fatal(err)
		}
	}

	goPosts, err := s.ListByTag("go")
	if err != nil {
		t.Fatal(err)
	}
	if len(goPosts) != 2 {
		t.Errorf("expected 2 go posts, got %d", len(goPosts))
	}
}

func TestUpdate(t *testing.T) {
	s := newTestDB(t)

	p := &Post{Slug: "update-me", Title: "Original", Body: ".", Published: false, Tags: []string{"a"}}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}

	p.Title = "Updated"
	p.Published = true
	p.Tags = []string{"b", "c"}
	if err := s.Update(p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetBySlug("update-me")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Updated" {
		t.Errorf("title not updated: %q", got.Title)
	}
	if !got.Published {
		t.Error("should be published")
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", got.Tags)
	}
}

func TestDelete(t *testing.T) {
	s := newTestDB(t)

	p := &Post{Slug: "delete-me", Title: "Delete Me", Body: ".", Published: true}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBySlug("delete-me")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected deleted post to be nil")
	}
}

func TestStats(t *testing.T) {
	s := newTestDB(t)

	posts := []Post{
		{Slug: "p1", Title: "P1", Body: ".", Published: true, Tags: []string{"go"}},
		{Slug: "p2", Title: "P2", Body: ".", Published: true, Tags: []string{"rust"}},
		{Slug: "p3", Title: "P3", Body: ".", Published: false},
	}
	for i := range posts {
		if err := s.Create(&posts[i]); err != nil {
			t.Fatal(err)
		}
	}

	total, published, drafts, tagCount, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total: expected 3, got %d", total)
	}
	if published != 2 {
		t.Errorf("published: expected 2, got %d", published)
	}
	if drafts != 1 {
		t.Errorf("drafts: expected 1, got %d", drafts)
	}
	if tagCount != 2 {
		t.Errorf("tagCount: expected 2, got %d", tagCount)
	}
}

func TestTagDeduplication(t *testing.T) {
	s := newTestDB(t)

	p1 := &Post{Slug: "t1", Title: "T1", Body: ".", Published: true, Tags: []string{"go"}}
	p2 := &Post{Slug: "t2", Title: "T2", Body: ".", Published: true, Tags: []string{"go"}}
	if err := s.Create(p1); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(p2); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 unique tag, got %d: %v", len(all), all)
	}
}

// Multi-tenant isolation tests

func TestSlugUniquePerSiteNotGlobally(t *testing.T) {
	siteA, siteB := newTestStores(t)

	if err := siteA.Create(&Post{Slug: "same-slug", Title: "A's post", Body: "."}); err != nil {
		t.Fatal(err)
	}
	if err := siteB.Create(&Post{Slug: "same-slug", Title: "B's post", Body: "."}); err != nil {
		t.Fatalf("expected same slug to be valid in a different site, got: %v", err)
	}

	if err := siteA.Create(&Post{Slug: "same-slug", Title: "A's second post", Body: "."}); err == nil {
		t.Error("expected duplicate slug within the same site to fail")
	}
}

func TestForSiteIsolatesReads(t *testing.T) {
	siteA, siteB := newTestStores(t)

	if err := siteA.Create(&Post{Slug: "a-only", Title: "A Only", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}

	got, err := siteB.GetBySlug("a-only")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("site B should not see site A's post")
	}

	all, err := siteB.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("expected site B to have 0 posts, got %d", len(all))
	}
}

func TestTagNameUniquePerSiteNotGlobally(t *testing.T) {
	siteA, siteB := newTestStores(t)

	if err := siteA.Create(&Post{Slug: "a1", Title: "A1", Body: ".", Tags: []string{"shared"}}); err != nil {
		t.Fatal(err)
	}
	if err := siteB.Create(&Post{Slug: "b1", Title: "B1", Body: ".", Tags: []string{"shared"}}); err != nil {
		t.Fatal(err)
	}

	aTags, err := siteA.AllTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(aTags) != 1 {
		t.Errorf("expected site A to see 1 tag, got %v", aTags)
	}
}
```

Note: this file needs `import "github.com/nod/ntx/internal/db"` for `db.Open` (the original file didn't import `db` directly because it lived in a different test file previously combined via package-level helper — confirm the import is present).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/models/... -v`
Expected: FAIL — `ForSite` undefined.

- [ ] **Step 3: Rewrite post.go**

```go
package models

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Post is the public type. Tags are []string for template/handler compatibility.
type Post struct {
	ID        int64
	Slug      string
	Title     string
	Body      string
	Published bool
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// dbPost is the internal GORM model. TableName ensures GORM uses "posts".
type dbPost struct {
	ID        uint    `gorm:"primarykey;autoIncrement"`
	SiteID    int64   `gorm:"not null;uniqueIndex:idx_posts_site_slug"`
	Slug      string  `gorm:"not null;uniqueIndex:idx_posts_site_slug"`
	Title     string  `gorm:"not null"`
	Body      string  `gorm:"not null"`
	Published bool    `gorm:"default:false"`
	Tags      []dbTag `gorm:"many2many:post_tags;joinForeignKey:post_id;joinReferences:tag_id"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (dbPost) TableName() string { return "posts" }

// dbTag is the internal GORM model for tags. TableName ensures GORM uses "tags".
type dbTag struct {
	ID     uint   `gorm:"primarykey"`
	SiteID int64  `gorm:"not null;uniqueIndex:idx_tags_site_name"`
	Name   string `gorm:"not null;uniqueIndex:idx_tags_site_name"`
}

func (dbTag) TableName() string { return "tags" }

// PostStore is scoped to one site once ForSite is called; a store with
// siteID 0 (the zero value) matches no real site's rows.
type PostStore struct {
	db     *gorm.DB
	siteID int64
}

func NewPostStore(db *gorm.DB) *PostStore {
	return &PostStore{db: db}
}

// ForSite returns a copy of the store scoped to siteID. Every query and
// write made through the returned store is confined to that site's rows.
func (s *PostStore) ForSite(siteID int64) *PostStore {
	return &PostStore{db: s.db, siteID: siteID}
}

func (s *PostStore) List(publishedOnly bool) ([]Post, error) {
	var records []dbPost
	q := s.db.Preload("Tags").Where("site_id = ?", s.siteID).Order("created_at desc")
	if publishedOnly {
		q = q.Where("published = ?", true)
	}
	if err := q.Find(&records).Error; err != nil {
		return nil, err
	}
	posts := make([]Post, len(records))
	for i, r := range records {
		posts[i] = toPost(r)
	}
	return posts, nil
}

func (s *PostStore) ListByTag(tag string) ([]Post, error) {
	var records []dbPost
	err := s.db.Preload("Tags").
		Where("posts.site_id = ?", s.siteID).
		Joins("JOIN post_tags ON post_tags.post_id = posts.id").
		Joins("JOIN tags ON tags.id = post_tags.tag_id AND tags.name = ? AND tags.site_id = ?", tag, s.siteID).
		Where("posts.published = ?", true).
		Order("posts.created_at desc").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	posts := make([]Post, len(records))
	for i, r := range records {
		posts[i] = toPost(r)
	}
	return posts, nil
}

func (s *PostStore) GetBySlug(slug string) (*Post, error) {
	var r dbPost
	err := s.db.Preload("Tags").Where("site_id = ? AND slug = ?", s.siteID, slug).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := toPost(r)
	return &p, nil
}

func (s *PostStore) GetByID(id int64) (*Post, error) {
	var r dbPost
	err := s.db.Preload("Tags").Where("site_id = ?", s.siteID).First(&r, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := toPost(r)
	return &p, nil
}

func (s *PostStore) Create(p *Post) error {
	if !slugRe.MatchString(p.Slug) {
		return fmt.Errorf("invalid slug %q: must match %s", p.Slug, slugRe)
	}
	tags, err := s.resolveTags(p.Tags)
	if err != nil {
		return err
	}
	r := dbPost{
		SiteID:    s.siteID,
		Slug:      p.Slug,
		Title:     p.Title,
		Body:      p.Body,
		Published: p.Published,
		Tags:      tags,
	}
	if err := s.db.Create(&r).Error; err != nil {
		return err
	}
	p.ID = int64(r.ID)
	p.CreatedAt = r.CreatedAt
	p.UpdatedAt = r.UpdatedAt
	return nil
}

func (s *PostStore) Update(p *Post) error {
	if !slugRe.MatchString(p.Slug) {
		return fmt.Errorf("invalid slug %q: must match %s", p.Slug, slugRe)
	}
	tags, err := s.resolveTags(p.Tags)
	if err != nil {
		return err
	}
	r := dbPost{ID: uint(p.ID)}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&r).Where("site_id = ?", s.siteID).Updates(map[string]any{
			"slug":      p.Slug,
			"title":     p.Title,
			"body":      p.Body,
			"published": p.Published,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&r).Association("Tags").Replace(tags)
	})
}

func (s *PostStore) Delete(id int64) error {
	return s.db.Where("site_id = ?", s.siteID).Delete(&dbPost{}, id).Error
}

func (s *PostStore) AllTags() ([]string, error) {
	var tags []dbTag
	if err := s.db.Where("site_id = ?", s.siteID).Order("name").Find(&tags).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return names, nil
}

func (s *PostStore) Stats() (total, published, drafts, tagCount int, err error) {
	var t, p, tc int64
	if err = s.db.Model(&dbPost{}).Where("site_id = ?", s.siteID).Count(&t).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbPost{}).Where("site_id = ? AND published = ?", s.siteID, true).Count(&p).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbTag{}).Where("site_id = ?", s.siteID).Count(&tc).Error; err != nil {
		return
	}
	total = int(t)
	published = int(p)
	drafts = total - published
	tagCount = int(tc)
	return
}

// resolveTags finds or creates each tag by name (scoped to this site) and
// returns the dbTag slice.
func (s *PostStore) resolveTags(names []string) ([]dbTag, error) {
	var tags []dbTag
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var t dbTag
		if err := s.db.Where(dbTag{SiteID: s.siteID, Name: name}).FirstOrCreate(&t).Error; err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func toPost(r dbPost) Post {
	p := Post{
		ID:        int64(r.ID),
		Slug:      r.Slug,
		Title:     r.Title,
		Body:      r.Body,
		Published: r.Published,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
	for _, t := range r.Tags {
		p.Tags = append(p.Tags, t.Name)
	}
	return p
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/models/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/models/post.go internal/models/post_test.go
git commit -m "Scope PostStore reads and writes to a site via ForSite"
```

---

### Task 5: Request-scoped site context

**Files:**
- Create: `internal/sitectx/sitectx.go`
- Test: `internal/sitectx/sitectx_test.go`

**Interfaces:**
- Consumes: `config.Site` (Task 3).
- Produces: `sitectx.With(r *http.Request, site *config.Site) *http.Request`, `sitectx.From(r *http.Request) *config.Site` (nil if unset). Used by Task 8 (server routing, sets it) and Task 6/7 (handlers, auth, read it).

- [ ] **Step 1: Write the failing test**

```go
package sitectx

import (
	"net/http/httptest"
	"testing"

	"github.com/nod/ntx/internal/config"
)

func TestWithAndFrom(t *testing.T) {
	site := &config.Site{Key: "mysite"}
	r := httptest.NewRequest("GET", "/", nil)

	if got := From(r); got != nil {
		t.Errorf("expected nil before With, got %+v", got)
	}

	r = With(r, site)
	got := From(r)
	if got != site {
		t.Errorf("expected %+v, got %+v", site, got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sitectx/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

```go
// Package sitectx carries the request's resolved config.Site through
// context, so handlers, session auth, and routing agree on "which site
// is this?" without handlers importing server (which would be a cycle).
package sitectx

import (
	"context"
	"net/http"

	"github.com/nod/ntx/internal/config"
)

type ctxKey struct{}

// With returns a copy of r with site attached to its context.
func With(r *http.Request, site *config.Site) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, site))
}

// From returns the site attached to r's context, or nil if none was set.
func From(r *http.Request) *config.Site {
	site, _ := r.Context().Value(ctxKey{}).(*config.Site)
	return site
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sitectx/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sitectx/sitectx.go internal/sitectx/sitectx_test.go
git commit -m "Add sitectx package for request-scoped site resolution"
```

---

### Task 6: Site-scoped sessions

**Files:**
- Modify: `internal/auth/auth.go`
- Modify: `internal/auth/auth_test.go` (full rewrite)

**Interfaces:**
- Produces: `auth.GlobalSiteID int64 = 0`; `SessionManager.SetSession(w http.ResponseWriter, siteID int64)`; `SessionManager.SessionSiteID(r *http.Request) (int64, bool)`; `SessionManager.IsAuthenticated(r *http.Request) bool` (unchanged signature, now site-agnostic — true for any valid session); `SessionManager.Middleware(currentSite func(*http.Request) int64) func(http.Handler) http.Handler` (signature changed — now takes a resolver instead of wrapping a handler directly).

- [ ] **Step 1: Rewrite the test file (failing against current auth.go)**

```go
package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHashAndCheck(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "hunter2"); err != nil {
		t.Error("correct password rejected")
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Error("wrong password accepted")
	}
}

func TestSessionRoundtrip(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w, 42)

	resp := w.Result()
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])

	if !sm.IsAuthenticated(req) {
		t.Error("valid session not authenticated")
	}
	siteID, ok := sm.SessionSiteID(req)
	if !ok {
		t.Fatal("expected a valid session")
	}
	if siteID != 42 {
		t.Errorf("expected siteID 42, got %d", siteID)
	}
}

func TestSessionTampering(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w, 1)
	resp := w.Result()
	cookie := resp.Cookies()[0]

	cookie.Value = cookie.Value + "x"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if sm.IsAuthenticated(req) {
		t.Error("tampered session accepted")
	}
}

func TestSessionMissing(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if sm.IsAuthenticated(req) {
		t.Error("unauthenticated request passed")
	}
}

func TestDifferentSecrets(t *testing.T) {
	sm1 := NewSessionManager("secret-one-1234567890abcdef1234", false)
	sm2 := NewSessionManager("secret-two-1234567890abcdef1234", false)

	w := httptest.NewRecorder()
	sm1.SetSession(w, 1)
	cookie := w.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if sm2.IsAuthenticated(req) {
		t.Error("session from different secret accepted")
	}
}

func TestMiddlewareRedirectsWhenUnauthenticated(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	currentSite := func(*http.Request) int64 { return 1 }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", w.Code)
	}
}

func TestMiddlewareAllowsMatchingSite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w, 1)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) int64 { return 1 }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareRejectsMismatchedSite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w, 1)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) int64 { return 2 }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected redirect for mismatched site, got %d", rec.Code)
	}
}

func TestMiddlewareAllowsGlobalSessionOnAnySite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w, GlobalSiteID)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) int64 { return 99 }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected global session to pass on any site, got %d", rec.Code)
	}
}

func TestClearSession(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w, 1)
	cookie := w.Result().Cookies()[0]

	w2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(cookie)
	sm.ClearSession(w2)

	cleared := w2.Result().Cookies()
	for _, c := range cleared {
		if c.Name == cookieName && c.MaxAge < 0 {
			return
		}
	}
	t.Error("session cookie not cleared")
}

func TestSessionExpiry(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	expired := time.Now().Add(-25 * time.Hour).Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", expired, 1)))
	sig := sm.sign(payload)
	value := payload + "." + sig

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})

	if sm.IsAuthenticated(req) {
		t.Error("expired session accepted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -v`
Expected: FAIL — compile errors (`SetSession` wrong arg count, `Middleware` wrong signature, `GlobalSiteID`/`SessionSiteID` undefined).

- [ ] **Step 3: Rewrite auth.go**

```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName    = "ntx_session"
	sessionMaxAge = 24 * time.Hour

	// GlobalSiteID marks a session issued against the global admin
	// password: it authenticates against every site, not just one.
	GlobalSiteID int64 = 0
)

var ErrInvalidSession = errors.New("invalid session")

type SessionManager struct {
	secret []byte
	secure bool
}

func NewSessionManager(secret string, secure bool) *SessionManager {
	return &SessionManager{secret: []byte(secret), secure: secure}
}

func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// SetSession issues a session scoped to siteID (use GlobalSiteID for a
// cross-site admin session).
func (sm *SessionManager) SetSession(w http.ResponseWriter, siteID int64) {
	issuedAt := time.Now().Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", issuedAt, siteID)))
	sig := sm.sign(payload)
	value := payload + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionMaxAge.Seconds()),
		SameSite: http.SameSiteStrictMode,
		Secure:   sm.secure,
	})
}

func (sm *SessionManager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		SameSite: http.SameSiteStrictMode,
		Secure:   sm.secure,
	})
}

// SessionSiteID returns the siteID carried by r's session cookie, and
// whether a valid, unexpired session was present at all.
func (sm *SessionManager) SessionSiteID(r *http.Request) (int64, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return 0, false
	}
	siteID, err := sm.verify(cookie.Value)
	if err != nil {
		return 0, false
	}
	return siteID, true
}

// IsAuthenticated reports whether r carries any valid session, regardless
// of which site it's scoped to.
func (sm *SessionManager) IsAuthenticated(r *http.Request) bool {
	_, ok := sm.SessionSiteID(r)
	return ok
}

// Middleware redirects to /admin/login unless r carries a valid session
// that is either global or scoped to currentSite(r).
func (sm *SessionManager) Middleware(currentSite func(*http.Request) int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			siteID, ok := sm.SessionSiteID(r)
			if !ok || (siteID != GlobalSiteID && siteID != currentSite(r)) {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (sm *SessionManager) sign(payload string) string {
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (sm *SessionManager) verify(value string) (int64, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return 0, ErrInvalidSession
	}
	expected := sm.sign(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return 0, ErrInvalidSession
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrInvalidSession
	}
	var issuedAt, siteID int64
	if _, err := fmt.Sscanf(string(decoded), "%d:%d", &issuedAt, &siteID); err != nil {
		return 0, ErrInvalidSession
	}
	if time.Since(time.Unix(issuedAt, 0)) > sessionMaxAge {
		return 0, ErrInvalidSession
	}

	return siteID, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/auth.go internal/auth/auth_test.go
git commit -m "Scope sessions to a site, with a global-admin sentinel"
```

---

### Task 7: Site-aware Handler core

**Files:**
- Modify: `internal/handlers/handler.go`

**Interfaces:**
- Consumes: `config.Site` (Task 3), `models.PostStore.ForSite` (Task 4), `auth.SessionManager` (Task 6), `sitectx.From` (Task 5).
- Produces: `handlers.New(sites []config.Site, posts *models.PostStore, session *auth.SessionManager, dev bool, globalAdminHash string) (*Handler, error)` — signature changed from `New(cfg *config.Config, posts *models.PostStore, session *auth.SessionManager)`. Internal `siteHandler` type (unexported) bundling one site's `config.Site`, scoped `*models.PostStore`, and `*TemplateCache`; `Handler.current(r *http.Request) *siteHandler` (unexported, used by every other handler file); `Handler.render` keeps its existing signature `(w, r, name, data)` but now resolves the site internally. This task alone will not compile the package — admin.go/blog.go/home.go/projects.go/generate.go still reference the old `h.cfg`/`h.posts`/`h.templates` fields until Task 8 updates them. Do this task and Task 8 back-to-back before running `go build`.

- [ ] **Step 1: Rewrite handler.go**

```go
package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitectx"
)

type TemplateCache struct {
	templates map[string]*template.Template
	path      string
	dev       bool
}

func NewTemplateCache(path string, dev bool) (*TemplateCache, error) {
	tc := &TemplateCache{path: path, dev: dev}
	if !dev {
		if err := tc.load(); err != nil {
			return nil, err
		}
	}
	return tc, nil
}

func (tc *TemplateCache) Has(name string) bool {
	if tc.dev {
		_, err := os.Stat(filepath.Join(tc.path, name))
		return err == nil
	}
	_, ok := tc.templates[name]
	return ok
}

func (tc *TemplateCache) Get(name string) (*template.Template, error) {
	if tc.dev {
		return tc.parse(name)
	}
	t, ok := tc.templates[name]
	if !ok {
		return nil, fmt.Errorf("template %q not found", name)
	}
	return t, nil
}

func (tc *TemplateCache) load() error {
	tc.templates = make(map[string]*template.Template)
	base := filepath.Join(tc.path, "base.html")

	err := filepath.Walk(tc.path, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(tc.path, path)
		if err != nil {
			return err
		}
		if rel == "base.html" {
			return nil
		}
		if filepath.Ext(path) != ".html" {
			return nil
		}
		t, err := template.New(filepath.Base(base)).Funcs(templateFuncs()).ParseFiles(base, path)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", rel, err)
		}
		tc.templates[rel] = t
		return nil
	})
	return err
}

func (tc *TemplateCache) parse(name string) (*template.Template, error) {
	base := filepath.Join(tc.path, "base.html")
	page := filepath.Join(tc.path, name)
	return template.New(filepath.Base(base)).Funcs(templateFuncs()).ParseFiles(base, page)
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"join": strings.Join,
	}
}

// siteHandler bundles the per-site resources a request needs once its
// site has been resolved: this site's config, its scoped post store, and
// its own template cache (which points at the shared global
// template/static paths if the site didn't override them — see Task 3's
// config defaulting).
type siteHandler struct {
	site      config.Site
	posts     *models.PostStore
	templates *TemplateCache
}

func (sh *siteHandler) siteData() map[string]interface{} {
	return map[string]interface{}{
		"SiteName":        sh.site.SiteName,
		"SiteURL":         sh.site.SiteURL,
		"SiteDescription": sh.site.SiteDescription,
	}
}

type Handler struct {
	session         *auth.SessionManager
	limiter         *loginLimiter
	sites           map[int64]*siteHandler
	globalAdminHash string
}

// New builds a Handler serving every site in sites, backed by posts
// (an unscoped store — New calls posts.ForSite per site internally).
func New(sites []config.Site, posts *models.PostStore, session *auth.SessionManager, dev bool, globalAdminHash string) (*Handler, error) {
	h := &Handler{
		session:         session,
		limiter:         newLoginLimiter(),
		sites:           make(map[int64]*siteHandler, len(sites)),
		globalAdminHash: globalAdminHash,
	}
	for _, site := range sites {
		tc, err := NewTemplateCache(site.TemplatePath, dev)
		if err != nil {
			return nil, fmt.Errorf("loading templates for site %q: %w", site.Key, err)
		}
		h.sites[site.ID] = &siteHandler{
			site:      site,
			posts:     posts.ForSite(site.ID),
			templates: tc,
		}
	}
	return h, nil
}

// current resolves the siteHandler for the request's already-resolved
// site (set by server's Host-routing middleware via sitectx). Returns
// nil if no site was resolved, which callers treat as a 404.
func (h *Handler) current(r *http.Request) *siteHandler {
	site := sitectx.From(r)
	if site == nil {
		return nil
	}
	return h.sites[site.ID]
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	t, err := sh.templates.Get(name)
	if err != nil {
		h.serverError(w, err)
		return
	}
	merged := sh.siteData()
	for k, v := range data {
		merged[k] = v
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, merged); err != nil {
		log.Printf("render %s: %v", name, err)
		h.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	log.Printf("server error: %v", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func (h *Handler) notFound(w http.ResponseWriter) {
	http.Error(w, "Not Found", http.StatusNotFound)
}
```

- [ ] **Step 2: Confirm expected compile errors in sibling files**

Run: `go build ./internal/handlers/...`
Expected: FAIL — `admin.go`, `blog.go`, `generate.go`, `projects.go` reference `h.cfg`, `h.posts`, `h.templates`, which no longer exist. This is expected; Task 8 fixes it. Do not attempt to fix those files here.

- [ ] **Step 3: Commit (package intentionally not building yet)**

```bash
git add internal/handlers/handler.go
git commit -m "WIP: rework Handler around per-site resources (package does not build yet)"
```

---

### Task 8: Site-aware handler call sites

**Files:**
- Modify: `internal/handlers/admin.go`
- Modify: `internal/handlers/blog.go`
- Modify: `internal/handlers/projects.go`
- Modify: `internal/handlers/generate.go`
- Modify: `internal/handlers/home.go` (no change needed — confirm it still compiles; `Home` only calls `h.render`, which is now site-aware internally)

**Interfaces:**
- Consumes: `Handler.current(r) *siteHandler` (Task 7), `auth.GlobalSiteID` (Task 6).
- Produces: `Handler.SectionPage(w http.ResponseWriter, r *http.Request, sectionSlug string)` — signature changed from `(w, r, sec config.Section)`; the section itself is now looked up from `h.current(r).site.Sections` by slug, since which site's section applies depends on the resolved request, not a route-registration-time closure. Task 9 (server routing) depends on this new signature.

- [ ] **Step 1: Rewrite admin.go**

```go
package handlers

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/models"
)

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string][]time.Time)}
}

// allow returns true if key is under the threshold (5 attempts per 15
// minutes). key is "<siteID>:<ip>" so one site's brute-force lockout
// doesn't collaterally lock out login attempts on another site.
func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	var recent []time.Time
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.attempts, key)
	} else {
		l.attempts[key] = recent
	}
	if len(recent) >= 5 {
		return false
	}
	l.attempts[key] = append(l.attempts[key], time.Now())
	return true
}

func (h *Handler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin/login.html", nil)
}

func (h *Handler) AdminLoginPost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	limitKey := fmt.Sprintf("%d:%s", sh.site.ID, ip)
	if !h.limiter.allow(limitKey) {
		h.render(w, r, "admin/login.html", map[string]interface{}{
			"Error": "Too many attempts. Try again later.",
		})
		return
	}

	password := r.FormValue("password")
	switch {
	case auth.CheckPassword(sh.site.AdminPasswordHash, password) == nil:
		h.session.SetSession(w, sh.site.ID)
	case h.globalAdminHash != "" && auth.CheckPassword(h.globalAdminHash, password) == nil:
		h.session.SetSession(w, auth.GlobalSiteID)
	default:
		h.render(w, r, "admin/login.html", map[string]interface{}{
			"Error": "Invalid password.",
		})
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (h *Handler) AdminLogout(w http.ResponseWriter, r *http.Request) {
	h.session.ClearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (h *Handler) AdminPosts(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	posts, err := sh.posts.List(false)
	if err != nil {
		h.serverError(w, err)
		return
	}
	total, published, drafts, tagCount, err := sh.posts.Stats()
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, r, "admin/posts.html", map[string]interface{}{
		"Posts":     posts,
		"Total":     total,
		"Published": published,
		"Drafts":    drafts,
		"TagCount":  tagCount,
	})
}

func (h *Handler) AdminNewPost(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin/editor.html", map[string]interface{}{
		"Post": nil,
	})
}

func (h *Handler) AdminCreatePost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	post := postFromForm(r)
	post.Published = r.FormValue("action") == "publish"

	if err := sh.posts.Create(post); err != nil {
		h.render(w, r, "admin/editor.html", map[string]interface{}{
			"Post":  post,
			"Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (h *Handler) AdminEditPost(w http.ResponseWriter, r *http.Request) {
	post, err := h.getPostFromParam(w, r)
	if err != nil || post == nil {
		return
	}
	h.render(w, r, "admin/editor.html", map[string]interface{}{
		"Post": post,
	})
}

func (h *Handler) AdminUpdatePost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	existing, err := h.getPostFromParam(w, r)
	if err != nil || existing == nil {
		return
	}

	action := r.FormValue("action")

	if action == "delete" {
		if err := sh.posts.Delete(existing.ID); err != nil {
			h.serverError(w, err)
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
		return
	}

	updated := postFromForm(r)
	updated.ID = existing.ID
	switch action {
	case "publish":
		updated.Published = true
	case "unpublish":
		updated.Published = false
	default: // "save" preserves current state
		updated.Published = existing.Published
	}

	if err := sh.posts.Update(updated); err != nil {
		h.render(w, r, "admin/editor.html", map[string]interface{}{
			"Post":  updated,
			"Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (h *Handler) getPostFromParam(w http.ResponseWriter, r *http.Request) (*models.Post, error) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return nil, fmt.Errorf("no site resolved")
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.notFound(w)
		return nil, err
	}
	post, err := sh.posts.GetByID(id)
	if err != nil {
		h.serverError(w, err)
		return nil, err
	}
	if post == nil {
		h.notFound(w)
	}
	return post, nil
}

func postFromForm(r *http.Request) *models.Post {
	tagsRaw := r.FormValue("tags")
	var tags []string
	for _, t := range strings.Split(tagsRaw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return &models.Post{
		Slug:  r.FormValue("slug"),
		Title: r.FormValue("title"),
		Body:  r.FormValue("body"),
		Tags:  tags,
	}
}
```

- [ ] **Step 2: Rewrite blog.go**

```go
package handlers

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

func renderMarkdown(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func (h *Handler) BlogIndex(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	tag := r.URL.Query().Get("tag")

	var posts interface{}
	var err error

	if tag != "" {
		posts, err = sh.posts.ListByTag(tag)
	} else {
		posts, err = sh.posts.List(true)
	}
	if err != nil {
		h.serverError(w, err)
		return
	}

	allTags, err := sh.posts.AllTags()
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, "blog/index.html", map[string]interface{}{
		"Posts":     posts,
		"AllTags":   allTags,
		"ActiveTag": tag,
	})
}

func (h *Handler) BlogPost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	slug := chi.URLParam(r, "slug")
	post, err := sh.posts.GetBySlug(slug)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if post == nil || !post.Published {
		h.notFound(w)
		return
	}

	rendered, err := renderMarkdown(post.Body)
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, "blog/post.html", map[string]interface{}{
		"Post":     post,
		"Rendered": rendered,
	})
}
```

- [ ] **Step 3: Rewrite projects.go**

```go
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nod/ntx/internal/config"
)

// SectionPage serves a page within sectionSlug, resolved against the
// request's already-resolved site (server registers one route per unique
// section slug across all sites — which site's Section content applies
// is decided here, per request, not at route-registration time).
func (h *Handler) SectionPage(w http.ResponseWriter, r *http.Request, sectionSlug string) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	var sec *config.Section
	for i := range sh.site.Sections {
		if sh.site.Sections[i].Slug == sectionSlug {
			sec = &sh.site.Sections[i]
			break
		}
	}
	if sec == nil {
		h.notFound(w)
		return
	}

	pageSlug := chi.URLParam(r, "page")
	var page *config.SectionPage
	for i := range sec.Pages {
		if sec.Pages[i].Slug == pageSlug {
			page = &sec.Pages[i]
			break
		}
	}
	if page == nil {
		h.notFound(w)
		return
	}

	tmplName := "sections/" + sec.Slug + "/" + pageSlug + "/index.html"
	if !sh.templates.Has(tmplName) {
		h.notFound(w)
		return
	}

	data := make(map[string]interface{})
	for k, v := range sec.Vars {
		data[k] = v
	}
	if sec.Title != "" {
		data["SectionTitle"] = sec.Title
	}
	for k, v := range page.Vars {
		data[k] = v
	}
	if page.Title != "" {
		data["Title"] = page.Title
	}

	h.render(w, r, tmplName, data)
}
```

- [ ] **Step 4: Rewrite generate.go**

```go
package handlers

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// Generate renders every configured site's static output into its own
// <outDir>/<site.key>/ subdirectory.
func (h *Handler) Generate(outDir string) error {
	for _, sh := range h.sites {
		siteOut := filepath.Join(outDir, sh.site.Key)
		if err := h.generateSite(sh, siteOut); err != nil {
			return fmt.Errorf("generating site %q: %w", sh.site.Key, err)
		}
	}
	return nil
}

func (h *Handler) generateSite(sh *siteHandler, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if err := h.genPage(sh, outDir, "/", "home.html", nil); err != nil {
		return err
	}

	posts, err := sh.posts.List(true)
	if err != nil {
		return fmt.Errorf("listing posts: %w", err)
	}
	allTags, err := sh.posts.AllTags()
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}
	if err := h.genPage(sh, outDir, "/blog", "blog/index.html", map[string]interface{}{
		"Posts":     posts,
		"AllTags":   allTags,
		"ActiveTag": "",
	}); err != nil {
		return err
	}

	for _, p := range posts {
		post := p
		rendered, err := renderMarkdown(post.Body)
		if err != nil {
			return fmt.Errorf("rendering markdown for %s: %w", post.Slug, err)
		}
		if err := h.genPage(sh, outDir, "/blog/"+post.Slug, "blog/post.html", map[string]interface{}{
			"Post":     &post,
			"Rendered": rendered,
		}); err != nil {
			return err
		}
	}

	for _, sec := range sh.site.Sections {
		for _, pg := range sec.Pages {
			tmplName := "sections/" + sec.Slug + "/" + pg.Slug + "/index.html"
			if !sh.templates.Has(tmplName) {
				log.Printf("warning: template %s not found, skipping /%s/%s", tmplName, sec.Slug, pg.Slug)
				continue
			}
			data := make(map[string]interface{})
			for k, v := range sec.Vars {
				data[k] = v
			}
			if sec.Title != "" {
				data["SectionTitle"] = sec.Title
			}
			for k, v := range pg.Vars {
				data[k] = v
			}
			if pg.Title != "" {
				data["Title"] = pg.Title
			}
			if err := h.genPage(sh, outDir, "/"+sec.Slug+"/"+pg.Slug, tmplName, data); err != nil {
				return err
			}
		}
	}

	if err := copyDir(sh.site.StaticPath, filepath.Join(outDir, "static")); err != nil {
		return fmt.Errorf("copying static files: %w", err)
	}
	log.Printf("copied static/ → %s/static/", outDir)

	return nil
}

func (h *Handler) genPage(sh *siteHandler, outDir, urlPath, tmplName string, data map[string]interface{}) error {
	content, err := h.renderToBytes(sh, tmplName, data)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", urlPath, err)
	}

	var filePath string
	if urlPath == "/" {
		filePath = filepath.Join(outDir, "index.html")
	} else {
		filePath = filepath.Join(outDir, urlPath, "index.html")
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", filePath, err)
	}
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filePath, err)
	}
	log.Printf("generated %s%s", sh.site.Key, urlPath)
	return nil
}

func (h *Handler) renderToBytes(sh *siteHandler, name string, data map[string]interface{}) ([]byte, error) {
	t, err := sh.templates.Get(name)
	if err != nil {
		return nil, err
	}
	merged := sh.siteData()
	for k, v := range data {
		merged[k] = v
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, merged); err != nil {
		return nil, fmt.Errorf("executing %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
```

- [ ] **Step 5: Run the package build**

Run: `go build ./internal/handlers/...`
Expected: PASS (Task 9 will add the multi-site tests that actually exercise these handlers through HTTP; this task only needs to compile since there's no `server` wiring yet to construct a `Handler` end-to-end without a resolved site in context).

- [ ] **Step 6: Commit**

```bash
git add internal/handlers/admin.go internal/handlers/blog.go internal/handlers/projects.go internal/handlers/generate.go
git commit -m "Make handler call sites resolve their site from context"
```

---

### Task 9: Host-based routing in server.go

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go` (full rewrite)

**Interfaces:**
- Consumes: `handlers.New(sites, posts, session, dev, globalAdminHash)` (Task 7/8), `sitectx.With`/`From` (Task 5), `config.Config`/`config.Site` (Task 3).
- Produces: `server.New(cfg *config.Config, posts *models.PostStore, session *auth.SessionManager) (*http.Server, error)` — same signature as before. Assumes `cfg.Sites[i].ID` has already been populated by `models.EnsureSites` before this is called (Task 10 wires that in `main.go`).

- [ ] **Step 1: Rewrite the test file (failing against current server.go)**

```go
package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/models"
)

// setupServer builds a single-site server whose site_url is "example.com"
// and returns a client that always sends that Host header, since
// httptest.Server otherwise sends "127.0.0.1:PORT" (which won't match any
// configured site_url and would 404 at the resolveSite middleware).
func setupServer(t *testing.T) (*httptest.Server, *models.PostStore, *http.Client) {
	t.Helper()
	return setupServerWithConfig(t, nil)
}

func setupServerWithConfig(t *testing.T, extra []config.Section) (*httptest.Server, *models.PostStore, *http.Client) {
	t.Helper()

	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)
	if extra != nil {
		sectionTmpl := filepath.Join(tmpDir, "sections/projects/one/index.html")
		os.MkdirAll(filepath.Dir(sectionTmpl), 0755)
		os.WriteFile(sectionTmpl, []byte(`{{template "base.html" .}}{{define "content"}}project-one{{end}}`), 0644)
	}

	hash, _ := auth.HashPassword("testpass")
	sites := []config.Site{
		{
			Key:               "mysite",
			SiteURL:           "example.com",
			TemplatePath:      tmpDir,
			StaticPath:        tmpDir,
			AboutSlug:         "about-page",
			AdminPasswordHash: hash,
			Sections:          extra,
		},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SessionSecret: "testsecretkey12345678901234567890",
		Dev:           true,
		Sites:         sites,
	}

	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, rawPosts, session)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	client := &http.Client{
		Transport: hostRewriteTransport{host: "example.com", inner: http.DefaultTransport, base: ts.URL},
	}
	return ts, rawPosts.ForSite(sites[0].ID), client
}

// hostRewriteTransport sends every request to ts's real address but with
// a Host header the resolveSite middleware will recognize.
type hostRewriteTransport struct {
	host  string
	inner http.RoundTripper
	base  string
}

func (t hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	baseURL, _ := url.Parse(t.base)
	req = req.Clone(req.Context())
	req.URL.Scheme = baseURL.Scheme
	req.URL.Host = baseURL.Host
	req.Host = t.host
	return t.inner.RoundTrip(req)
}

func setupTemplates(t *testing.T, dir string) {
	t.Helper()

	base := `<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>`
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(full), 0755)
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("base.html", base)
	write("home.html", `{{template "base.html" .}}{{define "content"}}home{{end}}`)
	write("blog/index.html", `{{template "base.html" .}}{{define "content"}}blog-index{{end}}`)
	write("blog/post.html", `{{template "base.html" .}}{{define "content"}}{{.Post.Title}}{{end}}`)
	write("admin/login.html", `{{template "base.html" .}}{{define "content"}}login{{if .Error}} error:{{.Error}}{{end}}{{end}}`)
	write("admin/posts.html", `{{template "base.html" .}}{{define "content"}}posts-admin{{end}}`)
	write("admin/editor.html", `{{template "base.html" .}}{{define "content"}}editor{{end}}`)
}

func setupServerWithSections(t *testing.T) (*httptest.Server, *models.PostStore, *http.Client) {
	t.Helper()
	return setupServerWithConfig(t, []config.Section{
		{
			Slug:  "projects",
			Title: "Projects",
			Pages: []config.SectionPage{
				{Slug: "one", Title: "Project One"},
			},
		},
	})
}

func TestHomeRoute(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUnmatchedHostReturns404(t *testing.T) {
	_, _, client := setupServer(t)
	req, _ := http.NewRequest(http.MethodGet, "http://not-configured.example.com/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unmatched host, got %d", resp.StatusCode)
	}
}

func TestAboutRedirect(t *testing.T) {
	_, _, baseClient := setupServer(t)
	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://example.com/about")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "about-page") {
		t.Errorf("redirect to wrong location: %s", loc)
	}
}

func TestBlogIndex(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/blog")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBlogPost404(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/blog/no-such-post")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestBlogPostDraftHidden(t *testing.T) {
	_, posts, client := setupServer(t)

	p := &models.Post{Slug: "secret-draft", Title: "Secret", Body: ".", Published: false}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.com/blog/secret-draft")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("draft should return 404, got %d", resp.StatusCode)
	}
}

func TestBlogPostPublished(t *testing.T) {
	_, posts, client := setupServer(t)

	p := &models.Post{Slug: "my-post", Title: "My Post", Body: "# Hello", Published: true}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.com/blog/my-post")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSectionPage(t *testing.T) {
	_, _, client := setupServerWithSections(t)
	resp, err := client.Get("http://example.com/projects/one")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSectionPageNotInConfig(t *testing.T) {
	_, _, client := setupServerWithSections(t)
	resp, err := client.Get("http://example.com/projects/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSectionUnknownNamespace(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/projects/one")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unconfigured section, got %d", resp.StatusCode)
	}
}

func TestAdminLoginRequired(t *testing.T) {
	_, _, baseClient := setupServer(t)
	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://example.com/admin/posts")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", resp.StatusCode)
	}
}

func TestAdminLoginFlow(t *testing.T) {
	_, _, baseClient := setupServer(t)
	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.PostForm("http://example.com/admin/login", url.Values{
		"password": {"testpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after login, got %d", resp.StatusCode)
	}

	cookie := resp.Cookies()
	if len(cookie) == 0 {
		t.Fatal("no session cookie set")
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/admin/posts", nil)
	req.AddCookie(cookie[0])
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for authenticated request, got %d", resp2.StatusCode)
	}
}

func TestAdminLoginBadPassword(t *testing.T) {
	_, _, client := setupServer(t)

	resp, err := client.PostForm("http://example.com/admin/login", url.Values{
		"password": {"wrongpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with error form, got %d", resp.StatusCode)
	}
}

func loginCookie(t *testing.T, client *http.Client) *http.Cookie {
	t.Helper()
	c := &http.Client{
		Transport: client.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := c.PostForm("http://example.com/admin/login", url.Values{"password": {"testpass"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "ntx_session" {
			return c
		}
	}
	t.Fatal("no session cookie after login")
	return nil
}

func TestBlogIndexTagFilter(t *testing.T) {
	_, posts, client := setupServer(t)

	_ = createPost(t, posts, "go-post", "Go Post", true, "go")
	_ = createPost(t, posts, "rust-post", "Rust Post", true, "rust")

	resp, err := client.Get("http://example.com/blog?tag=go")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "blog-index") {
		t.Error("expected blog index content")
	}
}

func TestAdminLogout(t *testing.T) {
	_, _, client := setupServer(t)
	cookie := loginCookie(t, client)

	c := &http.Client{
		Transport: client.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/admin/logout", nil)
	req.AddCookie(cookie)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after logout, got %d", resp.StatusCode)
	}

	for _, c := range resp.Cookies() {
		if c.Name == "ntx_session" && c.MaxAge < 0 {
			return
		}
	}
	t.Error("session cookie not cleared after logout")
}

func TestAllAdminRoutesRequireAuth(t *testing.T) {
	_, posts, baseClient := setupServer(t)
	p := createPost(t, posts, "test-auth", "Auth Test", false)

	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/posts"},
		{http.MethodGet, "/admin/posts/new"},
		{http.MethodPost, "/admin/posts"},
		{http.MethodGet, "/admin/posts/" + itoa(p.ID) + "/edit"},
		{http.MethodPost, "/admin/posts/" + itoa(p.ID)},
		{http.MethodPost, "/admin/logout"},
	}

	for _, rt := range routes {
		req, _ := http.NewRequest(rt.method, "http://example.com"+rt.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", rt.method, rt.path, err)
		}
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s %s: expected 303 redirect, got %d", rt.method, rt.path, resp.StatusCode)
		}
	}
}

func TestAdminCreatePost(t *testing.T) {
	_, posts, baseClient := setupServer(t)
	cookie := loginCookie(t, baseClient)

	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/admin/posts", strings.NewReader(url.Values{
		"title":  {"New Post"},
		"slug":   {"new-post"},
		"body":   {"# Hello"},
		"tags":   {"go, testing"},
		"action": {"publish"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after create, got %d", resp.StatusCode)
	}

	p, err := posts.GetBySlug("new-post")
	if err != nil || p == nil {
		t.Fatal("post not found after create")
	}
	if !p.Published {
		t.Error("post should be published")
	}
	if len(p.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", p.Tags)
	}
}

func TestBlogPostMarkdownXSS(t *testing.T) {
	_, posts, client := setupServer(t)

	p := &models.Post{
		Slug:      "xss-test",
		Title:     "XSS Test",
		Body:      `safe content\n\n<script>alert('xss')</script>`,
		Published: true,
	}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.com/blog/xss-test")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<script>") {
		t.Error("raw <script> tag present in rendered output — XSS not stripped")
	}
}

// Multi-site: two sites in one server, isolated by Host

func TestTwoSitesIsolatedByHost(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)

	hashA, _ := auth.HashPassword("passA")
	hashB, _ := auth.HashPassword("passB")
	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashA},
		{Key: "b", SiteURL: "b.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashB},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{SessionSecret: "testsecretkey12345678901234567890", Dev: true, Sites: sites}
	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, rawPosts, session)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	postsA := rawPosts.ForSite(sites[0].ID)
	postsB := rawPosts.ForSite(sites[1].ID)
	if err := postsA.Create(&models.Post{Slug: "hello", Title: "A's Hello", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}
	if err := postsB.Create(&models.Post{Slug: "hello", Title: "B's Hello", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}

	get := func(host, path string) string {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	bodyA := get("a.example.com", "/blog/hello")
	if !strings.Contains(bodyA, "A's Hello") {
		t.Errorf("expected site A's post title, got: %s", bodyA)
	}
	bodyB := get("b.example.com", "/blog/hello")
	if !strings.Contains(bodyB, "B's Hello") {
		t.Errorf("expected site B's post title, got: %s", bodyB)
	}
}

func TestSessionScopedToOneSiteRejectedOnAnother(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)

	hashA, _ := auth.HashPassword("passA")
	hashB, _ := auth.HashPassword("passB")
	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashA},
		{Key: "b", SiteURL: "b.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashB},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{SessionSecret: "testsecretkey12345678901234567890", Dev: true, Sites: sites}
	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, rawPosts, session)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/login", strings.NewReader(url.Values{"password": {"passA"}}.Encode()))
	loginReq.Host = "a.example.com"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginResp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set")
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/posts", nil)
	req.Host = "b.example.com"
	req.AddCookie(cookies[0])
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected site A's session to be rejected on site B, got %d", resp.StatusCode)
	}
}

// helpers

func createPost(t *testing.T, s *models.PostStore, slug, title string, published bool, tags ...string) *models.Post {
	t.Helper()
	p := &models.Post{Slug: slug, Title: title, Body: ".", Published: published, Tags: tags}
	if err := s.Create(p); err != nil {
		t.Fatalf("createPost %q: %v", slug, err)
	}
	return p
}

func itoa(n int64) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(
		fmt.Sprintf("%d", n), " ", ""), "\n", ""))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/... -v`
Expected: FAIL — `New` still takes the old 3-arg-no-multi-site signature and there's no Host-routing; every request 404s or otherwise mismatches.

- [ ] **Step 3: Rewrite server.go**

```go
package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/handlers"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitectx"
)

func New(cfg *config.Config, posts *models.PostStore, session *auth.SessionManager) (*http.Server, error) {
	h, err := handlers.New(cfg.Sites, posts, session, cfg.Dev, cfg.GlobalAdminPasswordHash)
	if err != nil {
		return nil, err
	}

	sites := make(map[string]*config.Site, len(cfg.Sites))
	for i := range cfg.Sites {
		sites[strings.ToLower(cfg.Sites[i].SiteURL)] = &cfg.Sites[i]
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(resolveSite(sites))

	r.Get("/", h.Home)
	r.Get("/about", func(w http.ResponseWriter, r *http.Request) {
		site := sitectx.From(r)
		http.Redirect(w, r, "/blog/"+site.AboutSlug, http.StatusFound)
	})
	r.Get("/blog", h.BlogIndex)
	r.Get("/blog/{slug}", h.BlogPost)

	sectionSlugs := map[string]bool{}
	for _, site := range cfg.Sites {
		for _, sec := range site.Sections {
			sectionSlugs[sec.Slug] = true
		}
	}
	for slug := range sectionSlugs {
		slug := slug
		r.Get("/"+slug+"/{page}", func(w http.ResponseWriter, r *http.Request) {
			h.SectionPage(w, r, slug)
		})
	}

	r.Handle("/static/*", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site := sitectx.From(r)
		http.FileServer(http.Dir(site.StaticPath)).ServeHTTP(w, r)
	})))

	r.Get("/admin/login", h.AdminLogin)
	r.Post("/admin/login", h.AdminLoginPost)

	r.Group(func(r chi.Router) {
		r.Use(session.Middleware(func(r *http.Request) int64 {
			site := sitectx.From(r)
			if site == nil {
				return -1 // never matches a real site or auth.GlobalSiteID
			}
			return site.ID
		}))
		r.Post("/admin/logout", h.AdminLogout)
		r.Get("/admin/posts", h.AdminPosts)
		r.Get("/admin/posts/new", h.AdminNewPost)
		r.Post("/admin/posts", h.AdminCreatePost)
		r.Get("/admin/posts/{id}/edit", h.AdminEditPost)
		r.Post("/admin/posts/{id}", h.AdminUpdatePost)
	})

	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "localhost"
	}

	return &http.Server{
		Addr:         fmt.Sprintf("%s:%d", bindAddr, cfg.Port),
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

// resolveSite matches the request's Host header (port stripped) against
// configured site_urls and attaches the match via sitectx. Unmatched
// hosts get a bare 404 — no detail on which hostnames are configured.
func resolveSite(sites map[string]*config.Site) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if hostOnly, _, err := net.SplitHostPort(host); err == nil {
				host = hostOnly
			}
			site, ok := sites[strings.ToLower(host)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, sitectx.With(r, site))
		})
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/server/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "Route requests to a site by Host header"
```

---

### Task 10: Wire multi-site boot into main.go

**Files:**
- Modify: `main.go`

**Interfaces:**
- Consumes: `config.Load` (Task 3), `models.EnsureSites` (Task 2), `handlers.New` (Task 7), `server.New` (Task 9).
- Produces: no new exported interface — this is the top-level wiring. `ntx serve`, `ntx generate`, and `ntx gen-config` all reflect the new multi-site config shape.

- [ ] **Step 1: Update the `serve` command**

In `rootCmd()`, replace the `serve` command's `RunE` body:

```go
RunE: func(cmd *cobra.Command, args []string) error {
	cfgFile, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	database, err := db.Open(cfg.DBDSN)
	if err != nil {
		return err
	}
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if err := models.EnsureSites(database, cfg.Sites); err != nil {
		return fmt.Errorf("ensuring sites: %w", err)
	}

	posts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, !cfg.Dev)

	srv, err := server.New(cfg, posts, session)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
},
```

- [ ] **Step 2: Update the `generate` command**

Replace the `generate` command's `RunE` body:

```go
RunE: func(cmd *cobra.Command, args []string) error {
	cfgFile, _ := cmd.Flags().GetString("config")
	outDir, _ := cmd.Flags().GetString("out")

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}
	database, err := db.Open(cfg.DBDSN)
	if err != nil {
		return err
	}
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if err := models.EnsureSites(database, cfg.Sites); err != nil {
		return fmt.Errorf("ensuring sites: %w", err)
	}

	posts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, !cfg.Dev)
	h, err := handlers.New(cfg.Sites, posts, session, cfg.Dev, cfg.GlobalAdminPasswordHash)
	if err != nil {
		return err
	}
	return h.Generate(outDir)
},
```

- [ ] **Step 3: Rewrite the sample config and its templating**

Replace the `genConfig` command's `RunE` body and the `sampleConfig` const:

```go
genConfig := &cobra.Command{
	Use:   "gen-config",
	Short: "Print a sample ntx.toml to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		promptPw, _ := cmd.Flags().GetBool("password")

		secret, err := randomSecret()
		if err != nil {
			return err
		}

		hash := ""
		if promptPw {
			fmt.Fprint(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			hash, err = auth.HashPassword(string(pw))
			if err != nil {
				return err
			}
		}

		out := strings.ReplaceAll(sampleConfig, `session_secret = ""`, fmt.Sprintf(`session_secret = "%s"`, secret))
		if hash != "" {
			out = strings.ReplaceAll(out, `admin_password_hash = ""`, fmt.Sprintf(`admin_password_hash = "%s"`, hash))
		}
		fmt.Print(out)
		return nil
	},
}
genConfig.Flags().BoolP("password", "p", false, "prompt for the first site's admin password and embed bcrypt hash")
```

```go
const sampleConfig = `# ntx.toml — ntx server configuration
# Run 'ntx gen-config -p' to set the first site's admin_password_hash interactively.
# session_secret is auto-generated by gen-config.

bind_addr      = ""   # empty = localhost only; use "0.0.0.0" to listen on all interfaces
port           = 8080
db_dsn         = "sqlite://./ntx.db"
template_path  = "./templates"   # shared default; a [[site]] can override with its own template_path
static_path    = "./static"      # shared default; a [[site]] can override with its own static_path
session_secret = ""   # required: random string, min 32 chars
dev            = false
# global_admin_password_hash = ""  # optional: bcrypt hash that can log into any site's admin

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
site_name           = "mysite"
site_description    = ""
about_slug          = "about"
admin_password_hash = ""   # required: bcrypt hash
`
```

- [ ] **Step 4: Build and run the full test suite**

Run: `go build ./... && go test ./...`
Expected: all packages build; all tests PASS.

- [ ] **Step 5: Smoke-test the CLI**

```bash
go run . gen-config -p <<< "testpass" > /tmp/ntx-smoke.toml
go run . serve --config /tmp/ntx-smoke.toml &
SERVER_PID=$!
sleep 1
curl -s -o /dev/null -w "%{http_code}\n" -H "Host: mysite.example.com" http://localhost:8080/
kill $SERVER_PID
rm /tmp/ntx-smoke.toml
```

Expected: prints `200`.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "Wire multi-site boot: EnsureSites, per-site generate, sample config"
```

---

## Post-plan note

After Task 10, `ntx` serves multiple sites from one process via Host-based routing, per the approved spec. Anything not covered here (e.g. an admin UI for editing `site_name`/`site_description` in the DB copy seeded by `EnsureSites`, or dynamic site add/remove without a restart) was explicitly out of scope in the spec's Non-goals.
