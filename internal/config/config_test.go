package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
admin_password_hash = "$2a$10$placeholder"
`

func TestEmptySessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
session_secret = ""

[[site]]
key                 = "mysite"
site_url            = "mysite.example.com"
admin_password_hash = "$2a$10$placeholder"
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
admin_password_hash = "$2a$10$placeholder"
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
admin_password_hash = "$2a$10$placeholder"
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
admin_password_hash = "$2a$10$placeholder"
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
admin_password_hash = "$2a$10$placeholder"

[[site]]
key                 = "dup"
site_url            = "b.example.com"
admin_password_hash = "$2a$10$placeholder"
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
admin_password_hash = "$2a$10$placeholder"

[[site]]
key                 = "b"
site_url            = "dup.example.com"
admin_password_hash = "$2a$10$placeholder"
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
admin_password_hash = "$2a$10$placeholder"
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

func TestMultipleSites(t *testing.T) {
	f := writeTOML(t, `
session_secret = "supersecretkey1234567890abcdef12"

[[site]]
key                 = "a"
site_url            = "a.example.com"
admin_password_hash = "$2a$10$placeholder"

  [[site.section]]
  slug  = "projects"
  title = "Projects"

    [[site.section.pages]]
    slug  = "one"
    title = "One"

[[site]]
key                 = "b"
site_url            = "b.example.com"
admin_password_hash = "$2a$10$placeholder"
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
