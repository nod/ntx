package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName    = "ntx_session"
	sessionMaxAge = 24 * time.Hour

	// GlobalSiteID marks a session issued against the global admin
	// password: it authenticates against every site, not just one.
	GlobalSiteID int64 = 0
)

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

// SetSession issues a session scoped to siteID (use GlobalSiteID for a
// cross-site admin session).
func (sm *SessionManager) SetSession(w http.ResponseWriter, siteID int64) {
	issuedAt := time.Now().Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", issuedAt, siteID)))
	sig := sm.sign(payload)
	value := payload + "." + sig
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

// SessionSiteID returns the siteID carried by r's session cookie, and
// whether a valid, unexpired session was present at all.
func (sm *SessionManager) SessionSiteID(r *http.Request) (int64, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return 0, false
	}
	siteID, err := sm.verify(cookie.Value)
	if err != nil {
		return 0, false
	}
	return siteID, true
}

// IsAuthenticated reports whether r carries any valid session, regardless
// of which site it's scoped to.
func (sm *SessionManager) IsAuthenticated(r *http.Request) bool {
	_, ok := sm.SessionSiteID(r)
	return ok
}

// Middleware redirects to /admin/login unless r carries a valid session
// that is either global or scoped to currentSite(r).
func (sm *SessionManager) Middleware(currentSite func(*http.Request) int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			siteID, ok := sm.SessionSiteID(r)
			if !ok || (siteID != GlobalSiteID && siteID != currentSite(r)) {
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

func (sm *SessionManager) verify(value string) (int64, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return 0, ErrInvalidSession
	}
	expected := sm.sign(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return 0, ErrInvalidSession
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrInvalidSession
	}
	var issuedAt, siteID int64
	if _, err := fmt.Sscanf(string(decoded), "%d:%d", &issuedAt, &siteID); err != nil {
		return 0, ErrInvalidSession
	}
	if time.Since(time.Unix(issuedAt, 0)) > sessionMaxAge {
		return 0, ErrInvalidSession
	}

	return siteID, nil
}
