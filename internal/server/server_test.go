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

	"github.com/nerdtx/ntx/internal/auth"
	"github.com/nerdtx/ntx/internal/config"
	"github.com/nerdtx/ntx/internal/db"
	"github.com/nerdtx/ntx/internal/models"
)

func setupServer(t *testing.T) (*httptest.Server, *models.PostStore) {
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

	hash, _ := auth.HashPassword("testpass")
	cfg := &config.Config{
		TemplatePath:      tmpDir,
		StaticPath:        tmpDir,
		AboutSlug:         "about-page",
		AdminPasswordHash: hash,
		SessionSecret:     "testsecretkey12345678901234567890",
		Dev:               true,
	}

	posts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, posts, session)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, posts
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

func setupServerWithSections(t *testing.T) (*httptest.Server, *models.PostStore) {
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

	// section template
	sectionTmpl := filepath.Join(tmpDir, "sections/projects/one/index.html")
	os.MkdirAll(filepath.Dir(sectionTmpl), 0755)
	os.WriteFile(sectionTmpl, []byte(`{{template "base.html" .}}{{define "content"}}project-one{{end}}`), 0644)

	hash, _ := auth.HashPassword("testpass")
	cfg := &config.Config{
		TemplatePath:      tmpDir,
		StaticPath:        tmpDir,
		AboutSlug:         "about-page",
		AdminPasswordHash: hash,
		SessionSecret:     "testsecretkey12345678901234567890",
		Dev:               true,
		Sections: []config.Section{
			{
				Slug:  "projects",
				Title: "Projects",
				Pages: []config.SectionPage{
					{Slug: "one", Title: "Project One"},
				},
			},
		},
	}

	posts := models.NewPostStore(database)
	session := auth.NewSessionManager(cfg.SessionSecret, false)

	srv, err := New(cfg, posts, session)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, posts
}

func TestHomeRoute(t *testing.T) {
	ts, _ := setupServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAboutRedirect(t *testing.T) {
	ts, _ := setupServer(t)
	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/about")
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
	ts, _ := setupServer(t)
	resp, err := http.Get(ts.URL + "/blog")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBlogPost404(t *testing.T) {
	ts, _ := setupServer(t)
	resp, err := http.Get(ts.URL + "/blog/no-such-post")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestBlogPostDraftHidden(t *testing.T) {
	ts, posts := setupServer(t)

	p := &models.Post{Slug: "secret-draft", Title: "Secret", Body: ".", Published: false}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/blog/secret-draft")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("draft should return 404, got %d", resp.StatusCode)
	}
}

func TestBlogPostPublished(t *testing.T) {
	ts, posts := setupServer(t)

	p := &models.Post{Slug: "my-post", Title: "My Post", Body: "# Hello", Published: true}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/blog/my-post")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSectionPage(t *testing.T) {
	ts, _ := setupServerWithSections(t)
	resp, err := http.Get(ts.URL + "/projects/one")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSectionPageNotInConfig(t *testing.T) {
	ts, _ := setupServerWithSections(t)
	resp, err := http.Get(ts.URL + "/projects/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSectionUnknownNamespace(t *testing.T) {
	ts, _ := setupServer(t)
	resp, err := http.Get(ts.URL + "/projects/one")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unconfigured section, got %d", resp.StatusCode)
	}
}

func TestAdminLoginRequired(t *testing.T) {
	ts, _ := setupServer(t)
	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/admin/posts")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", resp.StatusCode)
	}
}

func TestAdminLoginFlow(t *testing.T) {
	ts, _ := setupServer(t)
	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := client.PostForm(ts.URL+"/admin/login", url.Values{
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

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/posts", nil)
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
	ts, _ := setupServer(t)

	resp, err := http.PostForm(ts.URL+"/admin/login", url.Values{
		"password": {"wrongpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with error form, got %d", resp.StatusCode)
	}
}

// loginCookie logs in and returns the session cookie. Fails the test if login fails.
func loginCookie(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/admin/login", url.Values{"password": {"testpass"}})
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

// #6: Blog tag filter
func TestBlogIndexTagFilter(t *testing.T) {
	ts, posts := setupServer(t)

	_ = createPost(t, posts, "go-post", "Go Post", true, "go")
	_ = createPost(t, posts, "rust-post", "Rust Post", true, "rust")

	resp, err := http.Get(ts.URL + "/blog?tag=go")
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

// #7: Admin logout clears session
func TestAdminLogout(t *testing.T) {
	ts, _ := setupServer(t)
	cookie := loginCookie(t, ts)

	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/logout", nil)
	req.AddCookie(cookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected redirect after logout, got %d", resp.StatusCode)
	}

	// Session cookie should be cleared (MaxAge < 0)
	for _, c := range resp.Cookies() {
		if c.Name == "ntx_session" && c.MaxAge < 0 {
			return
		}
	}
	t.Error("session cookie not cleared after logout")
}

// #8: All protected admin routes redirect without auth
func TestAllAdminRoutesRequireAuth(t *testing.T) {
	ts, posts := setupServer(t)
	p := createPost(t, posts, "test-auth", "Auth Test", false)

	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

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

	for _, r := range routes {
		req, _ := http.NewRequest(r.method, ts.URL+r.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", r.method, r.path, err)
		}
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s %s: expected 303 redirect, got %d", r.method, r.path, resp.StatusCode)
		}
	}
}

// #9: Admin create post via HTTP
func TestAdminCreatePost(t *testing.T) {
	ts, posts := setupServer(t)
	cookie := loginCookie(t, ts)

	client := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/admin/posts", strings.NewReader(url.Values{
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

// #10: Markdown XSS — raw HTML stripped now that WithUnsafe is removed
func TestBlogPostMarkdownXSS(t *testing.T) {
	ts, posts := setupServer(t)

	p := &models.Post{
		Slug:      "xss-test",
		Title:     "XSS Test",
		Body:      `safe content\n\n<script>alert('xss')</script>`,
		Published: true,
	}
	if err := posts.Create(p); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/blog/xss-test")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<script>") {
		t.Error("raw <script> tag present in rendered output — XSS not stripped")
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

