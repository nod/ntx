package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/models"
)

// setupServer builds a single-site server whose site_url is "example.com"
// and returns a client that always sends that Host header, since
// httptest.Server otherwise sends "127.0.0.1:PORT" (which won't match any
// configured site_url and would 404 at the resolveSite middleware).
func setupServer(t *testing.T) (*httptest.Server, *models.PostStore, *http.Client) {
	t.Helper()
	return setupServerWithConfig(t, nil)
}

func setupServerWithConfig(t *testing.T, extra []config.Section) (*httptest.Server, *models.PostStore, *http.Client) {
	t.Helper()

	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)
	if extra != nil {
		sectionTmpl := filepath.Join(tmpDir, "sections/projects/one/index.html")
		os.MkdirAll(filepath.Dir(sectionTmpl), 0755)
		os.WriteFile(sectionTmpl, []byte(`{{template "base.html" .}}{{define "content"}}project-one{{end}}`), 0644)
	}

	hash, _ := auth.HashPassword("testpass")
	sites := []config.Site{
		{
			Key:               "mysite",
			SiteURL:           "example.com",
			TemplatePath:      tmpDir,
			StaticPath:        tmpDir,
			AboutSlug:         "about-page",
			AdminPasswordHash: hash,
			Sections:          extra,
		},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SessionSecret: "testsecretkey12345678901234567890",
		Dev:           true,
		Sites:         sites,
	}

	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, rawPosts, session)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	client := &http.Client{
		Transport: hostRewriteTransport{host: "example.com", inner: http.DefaultTransport, base: ts.URL},
	}
	return ts, rawPosts.ForSite(sites[0].ID), client
}

// hostRewriteTransport sends every request to ts's real address but with
// a Host header the resolveSite middleware will recognize.
type hostRewriteTransport struct {
	host  string
	inner http.RoundTripper
	base  string
}

func (t hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	baseURL, _ := url.Parse(t.base)
	req = req.Clone(req.Context())
	req.URL.Scheme = baseURL.Scheme
	req.URL.Host = baseURL.Host
	req.Host = t.host
	return t.inner.RoundTrip(req)
}

func setupTemplates(t *testing.T, dir string) {
	t.Helper()

	base := `<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>`
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(full), 0755)
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("base.html", base)
	write("home.html", `{{template "base.html" .}}{{define "content"}}home{{end}}`)
	write("blog/index.html", `{{template "base.html" .}}{{define "content"}}blog-index{{end}}`)
	write("blog/post.html", `{{template "base.html" .}}{{define "content"}}{{.Post.Title}}{{end}}`)
	write("admin/login.html", `{{template "base.html" .}}{{define "content"}}login{{if .Error}} error:{{.Error}}{{end}}{{end}}`)
	write("admin/posts.html", `{{template "base.html" .}}{{define "content"}}posts-admin{{end}}`)
	write("admin/editor.html", `{{template "base.html" .}}{{define "content"}}editor{{end}}`)
}

func setupServerWithSections(t *testing.T) (*httptest.Server, *models.PostStore, *http.Client) {
	t.Helper()
	return setupServerWithConfig(t, []config.Section{
		{
			Slug:  "projects",
			Title: "Projects",
			Pages: []config.SectionPage{
				{Slug: "one", Title: "Project One"},
			},
		},
	})
}

func TestHomeRoute(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// TestUnmatchedHostReturns404 guards a spec requirement: an unmatched Host
// must get a bare 404, which is what stops one tenant's URLs from
// resolving on another tenant's hostname. It must bypass the
// hostRewriteTransport (which unconditionally overwrites req.Host to
// "example.com") to actually exercise the unmatched path. See addendum
// Ruling 5.
func TestUnmatchedHostReturns404(t *testing.T) {
	ts, _, _ := setupServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Host = "not-configured.example.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unmatched host, got %d", resp.StatusCode)
	}
}

func TestAboutRedirect(t *testing.T) {
	_, _, baseClient := setupServer(t)
	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://example.com/about")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "about-page") {
		t.Errorf("redirect to wrong location: %s", loc)
	}
}

func TestBlogIndex(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/blog")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBlogPost404(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/blog/no-such-post")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestBlogPostDraftHidden(t *testing.T) {
	_, posts, client := setupServer(t)

	p := &models.Post{Slug: "secret-draft", Title: "Secret", Body: ".", Published: false}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.com/blog/secret-draft")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("draft should return 404, got %d", resp.StatusCode)
	}
}

