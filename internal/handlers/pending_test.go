package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/models"
)

// pending stores u's pending changes on post (re-read, so the base is the
// stored UpdatedAt) and returns the revision as stored.
func (e *env) pending(siteID int64, u *models.User, post *models.Post, title string, tags ...string) *models.Revision {
	e.t.Helper()
	ps := e.posts.ForSite(siteID)
	p, err := ps.GetByID(post.ID)
	if err != nil || p == nil {
		e.t.Fatalf("GetByID: %v %v", p, err)
	}
	if _, err := ps.SavePending(p.ID, u.ID, title, "pending body", tags, p.UpdatedAt); err != nil {
		e.t.Fatal(err)
	}
	rev, err := ps.GetPending(p.ID)
	if err != nil || rev == nil {
		e.t.Fatalf("GetPending: %v %v", rev, err)
	}
	return rev
}

func revisionAt(r *models.Revision) string { return r.UpdatedAt.Format(time.RFC3339Nano) }

func pendingPath(p *models.Post, action string) string {
	path := "/admin/posts/" + itoa(p.ID) + "/pending"
	if action != "" {
		path += "/" + action
	}
	return path
}

// revisionForm is the publish or discard form for r; confirm, if set, is
// the "publish anyway" value from the page (see pageConfirm).
func revisionForm(r *models.Revision, confirm string) url.Values {
	f := url.Values{"revision": {revisionAt(r)}}
	if confirm != "" {
		f.Set("confirm", confirm)
	}
	return f
}

var confirmRe = regexp.MustCompile(`name="confirm" value="([^"]*)"`)

// pageConfirm is the "publish anyway" value a review page carries.
func pageConfirm(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	m := confirmRe.FindStringSubmatch(text(rec))
	if m == nil {
		t.Fatalf("page has no publish anyway: %s", rec.Body.String())
	}
	return m[1]
}

func TestAdminPublishesPendingChanges(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	rev := e.pending(e.siteA.ID, me, p, "Reworded", "fresh")
	admin := e.siteAdminCookie()
	page := e.do(http.MethodGet, hostA, pendingPath(p, ""), nil, admin)
	body := text(page)
	if page.Code != http.StatusOK || !strings.Contains(body, "Title live") || !strings.Contains(body, "Reworded") || !strings.Contains(body, "publish changes") {
		t.Fatalf("pending page: %d %s", page.Code, body)
	}
	if strings.Contains(body, "edited after these changes were started") {
		t.Fatal("untouched post shows the stale warning")
	}
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, ""), admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("publish changes: %d %s", rec.Code, text(rec))
	}
	if body := e.do(http.MethodGet, hostA, "/blog/live", nil, nil).Body.String(); !strings.Contains(body, "Reworded") {
		t.Fatalf("public page after publishing changes: %s", body)
	}
	if body := e.do(http.MethodGet, hostA, "/blog?tag=fresh", nil, nil).Body.String(); !strings.Contains(body, "Reworded") {
		t.Fatalf("tag page after publishing changes: %s", body)
	}
	ps := e.posts.ForSite(e.siteA.ID)
	post, _ := ps.GetByID(p.ID)
	if !post.Published || post.Slug != "live" || !reflect.DeepEqual(post.Tags, []string{"fresh"}) {
		t.Fatalf("post after publishing changes: %+v", post)
	}
	if r, _ := ps.GetPending(p.ID); r != nil {
		t.Fatal("revision survived publishing")
	}
	if list := text(e.do(http.MethodGet, hostA, "/admin/posts", nil, admin)); strings.Contains(list, "pending changes") {
		t.Fatal("list still shows pending changes")
	}
}

func TestPublishChangesKeepsUnpublished(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	p.Published = false
	if err := e.posts.ForSite(e.siteA.ID).Update(p); err != nil {
		t.Fatal(err)
	}
	rev := e.pending(e.siteA.ID, me, p, "Reworded")
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, ""), e.siteAdminCookie()); rec.Code != http.StatusSeeOther {
		t.Fatalf("publish changes: %d", rec.Code)
	}
	post, _ := e.posts.ForSite(e.siteA.ID).GetByID(p.ID)
	if post.Published || post.Title != "Reworded" {
		t.Fatalf("post = %+v", post)
	}
	if rec := e.do(http.MethodGet, hostA, "/blog/live", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unpublished post is public: %d", rec.Code)
	}
}

