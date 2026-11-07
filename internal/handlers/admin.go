package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/clientip"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitectx"
)

// landing is where a successful sign-in goes: the site's posts on a site
// host, the platform views on the platform host.
func landing(r *http.Request) string {
	if sitectx.From(r) != nil {
		return "/admin/posts"
	}
	return "/admin/platform/sites"
}

func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	if auth.ActorFrom(r) != nil {
		http.Redirect(w, r, landing(r), http.StatusSeeOther)
		return
	}
	h.admin(w, r, "login", nil)
}

// LoginPost signs root in. Only failures count against the client IP's
// budget, so a correct password is never refused because of someone else's
// guesses (the IP is the real client's; see clientip).
func (h *Handler) LoginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientip.IP(r)
	// Reserve counts the attempt as a failure before the password check,
	// so parallel guesses can't all get past the budget; a success gives
	// it back.
	if !h.d.Limiter.Reserve(ip) {
		h.admin(w, r, "login", map[string]any{"Error": "Too many attempts. Try again later."})
		return
	}
	if r.FormValue("username") != "root" || auth.CheckPassword(h.d.RootPasswordHash, r.FormValue("password")) != nil {
		h.admin(w, r, "login", map[string]any{"Error": "Invalid username or password."})
		return
	}
	h.d.Limiter.Release(ip)
	root, err := h.d.Users.EnsureRoot()
	if err != nil {
		h.serverError(w, err)
		return
	}
	var siteID *int64
	if site := sitectx.From(r); site != nil {
		siteID = &site.ID
	}
	if err := h.d.Auth.Login(w, root, siteID); err != nil {
		h.serverError(w, err)
		return
	}
	http.Redirect(w, r, landing(r), http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.d.Auth.Logout(w, r); err != nil {
		h.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (h *Handler) Posts(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		ps := h.posts(site)
		posts, err := ps.List(false)
		if err != nil {
			h.serverError(w, err)
			return
		}
		total, published, drafts, tagCount, err := ps.Stats()
		if err != nil {
			h.serverError(w, err)
			return
		}
		h.admin(w, r, "posts", map[string]any{
			"Posts": posts, "Total": total, "Published": published, "Drafts": drafts, "TagCount": tagCount,
		})
	})
}

func (h *Handler) NewPost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		h.admin(w, r, "editor", map[string]any{"Post": nil})
	})
}

func (h *Handler) CreatePost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		post := postFromForm(r)
		post.Published = r.FormValue("action") == "publish"
		if err := h.posts(site).Create(post); err != nil {
			h.admin(w, r, "editor", map[string]any{"Post": post, "Error": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
	})
}

func (h *Handler) EditPost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		post, ok := h.postFromParam(w, r, site)
		if !ok {
			return
		}
		h.admin(w, r, "editor", map[string]any{"Post": post})
	})
}

func (h *Handler) UpdatePost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		existing, ok := h.postFromParam(w, r, site)
		if !ok {
			return
		}
		ps := h.posts(site)
		action := r.FormValue("action")
		if action == "delete" {
			if err := ps.Delete(existing.ID); err != nil {
				h.serverError(w, err)
				return
			}
			http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
			return
		}

		updated := postFromForm(r)
		updated.ID = existing.ID
		switch action {
		case "publish":
			updated.Published = true
		case "unpublish":
			updated.Published = false
		default: // "save" keeps the current state
			updated.Published = existing.Published
		}
		if err := ps.Update(updated); err != nil {
			h.admin(w, r, "editor", map[string]any{"Post": updated, "Error": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
	})
}

// postFromParam loads the {id} post from site, writing a 404 or 500 and
// returning ok=false if it can't.
func (h *Handler) postFromParam(w http.ResponseWriter, r *http.Request, site *models.Site) (*models.Post, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.notFound(w)
		return nil, false
	}
	post, err := h.posts(site).GetByID(id)
	if err != nil {
		h.serverError(w, err)
		return nil, false
	}
	if post == nil {
		h.notFound(w)
		return nil, false
	}
	return post, true
}

func postFromForm(r *http.Request) *models.Post {
	var tags []string
	for _, t := range strings.Split(r.FormValue("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return &models.Post{
		Slug:  r.FormValue("slug"),
		Title: r.FormValue("title"),
		Body:  r.FormValue("body"),
		Tags:  tags,
	}
}
