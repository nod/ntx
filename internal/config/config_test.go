package config

import (
	"os"
	"path/filepath"
	"testing"
)

// validHash is a real bcrypt hash (of "testpass"). Load bcrypt-validates
// every admin_password_hash, so a stand-in like "$2a$10$placeholder" no
// longer gets through -- and generating a fresh hash per test would pay
// bcrypt's deliberate cost over and over.
const validHash = `$2a$10$XKhLm60v72htK/lAO26kDuWPXh.3DovbWhmC/tN.E1UIzH0D.moZ.`

func writeTOML(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ntx.toml")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

const oneSite = `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "` + validHash + `"
`

func TestEmptySessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = ""

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for empty session_secret")
	}
}

func TestShortSessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "tooshort"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for session_secret shorter than 32 chars")
	}
}

func TestNoSitesErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error when no [[site]] blocks are configured")
	}
}

func TestSiteMissingKeyErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for site missing key")
	}
}

func TestSiteMissingURLErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "mysite"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for site missing site_url")
	}
}

func TestSiteMissingPasswordHashErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key      = "mysite"
site_url = "mysite.example.com"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for site missing admin_password_hash")
	}
}

func TestDuplicateSiteKeyErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "dup"
site_url            = "a.example.com"
admin_password_hash = "`+validHash+`"

[[site]]
key                 = "dup"
site_url            = "b.example.com"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for duplicate site key")
	}
}

func TestDuplicateSiteURLErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "a"
site_url            = "dup.example.com"
admin_password_hash = "`+validHash+`"

[[site]]
key                 = "b"
site_url            = "dup.example.com"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for duplicate site_url")
	}
}

func TestDefaults(t *testing.T) {
	f := writeTOML(t, oneSite)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 {
		t.Errorf("default port: want 8080, got %d", cfg.Port)
	}
	if cfg.DBDSN != "sqlite://./ntx.db" {
		t.Errorf("default db_dsn: want sqlite://./ntx.db, got %q", cfg.DBDSN)
	}
	if cfg.TemplatePath != "./templates" {
		t.Errorf("default template_path: want ./templates, got %q", cfg.TemplatePath)
	}
	if cfg.Dev {
		t.Error("dev should default to false")
	}
	if len(cfg.Sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(cfg.Sites))
	}
	site := cfg.Sites[0]
	if site.SiteName != "ntx" {
		t.Errorf("default site_name: want ntx, got %q", site.SiteName)
	}
	if site.AboutSlug != "about" {
		t.Errorf("default about_slug: want about, got %q", site.AboutSlug)
	}
	if site.TemplatePath != "./templates" {
		t.Errorf("site should inherit global template_path, got %q", site.TemplatePath)
	}
	if site.StaticPath != "./static" {
		t.Errorf("site should inherit global static_path, got %q", site.StaticPath)
	}
}

func TestSiteTemplatePathOverride(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"
template_path  = "./shared-templates"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
template_path       = "./mysite-templates"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].TemplatePath != "./mysite-templates" {
		t.Errorf("site override ignored: got %q", cfg.Sites[0].TemplatePath)
	}
}

func TestSiteStaticPathOverride(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"
static_path    = "./shared-static"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
static_path         = "./mysite-static"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].StaticPath != "./mysite-static" {
		t.Errorf("site override ignored: got %q", cfg.Sites[0].StaticPath)
	}
}

// TestMalformedSiteHashErrors and the two tests after it cover the spec's
// Validation requirement that password hashes be well-formed, not merely
// present. A truncated or mis-pasted hash is accepted by a non-empty check
// and then fails every login attempt with "Invalid password", with no
// boot-time signal that the config is the problem.
func TestMalformedSiteHashErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$truncated"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for a malformed site admin_password_hash")
	}
}

func TestMalformedGlobalHashErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret             = "supersecretkey1234567890abcdef12"
global_admin_password_hash = "$2a$10$truncated"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for a malformed global_admin_password_hash")
	}
}

