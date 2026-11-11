package sso_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/nod/ntx/internal/sso"
	"github.com/nod/ntx/internal/sso/ssotest"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

const redirect = "http://a.example.com/auth/callback"

func settings(fake *ssotest.Provider) sso.Settings {
	return sso.Settings{Issuer: fake.URL, ClientID: fake.ClientID, ClientSecret: fake.ClientSecret, RedirectURL: redirect}
}

// signIn runs AuthCodeURL, the fake's authorize step and Exchange.
func signIn(t *testing.T, c *sso.Client, s sso.Settings, fake *ssotest.Provider, f sso.Flow, exchangeFlow sso.Flow) (sso.Claims, error) {
	t.Helper()
	authURL, err := c.AuthCodeURL(context.Background(), s, f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := url.Parse(fake.Authorize(t, authURL))
	if err != nil {
		t.Fatal(err)
	}
	if back.Query().Get("state") != f.State {
		t.Fatalf("state in callback = %q, want %q", back.Query().Get("state"), f.State)
	}
	return c.Exchange(context.Background(), s, back.Query().Get("code"), exchangeFlow)
}

func TestIssuer(t *testing.T) {
	if got := sso.Issuer("google", "ignored"); got != "https://accounts.google.com" {
		t.Errorf("google issuer = %q", got)
	}
	if got := sso.Issuer("auth0", "acme.us.auth0.com"); got != "https://acme.us.auth0.com/" {
		t.Errorf("auth0 issuer = %q", got)
	}
}

func TestFlowRoundTrip(t *testing.T) {
	now := time.Now()
	f := sso.NewFlow(7, "inv-token", now)
	if f.State == "" || f.Nonce == "" || f.Verifier == "" || f.State == f.Nonce {
		t.Fatalf("NewFlow = %+v", f)
	}
	if !f.Expires.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("Expires = %v, want now+10m", f.Expires)
	}
	got, err := sso.DecodeFlow(secret, f.Encode(secret), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != f.State || got.Nonce != f.Nonce || got.Verifier != f.Verifier || got.SiteID != 7 || got.Invite != "inv-token" || !got.Expires.Equal(f.Expires) {
		t.Fatalf("decoded %+v, want %+v", got, f)
	}
}

func TestFlowTamperRejected(t *testing.T) {
	now := time.Now()
	v := []byte(sso.NewFlow(7, "", now).Encode(secret))
	v[3] ^= 1
	if _, err := sso.DecodeFlow(secret, string(v), now); !errors.Is(err, sso.ErrFlowInvalid) {
		t.Fatalf("tampered flow = %v, want ErrFlowInvalid", err)
	}
	if _, err := sso.DecodeFlow([]byte("another-secret-another-secret-xx"), sso.NewFlow(7, "", now).Encode(secret), now); !errors.Is(err, sso.ErrFlowInvalid) {
		t.Fatalf("flow under another secret = %v, want ErrFlowInvalid", err)
	}
	for _, junk := range []string{"", ".", "abc", "a.b.c"} {
		if _, err := sso.DecodeFlow(secret, junk, now); !errors.Is(err, sso.ErrFlowInvalid) {
			t.Fatalf("DecodeFlow(%q) = %v, want ErrFlowInvalid", junk, err)
		}
	}
}

func TestFlowExpired(t *testing.T) {
	now := time.Now()
	f := sso.NewFlow(7, "", now)
	if _, err := sso.DecodeFlow(secret, f.Encode(secret), f.Expires.Add(time.Second)); !errors.Is(err, sso.ErrFlowInvalid) {
		t.Fatalf("expired flow = %v, want ErrFlowInvalid", err)
	}
}

func TestAuthCodeURLHasPKCEAndScopes(t *testing.T) {
	fake := ssotest.New(t)
	f := sso.NewFlow(1, "", time.Now())
	u, err := sso.NewClient(nil).AuthCodeURL(context.Background(), settings(fake), f)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(u)
	v := q.Query()
	for k, want := range map[string]string{
		"code_challenge_method": "S256", "scope": "openid email profile",
		"state": f.State, "nonce": f.Nonce, "redirect_uri": redirect, "client_id": fake.ClientID,
	} {
		if v.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, v.Get(k), want)
		}
	}
	if v.Get("code_challenge") == "" || v.Get("code_challenge") == f.Verifier {
		t.Errorf("code_challenge = %q; want the S256 of the verifier", v.Get("code_challenge"))
	}
}

