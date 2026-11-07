package handlers

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/models"
)

var inviteLinkRe = regexp.MustCompile(`http://a\.example\.com/invite/([A-Za-z0-9_-]{20,})`)

func (e *env) siteAdminCookie() *http.Cookie {
	e.t.Helper()
	return e.sessionFor(e.member("boss@x.io", models.RoleAdmin, false))
}

func TestMembersAddPerson(t *testing.T) {
	e := newEnv(t)
	c := e.siteAdminCookie()
	rec := e.do(http.MethodPost, hostA, "/admin/members", url.Values{"email": {"New@X.io"}, "role": {"contributor"}}, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	if body := e.do(http.MethodGet, hostA, "/admin/members", nil, c).Body.String(); !strings.Contains(body, "new@x.io") {
		t.Fatalf("members page lacks new@x.io: %s", body)
	}
	if rec, sess := e.ssoLogin(hostA, verified("n1", "new@x.io"), ""); rec.Code != http.StatusOK || sess == nil {
		t.Fatalf("first sign-in of an added person: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMembersAddDuplicate(t *testing.T) {
	e := newEnv(t)
	c := e.siteAdminCookie()
	e.member("dup@x.io", models.RoleContributor, false)
	rec := e.do(http.MethodPost, hostA, "/admin/members", url.Values{"email": {"DUP@x.io"}, "role": {"admin"}}, c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already a member") {
		t.Fatalf("duplicate add: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMembersInviteShowsLinkOnce(t *testing.T) {
	e := newEnv(t)
	c := e.siteAdminCookie()
	rec := e.do(http.MethodPost, hostA, "/admin/members/invite", url.Values{"email": {"inv@x.io"}, "role": {"contributor"}, "can_publish": {"on"}}, c)
	m := inviteLinkRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body.String())
	}
	token := m[1]
	inv, _ := e.invites.Lookup(token, e.siteA.ID, time.Now())
	if inv == nil || !inv.CanPublish || inv.Role != models.RoleContributor {
		t.Fatalf("invite = %+v", inv)
	}
	page := e.do(http.MethodGet, hostA, "/admin/members", nil, c).Body.String()
	if !strings.Contains(page, "inv@x.io") || strings.Contains(page, token) {
		t.Fatalf("members page must list the invite but not its token: %s", page)
	}
	regen := e.do(http.MethodPost, hostA, "/admin/invites/"+itoa(inv.ID)+"/regenerate", nil, c)
	m2 := inviteLinkRe.FindStringSubmatch(regen.Body.String())
	if regen.Code != http.StatusOK || m2 == nil || m2[1] == token {
		t.Fatalf("regenerate: %d %s", regen.Code, regen.Body.String())
	}
	if old := e.do(http.MethodGet, hostA, "/invite/"+token, nil, nil); old.Code != http.StatusNotFound {
		t.Fatalf("old link after regenerate: %d", old.Code)
	}
}

func TestMembersCancelInvite(t *testing.T) {
	e := newEnv(t)
	c := e.siteAdminCookie()
	inv, token, err := e.invites.Create(e.siteA.ID, "inv@x.io", models.RoleContributor, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/invites/"+itoa(inv.ID)+"/cancel", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if got, _ := e.invites.Lookup(token, e.siteA.ID, time.Now()); got != nil {
		t.Fatal("cancelled invite still works")
	}
}

func TestMembersChangeRoleTakesEffect(t *testing.T) {
	e := newEnv(t)
	c := e.siteAdminCookie()
	other := e.member("other-admin@x.io", models.RoleAdmin, false)
	oc := e.sessionFor(other)
	if rec := e.do(http.MethodGet, hostA, "/admin/settings", nil, oc); rec.Code != http.StatusOK {
		t.Fatalf("admin on settings: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/members/"+itoa(other.ID), url.Values{"role": {"contributor"}}, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/settings", nil, oc); rec.Code != http.StatusForbidden {
		t.Fatalf("demoted admin on settings: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/members/"+itoa(other.ID)+"/remove", nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/posts", nil, oc); rec.Code != http.StatusSeeOther {
		t.Fatalf("removed member on posts: %d, want a redirect to sign in", rec.Code)
	}
}

func TestSiteAdminCannotTouchPlatformAdmin(t *testing.T) {
	e := newEnv(t)
	c := e.siteAdminCookie()
	p := e.member("plat@x.io", models.RoleContributor, false)
	if err := e.users.SetPlatformAdmin(p.ID, true); err != nil {
		t.Fatal(err)
	}
	base := "/admin/members/" + itoa(p.ID)
	if rec := e.do(http.MethodPost, hostA, base, url.Values{"role": {"admin"}}, c); rec.Code != http.StatusForbidden {
		t.Fatalf("site admin changing a platform admin: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, hostA, base+"/remove", nil, c); rec.Code != http.StatusForbidden {
		t.Fatalf("site admin removing a platform admin: %d", rec.Code)
	}
	if m, _ := e.mems.Get(p.ID, e.siteA.ID); m == nil || m.Role != models.RoleContributor {
		t.Fatalf("membership changed: %+v", m)
	}
	if page := e.do(http.MethodGet, hostA, "/admin/members", nil, c).Body.String(); strings.Contains(page, `action="`+base) {
		t.Fatal("members page offers controls for a platform admin to a site admin")
	}
	if rec := e.do(http.MethodPost, hostA, base, url.Values{"role": {"admin"}}, e.login(hostA)); rec.Code != http.StatusSeeOther {
		t.Fatalf("root changing a platform admin: %d", rec.Code)
	}
}

func TestMembersOtherSiteIDs(t *testing.T) {
	e := newEnv(t)
	b := &models.Site{Key: "b", Hostname: "b.example.com", Theme: "t1"}
	if err := e.sites.Create(b, platformHost); err != nil {
		t.Fatal(err)
	}
	inv, token, err := e.invites.Create(b.ID, "b@x.io", models.RoleContributor, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := e.siteAdminCookie()
	for _, path := range []string{"/admin/invites/" + itoa(inv.ID) + "/cancel", "/admin/invites/" + itoa(inv.ID) + "/regenerate"} {
		if rec := e.do(http.MethodPost, hostA, path, nil, c); rec.Code != http.StatusNotFound {
			t.Fatalf("%s with site B's invite: %d", path, rec.Code)
		}
	}
	if got, _ := e.invites.Lookup(token, b.ID, time.Now()); got == nil {
		t.Fatal("site B's invite was touched from site A")
	}
	bm, err := e.people.AddMember(b.ID, "bonly@x.io", models.RoleContributor, false)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/members/"+itoa(bm.ID)+"/remove", nil, c); rec.Code != http.StatusNotFound {
		t.Fatalf("removing a site-B-only member from site A: %d", rec.Code)
	}
}

func TestContributorCannotOpenMembers(t *testing.T) {
	e := newEnv(t)
	c := e.sessionFor(e.member("c@x.io", models.RoleContributor, true))
	if rec := e.do(http.MethodGet, hostA, "/admin/members", nil, c); rec.Code != http.StatusForbidden {
		t.Fatalf("GET members: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, hostA, "/admin/members", url.Values{"email": {"x@x.io"}, "role": {"admin"}}, c); rec.Code != http.StatusForbidden {
		t.Fatalf("POST members: %d", rec.Code)
	}
}
