package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These run in production mode (newEnv's Dev is false): theme edits on disk
// must show without a restart.

// editTheme writes a file in theme t1 and moves its mtime forward by step
// seconds, so the change is visible even on filesystems with coarse mtimes.
func (e *env) editTheme(rel, content string, step int) {
	e.t.Helper()
	full := filepath.Join(e.themes, "t1", rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		e.t.Fatal(err)
	}
	at := time.Now().Add(time.Duration(step) * time.Second)
	if err := os.Chtimes(full, at, at); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) home() (int, string) {
	e.t.Helper()
	rec := e.do(http.MethodGet, "a.example.com", "/", nil, nil)
	return rec.Code, rec.Body.String()
}

func TestThemePageEditShowsWithoutRestart(t *testing.T) {
	e := newEnv(t)
	if code, body := e.home(); code != http.StatusOK || !strings.Contains(body, "home of Site A") {
		t.Fatalf("home before edit: %d %s", code, body)
	}
	e.editTheme("templates/home.html", `{{template "base.html" .}}{{define "content"}}welcome to {{.SiteName}}{{end}}`, 1)
	if code, body := e.home(); code != http.StatusOK || !strings.Contains(body, "welcome to Site A") {
		t.Fatalf("home after edit: %d %s", code, body)
	}
}

func TestThemeBaseEditShowsWithoutRestart(t *testing.T) {
	e := newEnv(t)
	e.home()
	e.editTheme("templates/base.html", `<!DOCTYPE html><html><body class="v2">{{block "content" .}}{{end}}</body></html>`, 1)
	if code, body := e.home(); code != http.StatusOK || !strings.Contains(body, `class="v2"`) {
		t.Fatalf("home after base edit: %d %s", code, body)
	}
}

func (e *env) hasTemplate(name string) bool {
	e.t.Helper()
	tc, err := e.h.templates(e.siteA)
	if err != nil {
		e.t.Fatal(err)
	}
	has, err := tc.Has(name)
	if err != nil {
		e.t.Fatal(err)
	}
	return has
}

func TestThemeAddedAndRemovedTemplates(t *testing.T) {
	e := newEnv(t)
	const name = "sections/notes/first/index.html"
	if e.hasTemplate(name) {
		t.Fatal("template exists before it was written")
	}
	e.editTheme("templates/"+name, `{{template "base.html" .}}{{define "content"}}notes{{end}}`, 1)
	if !e.hasTemplate(name) {
		t.Fatal("a template added on disk is not picked up")
	}
	if err := os.Remove(filepath.Join(e.themes, "t1", "templates", name)); err != nil {
		t.Fatal(err)
	}
	if e.hasTemplate(name) {
		t.Fatal("a template removed on disk is still served")
	}
}

// A section page on a broken theme is a server error, not a 404.
func TestThemeBrokenSectionPageIsServerError(t *testing.T) {
	e := newEnv(t)
	form := map[string][]string{"name": {"Site A"}, "sections": {`[{"slug":"projects","title":"Projects","pages":[{"slug":"one","title":"One"}]}]`}}
	if rec := e.do(http.MethodPost, "a.example.com", "/admin/settings", form, e.login("a.example.com")); rec.Code != http.StatusSeeOther {
		t.Fatalf("saving sections: %d %s", rec.Code, rec.Body.String())
	}
	e.editTheme("templates/home.html", `{{template "base.html" .}}{{define "content"}}{{.SiteName`, 1)
	if rec := e.do(http.MethodGet, "a.example.com", "/projects/one", nil, nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("section page on a broken theme: %d %s", rec.Code, rec.Body.String())
	}
}

func TestThemeDeletedStopsServing(t *testing.T) {
	e := newEnv(t)
	e.home()
	if err := os.RemoveAll(filepath.Join(e.themes, "t1")); err != nil {
		t.Fatal(err)
	}
	if code, body := e.home(); code != http.StatusInternalServerError || strings.Contains(body, "home of") {
		t.Fatalf("home after deleting the theme: %d %s", code, body)
	}
}

// A broken edit is an error until it's fixed; the fix shows without a
// restart too.
func TestThemeBrokenEditThenFixed(t *testing.T) {
	e := newEnv(t)
	e.home()
	e.editTheme("templates/home.html", `{{template "base.html" .}}{{define "content"}}{{.SiteName`, 1)
	if code, _ := e.home(); code != http.StatusInternalServerError {
		t.Fatalf("home with a broken template: %d", code)
	}
	e.editTheme("templates/home.html", `{{template "base.html" .}}{{define "content"}}fixed {{.SiteName}}{{end}}`, 2)
	if code, body := e.home(); code != http.StatusOK || !strings.Contains(body, "fixed Site A") {
		t.Fatalf("home after the fix: %d %s", code, body)
	}
}

// Pages render while the theme is edited; run with -race.
func TestThemeEditsDuringRequests(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if code, body := e.home(); code != http.StatusOK {
					t.Errorf("home during edits: %d %s", code, body)
					return
				}
			}
		}()
	}
	for i := 1; i <= 20; i++ {
		e.editTheme("templates/home.html", fmt.Sprintf(`{{template "base.html" .}}{{define "content"}}v%d {{.SiteName}}{{end}}`, i), i)
	}
	close(stop)
	wg.Wait()
	if code, body := e.home(); code != http.StatusOK || !strings.Contains(body, "v20 Site A") {
		t.Fatalf("home after edits: %d %s", code, body)
	}
}
