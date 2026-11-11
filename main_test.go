package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/secretbox"
	"github.com/nod/ntx/internal/server"
)

// realBcryptHash is a genuine bcrypt hash (of "testpass"); config.Load
// bcrypt-validates root_password_hash.
const realBcryptHash = `$2a$10$XKhLm60v72htK/lAO26kDuWPXh.3DovbWhmC/tN.E1UIzH0D.moZ.`

func genConfigFile(t *testing.T, rootHash string) string {
	t.Helper()
	secret, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "ntx.toml")
	if err := os.WriteFile(f, []byte(applyGenConfigSubstitutions(sampleConfig, secret, key, rootHash)), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGenConfigOutputLoads(t *testing.T) {
	cfg, err := config.Load(genConfigFile(t, realBcryptHash))
	if err != nil {
		t.Fatalf("gen-config output must load: %v", err)
	}
	if cfg.PlatformHostname != "localhost" {
		t.Errorf("sample platform_hostname should be localhost so a first run works locally, got %q", cfg.PlatformHostname)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Errorf("sample should trust the loopback proxy addresses, got %v", cfg.TrustedProxies)
	}
}

// Without -p the root hash stays empty, and Load must say what's missing.
func TestGenConfigWithoutPasswordFailsLoadOnRootHash(t *testing.T) {
	_, err := config.Load(genConfigFile(t, ""))
	if err == nil || !strings.Contains(err.Error(), "root_password_hash") {
		t.Fatalf("expected a root_password_hash error, got %v", err)
	}
}

// TestReadPasswordLineRejectsEmptyInput guards against the bug found in
// fix round 1: hash-password's non-terminal branch used to accept a
// closed or empty stdin (bufio.Reader.ReadString returns "" with
// io.EOF, which the old guard let through), silently hashing the empty
// string and printing a well-formed bcrypt hash with no indication that
// nothing was actually read.
func TestReadPasswordLineRejectsEmptyInput(t *testing.T) {
	_, err := readPasswordLine(strings.NewReader(""))
	if err == nil {
		t.Error("expected an error for empty stdin, got nil")
	}
}

func TestReadPasswordLineAcceptsNewlineTerminatedPassword(t *testing.T) {
	pw, err := readPasswordLine(strings.NewReader("testpass\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pw != "testpass" {
		t.Errorf("expected pw = %q, got %q", "testpass", pw)
	}
}

// Root's password guards every site, so an empty one -- someone pressing
// Enter at the prompt -- must be refused, not hashed.
func TestHashRootPasswordRejectsEmpty(t *testing.T) {
	if _, err := hashRootPassword(""); err == nil {
		t.Error("an empty root password must be refused")
	}
	hash, err := hashRootPassword("s3cret")
	if err != nil || !strings.HasPrefix(hash, "$2") {
		t.Errorf("a real password must hash: %q, %v", hash, err)
	}
}

// serve's periodic sweep deletes expired sessions from the app's database
// and keeps live ones.
func TestAppSweepDeletesExpiredSessions(t *testing.T) {
	cfgFile := genConfigFile(t, realBcryptHash)
	t.Setenv("NTX_DB_DSN", "sqlite://"+filepath.Join(t.TempDir(), "t.db"))
	t.Setenv("NTX_THEMES_PATH", t.TempDir())
	a, err := openApp(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	root, err := a.users.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.sessions.Create(root.ID, nil, "d", time.Minute); err != nil {
		t.Fatal(err)
	}
	live, err := a.sessions.Create(root.ID, nil, "d", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Add(2 * time.Minute)
	a.sweep(now)
	if n, _ := a.sessions.DeleteExpired(now); n != 0 {
		t.Errorf("sweep left %d expired sessions", n)
	}
	if s, _ := a.sessions.Lookup(live, now); s == nil {
		t.Error("sweep deleted a live session")
	}
}

// openApp must wire every dependency sign-in needs: /auth/start on a site
// with a provider reaches the provider (here unreachable: 502), instead of
// tripping over a nil store or client (500).
func TestOpenAppWiresSignIn(t *testing.T) {
	cfgFile := genConfigFile(t, realBcryptHash)
	t.Setenv("NTX_DB_DSN", "sqlite://"+filepath.Join(t.TempDir(), "t.db"))
	themesDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(themesDir, "t1", "templates"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NTX_THEMES_PATH", themesDir)
	cfg, err := config.Load(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	sites := models.NewSiteStore(d)
	site := &models.Site{Key: "a", Hostname: "a.example.com", Theme: "t1"}
	if err := sites.Create(site, cfg.PlatformHostname); err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.Seal(cfg.EncryptionKey, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := sites.SetProvider(site.ID, models.ProviderSettings{Type: models.ProviderAuth0, Domain: "tenant.invalid", ClientID: "cid"}, sealed); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.DB(); s != nil {
		s.Close()
	}

	a, err := openApp(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	req := httptest.NewRequest(http.MethodGet, "/auth/start", nil)
	req.Host = "a.example.com"
	rec := httptest.NewRecorder()
	server.New(a.cfg, a.handler, a.registry, a.auth).Handler.ServeHTTP(rec, req)
	// Not the "re-enter the client secret" 502: that would mean the
	// encryption key wasn't wired.
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "reach the sign-in provider") {
		t.Fatalf("/auth/start through openApp: %d %s", rec.Code, rec.Body.String())
	}
}
