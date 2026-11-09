package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nod/ntx/internal/models"
)

// authored writes a post on site A by u directly through the store.
func (e *env) authored(u *models.User, slug string, published bool) *models.Post {
	e.t.Helper()
	p := &models.Post{Slug: slug, Title: "Title " + slug, Body: "body", Published: published, AuthorID: &u.ID}
	if err := e.posts.ForSite(e.siteA.ID).Create(p); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func postForm(title, slug, action string) url.Values {
	return url.Values{"title": {title}, "slug": {slug}, "body": {"b"}, "tags": {""}, "action": {action}}
}

func TestContributorSeesOnlyOwnPosts(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	other := e.member("other@x.io", models.RoleContributor, false)
	mine := e.authored(me, "mine", false)
	theirs := e.authored(other, "theirs", false)
	c := e.sessionFor(me)
	list := e.do(http.MethodGet, hostA, "/admin/posts", nil, c)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Title mine") || strings.Contains(list.Body.String(), "Title theirs") {
		t.Fatalf("contributor's list: %d %s", list.Code, list.Body.String())
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/posts/"+itoa(theirs.ID)+"/edit", nil, c); rec.Code != http.StatusNotFound {
		t.Fatalf("editing someone else's draft: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/posts/"+itoa(mine.ID)+"/edit", nil, c); rec.Code != http.StatusOK {
		t.Fatalf("editing own draft: %d", rec.Code)
	}
}

func TestContributorNoPublishCannotPublish(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	c := e.sessionFor(me)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts", postForm("Live", "live", "publish"), c); rec.Code != http.StatusForbidden {
		t.Fatalf("publish on create: %d", rec.Code)
	}
	if p, _ := e.posts.ForSite(e.siteA.ID).GetBySlug("live"); p != nil {
		t.Fatal("the refused post was created")
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/posts", postForm("Draft", "draft", "save"), c); rec.Code != http.StatusSeeOther {
		t.Fatalf("save on create: %d %s", rec.Code, rec.Body.String())
	}
	p, _ := e.posts.ForSite(e.siteA.ID).GetBySlug("draft")
	if p == nil || p.Published || p.AuthorID == nil || *p.AuthorID != me.ID {
		t.Fatalf("draft = %+v", p)
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Draft", "draft", "publish"), c); rec.Code != http.StatusForbidden {
		t.Fatalf("publishing own draft without can_publish: %d", rec.Code)
	}
}

var baseRe = regexp.MustCompile(`name="base" value="([^"]*)"`)

// editorBase returns the hidden base timestamp from an editor page.
func editorBase(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	m := baseRe.FindStringSubmatch(text(rec))
	if m == nil {
		t.Fatalf("editor has no base field: %s", rec.Body.String())
	}
	return m[1]
}

// publicPages fetches the reader-facing pages a pending edit must not change.
func (e *env) publicPages(slug, tag string) []string {
	var out []string
	for _, path := range []string{"/", "/blog", "/blog/" + slug, "/blog?tag=" + tag} {
		rec := e.do(http.MethodGet, hostA, path, nil, nil)
		out = append(out, strconv.Itoa(rec.Code)+" "+rec.Body.String())
	}
	return out
}

func TestContributorNoPublishSavesPendingChanges(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	c := e.sessionFor(me)
	before := e.publicPages("live", "fresh")
	edit := e.do(http.MethodGet, hostA, "/admin/posts/"+itoa(p.ID)+"/edit", nil, c)
	page := text(edit)
	if edit.Code != http.StatusOK || !strings.Contains(page, "Your changes will be reviewed before they go live.") {
		t.Fatalf("pending editor: %d %s", edit.Code, page)
	}
	if !regexp.MustCompile(`id="slug"[^>]*readonly`).MatchString(page) {
		t.Fatal("slug isn't read-only in the pending editor")
	}
	if !strings.Contains(page, `value="save"`) {
		t.Fatal("pending editor has no save button")
	}
	for _, action := range []string{`value="publish"`, `value="unpublish"`, `value="delete"`} {
		if strings.Contains(page, action) {
			t.Errorf("pending editor offers %s", action)
		}
	}
	f := postForm("Reworded", "live", "save")
	f.Set("tags", "fresh")
	f.Set("base", editorBase(t, edit))
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), f, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("pending save: %d %s", rec.Code, rec.Body.String())
	}
	ps := e.posts.ForSite(e.siteA.ID)
	post, _ := ps.GetByID(p.ID)
	if post.Title != "Title live" {
		t.Fatalf("the post changed: %+v", post)
	}
	rev, err := ps.GetPending(p.ID)
	if err != nil || rev == nil || rev.Title != "Reworded" || !reflect.DeepEqual(rev.Tags, []string{"fresh"}) {
		t.Fatalf("revision = %+v, %v", rev, err)
	}
	if rev.Stale(post) {
		t.Fatal("an untouched post reads as stale after the base went through the form")
	}
	if after := e.publicPages("live", "fresh"); !reflect.DeepEqual(after, before) {
		t.Fatalf("public pages changed:\nbefore %q\nafter  %q", before, after)
	}
	if page := text(e.do(http.MethodGet, hostA, "/admin/posts/"+itoa(p.ID)+"/edit", nil, c)); !strings.Contains(page, "Reworded") {
		t.Fatal("editor doesn't show the pending changes")
	}
	f.Set("title", "Reworded twice")
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), f, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("second pending save: %d", rec.Code)
	}
	if again, _ := ps.GetPending(p.ID); again == nil || again.ID != rev.ID || again.Title != "Reworded twice" {
		t.Fatalf("second save: %+v", again)
	}
}

