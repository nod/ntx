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
