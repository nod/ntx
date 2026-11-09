package server

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db/dbtest"
	"github.com/nod/ntx/internal/handlers"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/people"
	"github.com/nod/ntx/internal/secretbox"
	"github.com/nod/ntx/internal/sitereg"
	"github.com/nod/ntx/internal/sso"
	"github.com/nod/ntx/internal/sso/ssotest"
)

const platformHost = "admin.example.com"

type env struct {
	t       *testing.T
	ts      *httptest.Server
	themes  string
	posts   *models.PostStore
	people  *people.Service
	invites *models.InviteStore
	fake    *ssotest.Provider
	siteA   *models.Site
	siteB   *models.Site
}

func writeTheme(t *testing.T, root, name string) {
	t.Helper()
	for path, content := range map[string]string{
		"templates/base.html":                        `<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>`,
		"templates/home.html":                        `{{template "base.html" .}}{{define "content"}}home of {{.SiteName}}{{end}}`,
		"templates/blog/index.html":                  `{{template "base.html" .}}{{define "content"}}{{range .Posts}}{{.Title}} {{end}}{{end}}`,
		"templates/blog/post.html":                   `{{template "base.html" .}}{{define "content"}}{{.Post.Title}}{{end}}`,
		"templates/sections/projects/one/index.html": `{{template "base.html" .}}{{define "content"}}project {{.Title}}{{end}}`,
	} {
		full := filepath.Join(root, name, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func setup(t *testing.T) *env { t.Helper(); return setupMode(t, true) }

// setupMode builds the full server stack; dev=false is production mode
// (https callback URLs, Secure cookies). Both sites sign in through a fake
// Auth0 tenant.
func setupMode(t *testing.T, dev bool, opts ...func(*handlers.Deps)) *env {
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
	b := &models.Site{Key: "b", Hostname: "b.example.com", Name: "Site B", Theme: "t1"}
	for _, s := range []*models.Site{a, b} {
		if err := sites.Create(s, platformHost); err != nil {
			t.Fatal(err)
		}
	}
	fake := ssotest.New(t)
	encKey := []byte("0123456789abcdef0123456789abcdef")
	sealed, err := secretbox.Seal(encKey, fake.ClientSecret)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*models.Site{a, b} {
		if err := sites.SetProvider(s.ID, models.ProviderSettings{Type: models.ProviderAuth0, Domain: "tenant.example.com", ClientID: fake.ClientID}, sealed); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := sitereg.New(sites)
	if err != nil {
		t.Fatal(err)
	}
	rootHash, _ := auth.HashPassword("rootpass")
	cfg := &config.Config{
		Dev:              dev,
		TrustedProxies:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		ThemesPath:       themesRoot,
		PlatformHostname: platformHost,
		RootPasswordHash: rootHash,
	}
	mems, sessions := models.NewMembershipStore(d), models.NewSessionStore(d)
	ids, invites := models.NewIdentityStore(d), models.NewInviteStore(d)
	am := auth.NewManager(sessions, users, mems, rootHash, !dev)
	posts := models.NewPostStore(d)
	deps := handlers.Deps{
		Sites: sites, Posts: posts, Users: users, Registry: reg, Auth: am,
		Memberships: mems, Identities: ids, Invites: invites, Sessions: sessions,
		People: people.New(users, ids, mems, invites), SSO: sso.NewClient(nil),
		SessionSecret: []byte("session-secret-session-secret-xx"), EncryptionKey: encKey,
		IssuerFor: func(string, string) string { return fake.URL },
		Limiter:   auth.NewLimiter(), ThemesPath: themesRoot, PlatformHostname: platformHost,
		RootPasswordHash: rootHash, Dev: dev,
	}
	for _, o := range opts {
		o(&deps)
	}
	h := handlers.New(deps)
	// Run under the production server's own timeouts.
	srv := New(cfg, h, reg, am)
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config.ReadTimeout, ts.Config.WriteTimeout, ts.Config.IdleTimeout = srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout
	ts.Start()
	t.Cleanup(ts.Close)
	return &env{
		t: t, ts: ts, themes: themesRoot, posts: posts, siteA: a, siteB: b, fake: fake,
		people: people.New(users, ids, mems, invites), invites: invites,
	}
}

var noRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       20 * time.Second, // a hung handler fails the test instead of hanging it
}

// do sends a request with an explicit Host header, the way a browser
// addressing that site would, and returns the status and body.
func (e *env) do(method, host, path string, form url.Values, headers map[string]string, cookie *http.Cookie) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, body)
	req.Host = host
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) login(host string, headers map[string]string) (*http.Response, *http.Cookie) {
	e.t.Helper()
	resp, _ := e.do(http.MethodPost, host, "/admin/login", url.Values{"username": {"root"}, "password": {"rootpass"}}, headers, nil)
	for _, c := range resp.Cookies() {
		if c.Name == "ntx_session" {
			return resp, c
		}
	}
	return resp, nil
}

func TestHostResolutionNormalises(t *testing.T) {
	e := setup(t)
	if resp, body := e.do(http.MethodGet, "A.Example.COM:8443", "/", nil, nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "home of Site A") {
		t.Errorf("mixed-case host with a port: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(http.MethodGet, "ADMIN.example.com:443", "/admin/login", nil, nil, nil); resp.StatusCode != 200 {
		t.Errorf("platform host with a port: %d", resp.StatusCode)
	}
}

func TestUnknownHost404(t *testing.T) {
	e := setup(t)
	if resp, _ := e.do(http.MethodGet, "nope.example.com", "/", nil, nil, nil); resp.StatusCode != 404 {
		t.Errorf("unknown host: %d", resp.StatusCode)
	}
	if resp, _ := e.do(http.MethodGet, "nope.example.com", "/admin/login", nil, nil, nil); resp.StatusCode != 404 {
		t.Errorf("unknown host admin: %d", resp.StatusCode)
	}
}

func TestPlatformHostServesOnlyAdmin(t *testing.T) {
	e := setup(t)
	for _, path := range []string{"/", "/blog", "/projects/one", "/static/x.css"} {
		if resp, _ := e.do(http.MethodGet, platformHost, path, nil, nil, nil); resp.StatusCode != 404 {
			t.Errorf("%s on the platform host: %d, want 404", path, resp.StatusCode)
		}
	}
	if resp, _ := e.do(http.MethodGet, platformHost, "/admin/login", nil, nil, nil); resp.StatusCode != 200 {
		t.Errorf("/admin/login on the platform host: %d", resp.StatusCode)
	}
	if resp, _ := e.do(http.MethodGet, platformHost, "/admin/assets/admin.css", nil, nil, nil); resp.StatusCode != 200 {
		t.Errorf("admin assets on the platform host: %d", resp.StatusCode)
	}
}

func TestRootLoginFlowOnSite(t *testing.T) {
	e := setup(t)
	resp, c := e.login("a.example.com", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/posts" || c == nil {
		t.Fatalf("login on a site: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := e.do(http.MethodGet, "a.example.com", "/admin/posts", nil, nil, c); resp.StatusCode != 200 {
		t.Errorf("/admin/posts with the cookie: %d", resp.StatusCode)
	}
}

func TestRootLoginOnPlatformHost(t *testing.T) {
	e := setup(t)
	resp, c := e.login(platformHost, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/platform/sites" {
		t.Fatalf("login on the platform host: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, body := e.do(http.MethodGet, platformHost, "/admin/platform/sites", nil, nil, c); resp.StatusCode != 200 || !strings.Contains(body, "a.example.com") {
		t.Errorf("platform sites page: %d", resp.StatusCode)
	}
}

// Review Focus 5.
func TestSessionBoundToIssuingHost(t *testing.T) {
	e := setup(t)
	_, siteCookie := e.login("a.example.com", nil)
	for _, tc := range []struct{ host, path string }{
		{"b.example.com", "/admin/posts"},
		{platformHost, "/admin/platform/sites"},
	} {
		if resp, _ := e.do(http.MethodGet, tc.host, tc.path, nil, nil, siteCookie); resp.StatusCode != http.StatusSeeOther {
			t.Errorf("site A's session on %s%s: %d, want 303 to sign-in", tc.host, tc.path, resp.StatusCode)
		}
	}
	_, platCookie := e.login(platformHost, nil)
	if resp, _ := e.do(http.MethodGet, "a.example.com", "/admin/posts", nil, nil, platCookie); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the platform session on site A: %d, want 303", resp.StatusCode)
	}
}

func TestLoginFailuresRateLimitedPerClientIP(t *testing.T) {
	e := setup(t)
	attacker := map[string]string{"X-Forwarded-For": "1.1.1.1"}
	for i := 0; i < 5; i++ {
		e.do(http.MethodPost, "a.example.com", "/admin/login", url.Values{"username": {"root"}, "password": {"wrong"}}, attacker, nil)
	}
	_, body := e.do(http.MethodPost, "a.example.com", "/admin/login", url.Values{"username": {"root"}, "password": {"rootpass"}}, attacker, nil)
	if !strings.Contains(body, "Too many attempts") {
		t.Errorf("the 6th attempt from the attacker's IP must be refused, got: %s", body)
	}
	// Same budget across hosts.
	_, body = e.do(http.MethodPost, platformHost, "/admin/login", url.Values{"username": {"root"}, "password": {"rootpass"}}, attacker, nil)
	if !strings.Contains(body, "Too many attempts") {
		t.Error("the budget must be shared across hosts")
	}
	// Another client is unaffected.
	if resp, _ := e.login("a.example.com", map[string]string{"X-Forwarded-For": "2.2.2.2"}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("another client's correct login must succeed, got %d", resp.StatusCode)
	}
}

func TestSuccessfulLoginsDontConsumeBudget(t *testing.T) {
	e := setup(t)
	for i := 0; i < 6; i++ {
		if resp, _ := e.login("a.example.com", nil); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("login %d: %d", i+1, resp.StatusCode)
		}
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := setup(t)
	resp, _ := e.do(http.MethodPost, "a.example.com", "/admin/login", url.Values{"username": {"root"}, "password": {"rootpass"}},
		map[string]string{"Sec-Fetch-Site": "cross-site"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-site POST must be rejected, got %d", resp.StatusCode)
	}
}

// Review Focus 2.
func TestMissingThemeKeepsAdminWorking(t *testing.T) {
	e := setup(t)
	if err := os.RemoveAll(filepath.Join(e.themes, "t1")); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.do(http.MethodGet, "a.example.com", "/", nil, nil, nil); resp.StatusCode != 500 {
		t.Errorf("a site whose theme is gone: %d, want 500", resp.StatusCode)
	}
	if resp, _ := e.do(http.MethodGet, "a.example.com", "/admin/login", nil, nil, nil); resp.StatusCode != 200 {
		t.Errorf("admin must keep working without the theme: %d", resp.StatusCode)
	}
}

func TestSectionRouteDynamic(t *testing.T) {
	e := setup(t)
	_, c := e.login("a.example.com", nil)
	form := url.Values{"name": {"Site A"}, "sections": {`[{"slug":"projects","pages":[{"slug":"one","title":"One"}]}]`}}
	if resp, body := e.do(http.MethodPost, "a.example.com", "/admin/settings", form, nil, c); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("saving sections: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.do(http.MethodGet, "a.example.com", "/projects/one", nil, nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "project One") {
		t.Errorf("section page on site A: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(http.MethodGet, "b.example.com", "/projects/one", nil, nil, nil); resp.StatusCode != 404 {
		t.Errorf("site B didn't define the section: %d, want 404", resp.StatusCode)
	}
}

func TestBlogSlugNotShadowedBySection(t *testing.T) {
	e := setup(t)
	if err := e.posts.ForSite(e.siteA.ID).Create(&models.Post{Slug: "x", Title: "Post X", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}
	if resp, body := e.do(http.MethodGet, "a.example.com", "/blog/x", nil, nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "Post X") {
		t.Errorf("/blog/x: %d %s", resp.StatusCode, body)
	}
}

func TestTenantIsolation(t *testing.T) {
	e := setup(t)
	if err := e.posts.ForSite(e.siteA.ID).Create(&models.Post{Slug: "only-a", Title: "Only A", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.do(http.MethodGet, "b.example.com", "/blog/only-a", nil, nil, nil); resp.StatusCode != 404 {
		t.Errorf("site A's post on site B: %d, want 404", resp.StatusCode)
	}
	if _, body := e.do(http.MethodGet, "b.example.com", "/blog", nil, nil, nil); strings.Contains(body, "Only A") {
		t.Error("site B's blog index must not list site A's post")
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := setup(t)
	resp, _ := e.do(http.MethodGet, "a.example.com", "/", nil, nil, nil)
	if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:" {
		t.Errorf("CSP: %q", got)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("X-Frame-Options must be DENY")
	}
}

// Behind a proxy, logs must name the real client (from the trusted
// proxy's X-Forwarded-For), not the proxy.
func TestRequestLogShowsClientIP(t *testing.T) {
	e := setup(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	e.do(http.MethodGet, "a.example.com", "/", nil, map[string]string{"X-Forwarded-For": "203.0.113.9"}, nil)
	out := buf.String()
	if !strings.Contains(out, "203.0.113.9") {
		t.Errorf("request log must show the client IP, got: %s", out)
	}
	if !strings.Contains(out, "a.example.com") || !strings.Contains(out, " 200 ") {
		t.Errorf("request log must show host and status, got: %s", out)
	}
}

func TestRequestLogRedactsTokens(t *testing.T) {
	e := setup(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	e.do(http.MethodGet, "a.example.com", "/invite/SECRET123", nil, nil, nil)
	e.do(http.MethodGet, "a.example.com", "/auth/callback?code=CODE9&state=S", nil, nil, nil)
	out := buf.String()
	for _, secret := range []string{"SECRET123", "CODE9"} {
		if strings.Contains(out, secret) {
			t.Errorf("request log leaks %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "/invite/[redacted]") || !strings.Contains(out, "/auth/callback ") {
		t.Errorf("request log should still show the redacted paths: %s", out)
	}
}

func cookieOf(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ssoLogin runs a provider sign-in through the whole server on host, with
// headers on every request to ntx, and returns the callback response.
func (e *env) ssoLogin(host, email string, headers map[string]string) *http.Response {
	e.t.Helper()
	e.fake.SetNext(ssotest.Identity{Subject: "sub-" + email, Email: email, EmailVerified: true})
	resp, _ := e.do(http.MethodGet, host, "/auth/start", nil, headers, nil)
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("/auth/start: %d", resp.StatusCode)
	}
	flow := cookieOf(resp, "ntx_oauth")
	back, err := url.Parse(e.fake.Authorize(e.t, resp.Header.Get("Location")))
	if err != nil {
		e.t.Fatal(err)
	}
	resp, body := e.do(http.MethodGet, host, back.RequestURI(), nil, headers, flow)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	return resp
}

func TestMemberSessionNotValidOnOtherSite(t *testing.T) {
	e := setup(t)
	if _, err := e.people.AddMember(e.siteA.ID, "m@x.io", models.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.people.AddMember(e.siteB.ID, "m@x.io", models.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	sess := cookieOf(e.ssoLogin("a.example.com", "m@x.io", nil), "ntx_session")
	if resp, _ := e.do(http.MethodGet, "a.example.com", "/admin/posts", nil, nil, sess); resp.StatusCode != http.StatusOK {
		t.Fatalf("on its own site: %d", resp.StatusCode)
	}
	resp, _ := e.do(http.MethodGet, "b.example.com", "/admin/posts", nil, nil, sess)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login" {
		t.Fatalf("site A session on site B: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestInviteTokenOnlyOnItsSite(t *testing.T) {
	e := setup(t)
	_, token, err := e.invites.Create(e.siteA.ID, "i@x.io", models.RoleContributor, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.do(http.MethodGet, "a.example.com", "/invite/"+token, nil, nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("on its own site: %d", resp.StatusCode)
	}
	if resp, _ := e.do(http.MethodGet, "b.example.com", "/invite/"+token, nil, nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("site A's invite on site B: %d", resp.StatusCode)
	}
}

func TestSSOCallbackPassesCrossOriginProtection(t *testing.T) {
	e := setup(t)
	if _, err := e.people.AddMember(e.siteA.ID, "m@x.io", models.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	// The provider's redirect back is a cross-site top-level GET.
	if c := cookieOf(e.ssoLogin("a.example.com", "m@x.io", map[string]string{"Sec-Fetch-Site": "cross-site"}), "ntx_session"); c == nil {
		t.Fatal("cross-site callback set no session")
	}
	_, token, err := e.invites.Create(e.siteA.ID, "i@x.io", models.RoleContributor, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"invite": {token}}
	if resp, _ := e.do(http.MethodPost, "a.example.com", "/auth/start", form, map[string]string{"Sec-Fetch-Site": "same-origin"}, nil); resp.StatusCode != http.StatusFound {
		t.Fatalf("same-origin POST /auth/start: %d", resp.StatusCode)
	}
	if resp, _ := e.do(http.MethodPost, "a.example.com", "/auth/start", form, map[string]string{"Sec-Fetch-Site": "cross-site"}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST /auth/start: %d", resp.StatusCode)
	}
}

func TestSSOCallbackSetsSessionBehindProxy(t *testing.T) {
	e := setupMode(t, false)
	if _, err := e.people.AddMember(e.siteA.ID, "m@x.io", models.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	proxied := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.9"}
	resp, _ := e.do(http.MethodGet, "a.example.com", "/auth/start", nil, proxied, nil)
	authURL, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := authURL.Query().Get("redirect_uri"); got != "https://a.example.com/auth/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if c := cookieOf(resp, "ntx_oauth"); c == nil || !c.Secure {
		t.Fatalf("flow cookie not Secure: %+v", c)
	}
	sess := cookieOf(e.ssoLogin("a.example.com", "m@x.io", proxied), "ntx_session")
	if sess == nil || !sess.Secure {
		t.Fatalf("session cookie not Secure: %+v", sess)
	}
}

// The provider budget must leave room to write the 502 page before the
// server's write deadline, which starts when the request arrives.
func TestProviderBudgetFitsWriteTimeout(t *testing.T) {
	if handlers.DefaultProviderTimeout > WriteTimeout-3*time.Second {
		t.Fatalf("provider budget %v leaves too little of the %v write timeout", handlers.DefaultProviderTimeout, WriteTimeout)
	}
}

// Review Focus 2, hanging variant: a provider that accepts connections and
// never answers still gets the 502 page, not a dropped connection.
func TestHangingProviderGets502(t *testing.T) {
	hole, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hole.Close() })
	e := setupMode(t, true, func(d *handlers.Deps) {
		d.IssuerFor = func(string, string) string { return "http://" + hole.Addr().String() }
		d.ProviderTimeout = 300 * time.Millisecond
	})
	start := time.Now()
	resp, body := e.do(http.MethodGet, "a.example.com", "/auth/start", nil, nil, nil)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "reach the sign-in provider") {
		t.Fatalf("hanging provider: %d %s", resp.StatusCode, body)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v; the provider budget wasn't applied", took)
	}
}
