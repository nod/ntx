package models

import (
	"testing"
)

func newTestStores(t *testing.T) (siteA, siteB *PostStore) {
	t.Helper()
	database := newTestSiteDB(t)
	sites := NewSiteStore(database)
	a := mustCreateSite(t, sites, "site-a", "site-a.example.com")
	b := mustCreateSite(t, sites, "site-b", "site-b.example.com")
	raw := NewPostStore(database)
	return raw.ForSite(a.ID), raw.ForSite(b.ID)
}

func newTestDB(t *testing.T) *PostStore {
	t.Helper()
	s, _ := newTestStores(t)
	return s
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

// Multi-tenant isolation tests

func TestSlugUniquePerSiteNotGlobally(t *testing.T) {
	siteA, siteB := newTestStores(t)

	if err := siteA.Create(&Post{Slug: "same-slug", Title: "A's post", Body: "."}); err != nil {
		t.Fatal(err)
	}
	if err := siteB.Create(&Post{Slug: "same-slug", Title: "B's post", Body: "."}); err != nil {
		t.Fatalf("expected same slug to be valid in a different site, got: %v", err)
	}

	if err := siteA.Create(&Post{Slug: "same-slug", Title: "A's second post", Body: "."}); err == nil {
		t.Error("expected duplicate slug within the same site to fail")
	}
}

func TestForSiteIsolatesReads(t *testing.T) {
	siteA, siteB := newTestStores(t)

	if err := siteA.Create(&Post{Slug: "a-only", Title: "A Only", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}

	got, err := siteB.GetBySlug("a-only")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("site B should not see site A's post")
	}

	all, err := siteB.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("expected site B to have 0 posts, got %d", len(all))
	}
}

// TestTagNameUniquePerSiteNotGlobally asserts that two sites using the same
// tag name each own their own tags row.
//
// The row count is the load-bearing assertion. Counting what each site's
// AllTags() returns is not enough: if resolveTags dropped its site_id
// condition, site B's post would attach site A's existing tag row, and both
// sites would still report exactly 1 tag named "shared" -- a live
// cross-tenant association that per-site AllTags() cannot see. Only the
// "2 rows named shared" check distinguishes two private rows from one shared
// row, so that is what catches the mutation.
func TestTagNameUniquePerSiteNotGlobally(t *testing.T) {
	siteA, siteB := newTestStores(t)

	if err := siteA.Create(&Post{Slug: "a1", Title: "A1", Body: ".", Tags: []string{"shared"}}); err != nil {
		t.Fatal(err)
	}
	if err := siteB.Create(&Post{Slug: "b1", Title: "B1", Body: ".", Tags: []string{"shared"}}); err != nil {
		t.Fatal(err)
	}

	aTags, err := siteA.AllTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(aTags) != 1 || aTags[0] != "shared" {
		t.Errorf("expected site A to see exactly the tag \"shared\", got %v", aTags)
	}

	bTags, err := siteB.AllTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(bTags) != 1 || bTags[0] != "shared" {
		t.Errorf("expected site B to see exactly the tag \"shared\", got %v", bTags)
	}

	var sharedRows int64
	if err := siteA.db.Model(&dbTag{}).Where("name = ?", "shared").Count(&sharedRows).Error; err != nil {
		t.Fatal(err)
	}
	if sharedRows != 2 {
		t.Errorf("expected 2 tags rows named \"shared\" (one per site), got %d -- "+
			"1 means the two sites are sharing a single tag row across the tenant boundary", sharedRows)
	}

	var bTagSiteID int64
	if err := siteA.db.Model(&dbTag{}).
		Select("tags.site_id").
		Joins("JOIN post_tags ON post_tags.tag_id = tags.id").
		Joins("JOIN posts ON posts.id = post_tags.post_id AND posts.slug = ?", "b1").
		Scan(&bTagSiteID).Error; err != nil {
		t.Fatal(err)
	}
	if bTagSiteID != 2 {
		t.Errorf("site B's post is associated with a tag owned by site %d, want site 2", bTagSiteID)
	}
}

func TestUpdateCannotCrossSiteBoundary(t *testing.T) {
	siteA, siteB := newTestStores(t)

	p := &Post{Slug: "a-owned", Title: "A's Post", Body: ".", Tags: []string{"original"}}
	if err := siteA.Create(p); err != nil {
		t.Fatal(err)
	}

	hijack := &Post{ID: p.ID, Slug: "a-owned", Title: "Hijacked", Body: "hijacked body", Published: true, Tags: []string{"hijacked"}}
	if err := siteB.Update(hijack); err == nil {
		t.Error("expected site B updating site A's post id to fail")
	}

	got, err := siteA.GetBySlug("a-owned")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected site A's post to still exist")
	}
	if got.Title != "A's Post" {
		t.Errorf("title was modified by a cross-site update: got %q", got.Title)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "original" {
		t.Errorf("tags were modified by a cross-site update: got %v", got.Tags)
	}
}
