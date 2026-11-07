package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/models"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := database.DB()
		sqlDB.Close()
	})
	return database
}

// writeTemplates lays down the minimum template set Generate walks. The post
// template renders the post title so generated output can be told apart per
// site; the directory doubles as static_path, which Generate copies verbatim.
func writeTemplates(t *testing.T, dir string) {
	t.Helper()
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("base.html", `<!DOCTYPE html><html><body>{{block "content" .}}{{end}}</body></html>`)
	write("home.html", `{{template "base.html" .}}{{define "content"}}home of {{.SiteName}}{{end}}`)
	write("blog/index.html", `{{template "base.html" .}}{{define "content"}}{{range .Posts}}{{.Title}} {{end}}{{end}}`)
	write("blog/post.html", `{{template "base.html" .}}{{define "content"}}{{.Post.Title}}{{end}}`)
}

// TestNewRejectsSiteWithoutDatabaseID guards Ruling 10 at the place the
// collapse actually happens. Handler.sites is keyed by site.ID, so a set of
// sites that never went through models.EnsureSites all carry ID 0 and fold
// into a single map entry. Both server.New and `generate` rely on this check.
func TestNewRejectsSiteWithoutDatabaseID(t *testing.T) {
	database := newTestDB(t)
	tmpDir := t.TempDir()
	writeTemplates(t, tmpDir)

	hash, _ := auth.HashPassword("testpass")
	sites := []config.Site{
		{
			Key:               "mysite",
			SiteURL:           "mysite.example.com",
			TemplatePath:      tmpDir,
			StaticPath:        tmpDir,
			AdminPasswordHash: hash,
			// ID intentionally left at the zero value.
		},
	}

	posts := models.NewPostStore(database)
	session := auth.NewSessionManager("testsecretkey12345678901234567890", false)

	if _, err := New(sites, posts, session, true, ""); err == nil {
		t.Fatal("expected an error for a site with no database id, got nil")
	}
}

// TestGenerateIsolatesSiteOutput covers the only shipped feature with a
// per-site output path: each site's pages must land under <out>/<key>/, and
// neither site's output may contain the other's content.
func TestGenerateIsolatesSiteOutput(t *testing.T) {
	database := newTestDB(t)
	tmpDir := t.TempDir()
	writeTemplates(t, tmpDir)

	hash, _ := auth.HashPassword("testpass")
	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com", SiteName: "Site A", TemplatePath: tmpDir, StaticPath: tmpDir, AdminPasswordHash: hash},
		{Key: "b", SiteURL: "b.example.com", SiteName: "Site B", TemplatePath: tmpDir, StaticPath: tmpDir, AdminPasswordHash: hash},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}

	rawPosts := models.NewPostStore(database)
	// Same slug on both sites, so the only thing distinguishing the two
	// outputs is which site's row the generator read.
	if err := rawPosts.ForSite(sites[0].ID).Create(&models.Post{
		Slug: "shared-slug", Title: "Only On A", Body: ".", Published: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rawPosts.ForSite(sites[1].ID).Create(&models.Post{
		Slug: "shared-slug", Title: "Only On B", Body: ".", Published: true,
	}); err != nil {
		t.Fatal(err)
	}

	session := auth.NewSessionManager("testsecretkey12345678901234567890", false)
	h, err := New(sites, rawPosts, session, true, "")
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	if err := h.Generate(outDir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, tc := range []struct {
		key   string
		mine  string
		other string
	}{
		{key: "a", mine: "Only On A", other: "Only On B"},
		{key: "b", mine: "Only On B", other: "Only On A"},
	} {
		postPage := filepath.Join(outDir, tc.key, "blog", "shared-slug", "index.html")
		body, err := os.ReadFile(postPage)
		if err != nil {
			t.Errorf("site %q: expected generated post at %s: %v", tc.key, postPage, err)
			continue
		}
		if !strings.Contains(string(body), tc.mine) {
			t.Errorf("site %q: generated post should contain %q, got: %s", tc.key, tc.mine, body)
		}

		// Nothing anywhere under this site's output may mention the other
		// site's post -- not the post page, not the blog index.
		siteOut := filepath.Join(outDir, tc.key)
		err = filepath.Walk(siteOut, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(content), tc.other) {
				rel, _ := filepath.Rel(outDir, path)
				t.Errorf("site %q leaked the other site's post %q into %s", tc.key, tc.other, rel)
			}
			return nil
		})
		if err != nil {
			t.Errorf("walking %s: %v", siteOut, err)
		}
	}

	// Each site's home page must render its own identity, which is what
	// proves the per-site template data went with the per-site output path.
	for _, tc := range []struct{ key, name string }{{"a", "Site A"}, {"b", "Site B"}} {
		home, err := os.ReadFile(filepath.Join(outDir, tc.key, "index.html"))
		if err != nil {
			t.Errorf("site %q: expected a generated home page: %v", tc.key, err)
			continue
		}
		if !strings.Contains(string(home), "home of "+tc.name) {
			t.Errorf("site %q: home page should name the site, got: %s", tc.key, home)
		}
	}
}

// TestGenerateRunsSitesInConfigOrder: when a later site fails, every site
// before it in the config must already have been written -- the same
// partial output on every run, not whatever order a map happens to yield.
func TestGenerateRunsSitesInConfigOrder(t *testing.T) {
	database := newTestDB(t)
	goodDir := t.TempDir()
	writeTemplates(t, goodDir)
	brokenDir := t.TempDir() // no templates at all: generating this site fails

	hash, _ := auth.HashPassword("testpass")
	sites := []config.Site{
		{Key: "a", SiteURL: "a.example.com", TemplatePath: goodDir, StaticPath: goodDir, AdminPasswordHash: hash},
		{Key: "b", SiteURL: "b.example.com", TemplatePath: goodDir, StaticPath: goodDir, AdminPasswordHash: hash},
		{Key: "c", SiteURL: "c.example.com", TemplatePath: goodDir, StaticPath: goodDir, AdminPasswordHash: hash},
		{Key: "broken", SiteURL: "broken.example.com", TemplatePath: brokenDir, StaticPath: brokenDir, AdminPasswordHash: hash},
	}
	if err := models.EnsureSites(database, sites); err != nil {
		t.Fatal(err)
	}
	session := auth.NewSessionManager("testsecretkey12345678901234567890", false)
	h, err := New(sites, models.NewPostStore(database), session, true, "")
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	if err := h.Generate(outDir); err == nil {
		t.Fatal("expected Generate to fail on the site with no templates")
	}
	for _, key := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(outDir, key, "index.html")); err != nil {
			t.Errorf("site %q precedes the failing site in config, so it must have been generated: %v", key, err)
		}
	}
}