func TestBlogPostPublished(t *testing.T) {
	_, posts, client := setupServer(t)

	p := &models.Post{Slug: "my-post", Title: "My Post", Body: "# Hello", Published: true}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.com/blog/my-post")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSectionPage(t *testing.T) {
	_, _, client := setupServerWithSections(t)
	resp, err := client.Get("http://example.com/projects/one")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSectionPageNotInConfig(t *testing.T) {
	_, _, client := setupServerWithSections(t)
	resp, err := client.Get("http://example.com/projects/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSectionUnknownNamespace(t *testing.T) {
	_, _, client := setupServer(t)
	resp, err := client.Get("http://example.com/projects/one")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unconfigured section, got %d", resp.StatusCode)
	}
}

func TestAdminLoginRequired(t *testing.T) {
	_, _, baseClient := setupServer(t)
	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://example.com/admin/posts")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", resp.StatusCode)
	}
}

func TestAdminLoginFlow(t *testing.T) {
	_, _, baseClient := setupServer(t)
	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.PostForm("http://example.com/admin/login", url.Values{
		"password": {"testpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after login, got %d", resp.StatusCode)
	}

	cookie := resp.Cookies()
	if len(cookie) == 0 {
		t.Fatal("no session cookie set")
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/admin/posts", nil)
	req.AddCookie(cookie[0])
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for authenticated request, got %d", resp2.StatusCode)
	}
}

func TestAdminLoginBadPassword(t *testing.T) {
	_, _, client := setupServer(t)

	resp, err := client.PostForm("http://example.com/admin/login", url.Values{
		"password": {"wrongpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with error form, got %d", resp.StatusCode)
	}
}

func loginCookie(t *testing.T, client *http.Client) *http.Cookie {
	t.Helper()
	c := &http.Client{
		Transport: client.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := c.PostForm("http://example.com/admin/login", url.Values{"password": {"testpass"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "ntx_session" {
			return c
		}
	}
	t.Fatal("no session cookie after login")
	return nil
}

func TestBlogIndexTagFilter(t *testing.T) {
	_, posts, client := setupServer(t)

	_ = createPost(t, posts, "go-post", "Go Post", true, "go")
	_ = createPost(t, posts, "rust-post", "Rust Post", true, "rust")

	resp, err := client.Get("http://example.com/blog?tag=go")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "blog-index") {
		t.Error("expected blog index content")
	}
}

func TestAdminLogout(t *testing.T) {
	_, _, client := setupServer(t)
	cookie := loginCookie(t, client)

	c := &http.Client{
		Transport: client.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/admin/logout", nil)
	req.AddCookie(cookie)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after logout, got %d", resp.StatusCode)
	}

	for _, c := range resp.Cookies() {
		if c.Name == "ntx_session" && c.MaxAge < 0 {
			return
		}
	}
	t.Error("session cookie not cleared after logout")
}

func TestAllAdminRoutesRequireAuth(t *testing.T) {
	_, posts, baseClient := setupServer(t)
	p := createPost(t, posts, "test-auth", "Auth Test", false)

	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/posts"},
		{http.MethodGet, "/admin/posts/new"},
		{http.MethodPost, "/admin/posts"},
		{http.MethodGet, "/admin/posts/" + itoa(p.ID) + "/edit"},
		{http.MethodPost, "/admin/posts/" + itoa(p.ID)},
		{http.MethodPost, "/admin/logout"},
	}

	for _, rt := range routes {
		req, _ := http.NewRequest(rt.method, "http://example.com"+rt.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", rt.method, rt.path, err)
		}
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s %s: expected 303 redirect, got %d", rt.method, rt.path, resp.StatusCode)
		}
	}
}

func TestAdminCreatePost(t *testing.T) {
	_, posts, baseClient := setupServer(t)
	cookie := loginCookie(t, baseClient)

	client := &http.Client{
		Transport: baseClient.Transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/admin/posts", strings.NewReader(url.Values{
		"title":  {"New Post"},
		"slug":   {"new-post"},
		"body":   {"# Hello"},
		"tags":   {"go, testing"},
		"action": {"publish"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after create, got %d", resp.StatusCode)
	}

	p, err := posts.GetBySlug("new-post")
	if err != nil || p == nil {
		t.Fatal("post not found after create")
	}
	if !p.Published {
		t.Error("post should be published")
	}
	if len(p.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", p.Tags)
	}
}

func TestBlogPostMarkdownXSS(t *testing.T) {
	_, posts, client := setupServer(t)

	p := &models.Post{
		Slug:      "xss-test",
		Title:     "XSS Test",
		Body:      `safe content\n\n<script>alert('xss')</script>`,
		Published: true,
	}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.com/blog/xss-test")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<script>") {
		t.Error("raw <script> tag present in rendered output — XSS not stripped")
	}
}

// Multi-site: two sites in one server, isolated by Host

func TestTwoSitesIsolatedByHost(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)

	hashA, _ := auth.HashPassword("passA")
	hashB, _ := auth.HashPassword("passB")
	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashA},
		{Key: "b", SiteURL: "b.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashB},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{SessionSecret: "testsecretkey12345678901234567890", Dev: true, Sites: sites}
	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, rawPosts, session)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	postsA := rawPosts.ForSite(sites[0].ID)
	postsB := rawPosts.ForSite(sites[1].ID)
	if err := postsA.Create(&models.Post{Slug: "hello", Title: "Hello from A", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}
	if err := postsB.Create(&models.Post{Slug: "hello", Title: "Hello from B", Body: ".", Published: true}); err != nil {
		t.Fatal(err)
	}

	get := func(host, path string) string {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	bodyA := get("a.example.com", "/blog/hello")
	if !strings.Contains(bodyA, "Hello from A") {
		t.Errorf("expected site A's post title, got: %s", bodyA)
	}
	bodyB := get("b.example.com", "/blog/hello")
	if !strings.Contains(bodyB, "Hello from B") {
		t.Errorf("expected site B's post title, got: %s", bodyB)
	}
}

func TestSessionScopedToOneSiteRejectedOnAnother(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)

	hashA, _ := auth.HashPassword("passA")
	hashB, _ := auth.HashPassword("passB")
	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashA},
		{Key: "b", SiteURL: "b.example.com", TemplatePath: tmpDir, StaticPath: tmpDir, AboutSlug: "about", AdminPasswordHash: hashB},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{SessionSecret: "testsecretkey12345678901234567890", Dev: true, Sites: sites}
	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, rawPosts, session)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/login", strings.NewReader(url.Values{"password": {"passA"}}.Encode()))
	loginReq.Host = "a.example.com"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginResp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set")
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/posts", nil)
	req.Host = "b.example.com"
	req.AddCookie(cookies[0])
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected site A's session to be rejected on site B, got %d", resp.StatusCode)
	}
}