func TestExchangeReturnsClaims(t *testing.T) {
	fake := ssotest.New(t)
	fake.SetNext(ssotest.Identity{Subject: "sub-1", Email: "Alice@Example.com", Name: "Alice", EmailVerified: true})
	f := sso.NewFlow(1, "", time.Now())
	got, err := signIn(t, sso.NewClient(nil), settings(fake), fake, f, f)
	if err != nil {
		t.Fatal(err)
	}
	want := sso.Claims{Issuer: fake.URL, Subject: "sub-1", Email: "alice@example.com", Name: "Alice", EmailVerified: true}
	if got != want {
		t.Fatalf("claims = %+v, want %+v", got, want)
	}
}

func TestExchangeWrongNonce(t *testing.T) {
	fake := ssotest.New(t)
	fake.SetNext(ssotest.Identity{Subject: "s", Email: "a@x.io", EmailVerified: true})
	f := sso.NewFlow(1, "", time.Now())
	other := f
	other.Nonce = "not-the-nonce"
	if _, err := signIn(t, sso.NewClient(nil), settings(fake), fake, f, other); err == nil {
		t.Fatal("Exchange accepted an ID token with the wrong nonce")
	}
}

func TestExchangeWrongVerifier(t *testing.T) {
	fake := ssotest.New(t)
	fake.SetNext(ssotest.Identity{Subject: "s", Email: "a@x.io", EmailVerified: true})
	f := sso.NewFlow(1, "", time.Now())
	other := f
	other.Verifier = sso.NewFlow(1, "", time.Now()).Verifier
	if _, err := signIn(t, sso.NewClient(nil), settings(fake), fake, f, other); !errors.Is(err, sso.ErrCodeRejected) {
		t.Fatalf("Exchange with the wrong PKCE verifier = %v, want ErrCodeRejected", err)
	}
}

// The provider answered: a refused code or client is not "unreachable".
func TestExchangeRejections(t *testing.T) {
	fake := ssotest.New(t)
	c := sso.NewClient(nil)
	s := settings(fake)
	fake.SetNext(ssotest.Identity{Subject: "s", Email: "a@x.io", EmailVerified: true})
	f := sso.NewFlow(1, "", time.Now())
	authURL, err := c.AuthCodeURL(context.Background(), s, f)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := url.Parse(fake.Authorize(t, authURL))
	code := back.Query().Get("code")

	wrong := s
	wrong.ClientSecret = "not-the-secret"
	if _, err := c.Exchange(context.Background(), wrong, code, f); !errors.Is(err, sso.ErrClientRejected) || errors.Is(err, sso.ErrProvider) {
		t.Errorf("wrong client secret = %v, want ErrClientRejected only", err)
	}
	if _, err := c.Exchange(context.Background(), s, code, f); !errors.Is(err, sso.ErrCodeRejected) || errors.Is(err, sso.ErrProvider) {
		t.Errorf("a code already tried = %v, want ErrCodeRejected only", err)
	}
}

func TestExchangeUnverifiedEmail(t *testing.T) {
	fake := ssotest.New(t)
	fake.SetNext(ssotest.Identity{Subject: "s", Email: "a@x.io", EmailVerified: false})
	f := sso.NewFlow(1, "", time.Now())
	if _, err := signIn(t, sso.NewClient(nil), settings(fake), fake, f, f); !errors.Is(err, sso.ErrEmailNotVerified) {
		t.Fatalf("unverified email = %v, want ErrEmailNotVerified", err)
	}
}

func TestExchangeWrongAudience(t *testing.T) {
	fake := ssotest.New(t)
	fake.SetNext(ssotest.Identity{Subject: "s", Email: "a@x.io", EmailVerified: true})
	f := sso.NewFlow(1, "", time.Now())
	c := sso.NewClient(nil)
	s := settings(fake)
	authURL, err := c.AuthCodeURL(context.Background(), s, f)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := url.Parse(fake.Authorize(t, authURL))
	fake.AudienceOverride = "someone-else"
	if _, err := c.Exchange(context.Background(), s, back.Query().Get("code"), f); err == nil {
		t.Fatal("Exchange accepted an ID token for another audience")
	}
}

func TestDiscoveryFailureNotCached(t *testing.T) {
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	c := sso.NewClient(nil)
	s := sso.Settings{Issuer: deadURL, ClientID: "x", ClientSecret: "y", RedirectURL: redirect}
	if _, err := c.AuthCodeURL(context.Background(), s, sso.NewFlow(1, "", time.Now())); !errors.Is(err, sso.ErrProvider) {
		t.Fatalf("unreachable issuer = %v, want ErrProvider", err)
	}
	// The same issuer URL coming back to life must work: the failure
	// mustn't have been cached.
	fake := ssotest.NewAt(t, deadURL)
	if _, err := c.AuthCodeURL(context.Background(), settings(fake), sso.NewFlow(1, "", time.Now())); err != nil {
		t.Fatalf("after the provider came back: %v", err)
	}
}
