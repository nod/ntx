// Package sso signs people in with a site's Google or Auth0 provider, as
// OpenID Connect: authorization code flow with state, nonce and PKCE.
package sso

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var (
	// ErrProvider wraps failures talking to the provider: discovery, or the
	// code exchange. The provider is down, misconfigured, or refused us.
	ErrProvider = errors.New("sso: sign-in provider error")
	// ErrEmailNotVerified: the provider doesn't vouch for the email, so it
	// can't be used to match anyone.
	ErrEmailNotVerified = errors.New("sso: email not verified by the provider")
)

// Issuer is the configured issuer for a provider: what discovery runs
// against and what identities are stored under.
func Issuer(providerType, domain string) string {
	if providerType == "auth0" {
		return "https://" + domain + "/"
	}
	return "https://accounts.google.com"
}

// Settings is one site's provider, as needed for a sign-in.
type Settings struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Claims is the verified result of a sign-in. Issuer is Settings.Issuer,
// not the token's iss (Google has two spellings of it); Email is lowercase.
type Claims struct {
	Issuer        string
	Subject       string
	Email         string
	Name          string
	EmailVerified bool
}

type Client struct {
	hc        *http.Client
	mu        sync.Mutex
	providers map[string]*oidc.Provider // by issuer; failures aren't cached
}

// NewClient talks to providers through hc (nil: http.DefaultClient).
func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{hc: hc, providers: map[string]*oidc.Provider{}}
}

func (c *Client) ctx(ctx context.Context) context.Context { return oidc.ClientContext(ctx, c.hc) }

func (c *Client) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	c.mu.Lock()
	p, ok := c.providers[issuer]
	c.mu.Unlock()
	if ok {
		return p, nil
	}
	p, err := oidc.NewProvider(c.ctx(ctx), issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery for %s: %v", ErrProvider, issuer, err)
	}
	c.mu.Lock()
	c.providers[issuer] = p
	c.mu.Unlock()
	return p, nil
}

func (c *Client) config(ctx context.Context, s Settings) (*oauth2.Config, *oidc.Provider, error) {
	p, err := c.provider(ctx, s.Issuer)
	if err != nil {
		return nil, nil, err
	}
	return &oauth2.Config{
		ClientID: s.ClientID, ClientSecret: s.ClientSecret, RedirectURL: s.RedirectURL,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}, p, nil
}

// AuthCodeURL is where /auth/start sends the browser.
func (c *Client) AuthCodeURL(ctx context.Context, s Settings, f Flow) (string, error) {
	cfg, _, err := c.config(ctx, s)
	if err != nil {
		return "", err
	}
	return cfg.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier)), nil
}

// Exchange redeems the callback's code and verifies the ID token: issuer,
// audience, expiry, signature and the flow's nonce. The caller has already
// checked state.
func (c *Client) Exchange(ctx context.Context, s Settings, code string, f Flow) (Claims, error) {
	cfg, p, err := c.config(ctx, s)
	if err != nil {
		return Claims{}, err
	}
	tok, err := cfg.Exchange(c.ctx(ctx), code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return Claims{}, fmt.Errorf("%w: code exchange: %v", ErrProvider, err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Claims{}, fmt.Errorf("%w: no id_token in the token response", ErrProvider)
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: s.ClientID}).Verify(c.ctx(ctx), raw)
	if err != nil {
		return Claims{}, fmt.Errorf("sso: id token: %w", err)
	}
	if idt.Nonce != f.Nonce {
		return Claims{}, errors.New("sso: id token nonce mismatch")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Claims{}, fmt.Errorf("sso: id token claims: %w", err)
	}
	// Some providers send email_verified as the string "true".
	verified := claims.EmailVerified == true || claims.EmailVerified == "true"
	if !verified {
		return Claims{}, ErrEmailNotVerified
	}
	return Claims{
		Issuer: s.Issuer, Subject: idt.Subject, Email: strings.ToLower(strings.TrimSpace(claims.Email)),
		Name: strings.TrimSpace(claims.Name), EmailVerified: true,
	}, nil
}
