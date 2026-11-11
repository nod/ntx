package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/secretbox"
)

// Adding a membership from the platform users page cancels the person's
// pending invite to that site, as the site's members page does.
func TestPlatformAddMembershipCancelsInvite(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	u, err := e.users.FindOrCreateByEmail("m@x.io")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := e.invites.Create(e.siteA.ID, "m@x.io", models.RoleContributor, false, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/platform/users/" + itoa(u.ID) + "/memberships"
	if rec := e.do(http.MethodPost, platformHost, path, url.Values{"site": {e.siteA.Key}, "role": {"admin"}}, e.login(platformHost)); rec.Code != http.StatusSeeOther {
		t.Fatalf("add membership: %d %s", rec.Code, rec.Body.String())
	}
	if p, _ := e.invites.ListPending(e.siteA.ID, now); len(p) != 0 {
		t.Fatalf("pending invites after the add: %+v", p)
	}
	if rec := e.do(http.MethodGet, hostA, "/invite/"+token, nil, nil); rec.Code == http.StatusOK {
		t.Error("the invite link still works")
	}
}

func siteForm(s *models.Site, extra url.Values) url.Values {
	f := url.Values{
		"hostname": {s.Hostname}, "name": {s.Name}, "description": {s.Description},
		"about_slug": {s.AboutSlug}, "theme": {s.Theme},
		"provider_type": {s.ProviderType}, "provider_domain": {s.ProviderDomain}, "provider_client_id": {s.ProviderClientID},
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestPlatformProviderSaveAndClear(t *testing.T) {
	e := newEnv(t)
	root := e.login(platformHost)
	f := siteForm(e.siteA, url.Values{
		"provider_type": {"auth0"}, "provider_domain": {"acme.us.auth0.com"},
		"provider_client_id": {"cid"}, "provider_client_secret": {"s3cret"},
	})
	if rec := e.do(http.MethodPost, platformHost, "/admin/platform/sites/a", f, root); rec.Code != http.StatusSeeOther {
		t.Fatalf("save provider: %d %s", rec.Code, rec.Body.String())
	}
	stored := e.storedSite("a")
	sealed, _ := e.sites.ProviderSecret(stored.ID)
	if plain, err := secretbox.Open(testEncKey, sealed); err != nil || plain != "s3cret" {
		t.Fatalf("stored secret = %q, %v", plain, err)
	}
	page := e.do(http.MethodGet, platformHost, "/admin/platform/sites/a", nil, root).Body.String()
	if !strings.Contains(page, "A client secret is stored.") || strings.Contains(page, "s3cret") {
		t.Fatalf("edit page: %s", page)
	}

	f.Set("provider_domain", "evil.example.com")
	f.Set("provider_client_secret", "")
	rec := e.do(http.MethodPost, platformHost, "/admin/platform/sites/a", f, root)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "enter the client secret again") {
		t.Fatalf("domain change without secret: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.storedSite("a"); got.ProviderDomain != "acme.us.auth0.com" {
		t.Fatalf("domain = %q, want unchanged", got.ProviderDomain)
	}
}

func TestPlatformSiteFormShowsCallbackURL(t *testing.T) {
	e := newEnv(t)
	page := e.do(http.MethodGet, platformHost, "/admin/platform/sites/a", nil, e.login(platformHost)).Body.String()
	if !strings.Contains(page, "https://a.example.com/auth/callback") {
		t.Fatalf("edit page lacks the site's callback URL: %s", page)
	}
}

func TestPlatformSitesListShowsProviderAndMembers(t *testing.T) {
	e := newEnv(t)
	e.member("m@x.io", models.RoleContributor, false)
	page := e.do(http.MethodGet, platformHost, "/admin/platform/sites", nil, e.login(platformHost)).Body.String()
	if !strings.Contains(page, "Auth0") || !strings.Contains(page, "<td>1</td>") {
		t.Fatalf("sites list: %s", page)
	}
}

func TestPlatformUsersList(t *testing.T) {
	e := newEnv(t)
	e.member("m@x.io", models.RoleContributor, false)
	page := e.do(http.MethodGet, platformHost, "/admin/platform/users", nil, e.login(platformHost)).Body.String()
	for _, want := range []string{"root", "m@x.io", "a: contributor"} {
		if !strings.Contains(page, want) {
			t.Errorf("users list lacks %q", want)
		}
	}
}

func userForm(email string, platformAdmin bool) url.Values {
	f := url.Values{"email": {email}}
	if platformAdmin {
		f.Set("platform_admin", "on")
	}
	return f
}

func TestGrantPlatformAdmin(t *testing.T) {
	e := newEnv(t)
	u := e.member("m@x.io", models.RoleContributor, false)
	uc := e.sessionFor(u)
	if rec := e.do(http.MethodGet, hostA, "/admin/platform/sites", nil, uc); rec.Code != http.StatusForbidden {
		t.Fatalf("before the grant: %d", rec.Code)
	}
	root := e.login(platformHost)
	if rec := e.do(http.MethodPost, platformHost, "/admin/platform/users/"+itoa(u.ID), userForm(u.Email, true), root); rec.Code != http.StatusSeeOther {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/platform/sites", nil, uc); rec.Code != http.StatusOK {
		t.Fatalf("after the grant: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, platformHost, "/admin/platform/users/"+itoa(u.ID), userForm(u.Email, false), root); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/platform/sites", nil, uc); rec.Code != http.StatusForbidden {
		t.Fatalf("after revoking: %d", rec.Code)
	}
}

func TestPlatformAdminCanRevokeSelf(t *testing.T) {
	e := newEnv(t)
	p := e.member("p@x.io", models.RoleContributor, false)
	if err := e.users.SetPlatformAdmin(p.ID, true); err != nil {
		t.Fatal(err)
	}
	pc := e.sessionFor(p)
	if rec := e.do(http.MethodPost, hostA, "/admin/platform/users/"+itoa(p.ID), userForm(p.Email, false), pc); rec.Code != http.StatusSeeOther {
		t.Fatalf("self-revoke: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/platform/sites", nil, pc); rec.Code != http.StatusForbidden {
		t.Fatalf("after self-revoke: %d", rec.Code)
	}
}

func TestRootUserHasNoControls(t *testing.T) {
	e := newEnv(t)
	root := e.login(platformHost)
	all, _ := e.users.List()
	rootID := itoa(all[0].ID)
	page := e.do(http.MethodGet, platformHost, "/admin/platform/users/"+rootID, nil, root).Body.String()
	if strings.Contains(page, `action="/admin/platform/users/`) {
		t.Fatalf("root's page has controls: %s", page)
	}
	base := "/admin/platform/users/" + rootID
	for path, form := range map[string]url.Values{
		base:                           userForm("root@x.io", false),
		base + "/memberships":          {"site": {"a"}, "role": {"admin"}},
		base + "/memberships/a/remove": nil,
		base + "/identities/1/unlink":  nil,
		base + "/revoke":               nil,
		base + "/delete":               nil,
	} {
		if rec := e.do(http.MethodPost, platformHost, path, form, root); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s on root: %d", path, rec.Code)
		}
	}
	if got, _ := e.users.GetByID(all[0].ID); got == nil || !got.IsRoot || got.Email != "" {
		t.Fatalf("root changed: %+v", got)
	}
	if rec := e.do(http.MethodGet, platformHost, "/admin/posts", nil, root); rec.Code != http.StatusNotFound {
		t.Fatalf("root session broken: %d", rec.Code)
	}
}

func TestUnlinkIdentity(t *testing.T) {
	e := newEnv(t)
	u := e.member("m@x.io", models.RoleContributor, false)
	if rec, _ := e.ssoLogin(hostA, verified("s1", "m@x.io"), ""); rec.Code != http.StatusOK {
		t.Fatalf("first sign-in: %d", rec.Code)
	}
	ids, _ := e.ids.ListForUser(u.ID)
	if len(ids) != 1 {
		t.Fatalf("identities = %+v", ids)
	}
	root := e.login(platformHost)
	path := "/admin/platform/users/" + itoa(u.ID) + "/identities/" + itoa(ids[0].ID) + "/unlink"
	if rec := e.do(http.MethodPost, platformHost, path, nil, root); rec.Code != http.StatusSeeOther {
		t.Fatalf("unlink: %d", rec.Code)
	}
	if ids, _ := e.ids.ListForUser(u.ID); len(ids) != 0 {
		t.Fatalf("identities after unlink = %+v", ids)
	}
	if rec, _ := e.ssoLogin(hostA, verified("s1", "m@x.io"), ""); rec.Code != http.StatusOK {
		t.Fatalf("sign-in after unlink: %d", rec.Code)
	}
	if ids, _ := e.ids.ListForUser(u.ID); len(ids) != 1 {
		t.Fatalf("identity not re-linked: %+v", ids)
	}
}

func TestRevokeSessions(t *testing.T) {
	e := newEnv(t)
	u := e.member("m@x.io", models.RoleContributor, false)
	uc := e.sessionFor(u)
	if rec := e.do(http.MethodPost, platformHost, "/admin/platform/users/"+itoa(u.ID)+"/revoke", nil, e.login(platformHost)); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, hostA, "/admin/posts", nil, uc); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Fatalf("revoked session: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestDeleteUserKeepsTheirPosts(t *testing.T) {
	e := newEnv(t)
	u := e.member("m@x.io", models.RoleContributor, false)
	e.authored(u, "kept", true)
	if rec := e.do(http.MethodPost, platformHost, "/admin/platform/users/"+itoa(u.ID)+"/delete", nil, e.login(platformHost)); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", rec.Code)
	}
	if got, _ := e.users.GetByID(u.ID); got != nil {
		t.Fatal("user not deleted")
	}
	if body := e.do(http.MethodGet, hostA, "/blog/kept", nil, nil).Body.String(); !strings.Contains(body, "Title kept") {
		t.Fatalf("public post gone: %s", body)
	}
	if body := e.do(http.MethodGet, hostA, "/admin/posts", nil, e.login(hostA)).Body.String(); !strings.Contains(body, "former member") {
		t.Fatal("admin list doesn't show former member")
	}
}

func TestPlatformAddMembershipOnOtherSite(t *testing.T) {
	e := newEnv(t)
	b := &models.Site{Key: "b", Hostname: "b.example.com", Theme: "t1"}
	if err := e.sites.Create(b, platformHost); err != nil {
		t.Fatal(err)
	}
	sealed, _ := secretbox.Seal(testEncKey, e.fake.ClientSecret)
	if err := e.sites.SetProvider(b.ID, models.ProviderSettings{Type: models.ProviderGoogle, ClientID: e.fake.ClientID}, sealed); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.Reload(); err != nil {
		t.Fatal(err)
	}
	u := e.member("m@x.io", models.RoleContributor, false)
	root := e.login(platformHost)
	path := "/admin/platform/users/" + itoa(u.ID) + "/memberships"
	if rec := e.do(http.MethodPost, platformHost, path, url.Values{"site": {"b"}, "role": {"admin"}}, root); rec.Code != http.StatusSeeOther {
		t.Fatalf("add membership: %d %s", rec.Code, rec.Body.String())
	}
	if rec, sess := e.ssoLogin("b.example.com", verified("s1", "m@x.io"), ""); rec.Code != http.StatusOK || sess == nil {
		t.Fatalf("sign-in on B: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodPost, platformHost, path+"/b/remove", nil, root); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove membership: %d", rec.Code)
	}
	if m, _ := e.mems.Get(u.ID, b.ID); m != nil {
		t.Fatal("membership not removed")
	}
}
