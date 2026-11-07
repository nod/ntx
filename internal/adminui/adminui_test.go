package adminui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var testPages = []string{"login", "posts", "editor", "settings", "platform_sites", "platform_site_form", "platform_site_delete"}

func TestEveryPageRenders(t *testing.T) {
	for _, p := range testPages {
		rec := httptest.NewRecorder()
		Render(rec, p, map[string]any{"Actor": nil, "Site": nil})
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, body: %s", p, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", p, ct)
		}
		if !strings.Contains(rec.Body.String(), "/admin/assets/admin.css") {
			t.Errorf("%s: page must use the shared layout", p)
		}
	}
}

// The CSP is default-src 'self': inline scripts and handlers would be
// blocked, so every bit of admin JS must come from /admin/assets/.
func TestNoInlineScripts(t *testing.T) {
	inline := regexp.MustCompile(`<script(?:\s[^>]*)?>`)
	err := fs.WalkDir(files, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		for _, tag := range inline.FindAllString(string(b), -1) {
			if !strings.Contains(tag, "src=") {
				t.Errorf("%s: inline <script> without src: %s", path, tag)
			}
		}
		if regexp.MustCompile(`\son[a-z]+=`).Match(b) {
			t.Errorf("%s: inline event handler attribute", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAssetsServed(t *testing.T) {
	h := http.StripPrefix("/admin/assets/", Assets())
	for _, f := range []string{"admin.js", "admin.css"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/assets/"+f, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", f, rec.Code)
		}
	}
}

func TestUnknownPageIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "nope", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unknown page: status %d", rec.Code)
	}
}

type unsavedPost struct {
	ID          int64
	Title, Slug string
	Body        string
	Tags        []string
}

// When a new post fails to save (say, a bad slug), the editor comes back
// with the typed post, which has no ID yet. It must still post to the
// create route, or fixing the slug and resubmitting 404s and loses the text.
func TestEditorForUnsavedPostPostsToCreate(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "editor", map[string]any{"Post": &unsavedPost{Title: "Draft", Slug: "Bad Slug", Body: "long text"}})
	body := rec.Body.String()
	if !strings.Contains(body, `action="/admin/posts"`) {
		t.Errorf("an unsaved post's form must post to /admin/posts: %s", body)
	}
	if strings.Contains(body, "/admin/posts/0") || strings.Contains(body, `value="delete"`) {
		t.Errorf("an unsaved post must not get an edit action or a delete button: %s", body)
	}
	if !strings.Contains(body, "long text") {
		t.Error("the typed text must be kept")
	}

	rec = httptest.NewRecorder()
	Render(rec, "editor", map[string]any{"Post": &unsavedPost{ID: 7, Title: "Saved", Slug: "saved"}, "CanDelete": true, "CanPublish": true})
	if !strings.Contains(rec.Body.String(), `action="/admin/posts/7"`) || !strings.Contains(rec.Body.String(), `value="delete"`) {
		t.Errorf("a saved post's form must post to its own route with delete: %s", rec.Body.String())
	}
}
