package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitectx"
)

type TemplateCache struct {
	templates map[string]*template.Template
	path      string
	dev       bool
}

func NewTemplateCache(path string, dev bool) (*TemplateCache, error) {
	tc := &TemplateCache{path: path, dev: dev}
	if !dev {
		if err := tc.load(); err != nil {
			return nil, err
		}
	}
	return tc, nil
}

func (tc *TemplateCache) Has(name string) bool {
	if tc.dev {
		_, err := os.Stat(filepath.Join(tc.path, name))
		return err == nil
	}
	_, ok := tc.templates[name]
	return ok
}

func (tc *TemplateCache) Get(name string) (*template.Template, error) {
	if tc.dev {
		return tc.parse(name)
	}
	t, ok := tc.templates[name]
	if !ok {
		return nil, fmt.Errorf("template %q not found", name)
	}
	return t, nil
}

func (tc *TemplateCache) load() error {
	tc.templates = make(map[string]*template.Template)
	base := filepath.Join(tc.path, "base.html")

	err := filepath.Walk(tc.path, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(tc.path, path)
		if err != nil {
			return err
		}
		if rel == "base.html" {
			return nil
		}
		if filepath.Ext(path) != ".html" {
			return nil
		}
		t, err := template.New(filepath.Base(base)).Funcs(templateFuncs()).ParseFiles(base, path)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", rel, err)
		}
		tc.templates[rel] = t
		return nil
	})
	return err
}

func (tc *TemplateCache) parse(name string) (*template.Template, error) {
	base := filepath.Join(tc.path, "base.html")
	page := filepath.Join(tc.path, name)
	return template.New(filepath.Base(base)).Funcs(templateFuncs()).ParseFiles(base, page)
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"join": strings.Join,
	}
}

// siteHandler bundles the per-site resources a request needs once its
// site has been resolved: this site's config, its scoped post store, and
// its own template cache (which points at the shared global
// template/static paths if the site didn't override them — see Task 3's
// config defaulting).
type siteHandler struct {
	site      config.Site
	posts     *models.PostStore
	templates *TemplateCache
}

func (sh *siteHandler) siteData() map[string]interface{} {
	return map[string]interface{}{
		"SiteName":        sh.site.SiteName,
		"SiteURL":         sh.site.SiteURL,
		"SiteDescription": sh.site.SiteDescription,
	}
}

type Handler struct {
	session         *auth.SessionManager
	limiter         *loginLimiter
	sites           map[int64]*siteHandler
	ordered         []*siteHandler // same handlers as sites, in config order
	globalAdminHash string
}

// New builds a Handler serving every site in sites, backed by posts
// (an unscoped store — New calls posts.ForSite per site internally).
func New(sites []config.Site, posts *models.PostStore, session *auth.SessionManager, dev bool, globalAdminHash string) (*Handler, error) {
	// Ruling 10: h.sites is keyed by site.ID, so if EnsureSites never ran,
	// every site has ID 0 and they all collapse into one map entry, last
	// write wins -- every host would serve one site's posts. Persisted ids
	// start at 1, so ID == 0 always means "EnsureSites was not called",
	// never a real tenant. Both `serve` (via server.New) and `generate`
	// come through here, so this is the one place the guard lives.
	for i := range sites {
		if sites[i].ID == 0 {
			return nil, fmt.Errorf("site %q has no database id: models.EnsureSites must run before handlers.New", sites[i].Key)
		}
	}

	h := &Handler{
		session:         session,
		limiter:         newLoginLimiter(),
		sites:           make(map[int64]*siteHandler, len(sites)),
		globalAdminHash: globalAdminHash,
	}
	for _, site := range sites {
		tc, err := NewTemplateCache(site.TemplatePath, dev)
		if err != nil {
			return nil, fmt.Errorf("loading templates for site %q: %w", site.Key, err)
		}
		sh := &siteHandler{
			site:      site,
			posts:     posts.ForSite(site.ID),
			templates: tc,
		}
		h.sites[site.ID] = sh
		h.ordered = append(h.ordered, sh)
	}
	return h, nil
}

// current resolves the siteHandler for the request's already-resolved
// site (set by server's Host-routing middleware via sitectx). Returns
// nil if no site was resolved, which callers treat as a 404.
func (h *Handler) current(r *http.Request) *siteHandler {
	site := sitectx.From(r)
	if site == nil {
		return nil
	}
	return h.sites[site.ID]
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	t, err := sh.templates.Get(name)
	if err != nil {
		h.serverError(w, err)
		return
	}
	merged := sh.siteData()
	for k, v := range data {
		merged[k] = v
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, merged); err != nil {
		log.Printf("render %s: %v", name, err)
		h.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	log.Printf("server error: %v", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func (h *Handler) notFound(w http.ResponseWriter) {
	http.Error(w, "Not Found", http.StatusNotFound)
}
