// Package people decides who a provider sign-in is, and adds people to
// sites. The order of SignIn is the design's identity resolution: invite,
// then known identity, then email match against this site's members, then
// the membership check. A provider login on its own grants nothing.
package people

import (
	"errors"
	"time"

	"github.com/nod/ntx/internal/models"
)

// ErrNoAccess is the one answer for "you can't sign in here": unknown
// person, no membership on this site, or a reassigned-account conflict.
// It never says which, so it can't be used to probe who exists.
var ErrNoAccess = errors.New("you don't have access to this site")

type Service struct {
	users   *models.UserStore
	ids     *models.IdentityStore
	mems    *models.MembershipStore
	invites *models.InviteStore
}

func New(users *models.UserStore, ids *models.IdentityStore, mems *models.MembershipStore, invites *models.InviteStore) *Service {
	return &Service{users: users, ids: ids, mems: mems, invites: invites}
}

// SignIn resolves a verified provider login on siteID to a user who holds a
// membership there. invite is the invite token carried through the flow,
// or "". It returns ErrNoAccess, *models.InviteEmailMismatch or
// models.ErrInviteInvalid for the person-facing failures.
func (s *Service) SignIn(siteID int64, c models.IdentityClaims, invite string, now time.Time) (*models.User, error) {
	c.Email = models.NormalizeEmail(c.Email)
	user, err := s.resolve(siteID, c, invite, now)
	if errors.Is(err, models.ErrIdentityConflict) {
		return nil, ErrNoAccess
	}
	if err != nil {
		return nil, err
	}
	m, err := s.mems.Get(user.ID, siteID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNoAccess
	}
	if c.Name != "" && c.Name != user.Name {
		if err := s.users.SetName(user.ID, c.Name); err != nil {
			return nil, err
		}
		user.Name = c.Name
	}
	return user, nil
}

func (s *Service) resolve(siteID int64, c models.IdentityClaims, invite string, now time.Time) (*models.User, error) {
	if invite != "" {
		return s.invites.Accept(invite, siteID, c, now)
	}
	known, err := s.ids.Find(c.Issuer, c.Subject)
	if err != nil {
		return nil, err
	}
	if known != nil {
		u, err := s.users.GetByID(known.UserID)
		if err != nil {
			return nil, err
		}
		if u == nil {
			return nil, ErrNoAccess
		}
		return u, nil
	}
	// The "added by an admin" path: a member of this site, matched by the
	// email the provider verified, gets this login linked on first use.
	if c.Email == "" {
		return nil, ErrNoAccess
	}
	u, err := s.users.FindByEmail(c.Email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNoAccess
	}
	m, err := s.mems.Get(u.ID, siteID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNoAccess
	}
	if err := s.ids.Link(u.ID, c.Issuer, c.Subject, c.Email); err != nil {
		return nil, err
	}
	return u, nil
}

// AddMember gives the person with this email a membership on the site
// straight away, creating the user if needed. Any pending invite for them
// on the site is cancelled: it would only offer what they now have.
func (s *Service) AddMember(siteID int64, email, role string, canPublish bool) (*models.User, error) {
	u, err := s.users.FindOrCreateByEmail(email)
	if err != nil {
		return nil, err
	}
	if err := s.mems.Add(u.ID, siteID, role, canPublish); err != nil {
		return nil, err
	}
	if err := s.invites.CancelPendingFor(siteID, u.Email); err != nil {
		return nil, err
	}
	return u, nil
}
