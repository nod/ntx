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
)

var ErrInvalidSession = errors.New("invalid session")

type SessionManager struct {
	secret []byte
}

func NewSessionManager(secret string) *SessionManager {
	return &SessionManager{secret: []byte(secret)}
}

func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func (sm *SessionManager) SetSession(w http.ResponseWriter) {
	issuedAt := time.Now().Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", issuedAt)))
	sig := sm.sign(payload)
	value := payload + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionMaxAge.Seconds()),
		SameSite: http.SameSiteStrictMode,
	})
}

func (sm *SessionManager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    cookieName,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})
}

func (sm *SessionManager) IsAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return sm.verify(cookie.Value) == nil
}

func (sm *SessionManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sm.IsAuthenticated(r) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (sm *SessionManager) sign(payload string) string {
	mac := hmac.New(sha256.New, sm.secret)
	mac.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (sm *SessionManager) verify(value string) error {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return ErrInvalidSession
	}
	expected := sm.sign(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return ErrInvalidSession
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidSession
	}
	var issuedAt int64
	if _, err := fmt.Sscanf(string(decoded), "%d", &issuedAt); err != nil {
		return ErrInvalidSession
	}
	if time.Since(time.Unix(issuedAt, 0)) > sessionMaxAge {
		return ErrInvalidSession
	}

	return nil
}
