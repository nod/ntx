package models

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/validate"
)

// ErrRootProtected is returned by every store write that would give root an
// email, identity, membership or name, or change or delete it. Root is
// managed only through the config file.
var ErrRootProtected = &ValidationError{Msg: "root can't be changed from the web"}

// NormalizeEmail is how every email is compared and stored.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// guardRoot returns ErrRootProtected if userID is root, and an error if the
// user doesn't exist.
func guardRoot(tx *gorm.DB, userID int64) error {
	var r dbUser
	if err := tx.Select("id", "is_root").First(&r, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalid("user %d does not exist", userID)
		}
		return err
	}
	if r.IsRoot {
		return ErrRootProtected
	}
	return nil
}

// User is a person (or root). Root has no email, identities or memberships;
// it signs in only with the password in config.
type User struct {
	ID              int64
	Email           string // lowercase; "" only for root
	Name            string
	IsRoot          bool
	IsPlatformAdmin bool
}

type dbUser struct {
	ID              uint `gorm:"primarykey"`
	Email           sql.NullString
	Name            string
	IsRoot          bool
	IsPlatformAdmin bool
	CreatedAt       time.Time
}

func (dbUser) TableName() string { return "users" }

func toUser(r dbUser) *User {
	return &User{ID: int64(r.ID), Email: r.Email.String, Name: r.Name, IsRoot: r.IsRoot, IsPlatformAdmin: r.IsPlatformAdmin}
}

type UserStore struct{ db *gorm.DB }

func NewUserStore(db *gorm.DB) *UserStore { return &UserStore{db: db} }

// EnsureRoot returns the root user, creating it on first boot. The schema's
// partial unique index guarantees there is never more than one.
func (s *UserStore) EnsureRoot() (*User, error) {
	var r dbUser
	err := s.db.Where("is_root = ?", true).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		r = dbUser{Name: "root", IsRoot: true}
		err = s.db.Create(&r).Error
	}
	if err != nil {
		return nil, err
	}
	return toUser(r), nil
}

// GetByID returns the user, or nil if there is none.
func (s *UserStore) GetByID(id int64) (*User, error) {
	var r dbUser
	err := s.db.First(&r, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toUser(r), nil
}

// FindByEmail returns the non-root user with this email, or nil.
func (s *UserStore) FindByEmail(email string) (*User, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return nil, nil
	}
	var r dbUser
	err := s.db.Where("email = ? AND is_root = ?", email, false).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toUser(r), nil
}

// FindOrCreateByEmail returns the user with this email, creating one (with
// no name yet) if there is none.
func (s *UserStore) FindOrCreateByEmail(email string) (*User, error) {
	return findOrCreateByEmail(s.db, email)
}

func findOrCreateByEmail(tx *gorm.DB, email string) (*User, error) {
	email = NormalizeEmail(email)
	if err := validate.Email(email); err != nil {
		return nil, invalid("%s", err)
	}
	r := dbUser{Email: sql.NullString{String: email, Valid: true}, CreatedAt: time.Now()}
	if err := tx.Where("email = ?", email).Attrs(r).FirstOrCreate(&r).Error; err != nil {
		return nil, err
	}
	return toUser(r), nil
}

// List returns every user, root first, then by email.
func (s *UserStore) List() ([]User, error) {
	var rows []dbUser
	if err := s.db.Order("is_root DESC, email").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = *toUser(r)
	}
	return out, nil
}

// GetByIDs returns the users that exist among ids, keyed by ID.
func (s *UserStore) GetByIDs(ids []int64) (map[int64]*User, error) {
	out := map[int64]*User{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []dbUser
	if err := s.db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[int64(r.ID)] = toUser(r)
	}
	return out, nil
}

// SetEmail changes a person's email (the address they're matched by).
func (s *UserStore) SetEmail(id int64, email string) error {
	if err := guardRoot(s.db, id); err != nil {
		return err
	}
	email = NormalizeEmail(email)
	if err := validate.Email(email); err != nil {
		return invalid("%s", err)
	}
	var n int64
	if err := s.db.Model(&dbUser{}).Where("email = ? AND id <> ?", email, id).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return invalid("%s is already used by another person", email)
	}
	return s.db.Model(&dbUser{}).Where("id = ?", id).Update("email", email).Error
}

func (s *UserStore) SetPlatformAdmin(id int64, on bool) error {
	if err := guardRoot(s.db, id); err != nil {
		return err
	}
	return s.db.Model(&dbUser{}).Where("id = ?", id).Update("is_platform_admin", on).Error
}

// SetName records the name a provider reported at sign-in.
func (s *UserStore) SetName(id int64, name string) error {
	if err := guardRoot(s.db, id); err != nil {
		return err
	}
	return s.db.Model(&dbUser{}).Where("id = ?", id).Update("name", strings.TrimSpace(name)).Error
}

// Delete removes a person. The schema cascades to their identities,
// memberships and sessions, and keeps their posts with no author.
func (s *UserStore) Delete(id int64) error {
	if err := guardRoot(s.db, id); err != nil {
		return err
	}
	return s.db.Delete(&dbUser{}, id).Error
}
