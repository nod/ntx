package models

import (
	"testing"
	"time"
)

func newSessionEnv(t *testing.T) (*SessionStore, *User, *Site) {
	t.Helper()
	d := newTestSiteDB(t)
	root, err := NewUserStore(d).EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	site := mustCreateSite(t, NewSiteStore(d), "a", "a.example.com")
	return NewSessionStore(d), root, site
}

func TestSessionCreateLookupDelete(t *testing.T) {
	store, root, site := newSessionEnv(t)
	token, err := store.Create(root.ID, &site.ID, "digest", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 {
		t.Errorf("token should be base64url of 32 bytes (43 chars), got %d", len(token))
	}
	var stored string
	if err := store.db.Raw("SELECT token_hash FROM sessions").Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored == token || stored == "" {
		t.Error("the database must store a hash of the token, not the token")
	}

	now := time.Now()
	s, err := store.Lookup(token, now)
	if err != nil || s == nil {
		t.Fatalf("Lookup before expiry = %v, %v", s, err)
	}
	if s.UserID != root.ID || s.SiteID == nil || *s.SiteID != site.ID || s.RootHashDigest != "digest" {
		t.Errorf("unexpected session %+v", s)
	}
	if s, _ := store.Lookup(token, now.Add(time.Hour+time.Second)); s != nil {
		t.Error("an expired session must not be returned")
	}
	if err := store.Delete(token); err != nil {
		t.Fatal(err)
	}
	if s, _ := store.Lookup(token, now); s != nil {
		t.Error("a deleted session must not be returned")
	}
	if s, _ := store.Lookup("nonsense", now); s != nil {
		t.Error("an unknown token must not be returned")
	}
}

func TestPlatformSessionHasNoSite(t *testing.T) {
	store, root, _ := newSessionEnv(t)
	token, err := store.Create(root.ID, nil, "digest", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Lookup(token, time.Now())
	if err != nil || s == nil || s.SiteID != nil {
		t.Fatalf("a platform-host session must have no site, got %+v, %v", s, err)
	}
}

// last_seen_at is written at most once a minute: SQLite runs on a single
// connection, so a write per request would serialise everything.
func TestLookupTouchesAtMostOncePerMinute(t *testing.T) {
	store, root, site := newSessionEnv(t)
	token, err := store.Create(root.ID, &site.ID, "d", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.Lookup(token, time.Now())
	t0 := first.LastSeenAt

	s, _ := store.Lookup(token, t0.Add(30*time.Second))
	if !s.LastSeenAt.Equal(t0) {
		t.Errorf("a lookup within a minute must not touch last_seen_at (%v → %v)", t0, s.LastSeenAt)
	}
	store.Lookup(token, t0.Add(61*time.Second))
	s, _ = store.Lookup(token, t0.Add(62*time.Second))
	if !s.LastSeenAt.After(t0) {
		t.Errorf("a lookup after a minute must move last_seen_at, still %v", s.LastSeenAt)
	}
}
