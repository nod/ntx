// Package dbtest opens a throwaway, migrated database for a test: SQLite in
// memory by default, or a fresh Postgres schema when NTX_TEST_POSTGRES_DSN
// is set, so the same tests cover both drivers (CI runs them twice).
package dbtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

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

// AwaitBlocked returns once another connection is waiting on a lock that
// tx holds, and fails t if none is within 10 seconds. Race tests call it
// between starting a request and ending tx, so the request is known to
// have run into tx rather than having been too slow to reach it. On SQLite
// it returns at once: with one connection, the request simply runs after
// tx ends.
func AwaitBlocked(t testing.TB, tx *gorm.DB) {
	t.Helper()
	AwaitBlockedN(t, tx, 1)
}

// AwaitBlockedN is AwaitBlocked for n waiting connections. A connection
// counts when it waits on tx directly or on another one that counts, so a
// request queued behind another stuck request is included.
func AwaitBlockedN(t testing.TB, tx *gorm.DB, n int) {
	t.Helper()
	if err := awaitBlocked(tx, n, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

func awaitBlocked(tx *gorm.DB, n int, within time.Duration) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	var pid int
	if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
		return err
	}
	// A connection of its own: tx's is busy holding the locks, and the
	// pool may be busy with the waiters.
	watch, err := sql.Open("pgx", os.Getenv(EnvDSN))
	if err != nil {
		return err
	}
	defer watch.Close()
	const waiters = `
WITH RECURSIVE behind(pid) AS (
	SELECT pid FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))
	UNION
	SELECT a.pid FROM pg_stat_activity a JOIN behind b ON b.pid = ANY(pg_blocking_pids(a.pid))
)
SELECT count(*) FROM behind`
	got := 0
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if err := watch.QueryRow(waiters, pid).Scan(&got); err != nil {
			return err
		}
		if got >= n {
			return nil
		}
	}
	return fmt.Errorf("%d of %d connections waited on the open transaction within %v", got, n, within)
}
