package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/models"
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

type env struct {
	sessions *models.SessionStore
	users    *models.UserStore
	root     *models.User
	siteID   int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s, _ := d.DB(); s.Close() })
	users := models.NewUserStore(d)
	root, err := users.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	site := &models.Site{Key: "a", Hostname: "a.example.com", Theme: "default"}
	if err := models.NewSiteStore(d).Create(site, "admin.example.com"); err != nil {
		t.Fatal(err)
	}
	// A non-root user, inserted directly: phase 1 has no way to create one.
	if err := d.Exec("INSERT INTO users (id, email, name, is_root, is_platform_admin, created_at) VALUES (50, 'x@example.com', 'x', 0, 1, CURRENT_TIMESTAMP)").Error; err != nil {
		t.Fatal(err)
	}
	return &env{sessions: models.NewSessionStore(d), users: users, root: root, siteID: site.ID}
}

func (e *env) manager(rootHash string) *Manager {
	return NewManager(e.sessions, e.users, rootHash, true)
}

// login signs user in through m and returns the session cookie.
func login(t *testing.T, m *Manager, user *models.User, siteID *int64) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := m.Login(rec, user, siteID); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ntx_session" {
			return c
		}
	}
	t.Fatal("Login set no ntx_session cookie")
	return nil
}

// actorFor runs a request carrying cookie through m.Middleware, as if on
// the host current describes, and returns the Actor the handler saw.
func actorFor(m *Manager, cookie *http.Cookie, siteID int64, onSite bool) *Actor {
	var got *Actor
	h := m.Middleware(func(*http.Request) (int64, bool) { return siteID, onSite })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = ActorFrom(r) }))
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

const hash1 = "$2a$10$XKhLm60v72htK/lAO26kDuWPXh.3DovbWhmC/tN.E1UIzH0D.moZ."

func TestRootSessionValidOnIssuingSiteOnly(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	c := login(t, m, e.root, &e.siteID)

	if a := actorFor(m, c, e.siteID, true); a == nil || !a.IsRoot() || !a.IsPlatform() || !a.CanAdminSite() {
		t.Fatalf("root session on its own site: got %+v", a)
	}
	if a := actorFor(m, c, e.siteID+1, true); a != nil {
		t.Error("a session for one site must not authenticate on another")
	}
	if a := actorFor(m, c, 0, false); a != nil {
		t.Error("a site session must not authenticate on the platform host")
	}
}

func TestPlatformHostSession(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	c := login(t, m, e.root, nil)
	if a := actorFor(m, c, 0, false); a == nil || !a.IsRoot() {
		t.Fatalf("platform-host session on the platform host: got %+v", a)
	}
	if a := actorFor(m, c, e.siteID, true); a != nil {
		t.Error("a platform-host session must not authenticate on a site")
	}
}

// Changing root_password_hash in config and restarting must end every
// existing root session.
func TestRootSessionInvalidAfterPasswordChange(t *testing.T) {
	e := newEnv(t)
	c := login(t, e.manager(hash1), e.root, &e.siteID)
	hash2, _ := HashPassword("another")
	if a := actorFor(e.manager(hash2), c, e.siteID, true); a != nil {
		t.Error("a root session issued under the old password hash must be rejected")
	}
}

func TestCookieAttributes(t *testing.T) {
	e := newEnv(t)
	for _, secure := range []bool{true, false} {
		m := NewManager(e.sessions, e.users, hash1, secure)
		c := login(t, m, e.root, &e.siteID)
		if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" || c.Secure != secure {
			t.Errorf("secure=%v: unexpected cookie attributes %+v", secure, c)
		}
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	c := login(t, m, e.root, &e.siteID)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	if err := m.Logout(rec, req); err != nil {
		t.Fatal(err)
	}
	if a := actorFor(m, c, e.siteID, true); a != nil {
		t.Error("a logged-out session must not authenticate")
	}
	cleared := false
	for _, rc := range rec.Result().Cookies() {
		if rc.Name == "ntx_session" && rc.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("Logout must clear the cookie")
	}
}

// Phase 1 has no members: a session belonging to anyone but root is
// invalid, platform admin flag or not.
func TestNonRootSessionRejectedInPhase1(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	other, err := e.users.GetByID(50)
	if err != nil || other == nil {
		t.Fatal(err)
	}
	c := login(t, m, other, &e.siteID)
	if a := actorFor(m, c, e.siteID, true); a != nil {
		t.Errorf("a non-root session must not authenticate in phase 1, got %+v", a)
	}
}

func TestNoCookieNoActor(t *testing.T) {
	e := newEnv(t)
	if a := actorFor(e.manager(hash1), nil, e.siteID, true); a != nil {
		t.Error("no cookie must mean no actor")
	}
	if a := actorFor(e.manager(hash1), &http.Cookie{Name: "ntx_session", Value: "forged"}, e.siteID, true); a != nil {
		t.Error("an unknown token must mean no actor")
	}
}
