// Package authz is the role table (see the roles and SSO design): the only
// place ntx compares roles. Handlers and templates ask these functions;
// they never look at roles or flags themselves.
//
// Every function takes the request's *auth.Actor, which is nil for an
// anonymous request (false everywhere), and answers for the site the
// actor's session is on: a session is bound to one site, and its
// Membership is the actor's membership there.
package authz

import (
	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/models"
)

// SiteAdmin: root and platform admins have site-admin powers on whichever
// site they're signed in to; otherwise it takes an admin membership.
func SiteAdmin(a *auth.Actor) bool {
	return a.IsPlatform() || (a != nil && a.Membership != nil && a.Membership.Role == models.RoleAdmin)
}

// SiteMember can use the site's admin area at all.
func SiteMember(a *auth.Actor) bool {
	return SiteAdmin(a) || (a != nil && a.Membership != nil)
}

func own(a *auth.Actor, p *models.Post) bool {
	return a != nil && p.AuthorID != nil && *p.AuthorID == a.User.ID
}

func CanCreatePost(a *auth.Actor) bool { return SiteMember(a) }

// CanPublishOwn: can this actor make their own writing live? Site admins
// always can; contributors only with can_publish.
func CanPublishOwn(a *auth.Actor) bool {
	return SiteAdmin(a) || (a != nil && a.Membership != nil && a.Membership.Role == models.RoleContributor && a.Membership.CanPublish)
}

// CanSeePost: contributors see only their own posts in the admin area.
func CanSeePost(a *auth.Actor, p *models.Post) bool { return SiteAdmin(a) || own(a, p) }

// CanEditPost: a contributor without can_publish may edit only their own
// never-published drafts; anything else would change what readers see.
func CanEditPost(a *auth.Actor, p *models.Post) bool {
	return SiteAdmin(a) || (own(a, p) && (CanPublishOwn(a) || !p.LiveOnce()))
}

// CanPublishPost covers publishing and unpublishing.
func CanPublishPost(a *auth.Actor, p *models.Post) bool {
	return SiteAdmin(a) || (own(a, p) && CanPublishOwn(a))
}

func CanDeletePost(a *auth.Actor, p *models.Post) bool {
	return SiteAdmin(a) || (own(a, p) && (CanPublishOwn(a) || !p.LiveOnce()))
}

func CanManageMembers(a *auth.Actor) bool { return SiteAdmin(a) }

// CanManageMember: site admins can't change or remove a platform admin's
// membership; only root and platform admins can. Root has no memberships.
func CanManageMember(a *auth.Actor, target *models.User) bool {
	return SiteAdmin(a) && !target.IsRoot && (!target.IsPlatformAdmin || a.IsPlatform())
}

func CanUsePlatform(a *auth.Actor) bool { return a.IsPlatform() }

// CanChangeUser: the platform users view, for anyone but root.
func CanChangeUser(a *auth.Actor, target *models.User) bool {
	return a.IsPlatform() && !target.IsRoot
}
