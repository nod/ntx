package models

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// These tests hold another request's writes open in an uncommitted
// transaction while a store call runs, then commit or roll them back. On
// Postgres the call runs alongside the open transaction, as two requests
// would; on SQLite (one connection) it simply waits for it, so the same
// expectations hold for both.

// whileOpen starts op, gives it time to run into other's writes, ends
// other (commit or rollback), and returns op's result.
func whileOpen(t *testing.T, other *gorm.DB, commit bool, op func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- op() }()
	time.Sleep(200 * time.Millisecond)
	end := other.Rollback
	if commit {
		end = other.Commit
	}
	if err := end().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("still blocked after the other transaction ended")
		return nil
	}
}

func begin(t *testing.T, f *peopleFixture) *gorm.DB {
	t.Helper()
	tx := f.db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	return tx
}

// pendingFixture: a contributor's live post on site A with pending changes.
func pendingFixture(t *testing.T) (*peopleFixture, *PostStore, *User, *Post, *Revision) {
	t.Helper()
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "Pending", "pb", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	return f, ps, u, p, mustPending(t, ps, p.ID)
}

// An admin's direct edit committing while the pending changes are being
// published must make them stale, not be overwritten.
func TestPublishPendingWaitsForDirectEdit(t *testing.T) {
	f, ps, _, p, r := pendingFixture(t)
	tx := begin(t, f)
	if err := tx.Exec("UPDATE posts SET title = 'Direct edit', updated_at = ? WHERE id = ?",
		p.UpdatedAt.Add(time.Second), p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error { return ps.PublishPending(p.ID, r.UpdatedAt, false) })
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
	tx := begin(t, f)
	if err := tx.Exec("UPDATE post_revisions SET title = 'Newer', updated_at = ? WHERE post_id = ?",
		r.UpdatedAt.Add(time.Second), p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error { return ps.PublishPending(p.ID, r.UpdatedAt, false) })
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
	tx := begin(t, f)
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
	tx := begin(t, f)
	// What PublishPending writes: the post, then the revision gone.
	if err := tx.Exec("UPDATE posts SET title = 'Pending', updated_at = ? WHERE id = ?",
		p.UpdatedAt.Add(time.Second), p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("DELETE FROM post_revisions WHERE post_id = ?", p.ID).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, true, func() error {
		_, err := ps.SavePending(p.ID, u.ID, "Second", "sb", nil, p.UpdatedAt)
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
	tx := begin(t, f)
	now := revisionNow()
	if err := tx.Create(&dbRevision{PostID: p.ID, Title: "x", Tags: "[]", BaseUpdatedAt: p.UpdatedAt, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	err := whileOpen(t, tx, false, func() error {
		errs := make(chan error, 2)
		for _, title := range []string{"One", "Two"} {
			go func() {
				_, err := ps.SavePending(p.ID, u.ID, title, "b", nil, p.UpdatedAt)
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
