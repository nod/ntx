package models

import (
	"os"
	"reflect"
	"testing"

	"github.com/nod/ntx/internal/db"
)

// TestRevisionsOnPostgres runs the revision round trip against a real
// Postgres (JSONB tags, microsecond timestamps) when NTX_TEST_POSTGRES_DSN
// names a fresh database, e.g. a throwaway container.
func TestRevisionsOnPostgres(t *testing.T) {
	dsn := os.Getenv("NTX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NTX_TEST_POSTGRES_DSN not set")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := d.DB()
		sqlDB.Close()
	})
	f := newPeopleOn(t, d)
	checkSavePendingLeavesPostAlone(t, f)

	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p, err := ps.GetBySlug("live")
	if err != nil || p == nil {
		t.Fatalf("GetBySlug = %v, %v", p, err)
	}
	r := mustPending(t, ps, p.ID)
	if err := ps.PublishPending(p.ID, r.UpdatedAt, false); err != nil {
		t.Fatalf("PublishPending on an untouched post: %v", err)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "New" || !reflect.DeepEqual(got.Tags, []string{"new"}) {
		t.Fatalf("after publish: %+v", got)
	}
}