func TestPendingSaveIgnoresForgedFields(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	c := e.sessionFor(me)
	ps := e.posts.ForSite(e.siteA.ID)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Moved", "moved", "save"), c); rec.Code != http.StatusSeeOther {
		t.Fatalf("pending save with a new slug: %d", rec.Code)
	}
	if moved, _ := ps.GetBySlug("moved"); moved != nil {
		t.Fatal("a pending save changed the slug")
	}
	for _, action := range []string{"publish", "unpublish", "delete"} {
		if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Defaced", "live", action), c); rec.Code != http.StatusForbidden {
			t.Fatalf("%s on a live-once post: %d", action, rec.Code)
		}
	}
	post, _ := ps.GetByID(p.ID)
	if post == nil || post.Title != "Title live" || post.Slug != "live" || !post.Published {
		t.Fatalf("post after forged actions: %+v", post)
	}
	if body := e.do(http.MethodGet, hostA, "/blog/live", nil, nil).Body.String(); !strings.Contains(body, "Title live") || strings.Contains(body, "Defaced") || strings.Contains(body, "Moved") {
		t.Fatalf("public page changed: %s", body)
	}
}

func TestDraftPublishedWhileContributorEdits(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	d := e.authored(me, "d", false)
	ps := e.posts.ForSite(e.siteA.ID)
	c := e.sessionFor(me)
	// The contributor opens the draft in the real editor form.
	edit := e.do(http.MethodGet, hostA, "/admin/posts/"+itoa(d.ID)+"/edit", nil, c)
	if edit.Code != http.StatusOK {
		t.Fatalf("draft editor: %d", edit.Code)
	}
	base := editorBase(t, edit)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(d.ID), postForm("Fixed by admin", "d", "publish"), e.siteAdminCookie()); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin publish: %d", rec.Code)
	}
	f := postForm("Late edit", "d", "save")
	f.Set("base", base)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(d.ID), f, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("late save: %d %s", rec.Code, rec.Body.String())
	}
	post, _ := ps.GetByID(d.ID)
	if !post.Published || post.Title != "Fixed by admin" {
		t.Fatalf("the late save changed the live post: %+v", post)
	}
	rev, _ := ps.GetPending(d.ID)
	if rev == nil || rev.Title != "Late edit" || !rev.Stale(post) {
		t.Fatalf("late save should be a stale revision: %+v", rev)
	}
}

func TestEditorPendingNotices(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	p, _ = e.posts.ForSite(e.siteA.ID).GetByID(p.ID)
	if _, err := e.posts.ForSite(e.siteA.ID).SavePending(p.ID, me.ID, "Pending", "b", nil, p.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	editPath := "/admin/posts/" + itoa(p.ID) + "/edit"
	admin := text(e.do(http.MethodGet, hostA, editPath, nil, e.siteAdminCookie()))
	if !strings.Contains(admin, "This post has pending changes.") || !strings.Contains(admin, "/admin/posts/"+itoa(p.ID)+"/pending") {
		t.Fatalf("admin's editor lacks the pending notice: %s", admin)
	}
	if strings.Contains(admin, `value="Pending"`) {
		t.Fatal("admin's editor shows the pending text instead of the post")
	}
	if err := e.mems.Update(me.ID, e.siteA.ID, models.RoleContributor, true); err != nil {
		t.Fatal(err)
	}
	mine := text(e.do(http.MethodGet, hostA, editPath, nil, e.sessionFor(me)))
	if !strings.Contains(mine, "Your earlier changes are waiting for a site admin.") || strings.Contains(mine, "/pending") {
		t.Fatalf("publishing contributor's editor: %s", mine)
	}
}

// A pending save with no base (an old or hand-made form) can't prove the
// contributor saw the current post, so it must read as stale.
func TestPendingSaveWithoutBaseIsStale(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("No base", "live", "save"), e.sessionFor(me)); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	ps := e.posts.ForSite(e.siteA.ID)
	post, _ := ps.GetByID(p.ID)
	if rev, _ := ps.GetPending(p.ID); rev == nil || !rev.Stale(post) {
		t.Fatalf("a revision saved without a base should be stale: %+v", rev)
	}
}

