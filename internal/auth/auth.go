package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName    = "ntx_session"
	sessionMaxAge = 24 * time.Hour
)

// Session is what a valid session cookie grants. A global session -- issued
// against the global admin password -- is valid on every site; any other
// session is valid only on SiteID. Global is an explicit flag rather than a
// reserved SiteID so that no id, least of all Go's zero value, can ever be
// mistaken for "every site".
type Session struct {
	Global bool
	SiteID int64 // meaningful only when !Global; always > 0
}

// Allows reports whether s authenticates a request for siteID.
func (s Session) Allows(siteID int64) bool {
	return s.Global || s.SiteID == siteID
}

var ErrInvalidSession = errors.New("invalid session")

type SessionManager struct {
	secret []byte
	secure bool
}

func NewSessionManager(secret string, secure bool) *SessionManager {
	return &SessionManager{secret: []byte(secret), secure: secure}
}

func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// SetSiteSession issues a session valid only on siteID.
func (sm *SessionManager) SetSiteSession(w http.ResponseWriter, siteID int64) {
	sm.setCookie(w, fmt.Sprintf("site:%d", siteID))
}

// SetGlobalSession issues a session valid on every site.
func (sm *SessionManager) SetGlobalSession(w http.ResponseWriter) {
	sm.setCookie(w, "global")
}

// setCookie signs "<issuedAt>:<scope>" and sets it as the session cookie.
func (sm *SessionManager) setCookie(w http.ResponseWriter, scope string) {
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", time.Now().Unix(), scope)))
	value := payload + "." + sm.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionMaxAge.Seconds()),
		SameSite: http.SameSiteStrictMode,
		Secure:   sm.secure,
	})
}

func (sm *SessionManager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		SameSite: http.SameSiteStrictMode,
		Secure:   sm.secure,
	})
}

// Session returns what r's session cookie grants, and whether a valid,
// unexpired session was present at all.
func (sm *SessionManager) Session(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, false
	}
	sess, err := sm.verify(cookie.Value)
	if err != nil {
		return Session{}, false
	}
	return sess, true
}

// Middleware redirects to /admin/login unless r carries a valid session
// that allows the site currentSite resolves for r. If currentSite cannot
// resolve a site it fails closed, even for a global session.
func (sm *SessionManager) Middleware(currentSite func(*http.Request) (int64, bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			siteID, resolved := currentSite(r)
			sess, ok := sm.Session(r)
			if !resolved || !ok || !sess.Allows(siteID) {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (sm *SessionManager) sign(payload string) string {
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (sm *SessionManager) verify(value string) (Session, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return Session{}, ErrInvalidSession
	}
	expected := sm.sign(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return Session{}, ErrInvalidSession
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, ErrInvalidSession
	}

	// "<issuedAt>:global" or "<issuedAt>:site:<id>"; anything else --
	// including the pre-flag "<issuedAt>:<id>" format -- is rejected.
	fields := strings.Split(string(decoded), ":")
	issuedAt, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	var sess Session
	switch {
	case len(fields) == 2 && fields[1] == "global":
		sess.Global = true
	case len(fields) == 3 && fields[1] == "site":
		id, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || id <= 0 {
			return Session{}, ErrInvalidSession
		}
		sess.SiteID = id
	default:
		return Session{}, ErrInvalidSession
	}

	if time.Since(time.Unix(issuedAt, 0)) > sessionMaxAge {
		return Session{}, ErrInvalidSession
	}
	return sess, nil
}
