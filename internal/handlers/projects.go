package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nod/ntx/internal/config"
)

// SectionPage serves a page within sectionSlug, resolved against the
// request's already-resolved site (server registers one route per unique
// section slug across all sites — which site's Section content applies
// is decided here, per request, not at route-registration time).
func (h *Handler) SectionPage(w http.ResponseWriter, r *http.Request, sectionSlug string) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, sh *siteHandler) {
		h.sectionPage(w, r, sh, sectionSlug)
	})
}

func (h *Handler) sectionPage(w http.ResponseWriter, r *http.Request, sh *siteHandler, sectionSlug string) {

	var sec *config.Section
	for i := range sh.site.Sections {
		if sh.site.Sections[i].Slug == sectionSlug {
			sec = &sh.site.Sections[i]
			break
		}
	}
	if sec == nil {
		h.notFound(w)
		return
	}

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
	if !sh.templates.Has(tmplName) {
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

	h.render(w, sh, tmplName, data)
}