// TestServerNewRejectsSitesWithoutDatabaseID guards addendum Ruling 10: if
// EnsureSites was never run, every site's ID is the zero value, which
// equals auth.GlobalSiteID — and a site-scoped session would silently
// authenticate on every site. server.New must fail fast instead. This is
// the one test in this file that must NOT call models.EnsureSites.
func TestServerNewRejectsSitesWithoutDatabaseID(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})

	tmpDir := t.TempDir()
	setupTemplates(t, tmpDir)

	hash, _ := auth.HashPassword("testpass")
	sites := []config.Site{
		{
			Key:               "mysite",
			SiteURL:           "example.com",
			TemplatePath:      tmpDir,
			StaticPath:        tmpDir,
			AboutSlug:         "about-page",
			AdminPasswordHash: hash,
			// ID intentionally left at the zero value: EnsureSites was
			// never called.
		},
	}

	cfg := &config.Config{
		SessionSecret: "testsecretkey12345678901234567890",
		Dev:           true,
		Sites:         sites,
	}

	rawPosts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	_, err = New(cfg, rawPosts, session)
	if err == nil {
		t.Fatal("expected error for site with unpopulated database id, got nil")
	}
}

// helpers

func createPost(t *testing.T, s *models.PostStore, slug, title string, published bool, tags ...string) *models.Post {
	t.Helper()
	p := &models.Post{Slug: slug, Title: title, Body: ".", Published: published, Tags: tags}
	if err := s.Create(p); err != nil {
		t.Fatalf("createPost %q: %v", slug, err)
	}
	return p
}

func itoa(n int64) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(
		fmt.Sprintf("%d", n), " ", ""), "\n", ""))
}
