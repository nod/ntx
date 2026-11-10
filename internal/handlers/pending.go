package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/authz"
	"github.com/nod/ntx/internal/models"
)

// authorLabel names a post's or revision's author in admin lists.
func authorLabel(u *models.User) string {
	switch {
	case u == nil:
		return "former member"
	case u.Name != "":
		return u.Name
	case u.Email != "":
		return u.Email
	}
	return "root"
}

// reviewTarget loads the {id} post for the pending-changes routes: 404 if
// the actor can't see it, 403 if they can't review changes to it.
func (h *Handler) reviewTarget(w http.ResponseWriter, r *http.Request, site *models.Site) (*models.Post, bool) {
	post, ok := h.postFromParam(w, r, site)
	if !ok {
		return nil, false
	}
	if !authz.CanReviewChanges(auth.ActorFrom(r), post) {
		h.forbidden(w, r)
		return nil, false
	}
	return post, true
}

// pendingPage renders current and pending side by side. notice, if set,
// explains why the page came back instead of publishing. The post is read
// again here: after a refused publish it may have changed since the request
// loaded it, and the page's "publish anyway" must name the current version.
func (h *Handler) pendingPage(w http.ResponseWriter, r *http.Request, site *models.Site, status int, post *models.Post, notice string) {
	ps := h.posts(site)
	post, err := ps.GetByID(post.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if post == nil {
		h.notFound(w)
		return
	}
	rev, err := ps.GetPending(post.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if rev == nil {
		h.notFound(w)
		return
	}
	var author *models.User
	if rev.AuthorID != nil {
		users, err := h.d.Users.GetByIDs([]int64{*rev.AuthorID})
		if err != nil {
			h.serverError(w, err)
			return
		}
		author = users[*rev.AuthorID]
	}
	h.adminStatus(w, r, status, "pending", map[string]any{
		"Post":     post,
		"Pending":  rev,
		"Author":   authorLabel(author),
		"Stale":    rev.Stale(post),
		"Revision": rev.UpdatedAt.Format(time.RFC3339Nano),
		"Version":  post.ContentUpdatedAt.Format(time.RFC3339Nano),
		"Notice":   notice,
	})
}

func (h *Handler) PendingChanges(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		post, ok := h.reviewTarget(w, r, site)
		if !ok {
			return
		}
		h.pendingPage(w, r, site, http.StatusOK, post, "")
	})
}

// seenRevision parses the revision version the review page was showing.
func (h *Handler) seenRevision(w http.ResponseWriter, r *http.Request) (time.Time, bool) {
	seen, err := time.Parse(time.RFC3339Nano, r.FormValue("revision"))
	if err != nil {
		h.message(w, r, http.StatusBadRequest, "bad request", "That form was incomplete. Open the changes again.", "/admin/posts", "back to posts")
		return time.Time{}, false
	}
	return seen, true
}

// reviewConflict answers a publish or discard that no longer matches what
// is stored: the page again with the latest changes, or, if there are none
// left, a message.
func (h *Handler) reviewConflict(w http.ResponseWriter, r *http.Request, site *models.Site, post *models.Post) {
	rev, err := h.posts(site).GetPending(post.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if rev == nil {
		h.message(w, r, http.StatusConflict, "nothing to review", "These changes were already published or discarded.", "/admin/posts", "back to posts")
		return
	}
	h.pendingPage(w, r, site, http.StatusConflict, post, "The contributor saved again since you opened this page. Review the latest version.")
}

func (h *Handler) PublishChanges(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		post, ok := h.reviewTarget(w, r, site)
		if !ok {
			return
		}
		seen, ok := h.seenRevision(w, r)
		if !ok {
			return
		}
		// "publish anyway" names the post version it agreed to overwrite;
		// anything else (absent, unparseable) agrees to nothing.
		overwrite, _ := time.Parse(time.RFC3339Nano, r.FormValue("confirm"))
		err := h.posts(site).PublishPending(post.ID, seen, overwrite)
		switch {
		case err == nil:
			http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
		case errors.Is(err, models.ErrStale):
			notice := ""
			if !overwrite.IsZero() {
				notice = "The post was edited again since you opened this page. Check the current version before publishing."
			}
			h.pendingPage(w, r, site, http.StatusConflict, post, notice)
		case errors.Is(err, models.ErrRevisionChanged):
			h.reviewConflict(w, r, site, post)
		default:
			h.serverError(w, err)
		}
	})
}

func (h *Handler) DiscardChanges(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		post, ok := h.reviewTarget(w, r, site)
		if !ok {
			return
		}
		seen, ok := h.seenRevision(w, r)
		if !ok {
			return
		}
		err := h.posts(site).DiscardPending(post.ID, seen)
		switch {
		case err == nil:
			http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
		case errors.Is(err, models.ErrRevisionChanged):
			h.reviewConflict(w, r, site, post)
		default:
			h.serverError(w, err)
		}
	})
}
