package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/db/dbtest"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/people"
	"github.com/nod/ntx/internal/secretbox"
	"github.com/nod/ntx/internal/sitectx"
	"github.com/nod/ntx/internal/sitereg"
	"github.com/nod/ntx/internal/sso"
	"github.com/nod/ntx/internal/sso/ssotest"
)

var (
	testEncKey        = []byte("0123456789abcdef0123456789abcdef")
	testSessionSecret = []byte("session-secret-session-secret-xx")
)

const platformHost = "admin.example.com"

// writeTheme lays down a theme named name under root: the minimum template
// set public pages and Generate use, a section page, and one static file.
// The post template renders the title, so output can be told apart per site.
func writeTheme(t *testing.T, root, name string) {
	t.Helper()
	write := func(path, content string) {
		full := filepath.Join(root, name, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("templates/base.html", `<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>`)
	write("templates/home.html", `{{template "base.html" .}}{{define "content"}}home of {{.SiteName}}{{end}}`)
	write("templates/blog/index.html", `{{template "base.html" .}}{{define "content"}}{{range .Posts}}{{.Title}} {{end}}tags:{{range .AllTags}}{{.}} {{end}}{{end}}`)
	write("templates/blog/post.html", `{{template "base.html" .}}{{define "content"}}{{.Post.Title}}{{end}}`)
	write("templates/sections/projects/one/index.html", `{{template "base.html" .}}{{define "content"}}project {{.Title}}{{end}}`)
	write("static/css/x.css", `body{}`)
}

type env struct {
	t        *testing.T
	h        *Handler
	reg      *sitereg.Registry
	sites    *models.SiteStore
	posts    *models.PostStore
	users    *models.UserStore
	mems     *models.MembershipStore
	ids      *models.IdentityStore
	invites  *models.InviteStore
	sessions *models.SessionStore
	people   *people.Service
	am       *auth.Manager
	fake     *ssotest.Provider
	themes   string
	router   http.Handler
	siteA    *models.Site
	rootPass string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)

	themesRoot := t.TempDir()
	writeTheme(t, themesRoot, "t1")

	users := models.NewUserStore(d)
	if _, err := users.EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	sites := models.NewSiteStore(d)
	a := &models.Site{Key: "a", Hostname: "a.example.com", Name: "Site A", Theme: "t1"}
	if err := sites.Create(a, platformHost); err != nil {
		t.Fatal(err)
	}
	// Site A signs in through a fake Auth0 tenant.
	fake := ssotest.New(t)
	sealed, err := secretbox.Seal(testEncKey, fake.ClientSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := sites.SetProvider(a.ID, models.ProviderSettings{Type: models.ProviderAuth0, Domain: "a-tenant.example.com", ClientID: fake.ClientID}, sealed); err != nil {
		t.Fatal(err)
	}
	reg, err := sitereg.New(sites)
	if err != nil {
		t.Fatal(err)
	}
	a = reg.ByHost("a.example.com")
	mems, ids, invites, sessions := models.NewMembershipStore(d), models.NewIdentityStore(d), models.NewInviteStore(d), models.NewSessionStore(d)
	ppl := people.New(users, ids, mems, invites)
	rootHash, _ := auth.HashPassword("rootpass")
	am := auth.NewManager(sessions, users, mems, rootHash, false)
	h := New(Deps{
		Sites: sites, Posts: models.NewPostStore(d), Users: users, Registry: reg,
		Memberships: mems, Identities: ids, Invites: invites, Sessions: sessions, People: ppl,
		SSO: sso.NewClient(nil), IssuerFor: func(string, string) string { return fake.URL },
		SessionSecret: testSessionSecret, EncryptionKey: testEncKey,
		Auth: am, Limiter: auth.NewLimiter(), ThemesPath: themesRoot,
		PlatformHostname: platformHost, RootPasswordHash: rootHash, Dev: true,
	})

	// A cut-down version of server's host resolution, so these tests drive
	// the real route table and Require wrappers.
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if s := reg.ByHost(req.Host); s != nil {
				req = sitectx.With(req, s)
			} else if req.Host == platformHost {
				req = sitectx.WithPlatform(req)
			} else {
				http.NotFound(w, req)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Use(am.Middleware(func(req *http.Request) (int64, bool) {
		if s := sitectx.From(req); s != nil {
			return s.ID, true
		}
		return 0, false
	}))
	h.Mount(r)

	return &env{
		t: t, h: h, reg: reg, sites: sites, posts: models.NewPostStore(d), users: users,
		mems: mems, ids: ids, invites: invites, sessions: sessions, people: ppl, am: am, fake: fake,
		themes: themesRoot, router: r, siteA: a, rootPass: "rootpass",
	}
}

// do sends one request to the router on host, carrying cookie if set.
func (e *env) do(method, host, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	if cookie == nil {
		return e.doWith(method, host, path, form)
	}
	return e.doWith(method, host, path, form, cookie)
}

// doWith is do with any number of cookies.
func (e *env) doWith(method, host, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Host = host
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// login signs root in on host and returns the session cookie.
func (e *env) login(host string) *http.Cookie {
	e.t.Helper()
	rec := e.do(http.MethodPost, host, "/admin/login", url.Values{"username": {"root"}, "password": {e.rootPass}}, nil)
	if rec.Code != http.StatusSeeOther {
		e.t.Fatalf("root login on %s: status %d, body %s", host, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ntx_session" {
			return c
		}
	}
	e.t.Fatal("no session cookie after login")
	return nil
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// startSSO begins a sign-in on host as id (with an invite token, if set)
// and returns the flow cookie and the provider's redirect back to us.
func (e *env) startSSO(host string, id ssotest.Identity, invite string) (flow *http.Cookie, callback *url.URL) {
	e.t.Helper()
	e.fake.SetNext(id)
	var rec *httptest.ResponseRecorder
	if invite != "" {
		rec = e.do(http.MethodPost, host, "/auth/start", url.Values{"invite": {invite}}, nil)
	} else {
		rec = e.do(http.MethodGet, host, "/auth/start", nil, nil)
	}
	if rec.Code != http.StatusFound {
		e.t.Fatalf("/auth/start on %s: status %d, body %s", host, rec.Code, rec.Body.String())
	}
	flow = cookieNamed(rec, "ntx_oauth")
	if flow == nil {
		e.t.Fatal("/auth/start set no ntx_oauth cookie")
	}
	back, err := url.Parse(e.fake.Authorize(e.t, rec.Header().Get("Location")))
	if err != nil {
		e.t.Fatal(err)
	}
	return flow, back
}

// ssoLogin runs a whole provider sign-in on host and returns the callback
// response and the session cookie it set, if any.
func (e *env) ssoLogin(host string, id ssotest.Identity, invite string) (*httptest.ResponseRecorder, *http.Cookie) {
	e.t.Helper()
	flow, back := e.startSSO(host, id, invite)
	rec := e.doWith(http.MethodGet, host, back.RequestURI(), nil, flow)
	return rec, cookieNamed(rec, "ntx_session")
}

// verified is a provider identity with a verified email.
func verified(sub, email string) ssotest.Identity {
	return ssotest.Identity{Subject: sub, Email: email, Name: "Name " + sub, EmailVerified: true}
}

// member adds a person to site A.
func (e *env) member(email, role string, canPublish bool) *models.User {
	e.t.Helper()
	u, err := e.people.AddMember(e.siteA.ID, email, role, canPublish)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// sessionFor signs u in on site A directly, skipping the provider.
func (e *env) sessionFor(u *models.User) *http.Cookie {
	e.t.Helper()
	rec := httptest.NewRecorder()
	if err := e.am.Login(rec, u, &e.siteA.ID); err != nil {
		e.t.Fatal(err)
	}
	return cookieNamed(rec, "ntx_session")
}

func (e *env) storedSite(key string) *models.Site {
	e.t.Helper()
	all, err := e.sites.List()
	if err != nil {
		e.t.Fatal(err)
	}
	for i := range all {
		if all[i].Key == key {
			return &all[i]
		}
	}
	return nil
}

// Review Focus 3: bad sections JSON must re-show the form, never 500, and
// must leave the stored sections alone.
func TestSettingsRejectsBadSectionsJSON(t *testing.T) {
	e := newEnv(t)
	c := e.login("a.example.com")
	ok := url.Values{"name": {"Site A"}, "sections": {`[{"slug":"projects","pages":[{"slug":"one"}]}]`}}
	if rec := e.do(http.MethodPost, "a.example.com", "/admin/settings", ok, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("valid sections: status %d, body %s", rec.Code, rec.Body.String())
	}

	for name, sections := range map[string]string{
		"vars not an object": `[{"slug":"x","vars":[1]}]`,
		"not json":           `not json`,
		"reserved slug":      `[{"slug":"blog"}]`,
	} {
		rec := e.do(http.MethodPost, "a.example.com", "/admin/settings", url.Values{"name": {"Changed"}, "sections": {sections}}, c)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `class="error"`) {
			t.Errorf("%s: expected the form back with an error, got %d: %s", name, rec.Code, rec.Body.String())
		}
		got := e.storedSite("a")
		if len(got.Sections) != 1 || got.Sections[0].Slug != "projects" || got.Name != "Site A" {
			t.Errorf("%s: a rejected save must change nothing, got name %q sections %+v", name, got.Name, got.Sections)
		}
	}
}

func TestSettingsSavesSectionsAndReloads(t *testing.T) {
	e := newEnv(t)
	c := e.login("a.example.com")
	form := url.Values{"name": {"New Name"}, "about_slug": {"me"}, "sections": {`[{"slug":"projects","title":"Projects","vars":{"k":"v"},"pages":[{"slug":"one","title":"One"}]}]`}}
	if rec := e.do(http.MethodPost, "a.example.com", "/admin/settings", form, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	live := e.reg.ByHost("a.example.com")
	if live.Name != "New Name" || live.AboutSlug != "me" || len(live.Sections) != 1 || live.Sections[0].Vars["k"] != "v" {
		t.Errorf("the registry must reflect the saved settings at once, got %+v", live)
	}
	if rec := e.do(http.MethodGet, "a.example.com", "/projects/one", nil, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "project One") {
		t.Errorf("the new section page must be served: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPlatformEditShowsConflictError(t *testing.T) {
	e := newEnv(t)
	b := &models.Site{Key: "b", Hostname: "b.example.com", Theme: "t1"}
	if err := e.sites.Create(b, platformHost); err != nil {
		t.Fatal(err)
	}
	e.reg.Reload()
	c := e.login(platformHost)
	form := url.Values{"hostname": {"b.example.com"}, "name": {"Site A"}, "theme": {"t1"}}
	rec := e.do(http.MethodPost, platformHost, "/admin/platform/sites/a", form, c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already used") {
		t.Fatalf("expected the form back with the conflict, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := e.storedSite("a"); got.Hostname != "a.example.com" {
		t.Errorf("hostname must be unchanged, got %q", got.Hostname)
	}
}

func TestPlatformCreateRejectsUnknownTheme(t *testing.T) {
	e := newEnv(t)
	c := e.login(platformHost)
	form := url.Values{"key": {"new"}, "hostname": {"new.example.com"}, "theme": {"nope"}}
	rec := e.do(http.MethodPost, platformHost, "/admin/platform/sites", form, c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "theme") {
		t.Fatalf("expected the form back with a theme error, got %d: %s", rec.Code, rec.Body.String())
	}
	if e.storedSite("new") != nil {
		t.Error("no site may be created with an unknown theme")
	}
}

func TestPlatformCreateSite(t *testing.T) {
	e := newEnv(t)
	c := e.login(platformHost)
	form := url.Values{"key": {"new"}, "hostname": {"New.Example.com"}, "name": {"New"}, "theme": {"t1"}}
	if rec := e.do(http.MethodPost, platformHost, "/admin/platform/sites", form, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, "new.example.com", "/", nil, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "home of New") {
		t.Errorf("a new site must be served straight away: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPlatformDeleteNeedsTypedKey(t *testing.T) {
	e := newEnv(t)
	c := e.login(platformHost)
	rec := e.do(http.MethodPost, platformHost, "/admin/platform/sites/a/delete", url.Values{"confirm": {"wrong"}}, c)
	if rec.Code != http.StatusOK || e.storedSite("a") == nil {
		t.Fatalf("a wrong confirmation must keep the site: %d", rec.Code)
	}
	rec = e.do(http.MethodPost, platformHost, "/admin/platform/sites/a/delete", url.Values{"confirm": {"a"}}, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if e.storedSite("a") != nil || e.reg.ByID(e.siteA.ID) != nil {
		t.Error("the site must be gone from the store and the registry")
	}
}

func TestRequireRedirectsAnonymous(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ host, path string }{
		{platformHost, "/admin/platform/sites"},
		{"a.example.com", "/admin/platform/sites"},
		{"a.example.com", "/admin/posts"},
		{"a.example.com", "/admin/settings"},
	} {
		rec := e.do(http.MethodGet, tc.host, tc.path, nil, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s%s anonymous: got %d → %q", tc.host, tc.path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestSiteAdminPagesNotOnPlatformHost(t *testing.T) {
	e := newEnv(t)
	c := e.login(platformHost)
	for _, path := range []string{"/admin/posts", "/admin/settings", "/", "/blog"} {
		if rec := e.do(http.MethodGet, platformHost, path, nil, c); rec.Code != http.StatusNotFound {
			t.Errorf("%s on the platform host: got %d, want 404", path, rec.Code)
		}
	}
}

func TestPostLifecycle(t *testing.T) {
	e := newEnv(t)
	c := e.login("a.example.com")
	form := url.Values{"title": {"Hello"}, "slug": {"hello"}, "body": {"hi"}, "tags": {"go, web"}, "action": {"save"}}
	if rec := e.do(http.MethodPost, "a.example.com", "/admin/posts", form, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, "a.example.com", "/blog/hello", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a draft must not be public, got %d", rec.Code)
	}
	p, _ := e.posts.ForSite(e.siteA.ID).GetBySlug("hello")
	form.Set("action", "publish")
	if rec := e.do(http.MethodPost, "a.example.com", "/admin/posts/"+itoa(p.ID), form, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, "a.example.com", "/blog/hello", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("a published post must be public, got %d", rec.Code)
	}
}

func TestGenerateIsolatesSiteOutput(t *testing.T) {
	e := newEnv(t)
	b := &models.Site{Key: "b", Hostname: "b.example.com", Name: "Site B", Theme: "t1"}
	if err := e.sites.Create(b, platformHost); err != nil {
		t.Fatal(err)
	}
	e.reg.Reload()
	for _, tc := range []struct {
		id    int64
		title string
	}{{e.siteA.ID, "Only On A"}, {b.ID, "Only On B"}} {
		if err := e.posts.ForSite(tc.id).Create(&models.Post{Slug: "shared-slug", Title: tc.title, Body: ".", Published: true}); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "out")
	if err := e.h.Generate(out); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, mine, other, name string }{
		{"a", "Only On A", "Only On B", "Site A"},
		{"b", "Only On B", "Only On A", "Site B"},
	} {
		post, err := os.ReadFile(filepath.Join(out, tc.key, "blog", "shared-slug", "index.html"))
		if err != nil || !strings.Contains(string(post), tc.mine) || strings.Contains(string(post), tc.other) {
			t.Errorf("site %q post page wrong: %v %s", tc.key, err, post)
		}
		home, _ := os.ReadFile(filepath.Join(out, tc.key, "index.html"))
		if !strings.Contains(string(home), "home of "+tc.name) {
			t.Errorf("site %q home should name the site: %s", tc.key, home)
		}
		if _, err := os.Stat(filepath.Join(out, tc.key, "static", "css", "x.css")); err != nil {
			t.Errorf("site %q: theme static files must be copied: %v", tc.key, err)
		}
	}
}

// When a later site fails, every site before it (in key order) must have
// been written -- the same partial output on every run.
func TestGenerateRunsSitesInOrder(t *testing.T) {
	e := newEnv(t)
	for _, k := range []string{"b", "c"} {
		if err := e.sites.Create(&models.Site{Key: k, Hostname: k + ".example.com", Theme: "t1"}, platformHost); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.sites.Create(&models.Site{Key: "zz", Hostname: "zz.example.com", Theme: "gone"}, platformHost); err != nil {
		t.Fatal(err)
	}
	e.reg.Reload()
	out := filepath.Join(t.TempDir(), "out")
	if err := e.h.Generate(out); err == nil {
		t.Fatal("expected Generate to fail on the site whose theme doesn't exist")
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(out, k, "index.html")); err != nil {
			t.Errorf("site %q precedes the failing site, so it must have been generated: %v", k, err)
		}
	}
}

func TestGeneratePublicTagsOnlyPublished(t *testing.T) {
	e := newEnv(t)
	ps := e.posts.ForSite(e.siteA.ID)
	ps.Create(&models.Post{Slug: "draft", Title: "Draft", Body: ".", Tags: []string{"secret"}})
	ps.Create(&models.Post{Slug: "live", Title: "Live", Body: ".", Published: true, Tags: []string{"go"}})
	out := filepath.Join(t.TempDir(), "out")
	if err := e.h.Generate(out); err != nil {
		t.Fatal(err)
	}
	idx, _ := os.ReadFile(filepath.Join(out, "a", "blog", "index.html"))
	if strings.Contains(string(idx), "secret") || !strings.Contains(string(idx), "go") {
		t.Errorf("the public tag list must show only published posts' tags: %s", idx)
	}
}

// Every handler that needs a site must 404 without one, never panic.
func TestHandlersNotFoundWithoutResolvedSite(t *testing.T) {
	e := newEnv(t)
	for name, fn := range map[string]http.HandlerFunc{
		"Home": e.h.Home, "About": e.h.About, "BlogIndex": e.h.BlogIndex, "BlogPost": e.h.BlogPost,
		"Static": e.h.Static, "Section": e.h.Section, "Posts": e.h.Posts, "NewPost": e.h.NewPost,
		"CreatePost": e.h.CreatePost, "EditPost": e.h.EditPost, "UpdatePost": e.h.UpdatePost,
		"Settings": e.h.Settings, "SettingsPost": e.h.SettingsPost,
	} {
		rec := httptest.NewRecorder()
		fn(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404 with no resolved site, got %d", name, rec.Code)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
