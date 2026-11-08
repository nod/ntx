package models

import "testing"

func TestCreateStoresAuthor(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	o := f.person(t, "o@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	p := &Post{Slug: "mine", Title: "Mine", Body: "b", AuthorID: &u.ID}
	if err := ps.Create(p); err != nil {
		t.Fatal(err)
	}
	got, _ := ps.GetByID(p.ID)
	if got.AuthorID == nil || *got.AuthorID != u.ID {
		t.Fatalf("AuthorID = %v, want %d", got.AuthorID, u.ID)
	}
	got.AuthorID = &o.ID
	got.Title = "Changed"
	if err := ps.Update(got); err != nil {
		t.Fatal(err)
	}
	again, _ := ps.GetByID(p.ID)
	if again.AuthorID == nil || *again.AuthorID != u.ID || again.Title != "Changed" {
		t.Fatalf("after Update: AuthorID = %v title %q; the author must not change", again.AuthorID, again.Title)
	}
	if again.LiveOnce() {
		t.Fatal("a never-published post reports LiveOnce")
	}
	again.Published = true
	if err := ps.Update(again); err != nil {
		t.Fatal(err)
	}
	again.Published = false
	if err := ps.Update(again); err != nil {
		t.Fatal(err)
	}
	if final, _ := ps.GetByID(p.ID); !final.LiveOnce() {
		t.Fatal("an unpublished, once-published post must report LiveOnce")
	}
}

func TestListByAuthorScopedToSite(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	o := f.person(t, "o@x.io")
	raw := NewPostStore(f.db)
	for _, c := range []struct {
		site      int64
		slug      string
		author    *int64
		published bool
	}{
		{f.a.ID, "a-draft", &u.ID, false},
		{f.a.ID, "a-live", &u.ID, true},
		{f.a.ID, "a-other", &o.ID, false},
		{f.b.ID, "b-mine", &u.ID, true},
	} {
		if err := raw.ForSite(c.site).Create(&Post{Slug: c.slug, Title: c.slug, AuthorID: c.author, Published: c.published}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := raw.ForSite(f.a.ID).ListByAuthor(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]bool{}
	for _, p := range got {
		slugs[p.Slug] = true
	}
	if len(got) != 2 || !slugs["a-draft"] || !slugs["a-live"] {
		t.Fatalf("ListByAuthor on A = %v, want a-draft and a-live only", slugs)
	}
}
