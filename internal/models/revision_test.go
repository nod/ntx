package models

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// livePost creates a post by u on site A, published or a live-once
// unpublished one, and returns it as read back from the store.
func livePost(t *testing.T, ps *PostStore, u *User, slug string, published bool, tags ...string) *Post {
	t.Helper()
	p := &Post{Slug: slug, Title: "Title " + slug, Body: "body", Published: true, Tags: tags, AuthorID: &u.ID}
	if err := ps.Create(p); err != nil {
		t.Fatal(err)
	}
	if !published {
		p.Published = false
		if err := ps.Update(p); err != nil {
			t.Fatal(err)
		}
	}
	return mustGet(t, ps, p.ID)
}

func mustGet(t *testing.T, ps *PostStore, id int64) *Post {
	t.Helper()
	p, err := ps.GetByID(id)
	if err != nil || p == nil {
		t.Fatalf("GetByID(%d) = %v, %v", id, p, err)
	}
	return p
}

func mustPending(t *testing.T, ps *PostStore, id int64) *Revision {
	t.Helper()
	r, err := ps.GetPending(id)
	if err != nil || r == nil {
		t.Fatalf("GetPending(%d) = %v, %v", id, r, err)
	}
	return r
}

func TestSavePendingRefusesNeverPublished(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	d := &Post{Slug: "d", Title: "D", Body: "b", AuthorID: &u.ID}
	if err := ps.Create(d); err != nil {
		t.Fatal(err)
	}
	d = mustGet(t, ps, d.ID)
	if _, err := ps.SavePending(d.ID, u.ID, "New", "nb", nil, d.UpdatedAt); !errors.Is(err, ErrNotLiveOnce) {
		t.Fatalf("SavePending on a draft = %v, want ErrNotLiveOnce", err)
	}
	if r, err := ps.GetPending(d.ID); r != nil || err != nil {
		t.Fatalf("a draft got a revision: %+v, %v", r, err)
	}
}

func TestSavePendingLeavesPostAlone(t *testing.T) {
	f := newPeople(t)
	checkSavePendingLeavesPostAlone(t, f)
}

func checkSavePendingLeavesPostAlone(t *testing.T, f *peopleFixture) {
	t.Helper()
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true, "old")
	if _, err := ps.SavePending(p.ID, u.ID, "New", "nb", []string{" new ", ""}, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, ps, p.ID)
	if got.Title != "Title live" || !reflect.DeepEqual(got.Tags, []string{"old"}) {
		t.Fatalf("post changed by a pending save: %+v", got)
	}
	r := mustPending(t, ps, p.ID)
	if r.Title != "New" || r.Body != "nb" || !reflect.DeepEqual(r.Tags, []string{"new"}) || r.AuthorID == nil || *r.AuthorID != u.ID {
		t.Fatalf("revision = %+v", r)
	}
	ids, err := ps.PendingIDs()
	if err != nil || !reflect.DeepEqual(ids, map[int64]bool{p.ID: true}) {
		t.Fatalf("PendingIDs = %v, %v", ids, err)
	}
	tags, _ := ps.AllTags()
	if slices.Contains(tags, "new") {
		t.Fatalf("a pending tag is public: %v", tags)
	}
	if r.Stale(got) {
		t.Fatal("an untouched post reads as stale")
	}
}

func TestSavePendingOverwrites(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "First", "b", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	first := mustPending(t, ps, p.ID)
	if _, err := ps.SavePending(p.ID, u.ID, "Second", "b", nil, p.UpdatedAt.Add(1e9)); err != nil {
		t.Fatal(err)
	}
	second := mustPending(t, ps, p.ID)
	if second.ID != first.ID || second.Title != "Second" {
		t.Fatalf("second save = %+v, first %+v", second, first)
	}
	if !second.BaseUpdatedAt.Equal(first.BaseUpdatedAt) {
		t.Fatalf("base moved on overwrite: %v → %v", first.BaseUpdatedAt, second.BaseUpdatedAt)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("UpdatedAt didn't advance: %v → %v", first.UpdatedAt, second.UpdatedAt)
	}
}

func TestPendingNotStaleWhenUntouched(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "New", "nb", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if r := mustPending(t, ps, p.ID); r.Stale(mustGet(t, ps, p.ID)) {
		t.Fatal("untouched post reads as stale")
	}
	p.Title = "Edited"
	if err := ps.Update(p); err != nil {
		t.Fatal(err)
	}
	if r := mustPending(t, ps, p.ID); !r.Stale(mustGet(t, ps, p.ID)) {
		t.Fatal("a directly edited post doesn't read as stale")
	}
}

