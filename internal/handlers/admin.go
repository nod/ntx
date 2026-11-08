package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/authz"
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

// postRow is one line of the posts list.
type postRow struct {
	models.Post
	Status string // draft, published or unpublished
	Author string
	// Pending: the post has changes waiting for a site admin; Review:
	// this actor can publish or discard them.
	Pending, Review bool
}

func postStatus(p *models.Post) string {
	switch {
	case p.Published:
		return "published"
	case p.LiveOnce():
		return "unpublished"
	}
	return "draft"
}

// Posts lists every post for site admins, and only their own for
// contributors.
func (h *Handler) Posts(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		a := auth.ActorFrom(r)
		ps := h.posts(site)
		var posts []models.Post
		var err error
		admin := authz.SiteAdmin(a)
		if admin {
			posts, err = ps.List(false)
		} else {
			posts, err = ps.ListByAuthor(a.User.ID)
		}
		if err != nil {
			h.serverError(w, err)
			return
		}
		var ids []int64
		for _, p := range posts {
			if p.AuthorID != nil {
				ids = append(ids, *p.AuthorID)
			}
		}
		authors, err := h.d.Users.GetByIDs(ids)
		if err != nil {
			h.serverError(w, err)
			return
		}
		pending, err := ps.PendingIDs()
		if err != nil {
			h.serverError(w, err)
			return
		}
		rows := make([]postRow, len(posts))
		for i, p := range posts {
			rows[i] = postRow{Post: p, Status: postStatus(&p), Author: "former member", Pending: pending[p.ID]}
			rows[i].Review = rows[i].Pending && authz.CanReviewChanges(a, &p)
			if p.AuthorID != nil {
				rows[i].Author = authorLabel(authors[*p.AuthorID])
			}
		}
		data := map[string]any{"Posts": rows, "ShowStats": admin}
		if admin {
			total, published, drafts, tagCount, err := ps.Stats()
			if err != nil {
				h.serverError(w, err)
				return
			}
			data["Total"], data["Published"], data["Drafts"], data["TagCount"] = total, published, drafts, tagCount
		}
		h.admin(w, r, "posts", data)
	})
}

// editor renders the post editor, offering only the actions the actor is
// allowed. A post with ID 0 is new. Mode "pending" is a contributor's own
// live-once post without can_publish: the form shows their pending
// changes (if any), the slug is fixed, and Save stores a revision.
func (h *Handler) editor(w http.ResponseWriter, r *http.Request, site *models.Site, post *models.Post, errMsg string) {
	a := auth.ActorFrom(r)
	data := map[string]any{"Post": post, "Error": errMsg, "Mode": "direct"}
	if post == nil || post.ID == 0 {
		data["CanPublish"], data["CanDelete"] = authz.CanPublishOwn(a), false
		h.admin(w, r, "editor", data)
		return
	}
	rev, err := h.posts(site).GetPending(post.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	data["Pending"] = rev
	// Base is the post as this editor shows it. A save that turns into
	// pending changes -- including a draft published while it was open --
	// compares against it to warn the reviewer.
	data["Base"] = post.UpdatedAt.Format(time.RFC3339Nano)
	if authz.SavesAsPending(a, post) {
		data["Mode"] = "pending"
		if rev != nil {
			shown := *post
			shown.Title, shown.Body, shown.Tags = rev.Title, rev.Body, rev.Tags
			data["Post"] = &shown
		}
	} else {
		data["CanPublish"], data["CanDelete"] = authz.CanPublishPost(a, post), authz.CanDeletePost(a, post)
		data["ReviewLink"] = authz.CanReviewChanges(a, post)
	}
	h.admin(w, r, "editor", data)
}

func (h *Handler) forbidden(w http.ResponseWriter, r *http.Request) {
	h.message(w, r, http.StatusForbidden, "not allowed", "You can't do that to this post.", "/admin/posts", "back to posts")
}

func (h *Handler) NewPost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		h.editor(w, r, site, nil, "")
	})
}

func (h *Handler) CreatePost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		a := auth.ActorFrom(r)
		post := postFromForm(r)
		post.Published = r.FormValue("action") == "publish"
		if post.Published && !authz.CanPublishOwn(a) {
			h.forbidden(w, r)
			return
		}
		post.AuthorID = &a.User.ID
		if err := h.posts(site).Create(post); err != nil {
			post.ID = 0
			h.editor(w, r, site, post, err.Error())
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
		a := auth.ActorFrom(r)
		if !authz.CanEditPost(a, post) && !authz.SavesAsPending(a, post) {
			h.forbidden(w, r)
			return
		}
		h.editor(w, r, site, post, "")
	})
}

// UpdatePost applies one editor action, each gated by the role table.
func (h *Handler) UpdatePost(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		existing, ok := h.postFromParam(w, r, site)
		if !ok {
			return
		}
		a := auth.ActorFrom(r)
		ps := h.posts(site)
		action := r.FormValue("action")
		if action == "delete" {
			if !authz.CanDeletePost(a, existing) {
				h.forbidden(w, r)
				return
			}
			if err := ps.Delete(existing.ID); err != nil {
				h.serverError(w, err)
				return
			}
			http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
			return
		}

		updated := postFromForm(r)
		updated.ID = existing.ID
		if authz.SavesAsPending(a, existing) {
			// The edit waits for a site admin; the post is not touched.
			if action != "save" {
				h.forbidden(w, r)
				return
			}
			// Without the base the editor sent, there's no telling what the
			// contributor saw: the zero time marks the revision stale.
			base, err := time.Parse(time.RFC3339Nano, r.FormValue("base"))
			if err != nil {
				base = time.Time{}
			}
			if _, err := ps.SavePending(existing.ID, a.User.ID, updated.Title, updated.Body, updated.Tags, base); err != nil {
				h.serverError(w, err)
				return
			}
			http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
			return
		}

		// Every other action saves the form's content too.
		if !authz.CanEditPost(a, existing) {
			h.forbidden(w, r)
			return
		}
		var err error
		switch action {
		case "publish", "unpublish":
			if !authz.CanPublishPost(a, existing) {
				h.forbidden(w, r)
				return
			}
			updated.Published = action == "publish"
			err = ps.Update(updated)
		default: // "save" changes content only, never the published state
			err = ps.UpdateContent(updated)
			updated.Published = existing.Published
		}
		if err != nil {
			updated.AuthorID, updated.FirstPublishedAt, updated.UpdatedAt = existing.AuthorID, existing.FirstPublishedAt, existing.UpdatedAt
			h.editor(w, r, site, updated, err.Error())
			return
		}
		http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
	})
}

// postFromParam loads the {id} post from site, writing a 404 or 500 and
// returning ok=false if it can't. A post the actor may not see is a 404,
// so contributors can't probe for others' drafts.
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
	if post == nil || !authz.CanSeePost(auth.ActorFrom(r), post) {
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