func TestStaleChangesNeedConfirmation(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	rev := e.pending(e.siteA.ID, me, p, "Reworded")
	admin := e.siteAdminCookie()
	if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm("Admin edit", "live", "save"), admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin edit: %d", rec.Code)
	}
	rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, ""), admin)
	body := text(rec)
	if rec.Code != http.StatusConflict || !strings.Contains(body, "This post was edited after these changes were started. Publishing them will overwrite those edits.") || !strings.Contains(body, "publish anyway") {
		t.Fatalf("stale publish without confirmation: %d %s", rec.Code, body)
	}
	ps := e.posts.ForSite(e.siteA.ID)
	if post, _ := ps.GetByID(p.ID); post.Title != "Admin edit" {
		t.Fatalf("refused publish changed the post: %+v", post)
	}
	if r, _ := ps.GetPending(p.ID); r == nil {
		t.Fatal("refused publish removed the revision")
	}
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, pageConfirm(t, rec)), admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirmed publish: %d %s", rec.Code, text(rec))
	}
	if post, _ := ps.GetByID(p.ID); post.Title != "Reworded" {
		t.Fatalf("confirmed publish didn't apply: %+v", post)
	}
}

func TestPublishChangesRefusesNewerRevision(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	seen := e.pending(e.siteA.ID, me, p, "First")
	admin := e.siteAdminCookie()
	if rec := e.do(http.MethodGet, hostA, pendingPath(p, ""), nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("pending page: %d", rec.Code)
	}
	e.pending(e.siteA.ID, me, p, "Second")
	rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(seen, ""), admin)
	body := text(rec)
	if rec.Code != http.StatusConflict || !strings.Contains(body, "saved again") || !strings.Contains(body, "Second") {
		t.Fatalf("publishing an outdated view: %d %s", rec.Code, body)
	}
	ps := e.posts.ForSite(e.siteA.ID)
	if post, _ := ps.GetByID(p.ID); post.Title != "Title live" {
		t.Fatalf("outdated publish changed the post: %+v", post)
	}
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "discard"), revisionForm(seen, ""), admin); rec.Code != http.StatusConflict {
		t.Fatalf("discarding an outdated view: %d", rec.Code)
	}
	if r, _ := ps.GetPending(p.ID); r == nil || r.Title != "Second" {
		t.Fatalf("revision after outdated discard: %+v", r)
	}
}

func TestDiscardPendingChanges(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	rev := e.pending(e.siteA.ID, me, p, "Reworded")
	admin := e.siteAdminCookie()
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "discard"), revisionForm(rev, ""), admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("discard: %d %s", rec.Code, text(rec))
	}
	ps := e.posts.ForSite(e.siteA.ID)
	if r, _ := ps.GetPending(p.ID); r != nil {
		t.Fatal("discard left the revision")
	}
	if post, _ := ps.GetByID(p.ID); post.Title != "Title live" {
		t.Fatalf("discard changed the post: %+v", post)
	}
	rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, ""), admin)
	if rec.Code != http.StatusConflict || !strings.Contains(text(rec), "These changes were already published or discarded.") {
		t.Fatalf("publishing discarded changes: %d %s", rec.Code, text(rec))
	}
	if rec := e.do(http.MethodGet, hostA, pendingPath(p, ""), nil, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("pending page with no changes: %d", rec.Code)
	}
}

func TestBadRevisionValue(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	e.pending(e.siteA.ID, me, p, "Reworded")
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), url.Values{"revision": {"yesterday"}}, e.siteAdminCookie()); rec.Code != http.StatusBadRequest {
		t.Fatalf("unparseable revision: %d", rec.Code)
	}
}

func TestContributorCannotReviewChanges(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	other := e.member("other@x.io", models.RoleContributor, false)
	mine := e.authored(me, "mine", true)
	theirs := e.authored(other, "theirs", true)
	myRev := e.pending(e.siteA.ID, me, mine, "Mine reworded")
	theirRev := e.pending(e.siteA.ID, other, theirs, "Theirs reworded")
	c := e.sessionFor(me)
	for _, tc := range []struct {
		post *models.Post
		rev  *models.Revision
		want int
	}{{mine, myRev, http.StatusForbidden}, {theirs, theirRev, http.StatusNotFound}} {
		if rec := e.do(http.MethodGet, hostA, pendingPath(tc.post, ""), nil, c); rec.Code != tc.want {
			t.Errorf("GET pending on %s: %d, want %d", tc.post.Slug, rec.Code, tc.want)
		}
		for _, action := range []string{"publish", "discard"} {
			if rec := e.do(http.MethodPost, hostA, pendingPath(tc.post, action), revisionForm(tc.rev, ""), c); rec.Code != tc.want {
				t.Errorf("%s on %s: %d, want %d", action, tc.post.Slug, rec.Code, tc.want)
			}
		}
		if r, _ := e.posts.ForSite(e.siteA.ID).GetPending(tc.post.ID); r == nil || !r.UpdatedAt.Equal(tc.rev.UpdatedAt) {
			t.Errorf("revision on %s changed: %+v", tc.post.Slug, r)
		}
	}
}

