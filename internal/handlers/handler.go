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

	"github.com/nerdtx/ntx/internal/auth"
	"github.com/nerdtx/ntx/internal/config"
	"github.com/nerdtx/ntx/internal/models"
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

type Handler struct {
	cfg       *config.Config
	posts     *models.PostStore
	session   *auth.SessionManager
	templates *TemplateCache
}

func New(cfg *config.Config, posts *models.PostStore, session *auth.SessionManager) (*Handler, error) {
	tc, err := NewTemplateCache(cfg.TemplatePath, cfg.Dev)
	if err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}
	return &Handler{
		cfg:       cfg,
		posts:     posts,
		session:   session,
		templates: tc,
	}, nil
}

func (h *Handler) siteData() map[string]interface{} {
	return map[string]interface{}{
		"SiteName":        h.cfg.SiteName,
		"SiteURL":         h.cfg.SiteURL,
		"SiteDescription": h.cfg.SiteDescription,
	}
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	t, err := h.templates.Get(name)
	if err != nil {
		h.serverError(w, err)
		return
	}
	merged := h.siteData()
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
