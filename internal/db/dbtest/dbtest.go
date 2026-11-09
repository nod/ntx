// Package dbtest opens a throwaway, migrated database for a test: SQLite in
// memory by default, or a fresh Postgres schema when NTX_TEST_POSTGRES_DSN
// is set, so the same tests cover both drivers (CI runs them twice).
package dbtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"

	"github.com/nod/ntx/internal/db"
)

// EnvDSN names the Postgres server the tests use. Any database on it will
// do: each test gets its own schema there and drops it when done.
const EnvDSN = "NTX_TEST_POSTGRES_DSN"

// Open returns a migrated database for t, closed when t ends.
func Open(t testing.TB) *gorm.DB {
	t.Helper()
	dsn := "sqlite://:memory:"
	if os.Getenv(EnvDSN) != "" {
		dsn = Schema(t)
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s, _ := d.DB()
		s.Close()
	})
	return d
}

// Schema creates an empty schema on the NTX_TEST_POSTGRES_DSN server and
// returns a DSN whose search_path is that schema; the schema is dropped
// when t ends. It skips t when the variable is unset.
func Schema(t testing.TB) string {
	t.Helper()
	base := os.Getenv(EnvDSN)
	if base == "" {
		t.Skip(EnvDSN + " not set")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("%s: %v", EnvDSN, err)
	}
	b := make([]byte, 8)
	rand.Read(b)
	name := "ntx_test_" + hex.EncodeToString(b)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		admin.Close()
		t.Fatalf("creating test schema: %v", err)
	}
	// Registered before the caller's close, so it runs after it.
	t.Cleanup(func() {
		defer admin.Close()
		if _, err := admin.Exec("DROP SCHEMA " + name + " CASCADE"); err != nil {
			t.Errorf("dropping test schema %s: %v", name, err)
		}
	})

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}
