package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/authz"
	"github.com/nod/ntx/internal/models"
)

// memberRow is one person on the members page. Manageable is false for
// rows the actor may not change (a platform admin, for a site admin).
type memberRow struct {
	models.Member
	Manageable bool
}

// membersPage renders the site's members and pending invites. extra adds
// Error or InviteLink.
func (h *Handler) membersPage(w http.ResponseWriter, r *http.Request, site *models.Site, extra map[string]any) {
	members, err := h.d.Memberships.ListForSite(site.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	invites, err := h.d.Invites.ListPending(site.ID, time.Now())
	if err != nil {
		h.serverError(w, err)
		return
	}
	a := auth.ActorFrom(r)
	rows := make([]memberRow, len(members))
	for i, m := range members {
		rows[i] = memberRow{Member: m, Manageable: authz.CanManageMember(a, &m.User)}
	}
	data := map[string]any{"Members": rows, "Invites": invites}
	for k, v := range extra {
		data[k] = v
	}
	h.admin(w, r, "members", data)
}

// formError re-shows the members page with a validation error, or reports
// a server error.
func (h *Handler) membersFormError(w http.ResponseWriter, r *http.Request, site *models.Site, err error) {
	var v *models.ValidationError
	if errors.As(err, &v) {
		h.membersPage(w, r, site, map[string]any{"Error": v.Msg})
		return
	}
	h.serverError(w, err)
}

func (h *Handler) Members(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		h.membersPage(w, r, site, nil)
	})
}

// MembersAdd gives someone a membership straight away; they sign in with
// the site's provider and are matched by email.
func (h *Handler) MembersAdd(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		if _, err := h.d.People.AddMember(site.ID, r.FormValue("email"), r.FormValue("role"), r.FormValue("can_publish") == "on"); err != nil {
			h.membersFormError(w, r, site, err)
			return
		}
		http.Redirect(w, r, "/admin/members", http.StatusSeeOther)
	})
}

// MembersInvite creates an invite and shows its link, once.
func (h *Handler) MembersInvite(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		_, token, err := h.d.Invites.Create(site.ID, r.FormValue("email"), r.FormValue("role"), r.FormValue("can_publish") == "on", auth.ActorFrom(r).User.ID, time.Now())
		if err != nil {
			h.membersFormError(w, r, site, err)
			return
		}
		h.membersPage(w, r, site, map[string]any{"InviteLink": h.externalURL(r, site, "/invite/"+token)})
	})
}

// memberTarget loads the {userID} member of site and checks the actor may
// manage them, writing a 404 or 403 and returning ok=false if not.
func (h *Handler) memberTarget(w http.ResponseWriter, r *http.Request, site *models.Site) (*models.User, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		h.notFound(w)
		return nil, false
	}
	m, err := h.d.Memberships.Get(id, site.ID)
	if err != nil {
		h.serverError(w, err)
		return nil, false
	}
	if m == nil {
		h.notFound(w)
		return nil, false
	}
	u, err := h.d.Users.GetByID(id)
	if err != nil || u == nil {
		h.serverError(w, err)
		return nil, false
	}
	if !authz.CanManageMember(auth.ActorFrom(r), u) {
		h.message(w, r, http.StatusForbidden, "not allowed", "You can't change this person's membership.", "/admin/members", "back to members")
		return nil, false
	}
	return u, true
}

func (h *Handler) MemberUpdate(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		u, ok := h.memberTarget(w, r, site)
		if !ok {
			return
		}
		if err := h.d.Memberships.Update(u.ID, site.ID, r.FormValue("role"), r.FormValue("can_publish") == "on"); err != nil {
			h.membersFormError(w, r, site, err)
			return
		}
		http.Redirect(w, r, "/admin/members", http.StatusSeeOther)
	})
}

func (h *Handler) MemberRemove(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		u, ok := h.memberTarget(w, r, site)
		if !ok {
			return
		}
		if err := h.d.Memberships.Remove(u.ID, site.ID); err != nil {
			h.membersFormError(w, r, site, err)
			return
		}
		http.Redirect(w, r, "/admin/members", http.StatusSeeOther)
	})
}

func inviteID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil
}

// InviteCancel and InviteRegenerate touch only this site's invites: an ID
// from another site is a 404.
func (h *Handler) InviteCancel(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		id, ok := inviteID(r)
		if !ok {
			h.notFound(w)
			return
		}
		var v *models.ValidationError
		if err := h.d.Invites.Cancel(id, site.ID); errors.As(err, &v) {
			h.notFound(w)
			return
		} else if err != nil {
			h.serverError(w, err)
			return
		}
		http.Redirect(w, r, "/admin/members", http.StatusSeeOther)
	})
}

func (h *Handler) InviteRegenerate(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		id, ok := inviteID(r)
		if !ok {
			h.notFound(w)
			return
		}
		token, err := h.d.Invites.Regenerate(id, site.ID, time.Now())
		var v *models.ValidationError
		if errors.As(err, &v) {
			h.notFound(w)
			return
		} else if err != nil {
			h.serverError(w, err)
			return
		}
		h.membersPage(w, r, site, map[string]any{"InviteLink": h.externalURL(r, site, "/invite/"+token)})
	})
}
