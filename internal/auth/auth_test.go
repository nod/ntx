package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHashAndCheck(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "hunter2"); err != nil {
		t.Error("correct password rejected")
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Error("wrong password accepted")
	}
}

func TestSessionRoundtrip(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w)

	resp := w.Result()
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])

	if !sm.IsAuthenticated(req) {
		t.Error("valid session not authenticated")
	}
}

func TestSessionTampering(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w)
	resp := w.Result()
	cookie := resp.Cookies()[0]

	// tamper with cookie value
	cookie.Value = cookie.Value + "x"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if sm.IsAuthenticated(req) {
		t.Error("tampered session accepted")
	}
}

func TestSessionMissing(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if sm.IsAuthenticated(req) {
		t.Error("unauthenticated request passed")
	}
}

func TestDifferentSecrets(t *testing.T) {
	sm1 := NewSessionManager("secret-one-1234567890abcdef1234", false)
	sm2 := NewSessionManager("secret-two-1234567890abcdef1234", false)

	w := httptest.NewRecorder()
	sm1.SetSession(w)
	cookie := w.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if sm2.IsAuthenticated(req) {
		t.Error("session from different secret accepted")
	}
}

func TestMiddlewareRedirects(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	handler := sm.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", w.Code)
	}
}

func TestClearSession(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSession(w)
	cookie := w.Result().Cookies()[0]

	w2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(cookie)
	sm.ClearSession(w2)

	cleared := w2.Result().Cookies()
	for _, c := range cleared {
		if c.Name == cookieName && c.MaxAge < 0 {
			return
		}
	}
	t.Error("session cookie not cleared")
}

func TestSessionExpiry(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	// Build a valid-signature cookie with an issuedAt > 24h ago.
	expired := time.Now().Add(-25 * time.Hour).Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", expired)))
	sig := sm.sign(payload)
	value := payload + "." + sig

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})

	if sm.IsAuthenticated(req) {
		t.Error("expired session accepted")
	}
}
