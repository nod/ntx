package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nod/ntx/internal/models"
)

func sectionsJSON(secs []models.Section) string {
	if len(secs) == 0 {
		return "[]"
	}
	b, err := json.MarshalIndent(secs, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		h.admin(w, r, "settings", map[string]any{"SectionsJSON": sectionsJSON(site.Sections)})
	})
}

// SettingsPost saves the site's name, description, about slug and
// sections. Everything is parsed and validated before anything is written,
// so a rejected save changes nothing.
func (h *Handler) SettingsPost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		raw := r.FormValue("sections")
		fail := func(msg string) {
			// Re-show what was typed, on top of the stored site.
			shown := *site
			shown.Name, shown.Description, shown.AboutSlug = r.FormValue("name"), r.FormValue("description"), r.FormValue("about_slug")
			h.admin(w, withSite(r, &shown), "settings", map[string]any{"SectionsJSON": raw, "Error": msg})
		}

		var secs []models.Section
		if strings.TrimSpace(raw) != "" {
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&secs); err != nil {
				fail("sections: not valid JSON for a list of sections: " + err.Error())
				return
			}
		}

		// SetSections validates everything before it writes, so it goes
		// first; the remaining fields can't fail validation (hostname and
		// theme are unchanged here).
		if err := h.d.Sites.SetSections(site.ID, secs); err != nil {
			var v *models.ValidationError
			if errors.As(err, &v) {
				fail(v.Msg)
				return
			}
			h.serverError(w, err)
			return
		}
		updated := *site
		updated.Name = r.FormValue("name")
		updated.Description = r.FormValue("description")
		updated.AboutSlug = r.FormValue("about_slug")
		if err := h.d.Sites.Update(&updated, h.d.PlatformHostname); err != nil {
			var v *models.ValidationError
			if errors.As(err, &v) {
				fail(v.Msg)
				return
			}
			h.serverError(w, err)
			return
		}
		if err := h.d.Registry.Reload(); err != nil {
			h.serverError(w, err)
			return
		}
		http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
	})
}
