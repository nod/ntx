package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitectx"
	"github.com/nod/ntx/internal/themes"
)

func withSite(r *http.Request, site *models.Site) *http.Request { return sitectx.With(r, site) }

func (h *Handler) themeNames() []string {
	names, err := themes.List(h.d.ThemesPath)
	if err != nil {
		return nil
	}
	return names
}

// siteByKey finds the {key} site, or writes a 404.
func (h *Handler) siteByKey(w http.ResponseWriter, r *http.Request) (*models.Site, bool) {
	key := chi.URLParam(r, "key")
	for _, s := range h.d.Registry.All() {
		if s.Key == key {
			return &s, true
		}
	}
	h.notFound(w)
	return nil, false
}

func siteFromForm(r *http.Request) *models.Site {
	return &models.Site{
		Key:         r.FormValue("key"),
		Hostname:    r.FormValue("hostname"),
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		AboutSlug:   r.FormValue("about_slug"),
		Theme:       r.FormValue("theme"),
	}
}

func (h *Handler) PlatformSites(w http.ResponseWriter, r *http.Request) {
	h.admin(w, r, "platform_sites", map[string]any{"Sites": h.d.Registry.All(), "Themes": h.themeNames()})
}

func (h *Handler) PlatformNewSite(w http.ResponseWriter, r *http.Request) {
	h.admin(w, r, "platform_site_form", map[string]any{"Form": &models.Site{AboutSlug: "about"}, "Themes": h.themeNames(), "IsNew": true})
}

// saveSite runs a create or update, re-showing the form on a problem with
// what was entered and returning ok=false.
func (h *Handler) saveSite(w http.ResponseWriter, r *http.Request, form *models.Site, isNew bool, save func() error) bool {
	names := h.themeNames()
	show := func(msg string) {
		h.admin(w, r, "platform_site_form", map[string]any{"Form": form, "Themes": names, "IsNew": isNew, "Error": msg})
	}
	if !slices.Contains(names, form.Theme) {
		show(fmt.Sprintf("theme %q is not available; pick one from the list", form.Theme))
		return false
	}
	if err := save(); err != nil {
		var v *models.ValidationError
		if errors.As(err, &v) {
			show(v.Msg)
			return false
		}
		h.serverError(w, err)
		return false
	}
	if err := h.d.Registry.Reload(); err != nil {
		h.serverError(w, err)
		return false
	}
	return true
}

func (h *Handler) PlatformCreateSite(w http.ResponseWriter, r *http.Request) {
	form := siteFromForm(r)
	if h.saveSite(w, r, form, true, func() error { return h.d.Sites.Create(form, h.d.PlatformHostname) }) {
		http.Redirect(w, r, "/admin/platform/sites", http.StatusSeeOther)
	}
}

func (h *Handler) PlatformEditSite(w http.ResponseWriter, r *http.Request) {
	site, ok := h.siteByKey(w, r)
	if !ok {
		return
	}
	h.admin(w, r, "platform_site_form", map[string]any{"Form": site, "Themes": h.themeNames(), "IsNew": false})
}

func (h *Handler) PlatformUpdateSite(w http.ResponseWriter, r *http.Request) {
	site, ok := h.siteByKey(w, r)
	if !ok {
		return
	}
	form := siteFromForm(r)
	form.ID, form.Key = site.ID, site.Key
	if h.saveSite(w, r, form, false, func() error { return h.d.Sites.Update(form, h.d.PlatformHostname) }) {
		http.Redirect(w, r, "/admin/platform/sites", http.StatusSeeOther)
	}
}

func (h *Handler) PlatformDeleteSiteForm(w http.ResponseWriter, r *http.Request) {
	site, ok := h.siteByKey(w, r)
	if !ok {
		return
	}
	h.admin(w, r, "platform_site_delete", map[string]any{"Target": site})
}

// PlatformDeleteSite permanently deletes a site once its key is typed.
func (h *Handler) PlatformDeleteSite(w http.ResponseWriter, r *http.Request) {
	site, ok := h.siteByKey(w, r)
	if !ok {
		return
	}
	if r.FormValue("confirm") != site.Key {
		h.admin(w, r, "platform_site_delete", map[string]any{"Target": site, "Error": fmt.Sprintf("Type %q exactly to delete this site.", site.Key)})
		return
	}
	if err := h.d.Sites.Delete(site.ID); err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.d.Registry.Reload(); err != nil {
		h.serverError(w, err)
		return
	}
	// Deleting the site you're signed in on also deleted your session.
	if cur := sitectx.From(r); cur != nil && cur.ID == site.ID {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/platform/sites", http.StatusSeeOther)
}
