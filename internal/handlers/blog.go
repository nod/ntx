package handlers

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
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
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	tag := r.URL.Query().Get("tag")

	var posts interface{}
	var err error

	if tag != "" {
		posts, err = sh.posts.ListByTag(tag)
	} else {
		posts, err = sh.posts.List(true)
	}
	if err != nil {
		h.serverError(w, err)
		return
	}

	allTags, err := sh.posts.AllTags()
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, "blog/index.html", map[string]interface{}{
		"Posts":     posts,
		"AllTags":   allTags,
		"ActiveTag": tag,
	})
}

func (h *Handler) BlogPost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	slug := chi.URLParam(r, "slug")
	post, err := sh.posts.GetBySlug(slug)
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

	h.render(w, r, "blog/post.html", map[string]interface{}{
		"Post":     post,
		"Rendered": rendered,
	})
}
