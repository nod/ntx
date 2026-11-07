package models

import (
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
)

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
