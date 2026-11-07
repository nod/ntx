package handlers

import (
	"net/http"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, h.home)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request, sh *siteHandler) {
	h.render(w, sh, "home.html", nil)
}
