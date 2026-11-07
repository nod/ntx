// Package auth signs people in and works out who a request belongs to.
// Sessions live in the database (models.SessionStore); the cookie holds
// only a random token, and a session is valid only on the host it was
// issued for.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nod/ntx/internal/models"
)

const (
	cookieName = "ntx_session"
	sessionTTL = 24 * time.Hour
)

func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// RootDigest fingerprints root_password_hash. Root sessions store it at
// sign-in, so changing the hash in config ends every existing root session.
func RootDigest(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])
}

// Actor is who a request belongs to. A nil *Actor is an anonymous request;
// every method is safe to call on nil.
type Actor struct {
	User    *models.User
	Session *models.Session
	// Membership is the user's current membership on the session's site:
	// nil for root, set for everyone else (a session without one is
	// invalid).
	Membership *models.Membership
}

func (a *Actor) IsRoot() bool { return a != nil && a.User.IsRoot }

// IsPlatform reports root or platform-admin powers.
func (a *Actor) IsPlatform() bool { return a != nil && (a.User.IsRoot || a.User.IsPlatformAdmin) }

type Manager struct {
	sessions    *models.SessionStore
	users       *models.UserStore
	memberships *models.MembershipStore
	rootDigest  string
	secure      bool
}

func NewManager(sessions *models.SessionStore, users *models.UserStore, memberships *models.MembershipStore, rootPasswordHash string, secure bool) *Manager {
	return &Manager{sessions: sessions, users: users, memberships: memberships, rootDigest: RootDigest(rootPasswordHash), secure: secure}
}

// Login starts a session for user on the current host -- siteID is the
// resolved site, or nil on the platform host -- and sets the cookie.
func (m *Manager) Login(w http.ResponseWriter, user *models.User, siteID *int64) error {
	digest := ""
	if user.IsRoot {
		digest = m.rootDigest
	}
	token, err := m.sessions.Create(user.ID, siteID, digest, sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionTTL.Seconds()),
		SameSite: http.SameSiteStrictMode,
		Secure:   m.secure,
	})
	return nil
}

// Logout deletes the request's session and clears the cookie.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(cookieName); err == nil {
		if err := m.sessions.Delete(c.Value); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		SameSite: http.SameSiteStrictMode,
		Secure:   m.secure,
	})
	return nil
}

type ctxKey struct{}

// Middleware attaches the request's Actor, if its session is valid for the
// current host. current reports the resolved site, or onSite=false on the
// platform host. It never redirects: handlers decide what anonymous
// requests get.
func (m *Manager) Middleware(current func(*http.Request) (siteID int64, onSite bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a := m.actor(r, current); a != nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, a))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (m *Manager) actor(r *http.Request, current func(*http.Request) (int64, bool)) *Actor {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := m.sessions.Lookup(c.Value, time.Now())
	if err != nil || sess == nil {
		return nil
	}
	siteID, onSite := current(r)
	if onSite {
		if sess.SiteID == nil || *sess.SiteID != siteID {
			return nil
		}
	} else if sess.SiteID != nil {
		return nil
	}
	user, err := m.users.GetByID(sess.UserID)
	if err != nil || user == nil {
		return nil
	}
	if user.IsRoot {
		if sess.RootHashDigest != m.rootDigest {
			return nil
		}
		return &Actor{User: user, Session: sess}
	}
	// Everyone else needs a current membership on the session's own site,
	// platform admins included, so removing it takes effect right away.
	if sess.SiteID == nil {
		return nil
	}
	mem, err := m.memberships.Get(user.ID, *sess.SiteID)
	if err != nil || mem == nil {
		return nil
	}
	return &Actor{User: user, Session: sess, Membership: mem}
}

// ActorFrom returns the request's Actor, or nil if it is anonymous.
func ActorFrom(r *http.Request) *Actor {
	a, _ := r.Context().Value(ctxKey{}).(*Actor)
	return a
}
