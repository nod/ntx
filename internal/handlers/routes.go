package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/adminui"
)

// Mount registers every route on r. Host resolution and session loading
// are the caller's middleware (see server.New); this is only the table.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.Home)
	r.Get("/about", h.About)
	r.Get("/blog", h.BlogIndex)
	r.Get("/blog/{slug}", h.BlogPost)
	r.Get("/static/*", h.Static)

	r.Handle("/admin/assets/*", http.StripPrefix("/admin/assets/", adminui.Assets()))
	r.Get("/admin/login", h.LoginForm)
	r.Post("/admin/login", h.LoginPost)
	r.Post("/admin/logout", h.Logout)

	r.Get("/auth/start", h.AuthStart)
	r.Post("/auth/start", h.AuthStart)
	r.Get("/auth/callback", h.AuthCallback)
	r.Get("/invite/{token}", h.InvitePage)

	// Posts: any member; each action is checked against authz in the
	// handler.
	r.Group(func(r chi.Router) {
		r.Use(h.RequireMember)
		r.Get("/admin/posts", h.Posts)
		r.Get("/admin/posts/new", h.NewPost)
		r.Post("/admin/posts", h.CreatePost)
		r.Get("/admin/posts/{id}/edit", h.EditPost)
		r.Post("/admin/posts/{id}", h.UpdatePost)
	})

	r.Group(func(r chi.Router) {
		r.Use(h.RequireSiteAdmin)
		r.Get("/admin/members", h.Members)
		r.Post("/admin/members", h.MembersAdd)
		r.Post("/admin/members/invite", h.MembersInvite)
		r.Post("/admin/members/{userID}", h.MemberUpdate)
		r.Post("/admin/members/{userID}/remove", h.MemberRemove)
		r.Post("/admin/invites/{id}/cancel", h.InviteCancel)
		r.Post("/admin/invites/{id}/regenerate", h.InviteRegenerate)
		r.Get("/admin/settings", h.Settings)
		r.Post("/admin/settings", h.SettingsPost)
	})

	r.Group(func(r chi.Router) {
		r.Use(h.RequirePlatform)
		r.Get("/admin/platform/sites", h.PlatformSites)
		r.Get("/admin/platform/sites/new", h.PlatformNewSite)
		r.Post("/admin/platform/sites", h.PlatformCreateSite)
		r.Get("/admin/platform/sites/{key}", h.PlatformEditSite)
		r.Post("/admin/platform/sites/{key}", h.PlatformUpdateSite)
		r.Get("/admin/platform/sites/{key}/delete", h.PlatformDeleteSiteForm)
		r.Post("/admin/platform/sites/{key}/delete", h.PlatformDeleteSite)
		r.Get("/admin/platform/users", h.PlatformUsers)
		r.Get("/admin/platform/users/{id}", h.PlatformUser)
		r.Post("/admin/platform/users/{id}", h.PlatformUpdateUser)
		r.Post("/admin/platform/users/{id}/memberships", h.PlatformAddMembership)
		r.Post("/admin/platform/users/{id}/memberships/{key}/remove", h.PlatformRemoveMembership)
		r.Post("/admin/platform/users/{id}/identities/{identityID}/unlink", h.PlatformUnlinkIdentity)
		r.Post("/admin/platform/users/{id}/revoke", h.PlatformRevokeSessions)
		r.Post("/admin/platform/users/{id}/delete", h.PlatformDeleteUser)
	})

	// Registered last: chi prefers static segments, so /blog/{slug},
	// /static/* and /admin/... keep precedence, and reserved section slugs
	// keep sections from colliding with them.
	r.Get("/{section}/{page}", h.Section)
}
