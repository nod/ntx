# GORM Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `database/sql` + custom migration runner with GORM + goose, supporting both SQLite and PostgreSQL via a single `db_dsn` config field.

**Architecture:** `db.Open(dsn)` infers driver from URL scheme (`sqlite://` or `postgres://`), opens the correct GORM dialector, runs goose migrations from a per-dialect embedded FS, and returns `*gorm.DB`. `PostStore` wraps `*gorm.DB` with unexported GORM model types; the public `Post` type keeps `Tags []string` unchanged.

**Tech Stack:** `gorm.io/gorm`, `github.com/glebarez/sqlite` (pure Go SQLite, no CGO), `gorm.io/driver/postgres`, `github.com/pressly/goose/v3`

## Global Constraints

- No CGO — use `github.com/glebarez/sqlite` not `gorm.io/driver/sqlite`
- Public `Post.Tags` remains `[]string` — conversion is internal to `PostStore`
- goose migrations use the `NewProvider` API (not package-level globals) to avoid shared state in tests
- SQLite connections limited to `MaxOpenConns(1)`
- All tests must pass: `go test ./...`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `go.mod` / `go.sum` | Modify | Add GORM + goose + glebarez; remove modernc |
| `internal/config/config.go` | Modify | Rename `DBPath` → `DBDSN`, update default |
| `internal/config/config_test.go` | Modify | Update field name + expected default value |
| `docs/ntx.sample.toml` | Modify | `db_path` → `db_dsn` with `sqlite://` prefix |
| `internal/db/migrations/001_initial.sql` | Delete | Replaced by per-dialect dirs |
| `internal/db/migrations/sqlite/001_initial.sql` | Create | SQLite schema with goose annotations |
| `internal/db/migrations/postgres/001_initial.sql` | Create | PostgreSQL schema with goose annotations |
| `internal/db/db.go` | Rewrite | GORM open + goose migration runner |
| `internal/models/post.go` | Rewrite | GORM-native internal types + PostStore |
| `internal/models/post_test.go` | Modify | Update `newTestDB` for GORM |
| `main.go` | Modify | `cfg.DBPath` → `cfg.DBDSN`, close via sqlDB, update sample config string |
| `internal/server/server_test.go` | Modify | Update two `setupServer` helpers for GORM |

---

### Task 1: Add dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces: `gorm.io/gorm`, `github.com/glebarez/sqlite`, `gorm.io/driver/postgres`, `github.com/pressly/goose/v3` available for import

- [ ] **Step 1: Add GORM, goose, and pure-Go SQLite driver**

```bash
cd /path/to/ntx
go get gorm.io/gorm
go get github.com/glebarez/sqlite
go get gorm.io/driver/postgres
go get github.com/pressly/goose/v3
```

Expected: each command prints `go: added ...` lines. No errors.

- [ ] **Step 2: Verify go.mod has the new entries**

```bash
grep -E "gorm|goose|glebarez" go.mod
```

Expected output (versions may vary):
```
github.com/glebarez/sqlite v1.x.x
github.com/pressly/goose/v3 v3.x.x
gorm.io/driver/postgres v1.x.x
gorm.io/gorm v1.x.x
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "feat: add GORM, goose, and glebarez/sqlite dependencies"
```

---

### Task 2: Create goose migration files

**Files:**
- Delete: `internal/db/migrations/001_initial.sql`
- Create: `internal/db/migrations/sqlite/001_initial.sql`
- Create: `internal/db/migrations/postgres/001_initial.sql`

**Interfaces:**
- Produces: two embedded migration dirs consumed by `db.Open` in Task 4

- [ ] **Step 1: Delete the old migration file**

```bash
rm internal/db/migrations/001_initial.sql
```

- [ ] **Step 2: Create the SQLite migration**

