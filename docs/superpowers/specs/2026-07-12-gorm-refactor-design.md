# GORM Refactor Design

**Date:** 2026-07-12  
**Status:** Approved

## Goal

Replace the current `database/sql` + `modernc.org/sqlite` layer with GORM, supporting both SQLite and PostgreSQL via a single `db_dsn` config field. Migrations move from a custom runner to goose with per-dialect SQL files.

## Decisions

| Question | Decision |
|----------|----------|
| ORM | GORM (`gorm.io/gorm`) |
| Migrations | goose with separate `sqlite/` and `postgres/` dirs |
| DB config | Single `db_dsn` field, driver inferred from URL scheme |
| Model depth | GORM-native: struct tags, `many2many` for tags |
| Migration strategy | Full replace in one shot (B) |

## Config

`db_path` is removed. `db_dsn` replaces it:

- SQLite: `db_dsn = "sqlite://./ntx.db"`
- PostgreSQL: `db_dsn = "postgres://user:pass@host:5432/db"`

`Config.DBPath string` → `Config.DBDSN string` (`mapstructure:"db_dsn"`).

Default in viper: `sqlite://./ntx.db`.

The sample config in `main.go` and `docs/ntx.sample.toml` update accordingly. Existing deployments rename `db_path` → `db_dsn` and add the `sqlite://` prefix.

## DB Layer (`internal/db/db.go`)

`Open(dsn string) (*gorm.DB, error)`:

1. Parse scheme from DSN prefix.
2. `sqlite://` → strip prefix, open `gorm.io/driver/sqlite` with WAL and foreign keys enabled (`?_foreign_keys=on&_journal_mode=WAL` appended to path), set `MaxOpenConns(1)`.
3. `postgres://` → pass DSN as-is to `gorm.io/driver/postgres`.
4. Run goose migrations via `db.DB()` (the underlying `*sql.DB`), selecting the embedded FS for the detected dialect.
5. Return `*gorm.DB`.

Custom migration runner in the current `db.go` is deleted entirely.

## Migrations (`internal/db/migrations/`)

Old structure deleted. New structure:

```
internal/db/migrations/
  sqlite/
    001_initial.sql
  postgres/
    001_initial.sql
```

Both directories embedded via `//go:embed`. At startup, `Open` selects the correct FS based on detected driver.

Each file uses goose annotations:

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS posts ( ... );
...

-- +goose Down
DROP TABLE IF EXISTS post_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS posts;
```

SQLite file uses `INTEGER PRIMARY KEY AUTOINCREMENT`. PostgreSQL file uses `BIGSERIAL PRIMARY KEY`. Both use `TEXT` for string columns. Timestamps stored as `TIMESTAMPTZ` in PostgreSQL, `DATETIME` in SQLite — managed by GORM's native `time.Time` handling (not Unix integers).

`post_tags` join table is defined explicitly in migrations (goose owns the schema); GORM uses it via the `many2many` tag but does not auto-create it.

## Models (`internal/models/post.go`)

```go
type Tag struct {
    ID   uint   `gorm:"primarykey"`
    Name string `gorm:"uniqueIndex;not null"`
}

type Post struct {
    ID        uint      `gorm:"primarykey;autoIncrement"`
    Slug      string    `gorm:"uniqueIndex;not null"`
    Title     string    `gorm:"not null"`
    Body      string    `gorm:"not null"`
    Published bool      `gorm:"default:false"`
    Tags      []Tag     `gorm:"many2many:post_tags;"`
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

- `boolToInt` deleted.
- All manual `rows.Scan` deleted.
- Unix integer timestamp handling deleted.
- `syncTagsTx` and `dbExecer` interface deleted.
- Tag conversion helpers (`tagsToStrings`, `stringsToTags`) added internally.

`PostStore` holds `*gorm.DB` instead of `*sql.DB`. `NewPostStore(db *gorm.DB) *PostStore`.

## PostStore Methods

Public signatures unchanged. Internal rewrites:

| Method | Implementation |
|--------|---------------|
| `List(publishedOnly bool)` | `db.Preload("Tags").Where(...).Order("created_at desc").Find(&posts)` |
| `ListByTag(tag)` | `db.Preload("Tags").Joins(...).Where("tags.name = ?", tag).Find(&posts)` |
| `GetBySlug(slug)` | `db.Preload("Tags").Where("slug = ?", slug).First(&post)` — `ErrRecordNotFound` → `nil, nil` |
| `GetByID(id)` | Same pattern as `GetBySlug` |
| `Create(p)` | Convert `[]string` → `[]Tag`, `db.Create(&post)`, copy `ID` back |
| `Update(p)` | Convert `[]string` → `[]Tag`, `db.Session(&gorm.Session{FullSaveAssociations: true}).Save(&post)` + `db.Model(&post).Association("Tags").Replace(tags)` |
| `Delete(id)` | `db.Delete(&Post{}, id)` |
| `AllTags()` | `db.Find(&tags)`, map to `[]string` |
| `Stats()` | GORM count queries with scopes for total/published/draft/tagCount |

## Tests

`newTestDB` in `post_test.go` opens an in-memory SQLite GORM connection (`sqlite://:memory:`) and runs goose migrations against the sqlite embedded FS. All existing test cases are unchanged — they test the public API and are insulated from internals.

`server_test.go` and `config_test.go` updated if they reference `db_path` → `db_dsn`.

## Dependencies

Added:

- `gorm.io/gorm`
- `gorm.io/driver/sqlite`
- `gorm.io/driver/postgres`
- `github.com/pressly/goose/v3`

Removed:

- `modernc.org/sqlite` (and its transitive deps: `modernc.org/libc`, `modernc.org/mathutil`, `modernc.org/memory`)

## Files Changed

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | Add GORM + goose, remove modernc |
| `internal/config/config.go` | `DBPath` → `DBDSN` |
| `internal/db/db.go` | Full rewrite |
| `internal/db/migrations/` | Delete old, create `sqlite/` + `postgres/` subdirs |
| `internal/models/post.go` | Full rewrite |
| `internal/models/post_test.go` | Update `newTestDB` |
| `main.go` | `cfg.DBPath` → `cfg.DBDSN`, sample config string |
| `docs/ntx.sample.toml` | `db_path` → `db_dsn` |
| `internal/server/server_test.go` | Update if referencing `db_path` |
