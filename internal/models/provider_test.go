package models

import (
	"bytes"
	"strings"
	"testing"
)

func providerFixture(t *testing.T) (*SiteStore, *Site) {
	t.Helper()
	s := NewSiteStore(newTestSiteDB(t))
	return s, mustCreateSite(t, s, "a", "a.example.com")
}

func mustSecret(t *testing.T, s *SiteStore, id int64) []byte {
	t.Helper()
	b, err := s.ProviderSecret(id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSetProviderClearsSecretOnChange(t *testing.T) {
	s, site := providerFixture(t)
	auth0 := ProviderSettings{Type: ProviderAuth0, Domain: "acme.us.auth0.com", ClientID: "cid"}
	if err := s.SetProvider(site.ID, auth0, []byte("s1")); err != nil {
		t.Fatal(err)
	}

	moved := auth0
	moved.Domain = "evil.example.com"
	err := s.SetProvider(site.ID, moved, nil)
	if !isValidation(err) || !strings.Contains(err.Error(), "client secret") {
		t.Fatalf("domain change without a secret = %v, want a client-secret validation error", err)
	}
	if got := mustSecret(t, s, site.ID); !bytes.Equal(got, []byte("s1")) {
		t.Fatalf("secret after refused change = %q, want s1", got)
	}
	if err := s.SetProvider(site.ID, moved, []byte("s2")); err != nil {
		t.Fatalf("domain change with a new secret = %v", err)
	}

	newID := moved
	newID.ClientID = "other"
	if err := s.SetProvider(site.ID, newID, nil); !isValidation(err) {
		t.Fatalf("client ID change without a secret = %v, want a validation error", err)
	}
	google := ProviderSettings{Type: ProviderGoogle, ClientID: moved.ClientID}
	if err := s.SetProvider(site.ID, google, nil); !isValidation(err) {
		t.Fatalf("type change without a secret = %v, want a validation error", err)
	}
}

func TestSetProviderKeepsSecretWhenUnchanged(t *testing.T) {
	s, site := providerFixture(t)
	p := ProviderSettings{Type: ProviderAuth0, Domain: "acme.us.auth0.com", ClientID: "cid"}
	if err := s.SetProvider(site.ID, p, []byte("s1")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProvider(site.ID, p, nil); err != nil {
		t.Fatalf("unchanged re-save = %v", err)
	}
	if got := mustSecret(t, s, site.ID); !bytes.Equal(got, []byte("s1")) {
		t.Fatalf("secret = %q, want s1", got)
	}
}

func TestSetProviderValidation(t *testing.T) {
	s, site := providerFixture(t)
	for name, p := range map[string]ProviderSettings{
		"unknown type":     {Type: "github", ClientID: "c"},
		"domain with url":  {Type: ProviderAuth0, Domain: "https://x.com", ClientID: "c"},
		"auth0 no domain":  {Type: ProviderAuth0, ClientID: "c"},
		"google no client": {Type: ProviderGoogle},
		"auth0 no client":  {Type: ProviderAuth0, Domain: "x.auth0.com"},
	} {
		if err := s.SetProvider(site.ID, p, []byte("sec")); !isValidation(err) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
	if err := s.SetProvider(site.ID, ProviderSettings{Type: ProviderGoogle, ClientID: "c"}, nil); !isValidation(err) {
		t.Errorf("first save without a secret = %v, want a validation error", err)
	}
	if err := s.SetProvider(site.ID, ProviderSettings{Type: ProviderGoogle, Domain: "ignored.com", ClientID: "c"}, []byte("sec")); err != nil {
		t.Fatal(err)
	}
	sites, _ := s.List()
	if sites[0].ProviderDomain != "" || sites[0].ProviderType != ProviderGoogle || !sites[0].ProviderSecretSet {
		t.Fatalf("google site = %+v, want empty domain and a secret set", sites[0])
	}
}

func TestSetProviderNone(t *testing.T) {
	s, site := providerFixture(t)
	if err := s.SetProvider(site.ID, ProviderSettings{Type: ProviderAuth0, Domain: "x.auth0.com", ClientID: "c"}, []byte("sec")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProvider(site.ID, ProviderSettings{Type: "", Domain: "junk", ClientID: "junk"}, nil); err != nil {
		t.Fatal(err)
	}
	sites, _ := s.List()
	got := sites[0]
	if got.ProviderType != "" || got.ProviderDomain != "" || got.ProviderClientID != "" || got.ProviderSecretSet {
		t.Fatalf("site after clearing = %+v", got)
	}
	if b := mustSecret(t, s, site.ID); b != nil {
		t.Fatalf("secret = %q, want nil", b)
	}
}
