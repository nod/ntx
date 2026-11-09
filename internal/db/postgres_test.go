package db_test

import (
	"database/sql"
	"errors"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/db/dbtest"
)

// The legacy check must look only at the schema ntx is about to use: an old
// goose table elsewhere on the server must neither block a fresh schema nor
// hide one in ours.
func TestPostgresLegacyCheckIsPerSchema(t *testing.T) {
	legacy := dbtest.Schema(t)
	raw, err := sql.Open("pgx", legacy)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, q := range []string{
		"CREATE TABLE goose_db_version (id SERIAL PRIMARY KEY, version_id BIGINT NOT NULL, is_applied BOOLEAN NOT NULL, tstamp TIMESTAMP DEFAULT now())",
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (3, TRUE)",
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	fresh, err := db.Open(dbtest.Schema(t))
	if err != nil {
		t.Fatalf("a fresh schema next to a legacy one must open: %v", err)
	}
	s, _ := fresh.DB()
	s.Close()

	if d, err := db.Open(legacy); !errors.Is(err, db.ErrLegacySchema) {
		if err == nil {
			s, _ := d.DB()
			s.Close()
		}
		t.Fatalf("the legacy schema must be refused, got %v", err)
	}
}
