package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validHash is a real bcrypt hash (of "testpass"). Load bcrypt-validates
// root_password_hash, so a stand-in string would be rejected.
const validHash = `$2a$10$XKhLm60v72htK/lAO26kDuWPXh.3DovbWhmC/tN.E1UIzH0D.moZ.`

var validKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func writeTOML(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ntx.toml")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

// line renders one TOML key = "value" line.
func line(k, v string) string { return k + ` = "` + v + `"` + "\n" }

// base returns a valid config with every required key, minus any key named
// in omit.
func base(omit ...string) string {
	keys := [][2]string{
		{"session_secret", "supersecretkey1234567890abcdef12"},
		{"encryption_key", validKey},
		{"platform_hostname", "localhost"},
		{"root_password_hash", validHash},
	}
	var b strings.Builder
outer:
	for _, kv := range keys {
		for _, o := range omit {
			if o == kv[0] {
				continue outer
			}
		}
		b.WriteString(line(kv[0], kv[1]))
	}
	return b.String()
}

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	return Load(writeTOML(t, content))
}

func TestLoadValidConfig(t *testing.T) {
	cfg, err := load(t, base())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 || cfg.ThemesPath != "./themes" || cfg.DBDSN != "sqlite://./ntx.db" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("expected no trusted proxies by default, got %v", cfg.TrustedProxies)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Errorf("expected 32-byte decoded encryption key, got %d", len(cfg.EncryptionKey))
	}
}

func TestPlatformHostnameRequiredAndValidated(t *testing.T) {
	if _, err := load(t, base("platform_hostname")); err == nil {
		t.Error("missing platform_hostname: expected error")
	}
	if _, err := load(t, base("platform_hostname")+line("platform_hostname", "https://x.com")); err == nil {
		t.Error("platform_hostname with scheme: expected error")
	}
	cfg, err := load(t, base("platform_hostname")+line("platform_hostname", "Admin.Example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlatformHostname != "admin.example.com" {
		t.Errorf("platform_hostname should be lowercased, got %q", cfg.PlatformHostname)
	}
}

func TestRootPasswordHashRequiredAndBcrypt(t *testing.T) {
	if _, err := load(t, base("root_password_hash")); err == nil {
		t.Error("missing root_password_hash: expected error")
	}
	if _, err := load(t, base("root_password_hash")+line("root_password_hash", "notahash")); err == nil {
		t.Error("malformed root_password_hash: expected error")
	}
}

func TestEncryptionKeyMustBe32Bytes(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	for name, content := range map[string]string{
		"missing":    base("encryption_key"),
		"not base64": base("encryption_key") + line("encryption_key", "!!!not base64!!!"),
		"16 bytes":   base("encryption_key") + line("encryption_key", short),
	} {
		if _, err := load(t, content); err == nil {
			t.Errorf("%s encryption_key: expected error", name)
		}
	}
}

func TestTrustedProxiesParsed(t *testing.T) {
	cfg, err := load(t, base()+`trusted_proxies = ["127.0.0.1/32", "::1/128"]`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "127.0.0.1/32" {
		t.Errorf("unexpected trusted proxies: %v", cfg.TrustedProxies)
	}
	if _, err := load(t, base()+`trusted_proxies = ["nope"]`+"\n"); err == nil {
		t.Error("invalid CIDR: expected error")
	}
}

// An old ntx.toml must fail loudly rather than be half-applied: sites now
// live in the database.
func TestLegacyKeysRejected(t *testing.T) {
	for key, extra := range map[string]string{
		"site":                       "\n[[site]]\nkey = \"a\"\n",
		"global_admin_password_hash": line("global_admin_password_hash", "x"),
		"template_path":              line("template_path", "./t"),
		"static_path":                line("static_path", "./s"),
	} {
		// Top-level keys must precede any [[site]] table, so prepend them.
		content := extra + base()
		if key == "site" {
			content = base() + extra
		}
		_, err := load(t, content)
		if err == nil {
			t.Errorf("%s: expected error", key)
			continue
		}
		if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "docs/usage.md") {
			t.Errorf("%s: error should name the key and point to docs/usage.md, got: %v", key, err)
		}
	}
}

func TestShortSessionSecretErrors(t *testing.T) {
	if _, err := load(t, base("session_secret")+line("session_secret", "tooshort")); err == nil {
		t.Error("short session_secret: expected error")
	}
}

func TestEnvVarOverride(t *testing.T) {
	t.Setenv("NTX_PORT", "9999")
	cfg, err := load(t, base())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 {
		t.Errorf("expected NTX_PORT to override port, got %d", cfg.Port)
	}
}
