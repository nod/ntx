package handlers

import (
	"bytes"
	"html"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/secretbox"
	"github.com/nod/ntx/internal/sso"
)

const hostA = "a.example.com"

// text is the response body with HTML entities decoded, for comparing
// against the exact sentences the pages show.
func text(rec *httptest.ResponseRecorder) string { return html.UnescapeString(rec.Body.String()) }

func noSession(t *testing.T, c *http.Cookie) {
	t.Helper()
	if c != nil && c.MaxAge >= 0 && c.Value != "" {
		t.Fatalf("a session cookie was set: %+v", c)
	}
}

func TestSSOSignInFlow(t *testing.T) {
	e := newEnv(t)
	// A site admin: contributors reach /admin/posts once Task 8 lands.
	e.member("c@x.io", models.RoleAdmin, false)
	flow, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	if flow.SameSite != http.SameSiteLaxMode || !flow.HttpOnly || flow.Path != "/auth" {
		t.Fatalf("ntx_oauth cookie attributes: %+v", flow)
	}
	rec := e.doWith(http.MethodGet, hostA, back.RequestURI(), nil, flow)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "/admin/posts") {
		t.Fatalf("callback: %d %s", rec.Code, body)
	}
	if c := cookieNamed(rec, "ntx_oauth"); c == nil || c.MaxAge >= 0 {
		t.Fatalf("ntx_oauth not cleared: %+v", c)
	}
	sess := cookieNamed(rec, "ntx_session")
	if sess == nil {
		t.Fatal("no session cookie")
	}
	if got := e.do(http.MethodGet, hostA, "/admin/posts", nil, sess); got.Code != http.StatusOK {
		t.Fatalf("/admin/posts with the new session: %d", got.Code)
	}
}

func TestSSOUnknownUser403(t *testing.T) {
	e := newEnv(t)
	rec, sess := e.ssoLogin(hostA, verified("s1", "stranger@x.io"), "")
	if rec.Code != http.StatusForbidden || !strings.Contains(text(rec), "You don't have access to this site.") {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	noSession(t, sess)
}

func TestSSOUnverifiedEmail403(t *testing.T) {
	e := newEnv(t)
	e.member("c@x.io", models.RoleContributor, false)
	id := verified("s1", "c@x.io")
	id.EmailVerified = false
	rec, sess := e.ssoLogin(hostA, id, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("callback: %d", rec.Code)
	}
	noSession(t, sess)
}

func TestCallbackOtherSite(t *testing.T) {
	e := newEnv(t)
	b := &models.Site{Key: "b", Hostname: "b.example.com", Theme: "t1"}
	if err := e.sites.Create(b, platformHost); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.Reload(); err != nil {
		t.Fatal(err)
	}
	u := e.member("c@x.io", models.RoleContributor, false)
	if err := e.mems.Add(u.ID, b.ID, models.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	flow, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	rec := e.doWith(http.MethodGet, "b.example.com", back.RequestURI(), nil, flow)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("callback finished on another site: %d %s", rec.Code, rec.Body.String())
	}
	noSession(t, cookieNamed(rec, "ntx_session"))
}

func TestCallbackStateMismatch(t *testing.T) {
	e := newEnv(t)
	e.member("c@x.io", models.RoleContributor, false)
	flow, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	q := back.Query()
	q.Set("state", "forged")
	rec := e.doWith(http.MethodGet, hostA, "/auth/callback?"+q.Encode(), nil, flow)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("state mismatch: %d", rec.Code)
	}
	noSession(t, cookieNamed(rec, "ntx_session"))
}

func TestCallbackWithoutFlowCookie(t *testing.T) {
	e := newEnv(t)
	e.member("c@x.io", models.RoleContributor, false)
	_, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	rec := e.do(http.MethodGet, hostA, back.RequestURI(), nil, nil)
	body := text(rec)
	if rec.Code != http.StatusBadRequest || !strings.Contains(body, "Your sign-in expired. Try again.") || !strings.Contains(body, `href="/admin/login"`) {
		t.Fatalf("no flow cookie: %d %s", rec.Code, body)
	}
	noSession(t, cookieNamed(rec, "ntx_session"))
}

func TestCallbackExpiredFlow(t *testing.T) {
	e := newEnv(t)
	e.member("c@x.io", models.RoleContributor, false)
	_, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	old := sso.NewFlow(e.siteA.ID, "", time.Now().Add(-time.Hour))
	old.State = back.Query().Get("state")
	stale := &http.Cookie{Name: "ntx_oauth", Value: old.Encode(testSessionSecret)}
	rec := e.doWith(http.MethodGet, hostA, back.RequestURI(), nil, stale)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expired flow: %d", rec.Code)
	}
	noSession(t, cookieNamed(rec, "ntx_session"))
}

func TestCallbackProviderError(t *testing.T) {
	e := newEnv(t)
	flow, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	rec := e.doWith(http.MethodGet, hostA, "/auth/callback?error=access_denied&state="+back.Query().Get("state"), nil, flow)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Sign-in was cancelled.") {
		t.Fatalf("provider error: %d %s", rec.Code, rec.Body.String())
	}
	noSession(t, cookieNamed(rec, "ntx_session"))
}

func TestStartProviderUnreachable(t *testing.T) {
	e := newEnv(t)
	e.h.d.IssuerFor = func(string, string) string { return "http://127.0.0.1:1" }
	rec := e.do(http.MethodGet, hostA, "/auth/start", nil, nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "reach the sign-in provider") {
		t.Fatalf("unreachable provider: %d %s", rec.Code, rec.Body.String())
	}
}

