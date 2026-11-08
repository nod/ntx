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
	"sync"
	"time"

	"github.com/nod/ntx/internal/adminui"
	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/authz"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/people"
	"github.com/nod/ntx/internal/sitectx"
	"github.com/nod/ntx/internal/sitereg"
	"github.com/nod/ntx/internal/sso"
	"github.com/nod/ntx/internal/themes"
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

// DefaultProviderTimeout is the per-request budget for the sign-in
// provider. It must leave time to write the error page inside the server's
// write timeout (10s), or a hung provider gets a dropped connection
// instead of "couldn't reach the sign-in provider".
const DefaultProviderTimeout = 6 * time.Second

// Deps is everything the handlers need.
type Deps struct {
	Sites       *models.SiteStore
	Posts       *models.PostStore // unscoped; handlers call ForSite
	Users       *models.UserStore
	Memberships *models.MembershipStore
	Identities  *models.IdentityStore
	Invites     *models.InviteStore
	Sessions    *models.SessionStore
	People      *people.Service
	SSO         *sso.Client
	// IssuerFor maps a site's provider to its configured issuer; nil means
	// sso.Issuer. Tests point it at a fake provider.
	IssuerFor func(providerType, domain string) string
	// ProviderTimeout bounds all talk with the sign-in provider in one
	// request (discovery, code exchange, keys); 0 means
	// DefaultProviderTimeout.
	ProviderTimeout time.Duration
	// SessionSecret signs the ntx_oauth flow cookie; EncryptionKey opens
	// provider client secrets.
	SessionSecret    []byte
	EncryptionKey    []byte
	Registry         *sitereg.Registry
	Auth             *auth.Manager
	Limiter          *auth.Limiter
	ThemesPath       string
	PlatformHostname string
	RootPasswordHash string
	Dev              bool
}

type Handler struct {
	d Deps

	mu     sync.Mutex
	caches map[string]*TemplateCache // by theme name
}

func New(d Deps) *Handler {
	return &Handler{d: d, caches: make(map[string]*TemplateCache)}
}

// serve hands the request's resolved site to fn, or 404s if there is none
// (the platform host has no site). It is the one place handlers deal with
// an unresolved site.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, *models.Site)) {
	site := sitectx.From(r)
	if site == nil {
		h.notFound(w)
		return
	}
	fn(w, r, site)
}

func (h *Handler) posts(site *models.Site) *models.PostStore { return h.d.Posts.ForSite(site.ID) }

// templates returns the template cache for site's theme. Sites on the same
// theme share one cache, built on first use.
func (h *Handler) templates(site *models.Site) (*TemplateCache, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if tc, ok := h.caches[site.Theme]; ok {
		return tc, nil
	}
	dir, err := themes.Dir(h.d.ThemesPath, site.Theme)
	if err != nil {
		return nil, err
	}
	tc, err := NewTemplateCache(filepath.Join(dir, "templates"), h.d.Dev)
	if err != nil {
		return nil, fmt.Errorf("loading theme %q: %w", site.Theme, err)
	}
	h.caches[site.Theme] = tc
	return tc, nil
}

func siteData(site *models.Site) map[string]interface{} {
	return map[string]interface{}{
		"SiteName":        site.Name,
		"SiteURL":         site.Hostname,
		"SiteDescription": site.Description,
	}
}

// renderToBytes renders a theme template for site with the site's data
// merged in.
func (h *Handler) renderToBytes(site *models.Site, name string, data map[string]interface{}) ([]byte, error) {
	tc, err := h.templates(site)
	if err != nil {
		return nil, err
	}
	t, err := tc.Get(name)
	if err != nil {
		return nil, err
	}
	merged := siteData(site)
	for k, v := range data {
		merged[k] = v
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, merged); err != nil {
		return nil, fmt.Errorf("executing %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// render writes a public page from the site's theme. A missing theme or
// template is a server error, logged; admin pages don't depend on themes,
// so root can still sign in and fix it.
func (h *Handler) render(w http.ResponseWriter, site *models.Site, name string, data map[string]interface{}) {
	content, err := h.renderToBytes(site, name, data)
	if err != nil {
		h.serverError(w, fmt.Errorf("site %q: %w", site.Key, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// admin renders an embedded admin page with the shared keys filled in.
func (h *Handler) admin(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	h.adminStatus(w, r, http.StatusOK, page, data)
}

func (h *Handler) adminStatus(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	site := sitectx.From(r)
	actor := auth.ActorFrom(r)
	data["Actor"] = actor
	data["Site"] = site
	data["IsSiteAdmin"] = site != nil && authz.SiteAdmin(actor)
	if site != nil {
		data["ProviderName"] = providerName(site.ProviderType)
	} else {
		data["ProviderName"] = ""
	}
	if _, ok := data["Error"]; !ok {
		data["Error"] = ""
	}
	adminui.RenderStatus(w, status, page, data)
}

// message renders a plain admin page with a title, a sentence and an
// optional link.
func (h *Handler) message(w http.ResponseWriter, r *http.Request, status int, title, msg, linkHref, linkText string) {
	h.adminStatus(w, r, status, "message", map[string]any{"Title": title, "Message": msg, "LinkHref": linkHref, "LinkText": linkText})
}

func providerName(providerType string) string {
	switch providerType {
	case models.ProviderGoogle:
		return "Google"
	case models.ProviderAuth0:
		return "Auth0"
	}
	return ""
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	log.Printf("server error: %v", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func (h *Handler) notFound(w http.ResponseWriter) {
	http.Error(w, "Not Found", http.StatusNotFound)
}

// RequireSiteAdmin gates site-admin pages: anonymous requests go to the
// sign-in page, the platform host has no site pages, and a signed-in actor
// without site-admin powers gets 403.
func (h *Handler) RequireSiteAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := auth.ActorFrom(r)
		switch {
		case sitectx.From(r) == nil:
			h.notFound(w)
		case a == nil:
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		case !authz.SiteAdmin(a):
			http.Error(w, "Forbidden", http.StatusForbidden)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// RequireMember gates the pages every member of the site may open (posts):
// anonymous requests go to sign-in, the platform host has none, and a
// signed-in actor with no part in this site gets 403.
func (h *Handler) RequireMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := auth.ActorFrom(r)
		switch {
		case sitectx.From(r) == nil:
			h.notFound(w)
		case a == nil:
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		case !authz.SiteMember(a):
			http.Error(w, "Forbidden", http.StatusForbidden)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// RequirePlatform gates the platform views to root and platform admins.
func (h *Handler) RequirePlatform(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := auth.ActorFrom(r)
		switch {
		case a == nil:
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		case !a.IsPlatform():
			http.Error(w, "Forbidden", http.StatusForbidden)
		default:
			next.ServeHTTP(w, r)
		}
	})
}
