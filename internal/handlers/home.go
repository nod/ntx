package handlers

import (
	"net/http"
)

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "home.html", nil)
}
