package handlers

import (
	"net/http"
	"net/url"
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

func TestContributorNoPublishLiveOnceReadOnly(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	c := e.sessionFor(me)
	page := e.do(http.MethodGet, hostA, "/admin/posts/"+itoa(p.ID)+"/edit", nil, c).Body.String()
	if !strings.Contains(page, "readonly") {
		t.Fatal("live-once post's editor isn't read-only for a contributor without can_publish")
	}
	for _, action := range []string{`value="publish"`, `value="unpublish"`, `value="delete"`, `value="save"`} {
		if strings.Contains(page, action) {
			t.Errorf("read-only editor offers %s", action)
		}
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Defaced", "live", "save"), c); rec.Code != http.StatusForbidden {
		t.Fatalf("saving a live-once post: %d", rec.Code)
	}
	for _, action := range []string{"unpublish", "delete"} {
		if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Title live", "live", action), c); rec.Code != http.StatusForbidden {
			t.Fatalf("%s on a live-once post: %d", action, rec.Code)
		}
	}
	if body := e.do(http.MethodGet, hostA, "/blog/live", nil, nil).Body.String(); !strings.Contains(body, "Title live") || strings.Contains(body, "Defaced") {
		t.Fatalf("public page changed: %s", body)
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
