package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/authz"
	"github.com/nod/ntx/internal/models"
)

// userMembership is a membership with its site's key, for display.
type userMembership struct {
	models.Membership
	SiteKey string
}

type userRow struct {
	models.User
	Memberships []userMembership
}

func (h *Handler) membershipsOf(userID int64) ([]userMembership, error) {
	ms, err := h.d.Memberships.ListForUser(userID)
	if err != nil {
		return nil, err
	}
	out := make([]userMembership, 0, len(ms))
	for _, m := range ms {
		key := "?"
		if s := h.d.Registry.ByID(m.SiteID); s != nil {
			key = s.Key
		}
		out = append(out, userMembership{Membership: m, SiteKey: key})
	}
	return out, nil
}

// PlatformUsers lists everyone across all sites.
func (h *Handler) PlatformUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.d.Users.List()
	if err != nil {
		h.serverError(w, err)
		return
	}
	rows := make([]userRow, len(users))
	for i, u := range users {
		ms, err := h.membershipsOf(u.ID)
		if err != nil {
			h.serverError(w, err)
			return
		}
		rows[i] = userRow{User: u, Memberships: ms}
	}
	h.admin(w, r, "platform_users", map[string]any{"Users": rows})
}

// userParam loads the {id} user, writing a 404 if there is none.
func (h *Handler) userParam(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.notFound(w)
		return nil, false
	}
	u, err := h.d.Users.GetByID(id)
	if err != nil {
		h.serverError(w, err)
		return nil, false
	}
	if u == nil {
		h.notFound(w)
		return nil, false
	}
	return u, true
}

// changeableUser is userParam plus the rule that nobody changes root from
// the web (the stores refuse too).
func (h *Handler) changeableUser(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	u, ok := h.userParam(w, r)
	if !ok {
		return nil, false
	}
	if !authz.CanChangeUser(auth.ActorFrom(r), u) {
		h.message(w, r, http.StatusForbidden, "not allowed", "Root is managed in the config file, not here.", "/admin/platform/users", "back to users")
		return nil, false
	}
	return u, true
}

func (h *Handler) userPage(w http.ResponseWriter, r *http.Request, u *models.User, errMsg string) {
	ms, err := h.membershipsOf(u.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	ids, err := h.d.Identities.ListForUser(u.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.admin(w, r, "platform_user", map[string]any{
		"Target": u, "Memberships": ms, "Identities": ids, "Sites": h.d.Registry.All(),
		"Changeable": authz.CanChangeUser(auth.ActorFrom(r), u), "Error": errMsg,
	})
}

func (h *Handler) PlatformUser(w http.ResponseWriter, r *http.Request) {
	if u, ok := h.userParam(w, r); ok {
		h.userPage(w, r, u, "")
	}
}

// userResult finishes a user change: back to the user's page, or the page
// again with a validation error.
func (h *Handler) userResult(w http.ResponseWriter, r *http.Request, u *models.User, err error) {
	var v *models.ValidationError
	switch {
	case errors.As(err, &v):
		h.userPage(w, r, u, v.Msg)
	case err != nil:
		h.serverError(w, err)
	default:
		http.Redirect(w, r, "/admin/platform/users/"+strconv.FormatInt(u.ID, 10), http.StatusSeeOther)
	}
}

// PlatformUpdateUser saves the email and the platform-admin flag.
func (h *Handler) PlatformUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := h.changeableUser(w, r)
	if !ok {
		return
	}
	if email := models.NormalizeEmail(r.FormValue("email")); email != u.Email {
		if err := h.d.Users.SetEmail(u.ID, email); err != nil {
			h.userResult(w, r, u, err)
			return
		}
	}
	h.userResult(w, r, u, h.d.Users.SetPlatformAdmin(u.ID, r.FormValue("platform_admin") == "on"))
}

func (h *Handler) siteKeyed(key string) *models.Site {
	for _, s := range h.d.Registry.All() {
		if s.Key == key {
			return &s
		}
	}
	return nil
}

func (h *Handler) PlatformAddMembership(w http.ResponseWriter, r *http.Request) {
	u, ok := h.changeableUser(w, r)
	if !ok {
		return
	}
	site := h.siteKeyed(r.FormValue("site"))
	if site == nil {
		h.userPage(w, r, u, "pick a site from the list")
		return
	}
	h.userResult(w, r, u, h.d.People.AddMembership(u, site.ID, r.FormValue("role"), r.FormValue("can_publish") == "on"))
}

func (h *Handler) PlatformRemoveMembership(w http.ResponseWriter, r *http.Request) {
	u, ok := h.changeableUser(w, r)
	if !ok {
		return
	}
	site := h.siteKeyed(chi.URLParam(r, "key"))
	if site == nil {
		h.notFound(w)
		return
	}
	h.userResult(w, r, u, h.d.Memberships.Remove(u.ID, site.ID))
}

// PlatformUnlinkIdentity detaches a login. The person's next sign-in
// matches by email again, so this is how a site moves to a new provider or
// a reassigned mailbox gets cleared.
func (h *Handler) PlatformUnlinkIdentity(w http.ResponseWriter, r *http.Request) {
	u, ok := h.changeableUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "identityID"), 10, 64)
	if err != nil {
		h.notFound(w)
		return
	}
	h.userResult(w, r, u, h.d.Identities.Unlink(id, u.ID))
}

// PlatformRevokeSessions signs the person out everywhere.
func (h *Handler) PlatformRevokeSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := h.changeableUser(w, r)
	if !ok {
		return
	}
	h.userResult(w, r, u, h.d.Sessions.DeleteForUser(u.ID))
}

// PlatformDeleteUser removes the person; their posts stay, authorless.
func (h *Handler) PlatformDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := h.changeableUser(w, r)
	if !ok {
		return
	}
	if err := h.d.Users.Delete(u.ID); err != nil {
		h.userResult(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/admin/platform/users", http.StatusSeeOther)
}
