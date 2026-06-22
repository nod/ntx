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

func TestEmptySessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
admin_password_hash = "$2a$10$placeholder"
session_secret      = ""
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for empty session_secret")
	}
}

func TestShortSessionSecretErrors(t *testing.T) {
	f := writeTOML(t, `
admin_password_hash = "$2a$10$placeholder"
session_secret      = "tooshort"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for session_secret shorter than 32 chars")
	}
}

func TestEmptyPasswordHashErrors(t *testing.T) {
	f := writeTOML(t, `
admin_password_hash = ""
session_secret      = "supersecretkey1234567890abcdef12"
`)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for empty admin_password_hash")
	}
}

func TestDefaults(t *testing.T) {
	f := writeTOML(t, `
admin_password_hash = "$2a$10$placeholder"
session_secret      = "supersecretkey1234567890abcdef12"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 {
		t.Errorf("default port: want 8080, got %d", cfg.Port)
	}
	if cfg.DBPath != "./ntx.db" {
		t.Errorf("default db_path: want ./ntx.db, got %q", cfg.DBPath)
	}
	if cfg.TemplatePath != "./templates" {
		t.Errorf("default template_path: want ./templates, got %q", cfg.TemplatePath)
	}
	if cfg.Dev {
		t.Error("dev should default to false")
	}
	if cfg.SiteName != "ntx" {
		t.Errorf("default site_name: want ntx, got %q", cfg.SiteName)
	}
	if cfg.SiteURL != "localhost" {
		t.Errorf("default site_url: want localhost, got %q", cfg.SiteURL)
	}
	if cfg.SiteDescription != "" {
		t.Errorf("default site_description: want empty, got %q", cfg.SiteDescription)
	}
}

func TestEnvVarOverride(t *testing.T) {
	t.Setenv("NTX_PORT", "9090")

	f := writeTOML(t, `
admin_password_hash = "$2a$10$placeholder"
session_secret      = "supersecretkey1234567890abcdef12"
`)
	cfg, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9090 {
		t.Errorf("env override: want port 9090, got %d", cfg.Port)
	}
}