func TestPublishPendingCopiesContent(t *testing.T) {
	for _, published := range []bool{true, false} {
		f := newPeople(t)
		u := f.person(t, "w@x.io")
		ps := NewPostStore(f.db).ForSite(f.a.ID)
		p := livePost(t, ps, u, "live", published, "t1")
		r, err := ps.SavePending(p.ID, u.ID, "New", "nb", []string{"t2"}, p.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
		r = mustPending(t, ps, r.PostID)
		if err := ps.PublishPending(p.ID, r.UpdatedAt, false); err != nil {
			t.Fatalf("published=%v: %v", published, err)
		}
		got := mustGet(t, ps, p.ID)
		if got.Title != "New" || got.Body != "nb" || !reflect.DeepEqual(got.Tags, []string{"t2"}) || got.Slug != "live" || got.Published != published || !got.LiveOnce() {
			t.Fatalf("published=%v: post after publish = %+v", published, got)
		}
		if r, _ := ps.GetPending(p.ID); r != nil {
			t.Fatalf("revision survived publishing: %+v", r)
		}
	}
}

func TestPublishPendingStale(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "New", "nb", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	r := mustPending(t, ps, p.ID)
	p.Title = "Admin edit"
	if err := ps.Update(p); err != nil {
		t.Fatal(err)
	}
	if err := ps.PublishPending(p.ID, r.UpdatedAt, false); !errors.Is(err, ErrStale) {
		t.Fatalf("PublishPending on a stale revision = %v, want ErrStale", err)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "Admin edit" {
		t.Fatalf("refused publish changed the post: %+v", got)
	}
	mustPending(t, ps, p.ID)
	if err := ps.PublishPending(p.ID, r.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "New" {
		t.Fatalf("confirmed publish didn't copy: %+v", got)
	}
}

func TestPublishPendingRefusesNewerRevision(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "First", "b", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	rev1 := mustPending(t, ps, p.ID)
	if _, err := ps.SavePending(p.ID, u.ID, "Second", "b", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	rev2 := mustPending(t, ps, p.ID)
	if err := ps.PublishPending(p.ID, rev1.UpdatedAt, true); !errors.Is(err, ErrRevisionChanged) {
		t.Fatalf("publishing an outdated view = %v, want ErrRevisionChanged", err)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "Title live" {
		t.Fatalf("post changed: %+v", got)
	}
	if err := ps.DiscardPending(p.ID, rev1.UpdatedAt); !errors.Is(err, ErrRevisionChanged) {
		t.Fatalf("discarding an outdated view = %v, want ErrRevisionChanged", err)
	}
	mustPending(t, ps, p.ID)
	if err := ps.DiscardPending(p.ID, rev2.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if r, _ := ps.GetPending(p.ID); r != nil {
		t.Fatalf("discard left the revision: %+v", r)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "Title live" {
		t.Fatalf("discard changed the post: %+v", got)
	}
	if err := ps.DiscardPending(p.ID, rev2.UpdatedAt); !errors.Is(err, ErrRevisionChanged) {
		t.Fatalf("discarding twice = %v, want ErrRevisionChanged", err)
	}
}

func TestPendingScopedToSite(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	if _, err := ps.SavePending(p.ID, u.ID, "New", "nb", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	r := mustPending(t, ps, p.ID)
	other := NewPostStore(f.db).ForSite(f.b.ID)
	if got, err := other.GetPending(p.ID); got != nil || err != nil {
		t.Fatalf("site B sees site A's revision: %+v, %v", got, err)
	}
	if ids, _ := other.PendingIDs(); len(ids) != 0 {
		t.Fatalf("site B's PendingIDs = %v", ids)
	}
	if _, err := other.SavePending(p.ID, u.ID, "B", "b", nil, p.UpdatedAt); err == nil {
		t.Fatal("site B saved a revision onto site A's post")
	}
	if err := other.PublishPending(p.ID, r.UpdatedAt, true); err == nil {
		t.Fatal("site B published site A's revision")
	}
	if err := other.DiscardPending(p.ID, r.UpdatedAt); err == nil {
		t.Fatal("site B discarded site A's revision")
	}
	if again := mustPending(t, ps, p.ID); again.Title != "New" {
		t.Fatalf("site A's revision changed: %+v", again)
	}
	if got := mustGet(t, ps, p.ID); got.Title != "Title live" {
		t.Fatalf("site A's post changed: %+v", got)
	}
}

func TestRevisionCascadesAndOrphans(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	gone := livePost(t, ps, u, "gone", true)
	kept := livePost(t, ps, u, "kept", true)
	for _, p := range []*Post{gone, kept} {
		if _, err := ps.SavePending(p.ID, u.ID, "New", "nb", nil, p.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := ps.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	var n int64
	f.db.Table("post_revisions").Where("post_id = ?", gone.ID).Count(&n)
	if n != 0 {
		t.Fatal("deleting a post left its revision")
	}
	if err := f.users.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	if r := mustPending(t, ps, kept.ID); r.AuthorID != nil {
		t.Fatalf("deleted author still on the revision: %v", *r.AuthorID)
	}
}

func TestUpdateContentKeepsPublishedState(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := livePost(t, ps, u, "live", true)
	p.Published = false
	p.Title = "T2"
	if err := ps.UpdateContent(p); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, ps, p.ID); !got.Published || got.Title != "T2" {
		t.Fatalf("after UpdateContent: published=%v title=%q", got.Published, got.Title)
	}
}

// UpdateDraft is the save for someone who may only edit drafts: once the
// post has been published (say, by an admin a moment earlier) it must
// change nothing.
func TestUpdateDraftRefusesLiveOnce(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	d := &Post{Slug: "d", Title: "D", Body: "b", AuthorID: &u.ID}
	if err := ps.Create(d); err != nil {
		t.Fatal(err)
	}
	d.Title = "Draft edit"
	if err := ps.UpdateDraft(d); err != nil {
		t.Fatalf("UpdateDraft on a draft: %v", err)
	}
	if got := mustGet(t, ps, d.ID); got.Title != "Draft edit" {
		t.Fatalf("draft not updated: %+v", got)
	}
	live := livePost(t, ps, u, "live", true)
	live.Title = "Unreviewed"
	if err := ps.UpdateDraft(live); !errors.Is(err, ErrLiveOnce) {
		t.Fatalf("UpdateDraft on a live-once post = %v, want ErrLiveOnce", err)
	}
	if got := mustGet(t, ps, live.ID); got.Title != "Title live" {
		t.Fatalf("live post changed: %+v", got)
	}
}