func TestPendingChangesSiteIsolation(t *testing.T) {
	e := newEnv(t)
	b := &models.Site{Key: "b", Hostname: "b.example.com", Theme: "t1"}
	if err := e.sites.Create(b, platformHost); err != nil {
		t.Fatal(err)
	}
	writer, err := e.people.AddMember(b.ID, "bwriter@x.io", models.RoleContributor, false)
	if err != nil {
		t.Fatal(err)
	}
	bp := &models.Post{Slug: "on-b", Title: "On B", Body: "b", Published: true, AuthorID: &writer.ID}
	if err := e.posts.ForSite(b.ID).Create(bp); err != nil {
		t.Fatal(err)
	}
	rev := e.pending(b.ID, writer, bp, "B reworded")
	admin := e.siteAdminCookie()
	if rec := e.do(http.MethodGet, hostA, pendingPath(bp, ""), nil, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("site A viewing site B's changes: %d", rec.Code)
	}
	for _, action := range []string{"publish", "discard"} {
		if rec := e.do(http.MethodPost, hostA, pendingPath(bp, action), revisionForm(rev, ""), admin); rec.Code != http.StatusNotFound {
			t.Fatalf("site A %s on site B's changes: %d", action, rec.Code)
		}
	}
	ps := e.posts.ForSite(b.ID)
	if r, _ := ps.GetPending(bp.ID); r == nil || r.Title != "B reworded" {
		t.Fatalf("site B's revision changed: %+v", r)
	}
	if post, _ := ps.GetByID(bp.ID); post.Title != "On B" {
		t.Fatalf("site B's post changed: %+v", post)
	}
}

func TestPendingPageFormerMember(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	e.pending(e.siteA.ID, me, p, "Reworded")
	if err := e.users.Delete(me.ID); err != nil {
		t.Fatal(err)
	}
	admin := e.siteAdminCookie()
	page := e.do(http.MethodGet, hostA, pendingPath(p, ""), nil, admin)
	if page.Code != http.StatusOK || !strings.Contains(text(page), "former member") {
		t.Fatalf("pending page by a deleted author: %d %s", page.Code, text(page))
	}
	rev, _ := e.posts.ForSite(e.siteA.ID).GetPending(p.ID)
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, ""), admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("publishing a former member's changes: %d", rec.Code)
	}
}

func TestPostListShowsPendingChanges(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	e.pending(e.siteA.ID, me, p, "Reworded")
	admin := text(e.do(http.MethodGet, hostA, "/admin/posts", nil, e.siteAdminCookie()))
	if !strings.Contains(admin, "pending changes") || !strings.Contains(admin, pendingPath(p, "")) {
		t.Fatalf("admin's list: %s", admin)
	}
	mine := text(e.do(http.MethodGet, hostA, "/admin/posts", nil, e.sessionFor(me)))
	if !strings.Contains(mine, "pending changes") || strings.Contains(mine, "/pending") {
		t.Fatalf("contributor's list: %s", mine)
	}
}

func TestPublishAnywayRefusesNewerEdit(t *testing.T) {
	e := newEnv(t)
	me := e.member("me@x.io", models.RoleContributor, false)
	p := e.authored(me, "live", true)
	rev := e.pending(e.siteA.ID, me, p, "Reworded")
	admin := e.siteAdminCookie()
	save := func(title string) {
		t.Helper()
		if rec := e.do(http.MethodPost, hostA, "/admin/posts/"+itoa(p.ID), postForm(title, "live", "save"), admin); rec.Code != http.StatusSeeOther {
			t.Fatalf("admin edit: %d", rec.Code)
		}
	}
	save("Admin edit")
	page := e.do(http.MethodGet, hostA, pendingPath(p, ""), nil, admin)
	confirm := pageConfirm(t, page)
	save("Second edit")

	rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, confirm), admin)
	body := text(rec)
	if rec.Code != http.StatusConflict || !strings.Contains(body, "The post was edited again since you opened this page.") || !strings.Contains(body, "Second edit") {
		t.Fatalf("publish anyway over a newer edit: %d %s", rec.Code, body)
	}
	ps := e.posts.ForSite(e.siteA.ID)
	if post, _ := ps.GetByID(p.ID); post.Title != "Second edit" {
		t.Fatalf("the newer edit was overwritten: %+v", post)
	}
	if rec := e.do(http.MethodPost, hostA, pendingPath(p, "publish"), revisionForm(rev, pageConfirm(t, rec)), admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("publish anyway from the refreshed page: %d %s", rec.Code, text(rec))
	}
	if post, _ := ps.GetByID(p.ID); post.Title != "Reworded" {
		t.Fatalf("publish anyway didn't apply: %+v", post)
	}
}
