// Package config loads ntx's server settings. Sites, sections and people
// live in the database and are managed in the browser; this file only holds
// what the process needs to start.
package config

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strings"

	"github.com/spf13/viper"
	"golang.org/x/crypto/bcrypt"

	"github.com/nod/ntx/internal/validate"
)

type Config struct {
	BindAddr         string
	Port             int
	DBDSN            string
	Dev              bool
	TrustedProxies   []netip.Prefix
	SessionSecret    string
	EncryptionKey    []byte // decoded; exactly 32 bytes
	ThemesPath       string
	PlatformHostname string // lowercased
	RootPasswordHash string
}

// raw mirrors the TOML shape before parsing and validation.
type raw struct {
	BindAddr         string   `mapstructure:"bind_addr"`
	Port             int      `mapstructure:"port"`
	DBDSN            string   `mapstructure:"db_dsn"`
	Dev              bool     `mapstructure:"dev"`
	TrustedProxies   []string `mapstructure:"trusted_proxies"`
	SessionSecret    string   `mapstructure:"session_secret"`
	EncryptionKey    string   `mapstructure:"encryption_key"`
	ThemesPath       string   `mapstructure:"themes_path"`
	PlatformHostname string   `mapstructure:"platform_hostname"`
	RootPasswordHash string   `mapstructure:"root_password_hash"`
}

// legacyKeys are settings from the config-file era of sites. A config that
// still has them predates web-managed sites, and is rejected rather than
// half-applied.
var legacyKeys = []string{"site", "global_admin_password_hash", "template_path", "static_path"}

func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	v.SetDefault("db_dsn", "sqlite://./ntx.db")
	v.SetDefault("port", 8080)
	v.SetDefault("themes_path", "./themes")
	v.SetDefault("dev", false)

	v.SetEnvPrefix("NTX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("ntx")
		v.SetConfigType("toml")
		v.AddConfigPath(".")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	for _, k := range legacyKeys {
		if v.InConfig(k) {
			return nil, fmt.Errorf("config key %q is no longer supported: sites are now created and managed in the browser by root (see docs/usage.md)", k)
		}
	}

	var r raw
	if err := v.Unmarshal(&r); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if len(r.SessionSecret) < 32 {
		return nil, fmt.Errorf("session_secret must be set, at least 32 characters (run: ntx gen-config)")
	}

	if r.EncryptionKey == "" {
		return nil, fmt.Errorf("encryption_key must be set: 32 random bytes, base64 (run: ntx gen-config)")
	}
	key, err := base64.StdEncoding.DecodeString(r.EncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("encryption_key must be 32 bytes, base64-encoded")
	}

	host := strings.ToLower(r.PlatformHostname)
	if host == "" {
		return nil, fmt.Errorf("platform_hostname must be set: the hostname root signs in on before any site exists")
	}
	if err := validate.Hostname(host); err != nil {
		return nil, fmt.Errorf("platform_hostname: %w", err)
	}

	if r.RootPasswordHash == "" {
		return nil, fmt.Errorf("root_password_hash must be set (run: ntx hash-password)")
	}
	if err := checkBcryptHash(r.RootPasswordHash); err != nil {
		return nil, fmt.Errorf("root_password_hash: %w", err)
	}

	proxies := make([]netip.Prefix, 0, len(r.TrustedProxies))
	for _, p := range r.TrustedProxies {
		pfx, err := netip.ParsePrefix(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies: %q is not a CIDR (e.g. \"127.0.0.1/32\"): %w", p, err)
		}
		proxies = append(proxies, pfx)
	}

	return &Config{
		BindAddr:         r.BindAddr,
		Port:             r.Port,
		DBDSN:            r.DBDSN,
		Dev:              r.Dev,
		TrustedProxies:   proxies,
		SessionSecret:    r.SessionSecret,
		EncryptionKey:    key,
		ThemesPath:       r.ThemesPath,
		PlatformHostname: host,
		RootPasswordHash: r.RootPasswordHash,
	}, nil
}

// checkBcryptHash rejects anything that is not a well-formed bcrypt hash.
// A truncated or mis-pasted hash is otherwise invisible until someone tries
// to log in, at which point every attempt fails with "Invalid password".
func checkBcryptHash(hash string) error {
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return fmt.Errorf("not a valid bcrypt hash (run: ntx hash-password): %w", err)
	}
	return nil
}
