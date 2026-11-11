package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Session is a signed-in browser. It is valid only on the host it was
// issued for: SiteID is that site, or nil for root on the platform host.
type Session struct {
	ID             int64
	UserID         int64
	SiteID         *int64
	RootHashDigest string // root only: sha256 of root_password_hash at sign-in
	ExpiresAt      time.Time
	LastSeenAt     time.Time
}

type dbSession struct {
	ID             uint `gorm:"primarykey"`
	TokenHash      string
	UserID         int64
	SiteID         *int64
	RootHashDigest *string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	LastSeenAt     time.Time
}

func (dbSession) TableName() string { return "sessions" }

// touchInterval bounds how often a lookup writes last_seen_at.
const touchInterval = time.Minute

type SessionStore struct{ db *gorm.DB }

func NewSessionStore(db *gorm.DB) *SessionStore { return &SessionStore{db: db} }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create stores a new session and returns the token for the cookie. Only
// the token's hash is stored.
func (s *SessionStore) Create(userID int64, siteID *int64, rootHashDigest string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	r := dbSession{
		TokenHash: hashToken(token), UserID: userID, SiteID: siteID,
		CreatedAt: now, ExpiresAt: now.Add(ttl), LastSeenAt: now,
	}
	if rootHashDigest != "" {
		r.RootHashDigest = &rootHashDigest
	}
	if err := s.db.Create(&r).Error; err != nil {
		return "", err
	}
	return token, nil
}

// Lookup returns the session for token, or nil if it is unknown or expired
// at now. It refreshes last_seen_at at most once per touchInterval.
func (s *SessionStore) Lookup(token string, now time.Time) (*Session, error) {
	var r dbSession
	err := s.db.Where("token_hash = ?", hashToken(token)).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !now.Before(r.ExpiresAt) {
		return nil, nil
	}
	if now.Sub(r.LastSeenAt) >= touchInterval {
		if err := s.db.Model(&r).Update("last_seen_at", now).Error; err != nil {
			return nil, err
		}
		r.LastSeenAt = now
	}
	sess := &Session{ID: int64(r.ID), UserID: r.UserID, SiteID: r.SiteID, ExpiresAt: r.ExpiresAt, LastSeenAt: r.LastSeenAt}
	if r.RootHashDigest != nil {
		sess.RootHashDigest = *r.RootHashDigest
	}
	return sess, nil
}

// Delete removes the session for token, if any.
func (s *SessionStore) Delete(token string) error {
	return s.db.Where("token_hash = ?", hashToken(token)).Delete(&dbSession{}).Error
}

// DeleteForUser signs the user out everywhere.
func (s *SessionStore) DeleteForUser(userID int64) error {
	return s.db.Where("user_id = ?", userID).Delete(&dbSession{}).Error
}

// DeleteExpired removes every session expired at now (the rule Lookup
// uses) and returns how many it removed. serve runs it periodically.
func (s *SessionStore) DeleteExpired(now time.Time) (int64, error) {
	res := s.db.Where("expires_at <= ?", now).Delete(&dbSession{})
	return res.RowsAffected, res.Error
}