func TestValidGlobalHashAccepted(t *testing.T) {
	f := writeTOML(t, `
session_secret             = "supersecretkey1234567890abcdef12"
global_admin_password_hash = "`+validHash+`"

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "`+validHash+`"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("a well-formed global_admin_password_hash should load: %v", err)
	}
	if cfg.GlobalAdminPasswordHash != validHash {
		t.Errorf("global_admin_password_hash not loaded: got %q", cfg.GlobalAdminPasswordHash)
	}
}

func TestMultipleSites(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "a"
site_url            = "a.example.com"
admin_password_hash = "`+validHash+`"

  [[site.section]]
  slug  = "projects"
  title = "Projects"

    [[site.section.pages]]
    slug  = "one"
    title = "One"

[[site]]
key                 = "b"
site_url            = "b.example.com"
admin_password_hash = "`+validHash+`"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(cfg.Sites))
	}
	if len(cfg.Sites[0].Sections) != 1 || cfg.Sites[0].Sections[0].Slug != "projects" {
		t.Errorf("expected site a to have a projects section, got %+v", cfg.Sites[0].Sections)
	}
	if len(cfg.Sites[1].Sections) != 0 {
		t.Errorf("expected site b to have no sections, got %+v", cfg.Sites[1].Sections)
	}
}

func TestEnvVarOverride(t *testing.T) {
	t.Setenv("NTX_PORT", "9090")

	f := writeTOML(t, oneSite)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9090 {
		t.Errorf("env override: want port 9090, got %d", cfg.Port)
	}
}

// siteTOML renders a config with one [[site]] block per (key, site_url)
// pair, plus any extra TOML appended verbatim after the last block (so a
// [[site.section]] lands on the last site).
func siteTOML(pairs [][2]string, extra string) string {
	out := "session_secret = \"supersecretkey1234567890abcdef12\"\n"
	for _, p := range pairs {
		out += "\n[[site]]\nkey = \"" + p[0] + "\"\nsite_url = \"" + p[1] + "\"\nadmin_password_hash = \"" + validHash + "\"\n"
	}
	return out + extra
}

// resolveSite lowercases the Host header before matching, so two site_urls
// differing only in case are the same routing key: the later one would
// silently shadow the earlier.
func TestDuplicateSiteURLIsCaseInsensitive(t *testing.T) {
	f := writeTOML(t, siteTOML([][2]string{{"a", "Dup.example.com"}, {"b", "dup.example.com"}}, ""))
	if _, err := Load(f); err == nil {
		t.Error("expected error for site_urls differing only in case")
	}
}

// resolveSite matches a bare hostname (port stripped, no scheme), so any
// site_url carrying a scheme, port or path can never match a request.
func TestSiteURLMustBeBareHostname(t *testing.T) {
	for _, u := range []string{
		"https://example.com",
		"http://example.com",
		"example.com:8080",
		"example.com/blog",
		"example.com/",
		"exa mple.com",
	} {
		f := writeTOML(t, siteTOML([][2]string{{"a", u}}, ""))
		if _, err := Load(f); err == nil {
			t.Errorf("site_url %q: expected error, got nil", u)
		}
	}
}

func TestSiteURLBareHostnamesAccepted(t *testing.T) {
	for _, u := range []string{"localhost", "example.com", "Example.COM", "sub.example.co.uk", "127.0.0.1", "::1"} {
		f := writeTOML(t, siteTOML([][2]string{{"a", u}}, ""))
		if _, err := Load(f); err != nil {
			t.Errorf("site_url %q: unexpected error: %v", u, err)
		}
	}
}

// key names the site's `ntx generate` output directory, so it must be a
// single, case-normalised path segment: no traversal, no separators, and
// no two keys that collide on a case-insensitive filesystem.
func TestSiteKeyMustBeSafeDirName(t *testing.T) {
	for _, k := range []string{"../www", "a/b", `a\b`, "..", ".", "Blog", " a", "a b", ".hidden"} {
		f := writeTOML(t, siteTOML([][2]string{{k, "a.example.com"}}, ""))
		if _, err := Load(f); err == nil {
			t.Errorf("key %q: expected error, got nil", k)
		}
	}
}

func TestSiteKeySafeNamesAccepted(t *testing.T) {
	for _, k := range []string{"mysite", "my-site", "my_site", "site2", "default", "2nd"} {
		f := writeTOML(t, siteTOML([][2]string{{k, "a.example.com"}}, ""))
		if _, err := Load(f); err != nil {
			t.Errorf("key %q: unexpected error: %v", k, err)
		}
	}
}

// Section slugs from every site share one router, so a slug that collides
// with a built-in route prefix would hijack that route on every site.
func TestReservedSectionSlugErrors(t *testing.T) {
	for _, slug := range []string{"blog", "static", "admin", "about"} {
		f := writeTOML(t, siteTOML([][2]string{{"a", "a.example.com"}},
			"\n[[site.section]]\nslug = \""+slug+"\"\n"))
		if _, err := Load(f); err == nil {
			t.Errorf("section slug %q: expected error, got nil", slug)
		}
	}
}

func TestMalformedSectionSlugErrors(t *testing.T) {
	for _, slug := range []string{"", "a/b", "a b", "..", "{page}", "*"} {
		f := writeTOML(t, siteTOML([][2]string{{"a", "a.example.com"}},
			"\n[[site.section]]\nslug = \""+slug+"\"\n"))
		if _, err := Load(f); err == nil {
			t.Errorf("section slug %q: expected error, got nil", slug)
		}
	}
}

func TestOrdinarySectionSlugAccepted(t *testing.T) {
	f := writeTOML(t, siteTOML([][2]string{{"a", "a.example.com"}},
		"\n[[site.section]]\nslug = \"projects\"\n"))
	if _, err := Load(f); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
