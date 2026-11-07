package handlers

import (
	"net/http"

	"github.com/nod/ntx/internal/models"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		h.render(w, site, "home.html", nil)
	})
}

func (h *Handler) About(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		http.Redirect(w, r, "/blog/"+site.AboutSlug, http.StatusFound)
	})
}
