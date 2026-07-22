package models

import (
	"testing"

	"github.com/nod/ntx/internal/db"
)

func newTestDB(t *testing.T) *PostStore {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})
	return NewPostStore(database)
}

func TestCreateAndGetBySlug(t *testing.T) {
	s := newTestDB(t)

	p := &Post{
		Slug:      "hello-world",
		Title:     "Hello World",
		Body:      "# Hello\n\ncontent here",
		Published: true,
		Tags:      []string{"go", "testing"},
	}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Error("expected ID to be set")
	}

	got, err := s.GetBySlug("hello-world")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected post, got nil")
	}
	if got.Title != "Hello World" {
		t.Errorf("title: got %q", got.Title)
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(got.Tags), got.Tags)
	}
}

func TestGetBySlugMissing(t *testing.T) {
	s := newTestDB(t)
	got, err := s.GetBySlug("nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for missing post")
	}
}

func TestList(t *testing.T) {
	s := newTestDB(t)

	posts := []Post{
		{Slug: "draft-one", Title: "Draft One", Body: "...", Published: false},
		{Slug: "pub-one", Title: "Pub One", Body: "...", Published: true},
		{Slug: "pub-two", Title: "Pub Two", Body: "...", Published: true},
	}
	for i := range posts {
		if err := s.Create(&posts[i]); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	published, err := s.List(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 {
		t.Errorf("expected 2 published, got %d", len(published))
	}
}

func TestListByTag(t *testing.T) {
	s := newTestDB(t)

	p1 := &Post{Slug: "a", Title: "A", Body: ".", Published: true, Tags: []string{"go"}}
	p2 := &Post{Slug: "b", Title: "B", Body: ".", Published: true, Tags: []string{"rust"}}
	p3 := &Post{Slug: "c", Title: "C", Body: ".", Published: true, Tags: []string{"go", "rust"}}
	for _, p := range []*Post{p1, p2, p3} {
		if err := s.Create(p); err != nil {
			t.Fatal(err)
		}
	}

	goPosts, err := s.ListByTag("go")
	if err != nil {
		t.Fatal(err)
	}
	if len(goPosts) != 2 {
		t.Errorf("expected 2 go posts, got %d", len(goPosts))
	}
}

func TestUpdate(t *testing.T) {
	s := newTestDB(t)

	p := &Post{Slug: "update-me", Title: "Original", Body: ".", Published: false, Tags: []string{"a"}}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}

	p.Title = "Updated"
	p.Published = true
	p.Tags = []string{"b", "c"}
	if err := s.Update(p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetBySlug("update-me")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Updated" {
		t.Errorf("title not updated: %q", got.Title)
	}
	if !got.Published {
		t.Error("should be published")
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", got.Tags)
	}
}

func TestDelete(t *testing.T) {
	s := newTestDB(t)

	p := &Post{Slug: "delete-me", Title: "Delete Me", Body: ".", Published: true}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBySlug("delete-me")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected deleted post to be nil")
	}
}

func TestStats(t *testing.T) {
	s := newTestDB(t)

	posts := []Post{
		{Slug: "p1", Title: "P1", Body: ".", Published: true, Tags: []string{"go"}},
		{Slug: "p2", Title: "P2", Body: ".", Published: true, Tags: []string{"rust"}},
		{Slug: "p3", Title: "P3", Body: ".", Published: false},
	}
	for i := range posts {
		if err := s.Create(&posts[i]); err != nil {
			t.Fatal(err)
		}
	}

	total, published, drafts, tagCount, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total: expected 3, got %d", total)
	}
	if published != 2 {
		t.Errorf("published: expected 2, got %d", published)
	}
	if drafts != 1 {
		t.Errorf("drafts: expected 1, got %d", drafts)
	}
	if tagCount != 2 {
		t.Errorf("tagCount: expected 2, got %d", tagCount)
	}
}

func TestTagDeduplication(t *testing.T) {
	s := newTestDB(t)

	p1 := &Post{Slug: "t1", Title: "T1", Body: ".", Published: true, Tags: []string{"go"}}
	p2 := &Post{Slug: "t2", Title: "T2", Body: ".", Published: true, Tags: []string{"go"}}
	if err := s.Create(p1); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(p2); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 unique tag, got %d: %v", len(all), all)
	}
}
