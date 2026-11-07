package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/themes"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

func renderMarkdown(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func (h *Handler) BlogIndex(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		ps := h.posts(site)
		tag := r.URL.Query().Get("tag")
		var posts []models.Post
		var err error
		if tag != "" {
			posts, err = ps.ListByTag(tag)
		} else {
			posts, err = ps.List(true)
		}
		if err != nil {
			h.serverError(w, err)
			return
		}
		allTags, err := ps.AllTags()
		if err != nil {
			h.serverError(w, err)
			return
		}
		h.render(w, site, "blog/index.html", map[string]interface{}{
			"Posts":     posts,
			"AllTags":   allTags,
			"ActiveTag": tag,
		})
	})
}

func (h *Handler) BlogPost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		post, err := h.posts(site).GetBySlug(chi.URLParam(r, "slug"))
		if err != nil {
			h.serverError(w, err)
			return
		}
		if post == nil || !post.Published {
			h.notFound(w)
			return
		}
		rendered, err := renderMarkdown(post.Body)
		if err != nil {
			h.serverError(w, err)
			return
		}
		h.render(w, site, "blog/post.html", map[string]interface{}{
			"Post":     post,
			"Rendered": rendered,
		})
	})
}

// Static serves /static/* from the site's theme.
func (h *Handler) Static(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		dir, err := themes.Dir(h.d.ThemesPath, site.Theme)
		if err != nil {
			h.notFound(w)
			return
		}
		http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(dir, "static")))).ServeHTTP(w, r)
	})
}
