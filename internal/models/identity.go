package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrIdentityConflict is the reassigned-account guard: the login is
// someone else's, or the person already has a different login at the same
// provider (a reused or reassigned mailbox). A platform admin resolves it
// by unlinking the old identity on purpose.
var ErrIdentityConflict = errors.New("that login is linked to a different account")

// Identity is one provider login linked to a person. Issuer is the
// configured issuer for the provider, never the token's iss claim.
type Identity struct {
	ID          int64
	UserID      int64
	Issuer      string
	Subject     string
	EmailAtLink string
	CreatedAt   time.Time
}

type dbIdentity struct {
	ID          uint `gorm:"primarykey"`
	UserID      int64
	Issuer      string
	Subject     string
	EmailAtLink string
	CreatedAt   time.Time
}

func (dbIdentity) TableName() string { return "identities" }

func toIdentity(r dbIdentity) *Identity {
	return &Identity{ID: int64(r.ID), UserID: r.UserID, Issuer: r.Issuer, Subject: r.Subject, EmailAtLink: r.EmailAtLink, CreatedAt: r.CreatedAt}
}

type IdentityStore struct{ db *gorm.DB }

func NewIdentityStore(db *gorm.DB) *IdentityStore { return &IdentityStore{db: db} }

// Find returns the identity for (issuer, subject), or nil.
func (s *IdentityStore) Find(issuer, subject string) (*Identity, error) {
	var r dbIdentity
	err := s.db.Where("issuer = ? AND subject = ?", issuer, subject).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toIdentity(r), nil
}

// Link attaches (issuer, subject) to the user, under the reassigned-account
// guard. Re-linking the same identity to the same user is a no-op.
func (s *IdentityStore) Link(userID int64, issuer, subject, email string) error {
	return linkIdentityTx(s.db, userID, issuer, subject, email)
}

func linkIdentityTx(tx *gorm.DB, userID int64, issuer, subject, email string) error {
	if err := guardRoot(tx, userID); err != nil {
		return err
	}
	var existing dbIdentity
	err := tx.Where("issuer = ? AND subject = ?", issuer, subject).First(&existing).Error
	switch {
	case err == nil && existing.UserID == userID:
		return nil
	case err == nil:
		return ErrIdentityConflict
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return err
	}
	var n int64
	if err := tx.Model(&dbIdentity{}).Where("user_id = ? AND issuer = ?", userID, issuer).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return ErrIdentityConflict
	}
	return tx.Create(&dbIdentity{UserID: userID, Issuer: issuer, Subject: subject, EmailAtLink: NormalizeEmail(email), CreatedAt: time.Now()}).Error
}

func (s *IdentityStore) ListForUser(userID int64) ([]Identity, error) {
	var rows []dbIdentity
	if err := s.db.Where("user_id = ?", userID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Identity, len(rows))
	for i, r := range rows {
		out[i] = *toIdentity(r)
	}
	return out, nil
}

// Unlink removes one of the user's identities. Their next sign-in matches
// by email again.
func (s *IdentityStore) Unlink(id, userID int64) error {
	res := s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&dbIdentity{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalid("identity %d does not belong to user %d", id, userID)
	}
	return nil
}
