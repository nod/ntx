package authz

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/models"
)

// Users. IDs: root 1, platform admin 2, site admin 3, contributors 4 and 5,
// another member 6.
var (
	rootUser     = &models.User{ID: 1, Name: "root", IsRoot: true}
	platformUser = &models.User{ID: 2, Email: "p@x.io", IsPlatformAdmin: true}
	adminUser    = &models.User{ID: 3, Email: "a@x.io"}
	contribUser  = &models.User{ID: 4, Email: "c@x.io"}
	publishUser  = &models.User{ID: 5, Email: "cp@x.io"}
	otherUser    = &models.User{ID: 6, Email: "o@x.io"}
)

func member(u *models.User, role string, canPublish bool) *auth.Actor {
	return &auth.Actor{User: u, Membership: &models.Membership{UserID: u.ID, SiteID: 1, Role: role, CanPublish: canPublish}}
}

var (
	anon             *auth.Actor
	root             = &auth.Actor{User: rootUser}
	platformContrib  = member(platformUser, models.RoleContributor, false)
	siteAdmin        = member(adminUser, models.RoleAdmin, false)
	contribNoPublish = member(contribUser, models.RoleContributor, false)
	contribPublish   = member(publishUser, models.RoleContributor, true)
)

func post(author *models.User, live bool) *models.Post {
	p := &models.Post{ID: 1, Slug: "p"}
	if author != nil {
		p.AuthorID = &author.ID
	}
	if live {
		now := time.Now()
		p.FirstPublishedAt = &now
	}
	return p
}

var (
	// "own" posts belong to contribNoPublish unless noted.
	ownDraft     = post(contribUser, false)
	ownLive      = post(contribUser, true)
	pubOwnDraft  = post(publishUser, false)
	pubOwnLive   = post(publishUser, true)
	otherDraft   = post(otherUser, false)
	otherLive    = post(otherUser, true)
	orphan       = post(nil, true)
	everyPost    = []*models.Post{ownDraft, ownLive, pubOwnDraft, pubOwnLive, otherDraft, otherLive, orphan}
	everyActor   = []*auth.Actor{root, platformContrib, siteAdmin, contribNoPublish, contribPublish}
	everyTarget  = []*models.User{rootUser, platformUser, adminUser, contribUser}
	postChecks   = []func(*auth.Actor, *models.Post) bool{CanSeePost, CanEditPost, CanPublishPost, CanDeletePost}
	actorChecks  = []func(*auth.Actor) bool{SiteAdmin, SiteMember, CanCreatePost, CanPublishOwn, CanManageMembers, CanUsePlatform}
	targetChecks = []func(*auth.Actor, *models.User) bool{CanManageMember, CanChangeUser}
)

func name(f any) string {
	n := runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
	return n[strings.LastIndex(n, ".")+1:]
}

func TestRoleTable(t *testing.T) {
	type pc struct {
		f    func(*auth.Actor, *models.Post) bool
		a    *auth.Actor
		p    *models.Post
		want bool
	}
	postCases := []pc{
		// Contributor without can_publish: own never-published drafts only.
		{CanEditPost, contribNoPublish, ownDraft, true},
		{CanEditPost, contribNoPublish, ownLive, false},
		{CanDeletePost, contribNoPublish, ownDraft, true},
		{CanDeletePost, contribNoPublish, ownLive, false},
		{CanPublishPost, contribNoPublish, ownDraft, false},
		{CanPublishPost, contribNoPublish, ownLive, false},
		{CanSeePost, contribNoPublish, ownLive, true},
		{CanSeePost, contribNoPublish, otherDraft, false},
		{CanSeePost, contribNoPublish, otherLive, false},
		{CanEditPost, contribNoPublish, otherDraft, false},
		{CanEditPost, contribNoPublish, orphan, false},
		// Contributor with can_publish: everything on their own posts only.
		{CanPublishPost, contribPublish, pubOwnLive, true},
		{CanPublishPost, contribPublish, pubOwnDraft, true},
		{CanEditPost, contribPublish, pubOwnLive, true},
		{CanDeletePost, contribPublish, pubOwnLive, true},
		{CanEditPost, contribPublish, otherLive, false},
		{CanPublishPost, contribPublish, otherDraft, false},
		{CanDeletePost, contribPublish, otherDraft, false},
		{CanSeePost, contribPublish, otherDraft, false},
	}
	// Site admins, platform admins (whatever their membership role) and
	// root can do everything to every post on the site.
	for _, a := range []*auth.Actor{siteAdmin, platformContrib, root} {
		for _, p := range everyPost {
			for _, f := range postChecks {
				postCases = append(postCases, pc{f, a, p, true})
			}
		}
	}
	for _, p := range everyPost {
		for _, f := range postChecks {
			postCases = append(postCases, pc{f, anon, p, false})
		}
	}
	for _, c := range postCases {
		if got := c.f(c.a, c.p); got != c.want {
			t.Errorf("%s(%s, post by %v live=%v) = %v, want %v", name(c.f), who(c.a), authorOf(c.p), c.p.LiveOnce(), got, c.want)
		}
	}

	actorWant := map[string]map[*auth.Actor]bool{
		"SiteAdmin":        {root: true, platformContrib: true, siteAdmin: true},
		"SiteMember":       {root: true, platformContrib: true, siteAdmin: true, contribNoPublish: true, contribPublish: true},
		"CanCreatePost":    {root: true, platformContrib: true, siteAdmin: true, contribNoPublish: true, contribPublish: true},
		"CanPublishOwn":    {root: true, platformContrib: true, siteAdmin: true, contribPublish: true},
		"CanManageMembers": {root: true, platformContrib: true, siteAdmin: true},
		"CanUsePlatform":   {root: true, platformContrib: true},
	}
	for _, f := range actorChecks {
		for _, a := range append(everyActor, anon) {
			if got, want := f(a), actorWant[name(f)][a]; got != want {
				t.Errorf("%s(%s) = %v, want %v", name(f), who(a), got, want)
			}
		}
	}

	type key struct {
		a *auth.Actor
		u *models.User
	}
	targetWant := map[string]map[key]bool{
		"CanManageMember": {
			{root, platformUser}: true, {root, adminUser}: true, {root, contribUser}: true,
			{platformContrib, platformUser}: true, {platformContrib, adminUser}: true, {platformContrib, contribUser}: true,
			{siteAdmin, adminUser}: true, {siteAdmin, contribUser}: true,
		},
		"CanChangeUser": {
			{root, platformUser}: true, {root, adminUser}: true, {root, contribUser}: true,
			{platformContrib, platformUser}: true, {platformContrib, adminUser}: true, {platformContrib, contribUser}: true,
		},
	}
	for _, f := range targetChecks {
		for _, a := range append(everyActor, anon) {
			for _, u := range everyTarget {
				if got, want := f(a, u), targetWant[name(f)][key{a, u}]; got != want {
					t.Errorf("%s(%s, %s) = %v, want %v", name(f), who(a), u.Email+u.Name, got, want)
				}
			}
		}
	}
}

func who(a *auth.Actor) string {
	switch a {
	case nil:
		return "anon"
	case root:
		return "root"
	case platformContrib:
		return "platformContrib"
	case siteAdmin:
		return "siteAdmin"
	case contribNoPublish:
		return "contribNoPublish"
	case contribPublish:
		return "contribPublish"
	}
	return "?"
}

func authorOf(p *models.Post) any {
	if p.AuthorID == nil {
		return "nobody"
	}
	return *p.AuthorID
}