func TestSaveKeepsPublishedState(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, true)
	p := e.authored(me, "live", true)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("New title", "live", "save"), e.sessionFor(me)); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	post, _ := e.posts.ForSite(e.siteA.ID).GetByID(p.ID)
	if !post.Published || post.Title != "New title" {
		t.Fatalf("after save: %+v", post)
	}
}

func TestContributorNoPublishTagsStayPrivate(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	f := postForm("Draft", "draft", "save")
	f.Set("tags", "secret-tag")
	if rec := e.do(http.MethodPost, hostA, "/admin/posts", f, e.sessionFor(me)); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	if body := e.do(http.MethodGet, hostA, "/blog", nil, nil).Body.String(); strings.Contains(body, "secret-tag") {
		t.Fatalf("a draft's tag is public: %s", body)
	}
}

func TestContributorNoPublishDeletesOwnDraft(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "d", false)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("x", "d", "delete"), e.sessionFor(me)); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete own draft: %d", rec.Code)
	}
	if got, _ := e.posts.ForSite(e.siteA.ID).GetByID(p.ID); got != nil {
		t.Fatal("draft not deleted")
	}
}

func TestContributorWithPublish(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, true)
	c := e.sessionFor(me)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts", postForm("Mine", "mine", "publish"), c); rec.Code != http.StatusSeeOther {
		t.Fatalf("publish on create: %d", rec.Code)
	}
	p, _ := e.posts.ForSite(e.siteA.ID).GetBySlug("mine")
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Mine v2", "mine", "save"), c); rec.Code != http.StatusSeeOther {
		t.Fatalf("editing own live post: %d", rec.Code)
	}
	if body := e.do(http.MethodGet, hostA, "/blog/mine", nil, nil).Body.String(); !strings.Contains(body, "Mine v2") {
		t.Fatalf("public page: %s", body)
	}
}

func TestContributorCannotOpenSettings(t *testing.T) {
	e := newEnv(t)
	c := e.sessionFor(e.member("me@x.io", models.RoleContributor, true))
	if rec := e.do(http.MethodGet, hostA, "/admin/settings", nil, c); rec.Code != http.StatusForbidden {
		t.Fatalf("contributor on settings: %d", rec.Code)
	}
	if body := e.do(http.MethodGet, hostA, "/admin/posts", nil, c).Body.String(); strings.Contains(body, `href="/admin/settings"`) {
		t.Fatal("nav shows settings to a contributor")
	}
}

func TestSiteAdminEditsOthersPosts(t *testing.T) {
	e := newEnv(t)
	writer := e.member("w@x.io", models.RoleContributor, false)
	admin := e.member("a@x.io", models.RoleAdmin, false)
	p := e.authored(writer, "theirs", false)
	c := e.sessionFor(admin)
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Edited by admin", "theirs", "publish"), c); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin publishing a contributor's draft: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := e.posts.ForSite(e.siteA.ID).GetByID(p.ID)
	if !got.Published || got.Title != "Edited by admin" || *got.AuthorID != writer.ID {
		t.Fatalf("post = %+v", got)
	}
}

func TestPostListStatuses(t *testing.T) {
	e := newEnv(t)
	writer := e.member("w@x.io", models.RoleContributor, false)
	e.authored(writer, "drafted", false)
	e.authored(writer, "shown", true)
	gone := e.member("gone@x.io", models.RoleContributor, false)
	e.authored(gone, "orphaned", true)
	un := e.authored(writer, "pulled", true)
	un.Published = false
	if err := e.posts.ForSite(e.siteA.ID).Update(un); err != nil {
		t.Fatal(err)
	}
	if err := e.users.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	body := e.do(http.MethodGet, hostA, "/admin/posts", nil, e.login(hostA)).Body.String()
	for _, want := range []string{">draft<", ">published<", ">unpublished<", "former member", "w@x.io"} {
		if !strings.Contains(body, want) {
			t.Errorf("post list lacks %q", want)
		}
	}
}
