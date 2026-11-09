package models

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/validate"
)

// InviteTTL is how long an invite link works.
const InviteTTL = 7 * 24 * time.Hour

// ErrInviteInvalid means the invite is unknown, used, expired, cancelled or
// for another site. Callers don't distinguish these to the visitor.
var ErrInviteInvalid = errors.New("this invite link is no longer valid")

// InviteEmailMismatch is returned when the person accepting an invite signed
// in with a different email from the one invited. Want is shown to them:
// whoever holds the link was sent it, so naming the address is acceptable.
type InviteEmailMismatch struct{ Want string }

func (e *InviteEmailMismatch) Error() string {
	return fmt.Sprintf("this invite is for %s", e.Want)
}

// IdentityClaims is what a provider vouched for at sign-in. Issuer is the
// configured issuer.
type IdentityClaims struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
}

// Invite is a pending offer of a membership. Its token is never stored.
type Invite struct {
	ID         int64
	SiteID     int64
	Email      string
	Role       string
	CanPublish bool
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

type dbInvite struct {
	ID         uint `gorm:"primarykey"`
	SiteID     int64
	Email      string
	Role       string
	CanPublish bool
	TokenHash  string
	CreatedBy  *int64
	ExpiresAt  time.Time
	UsedAt     *time.Time
	CreatedAt  time.Time
}

func (dbInvite) TableName() string { return "invites" }

func toInvite(r dbInvite) *Invite {
	return &Invite{ID: int64(r.ID), SiteID: r.SiteID, Email: r.Email, Role: r.Role, CanPublish: r.CanPublish, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt}
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type InviteStore struct{ db *gorm.DB }

func NewInviteStore(db *gorm.DB) *InviteStore { return &InviteStore{db: db} }

// Create makes an invite and returns it with its token, which is shown once
// and never stored. It replaces any other unused invite for the same
// (site, email); inviting an existing member is an error. createdBy 0 means
// unknown.
func (s *InviteStore) Create(siteID int64, email, role string, canPublish bool, createdBy int64, now time.Time) (*Invite, string, error) {
	email = NormalizeEmail(email)
	if err := validate.Email(email); err != nil {
		return nil, "", invalid("%s", err)
	}
	if err := validRole(role); err != nil {
		return nil, "", err
	}
	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	r := dbInvite{
		SiteID: siteID, Email: email, Role: role, CanPublish: canPublish,
		TokenHash: hashToken(token), ExpiresAt: now.Add(InviteTTL), CreatedAt: now,
	}
	if createdBy != 0 {
		r.CreatedBy = &createdBy
	}
	// A double-submitted invite: the retry replaces the other request's.
	err = retryOnDuplicate(s.db, func(tx *gorm.DB) error {
		var n int64
		if err := tx.Table("memberships").
			Joins("JOIN users ON users.id = memberships.user_id").
			Where("memberships.site_id = ? AND users.email = ?", siteID, email).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return invalid("%s is already a member of this site", email)
		}
		// The partial unique index allows one unused row per (site, email),
		// expired or not, so clear the way first.
		if err := tx.Where("site_id = ? AND email = ? AND used_at IS NULL", siteID, email).Delete(&dbInvite{}).Error; err != nil {
			return err
		}
		return tx.Create(&r).Error
	})
	if err != nil {
		return nil, "", err
	}
	return toInvite(r), token, nil
}

func pendingByToken(tx *gorm.DB, token string, siteID int64, now time.Time) (*dbInvite, error) {
	var r dbInvite
	err := tx.Where("token_hash = ? AND site_id = ? AND used_at IS NULL AND expires_at > ?", hashToken(token), siteID, now).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Lookup returns the usable invite for token on this site, or nil.
func (s *InviteStore) Lookup(token string, siteID int64, now time.Time) (*Invite, error) {
	r, err := pendingByToken(s.db, token, siteID, now)
	if err != nil || r == nil {
		return nil, err
	}
	return toInvite(*r), nil
}

// Regenerate gives an unused invite a new token (the old link stops
// working) and a fresh expiry.
func (s *InviteStore) Regenerate(id, siteID int64, now time.Time) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	res := s.db.Model(&dbInvite{}).Where("id = ? AND site_id = ? AND used_at IS NULL", id, siteID).
		Updates(map[string]any{"token_hash": hashToken(token), "expires_at": now.Add(InviteTTL)})
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", invalid("invite %d not found", id)
	}
	return token, nil
}

// Cancel deletes an unused invite on this site.
func (s *InviteStore) Cancel(id, siteID int64) error {
	res := s.db.Where("id = ? AND site_id = ? AND used_at IS NULL", id, siteID).Delete(&dbInvite{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalid("invite %d not found", id)
	}
	return nil
}

// CancelPendingFor deletes any unused invite for this (site, email).
func (s *InviteStore) CancelPendingFor(siteID int64, email string) error {
	return s.db.Where("site_id = ? AND email = ? AND used_at IS NULL", siteID, NormalizeEmail(email)).Delete(&dbInvite{}).Error
}

// ListPending returns the site's unused, unexpired invites by email.
func (s *InviteStore) ListPending(siteID int64, now time.Time) ([]Invite, error) {
	var rows []dbInvite
	if err := s.db.Where("site_id = ? AND used_at IS NULL AND expires_at > ?", siteID, now).Order("email").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Invite, len(rows))
	for i, r := range rows {
		out[i] = *toInvite(r)
	}
	return out, nil
}

// Accept redeems an invite for the person the provider vouched for, in one
// transaction: the invite is marked used only if the person, their identity
// link and their membership all work out, and only once, however many
// callbacks race.
func (s *InviteStore) Accept(token string, siteID int64, c IdentityClaims, now time.Time) (*User, error) {
	var user *User
	err := s.db.Transaction(func(tx *gorm.DB) error {
		inv, err := pendingByToken(tx, token, siteID, now)
		if err != nil {
			return err
		}
		if inv == nil {
			return ErrInviteInvalid
		}
		if NormalizeEmail(c.Email) != inv.Email {
			return &InviteEmailMismatch{Want: inv.Email}
		}
		res := tx.Model(&dbInvite{}).Where("id = ? AND used_at IS NULL", inv.ID).Update("used_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrInviteInvalid
		}

		var known dbIdentity
		err = tx.Where("issuer = ? AND subject = ?", c.Issuer, c.Subject).First(&known).Error
		switch {
		case err == nil:
			var r dbUser
			if err := tx.First(&r, known.UserID).Error; err != nil {
				return err
			}
			if r.Email.String != inv.Email {
				return &InviteEmailMismatch{Want: inv.Email}
			}
			user = toUser(r)
		case errors.Is(err, gorm.ErrRecordNotFound):
			if user, err = findOrCreateByEmail(tx, inv.Email); err != nil {
				return err
			}
		default:
			return err
		}

		if err := linkIdentityTx(tx, user.ID, c.Issuer, c.Subject, c.Email); err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&dbMembership{}).Where("user_id = ? AND site_id = ?", user.ID, siteID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return addMembership(tx, user.ID, siteID, inv.Role, inv.CanPublish)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