Create `internal/db/migrations/sqlite/001_initial.sql`:

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS posts (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    slug        TEXT     NOT NULL UNIQUE,
    title       TEXT     NOT NULL,
    body        TEXT     NOT NULL,
    published   INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS tags (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT    NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS post_tags (
    post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);

-- +goose Down
DROP TABLE IF EXISTS post_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS posts;
```

- [ ] **Step 3: Create the PostgreSQL migration**

Create `internal/db/migrations/postgres/001_initial.sql`:

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS posts (
    id          BIGSERIAL    PRIMARY KEY,
    slug        TEXT         NOT NULL UNIQUE,
    title       TEXT         NOT NULL,
    body        TEXT         NOT NULL,
    published   BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL
);

CREATE TABLE IF NOT EXISTS tags (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT      NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS post_tags (
    post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id  BIGINT NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);

-- +goose Down
DROP TABLE IF EXISTS post_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS posts;
```

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/
git commit -m "feat: replace single migration with per-dialect goose migrations"
```

---

### Task 3: Update config (DBPath → DBDSN)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `docs/ntx.sample.toml`

**Interfaces:**
- Produces: `Config.DBDSN string` (mapstructure `"db_dsn"`), default `"sqlite://./ntx.db"`
- Consumed by: `main.go` (Task 6), `db.Open` (Task 4)

- [ ] **Step 1: Update config.go**

In `internal/config/config.go`, change the struct field and default:

```go
// Change this field:
DBPath string `mapstructure:"db_path"`
// To:
DBDSN string `mapstructure:"db_dsn"`
```

Change the viper default:
```go
// Change:
v.SetDefault("db_path", "./ntx.db")
// To:
v.SetDefault("db_dsn", "sqlite://./ntx.db")
```

- [ ] **Step 2: Update config_test.go**

In `internal/config/config_test.go`, find the `TestDefaults` function and update the `DBPath` check:

```go
// Change:
if cfg.DBPath != "./ntx.db" {
    t.Errorf("default db_path: want ./ntx.db, got %q", cfg.DBPath)
}
// To:
if cfg.DBDSN != "sqlite://./ntx.db" {
    t.Errorf("default db_dsn: want sqlite://./ntx.db, got %q", cfg.DBDSN)
}
```

- [ ] **Step 3: Update docs/ntx.sample.toml**

Change the `db_path` line:
```toml
# Change:
db_path       = "./ntx.db"
# To:
db_dsn        = "sqlite://./ntx.db"
```

- [ ] **Step 4: Run config tests**

```bash
go test ./internal/config/...
```

Expected: `PASS` — all config tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go docs/ntx.sample.toml
git commit -m "feat: rename db_path to db_dsn with sqlite:// scheme prefix"
```

---

### Task 4: Rewrite db.go

**Files:**
- Rewrite: `internal/db/db.go`

**Interfaces:**
- Consumes: `internal/db/migrations/sqlite/*.sql` and `internal/db/migrations/postgres/*.sql` (from Task 2)
- Produces: `func Open(dsn string) (*gorm.DB, error)` — replaces old `func Open(path string) (*sql.DB, error)`
- Note: after this task, `main.go` and `server_test.go` will fail to compile until Task 6. `post_test.go` fails until Task 5. That's expected.

- [ ] **Step 1: Rewrite internal/db/db.go**

Replace the entire file with:

```go
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	glebsqlite "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

// Open parses the scheme from dsn and returns a ready *gorm.DB with migrations applied.
// Supported schemes: sqlite:// and postgres:// (or postgresql://).
func Open(dsn string) (*gorm.DB, error) {
	switch {
	case strings.HasPrefix(dsn, "sqlite://"):
		return openSQLite(strings.TrimPrefix(dsn, "sqlite://"))
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return openPostgres(dsn)
	default:
		return nil, fmt.Errorf("unsupported DSN scheme (want sqlite:// or postgres://): %s", dsn)
	}
}

func openSQLite(path string) (*gorm.DB, error) {
	db, err := gorm.Open(glebsqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		sqlDB.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
			sqlDB.Close()
			return nil, err
		}
	}

	sub, err := fs.Sub(sqliteMigrations, "migrations/sqlite")
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := runMigrations(sqlDB, goose.DialectSQLite3, sub); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("sqlite migrations: %w", err)
	}
	return db, nil
}

func openPostgres(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("opening postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(postgresMigrations, "migrations/postgres")
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := runMigrations(sqlDB, goose.DialectPostgres, sub); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("postgres migrations: %w", err)
	}
	return db, nil
}

func runMigrations(sqlDB *sql.DB, dialect goose.Dialect, migrations fs.FS) error {
	provider, err := goose.NewProvider(dialect, sqlDB, migrations)
	if err != nil {
		return err
	}
	_, err = provider.Up(context.Background())
	return err
}
```

Hmm, the `runMigrations` signature is getting complicated because `goose.NewProvider` expects `*sql.DB`. Let me simplify:

The actual content of `internal/db/db.go` to write is:

```go
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	glebsqlite "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

func Open(dsn string) (*gorm.DB, error) {
	switch {
	case strings.HasPrefix(dsn, "sqlite://"):
		return openSQLite(strings.TrimPrefix(dsn, "sqlite://"))
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return openPostgres(dsn)
	default:
		return nil, fmt.Errorf("unsupported DSN scheme (want sqlite:// or postgres://): %s", dsn)
	}
}

func openSQLite(path string) (*gorm.DB, error) {
	db, err := gorm.Open(glebsqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		sqlDB.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
			sqlDB.Close()
			return nil, err
		}
	}
	sub, err := fs.Sub(sqliteMigrations, "migrations/sqlite")
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := runMigrations(sqlDB, goose.DialectSQLite3, sub); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("sqlite migrations: %w", err)
	}
	return db, nil
}

func openPostgres(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("opening postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(postgresMigrations, "migrations/postgres")
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := runMigrations(sqlDB, goose.DialectPostgres, sub); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("postgres migrations: %w", err)
	}
	return db, nil
}

func runMigrations(sqlDB *sql.DB, dialect goose.Dialect, migrations fs.FS) error {
	provider, err := goose.NewProvider(dialect, sqlDB, migrations)
	if err != nil {
		return err
	}
	_, err = provider.Up(context.Background())
	return err
}
```

- [ ] **Step 2: Remove modernc.org/sqlite from go.mod**

```bash
go mod tidy
```

Expected: `modernc.org/sqlite` and its deps (`modernc.org/libc`, `modernc.org/mathutil`, `modernc.org/memory`) are removed. No errors.

- [ ] **Step 3: Verify db package compiles**

```bash
go build ./internal/db/...
```

Expected: no errors. (Other packages will fail — that's expected.)

- [ ] **Step 4: Commit**

```bash
git add internal/db/db.go go.mod go.sum
git commit -m "feat: rewrite db.go to return *gorm.DB with goose migrations"
```

---

### Task 5: Rewrite PostStore with GORM models

**Files:**
- Rewrite: `internal/models/post.go`
- Modify: `internal/models/post_test.go`

**Interfaces:**
- Consumes: `func Open(dsn string) (*gorm.DB, error)` from `internal/db`
- Produces:
  - `type Post struct { ID int64; Slug, Title, Body string; Published bool; Tags []string; CreatedAt, UpdatedAt time.Time }`
  - `func NewPostStore(db *gorm.DB) *PostStore`
  - `func (s *PostStore) List(publishedOnly bool) ([]Post, error)`
  - `func (s *PostStore) ListByTag(tag string) ([]Post, error)`
  - `func (s *PostStore) GetBySlug(slug string) (*Post, error)`
  - `func (s *PostStore) GetByID(id int64) (*Post, error)`
  - `func (s *PostStore) Create(p *Post) error`
  - `func (s *PostStore) Update(p *Post) error`
  - `func (s *PostStore) Delete(id int64) error`
  - `func (s *PostStore) AllTags() ([]string, error)`
  - `func (s *PostStore) Stats() (total, published, drafts, tagCount int, err error)`

- [ ] **Step 1: Rewrite internal/models/post.go**

Replace the entire file with:

```go
package models

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

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
	ID        uint     `gorm:"primarykey;autoIncrement"`
	Slug      string   `gorm:"uniqueIndex;not null"`
	Title     string   `gorm:"not null"`
	Body      string   `gorm:"not null"`
	Published bool     `gorm:"default:false"`
	Tags      []dbTag  `gorm:"many2many:post_tags;joinForeignKey:post_id;joinReferences:tag_id"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (dbPost) TableName() string { return "posts" }

// dbTag is the internal GORM model for tags. TableName ensures GORM uses "tags".
type dbTag struct {
	ID   uint   `gorm:"primarykey"`
	Name string `gorm:"uniqueIndex;not null"`
}

func (dbTag) TableName() string { return "tags" }

type PostStore struct {
	db *gorm.DB
}

func NewPostStore(db *gorm.DB) *PostStore {
	return &PostStore{db: db}
}

func (s *PostStore) List(publishedOnly bool) ([]Post, error) {
	var records []dbPost
	q := s.db.Preload("Tags").Order("created_at desc")
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
		Joins("JOIN post_tags ON post_tags.post_id = posts.id").
		Joins("JOIN tags ON tags.id = post_tags.tag_id AND tags.name = ?", tag).
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
	err := s.db.Preload("Tags").Where("slug = ?", slug).First(&r).Error
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
	err := s.db.Preload("Tags").First(&r, id).Error
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
	tags, err := s.resolveTags(p.Tags)
	if err != nil {
		return err
	}
	r := dbPost{
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
	r := dbPost{ID: uint(p.ID)}
	if err := s.db.Model(&r).Updates(map[string]any{
		"slug":      p.Slug,
		"title":     p.Title,
		"body":      p.Body,
		"published": p.Published,
	}).Error; err != nil {
		return err
	}
	tags, err := s.resolveTags(p.Tags)
	if err != nil {
		return err
	}
	return s.db.Model(&r).Association("Tags").Replace(tags)
}

func (s *PostStore) Delete(id int64) error {
	return s.db.Delete(&dbPost{}, id).Error
}

func (s *PostStore) AllTags() ([]string, error) {
	var tags []dbTag
	if err := s.db.Order("name").Find(&tags).Error; err != nil {
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
	if err = s.db.Model(&dbPost{}).Count(&t).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbPost{}).Where("published = ?", true).Count(&p).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbTag{}).Count(&tc).Error; err != nil {
		return
	}
	total = int(t)
	published = int(p)
	drafts = total - published
	tagCount = int(tc)
	return
}

// resolveTags finds or creates each tag by name and returns the dbTag slice.
func (s *PostStore) resolveTags(names []string) ([]dbTag, error) {
	var tags []dbTag
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var t dbTag
		if err := s.db.Where(dbTag{Name: name}).FirstOrCreate(&t).Error; err != nil {
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

- [ ] **Step 2: Update newTestDB in post_test.go**

In `internal/models/post_test.go`, replace `newTestDB`:

```go
func newTestDB(t *testing.T) *PostStore {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})
	return NewPostStore(database)
}
```

Also update the import block — add `"github.com/nerdtx/ntx/internal/db"` if not already present (it already is).

- [ ] **Step 3: Run model tests**

```bash
go test ./internal/models/... -v
```

Expected: all tests pass — `TestCreateAndGetBySlug`, `TestGetBySlugMissing`, `TestList`, `TestListByTag`, `TestUpdate`, `TestDelete`, `TestStats`, `TestTagDeduplication`.

- [ ] **Step 4: Commit**

```bash
git add internal/models/post.go internal/models/post_test.go
git commit -m "feat: rewrite PostStore with GORM-native internal types"
```

---

### Task 6: Wire up main.go and server tests

**Files:**
- Modify: `main.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `Config.DBDSN string` (Task 3), `db.Open(dsn) (*gorm.DB, error)` (Task 4), `NewPostStore(db *gorm.DB)` (Task 5)
- Produces: fully compiling, all tests green

- [ ] **Step 1: Update main.go**

Three changes in `main.go`:

**1. In the `serve` command `RunE`**, replace the DB open/close block:

```go
// Remove:
database, err := db.Open(cfg.DBPath)
if err != nil {
    return err
}
defer database.Close()

// Replace with:
database, err := db.Open(cfg.DBDSN)
if err != nil {
    return err
}
sqlDB, err := database.DB()
if err != nil {
    return err
}
defer sqlDB.Close()
```

**2. In the `generate` command `RunE`**, same replacement:

```go
// Remove:
database, err := db.Open(cfg.DBPath)
if err != nil {
    return err
}
defer database.Close()

// Replace with:
database, err := db.Open(cfg.DBDSN)
if err != nil {
    return err
}
sqlDB, err := database.DB()
if err != nil {
    return err
}
defer sqlDB.Close()
```

**3. In the `sampleConfig` const string**, change the `db_path` line:

```go
// Change:
db_path             = "./ntx.db"
// To:
db_dsn              = "sqlite://./ntx.db"
```

- [ ] **Step 2: Update server_test.go — setupServer**

In `internal/server/server_test.go`, replace the `setupServer` function's DB block:

```go
// Remove:
database, err := db.Open(":memory:")
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() { database.Close() })

// Replace with:
database, err := db.Open("sqlite://:memory:")
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() {
    sqlDB, _ := database.DB()
    sqlDB.Close()
})
```

- [ ] **Step 3: Update server_test.go — setupServerWithSections**

Same replacement in `setupServerWithSections`:

```go
// Remove:
database, err := db.Open(":memory:")
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() { database.Close() })

// Replace with:
database, err := db.Open("sqlite://:memory:")
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() {
    sqlDB, _ := database.DB()
    sqlDB.Close()
})
```

- [ ] **Step 4: Verify full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Run all tests**

```bash
go test ./...
```

Expected: all packages pass. Output ends with something like:
```
ok  	github.com/nerdtx/ntx/internal/config
ok  	github.com/nerdtx/ntx/internal/models
ok  	github.com/nerdtx/ntx/internal/server
```

- [ ] **Step 6: Commit**

```bash
git add main.go internal/server/server_test.go
git commit -m "feat: wire up GORM db.Open and DBDSN across main and server tests"
```
