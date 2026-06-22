package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nerdtx/ntx/internal/config"
)

func (h *Handler) SectionPage(w http.ResponseWriter, r *http.Request, sec config.Section) {
	pageSlug := chi.URLParam(r, "page")

	var page *config.SectionPage
	for i := range sec.Pages {
		if sec.Pages[i].Slug == pageSlug {
			page = &sec.Pages[i]
			break
		}
	}
	if page == nil {
		h.notFound(w)
		return
	}

	tmplName := "sections/" + sec.Slug + "/" + pageSlug + "/index.html"
	if !h.templates.Has(tmplName) {
		h.notFound(w)
		return
	}

	data := make(map[string]interface{})
	for k, v := range sec.Vars {
		data[k] = v
	}
	if sec.Title != "" {
		data["SectionTitle"] = sec.Title
	}
	for k, v := range page.Vars {
		data[k] = v
	}
	if page.Title != "" {
		data["Title"] = page.Title
	}

	h.render(w, r, tmplName, data)
}
