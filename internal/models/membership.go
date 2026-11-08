package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	RoleAdmin       = "admin"
	RoleContributor = "contributor"
)

// Membership is a person's role on one site. CanPublish matters only for
// contributors; site admins can always publish.
type Membership struct {
	ID         int64
	UserID     int64
	SiteID     int64
	Role       string
	CanPublish bool
}

// Member is a membership with its person, for listing a site's people.
type Member struct {
	Membership Membership
	User       User
}

type dbMembership struct {
	ID         uint `gorm:"primarykey"`
	UserID     int64
	SiteID     int64
	Role       string
	CanPublish bool
	CreatedAt  time.Time
}

func (dbMembership) TableName() string { return "memberships" }

func toMembership(r dbMembership) *Membership {
	return &Membership{ID: int64(r.ID), UserID: r.UserID, SiteID: r.SiteID, Role: r.Role, CanPublish: r.CanPublish}
}

func validRole(role string) error {
	if role != RoleAdmin && role != RoleContributor {
		return invalid("role must be admin or contributor")
	}
	return nil
}

type MembershipStore struct{ db *gorm.DB }

func NewMembershipStore(db *gorm.DB) *MembershipStore { return &MembershipStore{db: db} }

// Get returns the user's membership on the site, or nil.
func (s *MembershipStore) Get(userID, siteID int64) (*Membership, error) {
	var r dbMembership
	err := s.db.Where("user_id = ? AND site_id = ?", userID, siteID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toMembership(r), nil
}

// Add gives the user a role on the site. Adding an existing member is an
// error rather than a silent role change.
func (s *MembershipStore) Add(userID, siteID int64, role string, canPublish bool) error {
	return addMembership(s.db, userID, siteID, role, canPublish)
}

func addMembership(tx *gorm.DB, userID, siteID int64, role string, canPublish bool) error {
	if err := guardRoot(tx, userID); err != nil {
		return err
	}
	if err := validRole(role); err != nil {
		return err
	}
	var n int64
	if err := tx.Model(&dbMembership{}).Where("user_id = ? AND site_id = ?", userID, siteID).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return invalid("already a member of this site")
	}
	return tx.Create(&dbMembership{UserID: userID, SiteID: siteID, Role: role, CanPublish: canPublish, CreatedAt: time.Now()}).Error
}

func (s *MembershipStore) Update(userID, siteID int64, role string, canPublish bool) error {
	if err := validRole(role); err != nil {
		return err
	}
	res := s.db.Model(&dbMembership{}).Where("user_id = ? AND site_id = ?", userID, siteID).
		Updates(map[string]any{"role": role, "can_publish": canPublish})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalid("not a member of this site")
	}
	return nil
}

func (s *MembershipStore) Remove(userID, siteID int64) error {
	res := s.db.Where("user_id = ? AND site_id = ?", userID, siteID).Delete(&dbMembership{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalid("not a member of this site")
	}
	return nil
}

// ListForSite returns the site's members with their users, by email.
func (s *MembershipStore) ListForSite(siteID int64) ([]Member, error) {
	type row struct {
		dbMembership
		Email           *string
		Name            string
		IsPlatformAdmin bool
	}
	var rows []row
	err := s.db.Table("memberships").
		Select("memberships.*, users.email, users.name, users.is_platform_admin").
		Joins("JOIN users ON users.id = memberships.user_id").
		Where("memberships.site_id = ?", siteID).
		Order("users.email").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		u := User{ID: r.UserID, Name: r.Name, IsPlatformAdmin: r.IsPlatformAdmin}
		if r.Email != nil {
			u.Email = *r.Email
		}
		out[i] = Member{Membership: *toMembership(r.dbMembership), User: u}
	}
	return out, nil
}

func (s *MembershipStore) ListForUser(userID int64) ([]Membership, error) {
	var rows []dbMembership
	if err := s.db.Where("user_id = ?", userID).Order("site_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Membership, len(rows))
	for i, r := range rows {
		out[i] = *toMembership(r)
	}
	return out, nil
}

// CountBySite returns the number of members per site ID.
func (s *MembershipStore) CountBySite() (map[int64]int, error) {
	var rows []struct {
		SiteID int64
		N      int
	}
	if err := s.db.Model(&dbMembership{}).Select("site_id, COUNT(*) AS n").Group("site_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for _, r := range rows {
		out[r.SiteID] = r.N
	}
	return out, nil
}