func TestStartUndecryptableSecret(t *testing.T) {
	e := newEnv(t)
	sealed, _ := secretbox.Seal([]byte("another-key-another-key-another-"), "x")
	if err := e.sites.SetProvider(e.siteA.ID, models.ProviderSettings{Type: models.ProviderAuth0, Domain: "a-tenant.example.com", ClientID: e.fake.ClientID}, sealed); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodGet, hostA, "/auth/start", nil, nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "client secret") {
		t.Fatalf("undecryptable secret: %d %s", rec.Code, rec.Body.String())
	}
}

func TestStartNoProvider404(t *testing.T) {
	e := newEnv(t)
	if err := e.sites.SetProvider(e.siteA.ID, models.ProviderSettings{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.Reload(); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodGet, hostA, "/auth/start", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("/auth/start without a provider: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, platformHost, "/auth/start", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("/auth/start on the platform host: %d", rec.Code)
	}
}

func TestInviteFlow(t *testing.T) {
	e := newEnv(t)
	_, token, err := e.invites.Create(e.siteA.ID, "new@x.io", models.RoleAdmin, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	page := e.do(http.MethodGet, hostA, "/invite/"+token, nil, nil)
	body := page.Body.String()
	for _, want := range []string{"Site A", "admin", "Auth0", `action="/auth/start"`, `name="invite"`, `value="` + token + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("invite page lacks %q", want)
		}
	}
	rec, sess := e.ssoLogin(hostA, verified("s9", "new@x.io"), token)
	if rec.Code != http.StatusOK || sess == nil {
		t.Fatalf("accepting the invite: %d %s", rec.Code, rec.Body.String())
	}
	u, _ := e.users.FindByEmail("new@x.io")
	if m, _ := e.mems.Get(u.ID, e.siteA.ID); m == nil || m.Role != models.RoleAdmin {
		t.Fatalf("membership = %+v", m)
	}
}

// The invite page posts the token. One in a GET link is ignored, so
// invite tokens never travel in a URL that ends up in logs or history.
func TestInviteTokenIgnoredInGetLink(t *testing.T) {
	e := newEnv(t)
	_, token, err := e.invites.Create(e.siteA.ID, "new@x.io", models.RoleAdmin, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.fake.SetNext(verified("s9", "new@x.io"))
	start := e.do(http.MethodGet, hostA, "/auth/start?invite="+url.QueryEscape(token), nil, nil)
	if start.Code != http.StatusFound {
		t.Fatalf("/auth/start: %d", start.Code)
	}
	back, err := url.Parse(e.fake.Authorize(t, start.Header().Get("Location")))
	if err != nil {
		t.Fatal(err)
	}
	rec := e.doWith(http.MethodGet, hostA, back.RequestURI(), nil, cookieNamed(start, "ntx_oauth"))
	noSession(t, cookieNamed(rec, "ntx_session"))
	if u, _ := e.users.FindByEmail("new@x.io"); u != nil {
		if m, _ := e.mems.Get(u.ID, e.siteA.ID); m != nil {
			t.Fatalf("a GET link accepted the invite: %+v", m)
		}
	}
}

func TestInviteWrongAccount(t *testing.T) {
	e := newEnv(t)
	_, token, err := e.invites.Create(e.siteA.ID, "new@x.io", models.RoleAdmin, false, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec, sess := e.ssoLogin(hostA, verified("s9", "other@x.io"), token)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "new@x.io") {
		t.Fatalf("wrong account: %d %s", rec.Code, rec.Body.String())
	}
	noSession(t, sess)
}

func TestInvitePageInvalidToken(t *testing.T) {
	e := newEnv(t)
	rec := e.do(http.MethodGet, hostA, "/invite/not-a-token", nil, nil)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "This invite link is no longer valid.") {
		t.Fatalf("bad invite: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginPageShowsProviderButton(t *testing.T) {
	e := newEnv(t)
	body := e.do(http.MethodGet, hostA, "/admin/login", nil, nil).Body.String()
	if !strings.Contains(body, `href="/auth/start"`) || !strings.Contains(body, "Sign in with Auth0") {
		t.Fatalf("site login page lacks the provider button: %s", body)
	}
	body = e.do(http.MethodGet, platformHost, "/admin/login", nil, nil).Body.String()
	if strings.Contains(body, "/auth/start") {
		t.Fatal("platform login page shows a provider button")
	}
}

// A provider that refuses the sign-in (Auth0 rule, unauthorized_client,
// unregistered callback) says why in error and error_description; an admin
// debugging setup needs that in the log.
func TestCallbackProviderErrorIsLogged(t *testing.T) {
	e := newEnv(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	flow, back := e.startSSO(hostA, verified("s1", "c@x.io"), "")
	path := "/auth/callback?error=unauthorized_client&error_description=Callback+URL+mismatch&state=" + back.Query().Get("state")
	if rec := e.doWith(http.MethodGet, hostA, path, nil, flow); rec.Code != http.StatusBadRequest {
		t.Fatalf("provider error: %d", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "unauthorized_client") || !strings.Contains(out, "Callback URL mismatch") || !strings.Contains(out, `"a"`) {
		t.Fatalf("log lacks the provider's error: %q", out)
	}
}
