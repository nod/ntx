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
	mems     *models.MembershipStore
	root     *models.User
	siteID   int64
	siteB    int64
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
	siteB := &models.Site{Key: "b", Hostname: "b.example.com", Theme: "default"}
	if err := models.NewSiteStore(d).Create(siteB, "admin.example.com"); err != nil {
		t.Fatal(err)
	}
	return &env{sessions: models.NewSessionStore(d), users: users, mems: models.NewMembershipStore(d), root: root, siteID: site.ID, siteB: siteB.ID}
}

func (e *env) manager(rootHash string) *Manager {
	return NewManager(e.sessions, e.users, e.mems, rootHash, true)
}

// person creates a user with email and, if role is set, a membership on
// site A.
func (e *env) person(t *testing.T, email, role string) *models.User {
	t.Helper()
	u, err := e.users.FindOrCreateByEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	if role != "" {
		if err := e.mems.Add(u.ID, e.siteID, role, false); err != nil {
			t.Fatal(err)
		}
	}
	return u
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

	if a := actorFor(m, c, e.siteID, true); a == nil || !a.IsRoot() || !a.IsPlatform() || a.Membership != nil {
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
		m := NewManager(e.sessions, e.users, e.mems, hash1, secure)
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

func TestMemberSessionValidWithMembership(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "c@x.io", models.RoleContributor)
	a := actorFor(m, login(t, m, u, &e.siteID), e.siteID, true)
	if a == nil || a.Membership == nil || a.Membership.Role != models.RoleContributor || a.IsPlatform() {
		t.Fatalf("actor = %+v", a)
	}
}

func TestMemberSessionInvalidAfterRemoval(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "c@x.io", models.RoleContributor)
	c := login(t, m, u, &e.siteID)
	if err := e.mems.Remove(u.ID, e.siteID); err != nil {
		t.Fatal(err)
	}
	if a := actorFor(m, c, e.siteID, true); a != nil {
		t.Fatalf("session still valid after the membership was removed: %+v", a)
	}
}

func TestPlatformAdminNeedsMembership(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "p@x.io", "")
	if err := e.users.SetPlatformAdmin(u.ID, true); err != nil {
		t.Fatal(err)
	}
	u, _ = e.users.GetByID(u.ID)
	if a := actorFor(m, login(t, m, u, &e.siteID), e.siteID, true); a != nil {
		t.Fatalf("a platform admin with no membership here authenticated: %+v", a)
	}
}

func TestPlatformFlagReadEachRequest(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "p@x.io", models.RoleContributor)
	if err := e.users.SetPlatformAdmin(u.ID, true); err != nil {
		t.Fatal(err)
	}
	c := login(t, m, u, &e.siteID)
	if a := actorFor(m, c, e.siteID, true); !a.IsPlatform() {
		t.Fatal("platform admin not recognised")
	}
	if err := e.users.SetPlatformAdmin(u.ID, false); err != nil {
		t.Fatal(err)
	}
	if a := actorFor(m, c, e.siteID, true); a == nil || a.IsPlatform() {
		t.Fatalf("after revoking, actor = %+v; want a plain member", a)
	}
}

func TestMemberSessionOtherSiteInvalid(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "c@x.io", models.RoleContributor)
	if err := e.mems.Add(u.ID, e.siteB, models.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	if a := actorFor(m, login(t, m, u, &e.siteID), e.siteB, true); a != nil {
		t.Fatalf("a site-A session authenticated on site B: %+v", a)
	}
}

func TestMemberSessionNilSiteInvalid(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "c@x.io", models.RoleAdmin)
	if err := e.users.SetPlatformAdmin(u.ID, true); err != nil {
		t.Fatal(err)
	}
	u, _ = e.users.GetByID(u.ID)
	c := login(t, m, u, nil)
	if a := actorFor(m, c, 0, false); a != nil {
		t.Fatalf("a member session with no site authenticated on the platform host: %+v", a)
	}
}

func TestDeletedUserSessionInvalid(t *testing.T) {
	e := newEnv(t)
	m := e.manager(hash1)
	u := e.person(t, "c@x.io", models.RoleContributor)
	c := login(t, m, u, &e.siteID)
	if err := e.users.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	if a := actorFor(m, c, e.siteID, true); a != nil {
		t.Fatalf("deleted user's session authenticated: %+v", a)
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
