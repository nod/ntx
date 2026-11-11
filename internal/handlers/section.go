package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/models"
)

// findPage looks up a section page in site's sections.
func findPage(site *models.Site, sectionSlug, pageSlug string) (*models.Section, *models.SectionPage) {
	for i := range site.Sections {
		sec := &site.Sections[i]
		if sec.Slug != sectionSlug {
			continue
		}
		for j := range sec.Pages {
			if sec.Pages[j].Slug == pageSlug {
				return sec, &sec.Pages[j]
			}
		}
	}
	return nil, nil
}

// sectionData merges section vars, then page vars (page wins), plus the
// titles, as template data.
func sectionData(sec *models.Section, page *models.SectionPage) map[string]interface{} {
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
	return data
}

// Section serves /<section>/<page> for sections the resolved site defines.
func (h *Handler) Section(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		sec, page := findPage(site, chi.URLParam(r, "section"), chi.URLParam(r, "page"))
		if page == nil {
			h.notFound(w)
			return
		}
		tmplName := "sections/" + sec.Slug + "/" + page.Slug + "/index.html"
		tc, err := h.templates(site)
		if err != nil {
			h.serverError(w, err)
			return
		}
		has, err := tc.Has(tmplName)
		if err != nil {
			h.serverError(w, fmt.Errorf("site %q: theme %q: %w", site.Key, site.Theme, err))
			return
		}
		if !has {
			h.notFound(w)
			return
		}
		h.render(w, site, tmplName, sectionData(sec, page))
	})
}
