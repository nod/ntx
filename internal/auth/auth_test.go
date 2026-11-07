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
	sm.SetSiteSession(w, 42)

	resp := w.Result()
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])

	sess, ok := sm.Session(req)
	if !ok {
		t.Fatal("expected a valid session")
	}
	if sess.Global || sess.SiteID != 42 {
		t.Errorf("expected a site session for 42, got %+v", sess)
	}
}

func TestSessionTampering(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSiteSession(w, 1)
	resp := w.Result()
	cookie := resp.Cookies()[0]

	cookie.Value = cookie.Value + "x"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if _, ok := sm.Session(req); ok {
		t.Error("tampered session accepted")
	}
}

func TestSessionMissing(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := sm.Session(req); ok {
		t.Error("unauthenticated request passed")
	}
}

func TestDifferentSecrets(t *testing.T) {
	sm1 := NewSessionManager("secret-one-1234567890abcdef1234", false)
	sm2 := NewSessionManager("secret-two-1234567890abcdef1234", false)

	w := httptest.NewRecorder()
	sm1.SetSiteSession(w, 1)
	cookie := w.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if _, ok := sm2.Session(req); ok {
		t.Error("session from different secret accepted")
	}
}

func TestMiddlewareRedirectsWhenUnauthenticated(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	currentSite := func(*http.Request) (int64, bool) { return 1, true }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", w.Code)
	}
}

func TestMiddlewareAllowsMatchingSite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSiteSession(w, 1)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) (int64, bool) { return 1, true }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareRejectsMismatchedSite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSiteSession(w, 1)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) (int64, bool) { return 2, true }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected redirect for mismatched site, got %d", rec.Code)
	}
}

func TestMiddlewareAllowsGlobalSessionOnAnySite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetGlobalSession(w)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) (int64, bool) { return 99, true }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected global session to pass on any site, got %d", rec.Code)
	}
}

func TestClearSession(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	w := httptest.NewRecorder()
	sm.SetSiteSession(w, 1)
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

	expired := time.Now().Add(-25 * time.Hour).Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:site:%d", expired, 1)))
	sig := sm.sign(payload)
	value := payload + "." + sig

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})

	if _, ok := sm.Session(req); ok {
		t.Error("expired session accepted")
	}
}

func TestMiddlewareRejectsExpiredSession(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)

	// Validly signed, but issued outside sessionMaxAge.
	expired := time.Now().Add(-25 * time.Hour).Unix()
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:site:%d", expired, 1)))
	sig := sm.sign(payload)
	value := payload + "." + sig

	// currentSite matches the cookie's siteID (1), so this would pass if
	// the session were merely scope-matched but not expired: a failure
	// here can only be attributed to the expiry check.
	currentSite := func(*http.Request) (int64, bool) { return 1, true }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected redirect for expired session, got %d", rec.Code)
	}
}

// signedCookie forges a correctly signed session cookie around an arbitrary
// payload, for asserting how verify treats payloads SetSession never writes.
func signedCookie(sm *SessionManager, payload string) *http.Cookie {
	enc := base64.StdEncoding.EncodeToString([]byte(payload))
	return &http.Cookie{Name: cookieName, Value: enc + "." + sm.sign(enc)}
}

func TestGlobalSessionRoundtrip(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	w := httptest.NewRecorder()
	sm.SetGlobalSession(w)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(w.Result().Cookies()[0])
	sess, ok := sm.Session(req)
	if !ok || !sess.Global {
		t.Errorf("expected a valid global session, got %+v ok=%v", sess, ok)
	}
}

// The heart of cleanup 6: a site ID of 0 -- Go's zero value, what an
// un-persisted config.Site carries -- used to *be* the global marker, so a
// site session minted from an unset ID authenticated everywhere. It must
// grant nothing instead.
func TestSiteSessionWithZeroIDIsNotGlobal(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	w := httptest.NewRecorder()
	sm.SetSiteSession(w, 0)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) (int64, bool) { return 99, true }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("a site session for id 0 must not pass on site 99, got %d", rec.Code)
	}
}

// Payloads in the old "<ts>:<siteID>" format -- where "<ts>:0" meant
// global -- and site payloads with a non-positive id must not verify.
func TestMalformedSessionPayloadsRejected(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	now := time.Now().Unix()
	for _, payload := range []string{
		fmt.Sprintf("%d:0", now),        // legacy global
		fmt.Sprintf("%d:7", now),        // legacy site
		fmt.Sprintf("%d:site:0", now),   // no real site has id 0
		fmt.Sprintf("%d:site:-1", now),  // ...or a negative one
		fmt.Sprintf("%d:site:", now),    // missing id
		fmt.Sprintf("%d:site:7:x", now), // trailing junk
		fmt.Sprintf("%d:global:7", now), // global carries no id
		fmt.Sprintf("%d:admin", now),    // unknown kind
		"notanumber:global",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(signedCookie(sm, payload))
		if sess, ok := sm.Session(req); ok {
			t.Errorf("payload %q: expected rejection, got %+v", payload, sess)
		}
	}
}

// If the site can't be resolved the middleware must fail closed -- even
// for a global session -- rather than rely on a sentinel id.
func TestMiddlewareRejectsUnresolvedSite(t *testing.T) {
	sm := NewSessionManager("supersecretkey1234567890abcdef12", false)
	w := httptest.NewRecorder()
	sm.SetGlobalSession(w)
	cookie := w.Result().Cookies()[0]

	currentSite := func(*http.Request) (int64, bool) { return 0, false }
	handler := sm.Middleware(currentSite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected redirect when no site is resolved, got %d", rec.Code)
	}
}
