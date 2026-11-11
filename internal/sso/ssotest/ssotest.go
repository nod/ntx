// Package ssotest is an in-process OpenID Connect provider for tests:
// discovery, JWKS, authorize and token endpoints on an httptest server,
// signing ID tokens with a key generated per provider. No network.
package ssotest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Identity is who the provider says signed in on the next authorize.
type Identity struct {
	Subject       string
	Email         string
	Name          string
	EmailVerified bool
}

type grant struct {
	id        Identity
	nonce     string
	clientID  string
	challenge string
	redirect  string
}

type Provider struct {
	URL          string // the issuer
	ClientID     string
	ClientSecret string
	// AudienceOverride, if set, is put in ID tokens instead of the client
	// ID, to test audience checks.
	AudienceOverride string

	key    *rsa.PrivateKey
	mu     sync.Mutex
	next   Identity
	grants map[string]grant
}

// One signing key per test binary: generating RSA keys is slow, and fakes
// in one process never need to tell each other's tokens apart by key (their
// issuers differ).
var (
	keyOnce   sync.Once
	sharedKey *rsa.PrivateKey
	keyErr    error
)

func signingKey() (*rsa.PrivateKey, error) {
	keyOnce.Do(func() { sharedKey, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	return sharedKey, keyErr
}

// New starts a fake provider, closed when the test ends.
func New(t testing.TB) *Provider { return NewAt(t, "") }

// NewAt starts the provider listening at baseURL's host:port (an address
// that was in use earlier in the test), or on a fresh port if baseURL is "".
func NewAt(t testing.TB, baseURL string) *Provider {
	t.Helper()
	key, err := signingKey()
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{ClientID: "test-client", ClientSecret: "test-secret", key: key, grants: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	srv := httptest.NewUnstartedServer(mux)
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil {
			t.Fatal(err)
		}
		l, err := net.Listen("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		srv.Listener.Close()
		srv.Listener = l
	}
	srv.Start()
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

// SetNext sets the identity the next authorize request signs in as.
func (p *Provider) SetNext(id Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = id
}

// Authorize plays the browser at the provider: it GETs authURL without
// following the redirect and returns where the provider sends the browser
// back to (the callback URL with code and state).
func (p *Provider) Authorize(t testing.TB, authURL string) string {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: status %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// tokenError answers the token endpoint with an OAuth error, as JSON (RFC
// 6749 section 5.2). The header must be set before the status is written.
func tokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/authorize",
		"token_endpoint":                        p.URL + "/token",
		"jwks_uri":                              p.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad authorize request", http.StatusBadRequest)
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	code := base64.RawURLEncoding.EncodeToString(b)
	p.mu.Lock()
	p.grants[code] = grant{id: p.next, nonce: q.Get("nonce"), clientID: q.Get("client_id"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	p.mu.Unlock()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id, sec, ok := r.BasicAuth()
	if !ok {
		id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	p.mu.Lock()
	g, found := p.grants[r.PostForm.Get("code")]
	delete(p.grants, r.PostForm.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	switch {
	case id != p.ClientID || sec != p.ClientSecret:
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	case !found || g.clientID != id || g.redirect != r.PostForm.Get("redirect_uri") ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	aud := p.ClientID
	if p.AudienceOverride != "" {
		aud = p.AudienceOverride
	}
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{
		"iss": p.URL, "aud": aud, "sub": g.id.Subject, "email": g.id.Email, "email_verified": g.id.EmailVerified,
		"name": g.id.Name, "nonce": g.nonce, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	})
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jws, err := signer.Sign(claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idToken, _ := jws.CompactSerialize()
	writeJSON(w, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300, "id_token": idToken})
}
