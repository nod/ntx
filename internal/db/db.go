package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	glebsqlite "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var silentLogger = logger.Default.LogMode(logger.Silent)

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

// ErrLegacySchema means the database was created by an ntx release that
// predates the 100_initial schema. There is no upgrade path.
var ErrLegacySchema = errors.New("legacy database schema")

// legacyCutoff is the first goose version of the current schema. Any lower
// version recorded in goose_db_version belongs to the retired 001-003 chain.
const legacyCutoff = 100

func openSQLite(path string) (*gorm.DB, error) {
	// Foreign keys are enabled through the DSN so every connection the pool
	// opens gets them -- a one-off PRAGMA only covers the connection it ran on.
	dsn := path + "?_pragma=foreign_keys(1)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := gorm.Open(glebsqlite.Open(dsn), &gorm.Config{Logger: silentLogger})
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if path != ":memory:" {
		if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
			sqlDB.Close()
			return nil, err
		}
	}
	if err := refuseLegacy(sqlDB, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'"); err != nil {
		sqlDB.Close()
		return nil, err
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

// refuseLegacy returns ErrLegacySchema if goose has recorded any version
// below legacyCutoff. tableExistsQuery counts goose's version table, so a
// fresh database (no table yet) passes.
func refuseLegacy(sqlDB *sql.DB, tableExistsQuery string) error {
	var n int
	if err := sqlDB.QueryRow(tableExistsQuery).Scan(&n); err != nil {
		return fmt.Errorf("checking schema version: %w", err)
	}
	if n == 0 {
		return nil
	}
	var old int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM goose_db_version WHERE version_id > 0 AND version_id < $1", legacyCutoff).Scan(&old); err != nil {
		return fmt.Errorf("checking schema version: %w", err)
	}
	if old > 0 {
		return fmt.Errorf("%w: this database predates ntx's web-managed sites and cannot be upgraded; delete it (and any -wal/-shm files) and start again", ErrLegacySchema)
	}
	return nil
}

func openPostgres(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{Logger: silentLogger})
	if err != nil {
		return nil, fmt.Errorf("opening postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// Only the schema ntx migrates (the first on search_path, where goose
	// creates its table) counts; a goose table elsewhere on the server is
	// another application's.
	if err := refuseLegacy(sqlDB, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'goose_db_version'"); err != nil {
		sqlDB.Close()
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
