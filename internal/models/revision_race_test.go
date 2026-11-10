package models

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/db/dbtest"
)

// pendingFixture: a contributor's live post on site A with pending changes.
func pendingFixture(t *testing.T) (*peopleFixture, *PostStore, *User, *Post, *Revision) {
	t.Helper()
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "Pending", "pb", nil, p.ContentUpdatedAt); err != nil {
		t.Fatal(err)
	}
	return f, ps, u, p, mustPending(t, ps, p.ID)
}

// An admin's direct edit committing while the pending changes are being
// published must make them stale, not be overwritten.
func TestPublishPendingWaitsForDirectEdit(t *testing.T) {
	f, ps, _, p, r := pendingFixture(t)
	tx := begin(t, f.db)
	later := p.ContentUpdatedAt.Add(time.Second)
	if err := tx.Exec("UPDATE posts SET title = 'Direct edit', updated_at = ?, content_updated_at = ? WHERE id = ?",
		later, later, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error { return ps.PublishPending(p.ID, r.UpdatedAt, time.Time{}) })
	if !errors.Is(err, ErrStale) {
		t.Fatalf("PublishPending = %v, want ErrStale", err)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "Direct edit" {
		t.Errorf("the direct edit was overwritten: title %q", got.Title)
	}
	mustPending(t, ps, p.ID)
}

// A contributor saving again while the admin publishes must not have the
// newer save deleted along with the version the admin saw.
func TestPublishPendingKeepsConcurrentSave(t *testing.T) {
	f, ps, _, p, r := pendingFixture(t)
	tx := begin(t, f.db)
	if err := tx.Exec("UPDATE post_revisions SET title = 'Newer', updated_at = ? WHERE post_id = ?",
		r.UpdatedAt.Add(time.Second), p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error { return ps.PublishPending(p.ID, r.UpdatedAt, time.Time{}) })
	if !errors.Is(err, ErrRevisionChanged) {
		t.Fatalf("PublishPending = %v, want ErrRevisionChanged", err)
	}
	if got := mustPending(t, ps, p.ID); got.Title != "Newer" {
		t.Errorf("pending title %q, want the newer save", got.Title)
	}
	if got := mustGet(t, ps, p.ID); got.Title != p.Title {
		t.Errorf("post title %q, want it untouched", got.Title)
	}
}

func TestDiscardPendingKeepsConcurrentSave(t *testing.T) {
	f, ps, _, p, r := pendingFixture(t)
	tx := begin(t, f.db)
	if err := tx.Exec("UPDATE post_revisions SET title = 'Newer', updated_at = ? WHERE post_id = ?",
		r.UpdatedAt.Add(time.Second), p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error { return ps.DiscardPending(p.ID, r.UpdatedAt) })
	if !errors.Is(err, ErrRevisionChanged) {
		t.Fatalf("DiscardPending = %v, want ErrRevisionChanged", err)
	}
	if got := mustPending(t, ps, p.ID); got.Title != "Newer" {
		t.Errorf("pending title %q, want the newer save", got.Title)
	}
}

// A contributor's save landing while the admin publishes the previous
// version must become new pending changes, not vanish.
func TestSavePendingDuringPublishIsKept(t *testing.T) {
	f, ps, u, p, _ := pendingFixture(t)
	tx := begin(t, f.db)
	// What PublishPending writes: the post, then the revision gone.
	later := p.ContentUpdatedAt.Add(time.Second)
	if err := tx.Exec("UPDATE posts SET title = 'Pending', updated_at = ?, content_updated_at = ? WHERE id = ?",
		later, later, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("DELETE FROM post_revisions WHERE post_id = ?", p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error {
		_, err := ps.SavePending(p.ID, u.ID, "Second", "sb", nil, p.ContentUpdatedAt)
		return err
	})
	if err != nil {
		t.Fatalf("SavePending = %v", err)
	}
	if got := mustPending(t, ps, p.ID); got.Title != "Second" {
		t.Errorf("pending title %q, want the second save", got.Title)
	}
}

// Two first saves at once (a double-click) must both succeed, one after
// the other, rather than the second hitting the unique index.
func TestSimultaneousFirstSaves(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)

	// Another first save, in flight: it is rolled back, leaving the two
	// below to race for the post's only revision.
	tx := begin(t, f.db)
	now := dbNow()
	if err := tx.Create(&dbRevision{PostID: p.ID, Title: "x", Tags: "[]", BaseUpdatedAt: p.UpdatedAt, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, false, func() error {
		errs := make(chan error, 2)
		for _, title := range []string{"One", "Two"} {
			go func() {
				_, err := ps.SavePending(p.ID, u.ID, title, "b", nil, p.ContentUpdatedAt)
				errs <- err
			}()
		}
		return errors.Join(<-errs, <-errs)
	})
	if err != nil {
		t.Fatalf("simultaneous first saves: %v", err)
	}
	if got := mustPending(t, ps, p.ID); got.Title != "One" && got.Title != "Two" {
		t.Errorf("pending title %q", got.Title)
	}
}

// Two admins publishing pending changes at once, each adding the same new
// tags in a different order, must not deadlock: tags are created inside
// the publish transaction, so they have to be taken in one order.
func TestPublishesWithNewTagsInOppositeOrder(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	for round := range 5 {
		var names []string
		for i := range 30 {
			names = append(names, fmt.Sprintf("r%d-t%02d", round, i))
		}
		reversed := slices.Clone(names)
		slices.Reverse(reversed)
		var seen [2]time.Time
		var ids [2]int64
		for i, tags := range [][]string{names, reversed} {
			p := livePost(t, ps, u, fmt.Sprintf("r%d-p%d", round, i), true)
			if _, err := ps.SavePending(p.ID, u.ID, "New", "b", tags, p.ContentUpdatedAt); err != nil {
				t.Fatal(err)
			}
			ids[i], seen[i] = p.ID, mustPending(t, ps, p.ID).UpdatedAt
		}
		errs := make(chan error, 2)
		for i := range 2 {
			go func() { errs <- ps.PublishPending(ids[i], seen[i], time.Time{}) }()
		}
		if err := errors.Join(<-errs, <-errs); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

// Deleting someone (or a site) while a post write of theirs is in flight
// must let both finish, one after the other, not deadlock (#27). Another
// request holds the post, so the write and the delete both queue behind
// it, and ending it releases them together. SQLite runs them in sequence,
// in whichever order they reach its one connection, so these are
// Postgres-only.
func skipOnSQLite(t *testing.T, d *gorm.DB) {
	t.Helper()
	if d.Dialector.Name() != "postgres" {
		t.Skip("needs two connections")
	}
}

func TestDeleteAuthorWhileSavingPending(t *testing.T) {
	f := newPeople(t)
	skipOnSQLite(t, f.db)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)

	tx := begin(t, f.db)
	mustExec(t, tx.Model(&dbPost{}).Where("id = ?", p.ID).Update("slug", "held"))
	saved := make(chan error, 1)
	go func() {
		_, err := ps.SavePending(p.ID, u.ID, "Mine", "b", nil, p.ContentUpdatedAt)
		saved <- err
	}()
	dbtest.AwaitBlockedN(t, tx, 1)
	deleted := make(chan error, 1)
	go func() { deleted <- f.users.Delete(u.ID) }()
	dbtest.AwaitBlockedN(t, tx, 2)
	mustExec(t, tx.Commit())

	if err := <-saved; err != nil {
		t.Errorf("SavePending: %v", err)
	}
	if err := <-deleted; err != nil {
		t.Errorf("Delete: %v", err)
	}
	if got, _ := f.users.GetByID(u.ID); got != nil {
		t.Errorf("the person is still there")
	}
}

func TestDeleteSiteWhilePublishingNewTag(t *testing.T) {
	f := newPeople(t)
	skipOnSQLite(t, f.db)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	rev, err := ps.SavePending(p.ID, u.ID, "Tagged", "b", []string{"fresh"}, p.ContentUpdatedAt)
	if err != nil {
		t.Fatal(err)
	}

	tx := begin(t, f.db)
	mustExec(t, tx.Model(&dbPost{}).Where("id = ?", p.ID).Update("slug", "held"))
	published := make(chan error, 1)
	go func() { published <- ps.PublishPending(p.ID, rev.UpdatedAt, time.Time{}) }()
	dbtest.AwaitBlockedN(t, tx, 1)
	deleted := make(chan error, 1)
	go func() { deleted <- f.sites.Delete(f.a.ID) }()
	dbtest.AwaitBlockedN(t, tx, 2)
	mustExec(t, tx.Commit())

	if err := <-published; err != nil {
		t.Errorf("PublishPending: %v", err)
	}
	if err := <-deleted; err != nil {
		t.Errorf("Delete: %v", err)
	}
	var left int64
	mustExec(t, f.db.Model(&dbSite{}).Where("id = ?", f.a.ID).Count(&left))
	if left != 0 {
		t.Errorf("the site is still there")
	}
}
