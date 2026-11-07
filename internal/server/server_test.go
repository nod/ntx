package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/handlers"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitereg"
)

const platformHost = "admin.example.com"

type env struct {
	t      *testing.T
	ts     *httptest.Server
	themes string
	posts  *models.PostStore
	siteA  *models.Site
	siteB  *models.Site
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

func setup(t *testing.T) *env {
	t.Helper()
	d, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s, _ := d.DB(); s.Close() })

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
	reg, err := sitereg.New(sites)
	if err != nil {
		t.Fatal(err)
	}
	rootHash, _ := auth.HashPassword("rootpass")
	cfg := &config.Config{
		Dev:              true,
		TrustedProxies:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		ThemesPath:       themesRoot,
		PlatformHostname: platformHost,
		RootPasswordHash: rootHash,
	}
	am := auth.NewManager(models.NewSessionStore(d), users, models.NewMembershipStore(d), rootHash, false)
	posts := models.NewPostStore(d)
	h := handlers.New(handlers.Deps{
		Sites: sites, Posts: posts, Users: users, Registry: reg, Auth: am,
		Limiter: auth.NewLimiter(), ThemesPath: themesRoot, PlatformHostname: platformHost,
		RootPasswordHash: rootHash, Dev: true,
	})
	ts := httptest.NewServer(New(cfg, h, reg, am).Handler)
	t.Cleanup(ts.Close)
	return &env{t: t, ts: ts, themes: themesRoot, posts: posts, siteA: a, siteB: b}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

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
