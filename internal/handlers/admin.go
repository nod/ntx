package handlers

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/models"
)

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string][]time.Time)}
}

// allow returns true if key is under the threshold (5 attempts per 15
// minutes). key is "<siteID>:<ip>" so one site's brute-force lockout
// doesn't collaterally lock out login attempts on another site.
func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	var recent []time.Time
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.attempts, key)
	} else {
		l.attempts[key] = recent
	}
	if len(recent) >= 5 {
		return false
	}
	l.attempts[key] = append(l.attempts[key], time.Now())
	return true
}

func (h *Handler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin/login.html", nil)
}

func (h *Handler) AdminLoginPost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	limitKey := fmt.Sprintf("%d:%s", sh.site.ID, ip)
	if !h.limiter.allow(limitKey) {
		h.render(w, r, "admin/login.html", map[string]interface{}{
			"Error": "Too many attempts. Try again later.",
		})
		return
	}

	password := r.FormValue("password")
	switch {
	case auth.CheckPassword(sh.site.AdminPasswordHash, password) == nil:
		h.session.SetSession(w, sh.site.ID)
	case h.globalAdminHash != "" && auth.CheckPassword(h.globalAdminHash, password) == nil:
		h.session.SetSession(w, auth.GlobalSiteID)
	default:
		h.render(w, r, "admin/login.html", map[string]interface{}{
			"Error": "Invalid password.",
		})
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (h *Handler) AdminLogout(w http.ResponseWriter, r *http.Request) {
	h.session.ClearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (h *Handler) AdminPosts(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	posts, err := sh.posts.List(false)
	if err != nil {
		h.serverError(w, err)
		return
	}
	total, published, drafts, tagCount, err := sh.posts.Stats()
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, r, "admin/posts.html", map[string]interface{}{
		"Posts":     posts,
		"Total":     total,
		"Published": published,
		"Drafts":    drafts,
		"TagCount":  tagCount,
	})
}

func (h *Handler) AdminNewPost(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin/editor.html", map[string]interface{}{
		"Post": nil,
	})
}

func (h *Handler) AdminCreatePost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	post := postFromForm(r)
	post.Published = r.FormValue("action") == "publish"

	if err := sh.posts.Create(post); err != nil {
		h.render(w, r, "admin/editor.html", map[string]interface{}{
			"Post":  post,
			"Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (h *Handler) AdminEditPost(w http.ResponseWriter, r *http.Request) {
	post, err := h.getPostFromParam(w, r)
	if err != nil || post == nil {
		return
	}
	h.render(w, r, "admin/editor.html", map[string]interface{}{
		"Post": post,
	})
}

func (h *Handler) AdminUpdatePost(w http.ResponseWriter, r *http.Request) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return
	}
	existing, err := h.getPostFromParam(w, r)
	if err != nil || existing == nil {
		return
	}

	action := r.FormValue("action")

	if action == "delete" {
		if err := sh.posts.Delete(existing.ID); err != nil {
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
	default: // "save" preserves current state
		updated.Published = existing.Published
	}

	if err := sh.posts.Update(updated); err != nil {
		h.render(w, r, "admin/editor.html", map[string]interface{}{
			"Post":  updated,
			"Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

func (h *Handler) getPostFromParam(w http.ResponseWriter, r *http.Request) (*models.Post, error) {
	sh := h.current(r)
	if sh == nil {
		h.notFound(w)
		return nil, fmt.Errorf("no site resolved")
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.notFound(w)
		return nil, err
	}
	post, err := sh.posts.GetByID(id)
	if err != nil {
		h.serverError(w, err)
		return nil, err
	}
	if post == nil {
		h.notFound(w)
	}
	return post, nil
}

func postFromForm(r *http.Request) *models.Post {
	tagsRaw := r.FormValue("tags")
	var tags []string
	for _, t := range strings.Split(tagsRaw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
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
