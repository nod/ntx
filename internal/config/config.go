package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type SectionPage struct {
	Slug  string                 `mapstructure:"slug"`
	Title string                 `mapstructure:"title"`
	Vars  map[string]interface{} `mapstructure:",remain"`
}

type Section struct {
	Slug  string                 `mapstructure:"slug"`
	Title string                 `mapstructure:"title"`
	Pages []SectionPage          `mapstructure:"pages"`
	Vars  map[string]interface{} `mapstructure:",remain"`
}

// Site is one tenant's configuration. ID is populated by
// models.EnsureSites at boot, not read from TOML — it's the stable FK
// used to scope posts/tags/sessions.
type Site struct {
	ID                int64     `mapstructure:"-"`
	Key               string    `mapstructure:"key"`
	SiteURL           string    `mapstructure:"site_url"`
	SiteName          string    `mapstructure:"site_name"`
	SiteDescription   string    `mapstructure:"site_description"`
	AboutSlug         string    `mapstructure:"about_slug"`
	AdminPasswordHash string    `mapstructure:"admin_password_hash"`
	TemplatePath      string    `mapstructure:"template_path"`
	StaticPath        string    `mapstructure:"static_path"`
	Sections          []Section `mapstructure:"section"`
}

type Config struct {
	BindAddr                string `mapstructure:"bind_addr"`
	Port                    int    `mapstructure:"port"`
	DBDSN                   string `mapstructure:"db_dsn"`
	SessionSecret           string `mapstructure:"session_secret"`
	Dev                     bool   `mapstructure:"dev"`
	TemplatePath            string `mapstructure:"template_path"`
	StaticPath              string `mapstructure:"static_path"`
	GlobalAdminPasswordHash string `mapstructure:"global_admin_password_hash"`
	Sites                   []Site `mapstructure:"site"`
}

func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	v.SetDefault("static_path", "./static")
	v.SetDefault("db_dsn", "sqlite://./ntx.db")
	v.SetDefault("port", 8080)
	v.SetDefault("template_path", "./templates")
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

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("session_secret must be set (min 32 chars)")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("session_secret must be at least 32 characters")
	}

	if len(cfg.Sites) == 0 {
		return nil, fmt.Errorf("at least one [[site]] block is required")
	}

	keys := make(map[string]bool, len(cfg.Sites))
	urls := make(map[string]bool, len(cfg.Sites))
	for i := range cfg.Sites {
		s := &cfg.Sites[i]
		if s.Key == "" {
			return nil, fmt.Errorf("site %d: key must be set", i)
		}
		if s.SiteURL == "" {
			return nil, fmt.Errorf("site %q: site_url must be set", s.Key)
		}
		if s.AdminPasswordHash == "" {
			return nil, fmt.Errorf("site %q: admin_password_hash must be set (run: ntx hash-password)", s.Key)
		}
		if keys[s.Key] {
			return nil, fmt.Errorf("duplicate site key %q", s.Key)
		}
		keys[s.Key] = true
		if urls[s.SiteURL] {
			return nil, fmt.Errorf("duplicate site_url %q", s.SiteURL)
		}
		urls[s.SiteURL] = true

		if s.SiteName == "" {
			s.SiteName = "ntx"
		}
		if s.AboutSlug == "" {
			s.AboutSlug = "about"
		}
		if s.TemplatePath == "" {
			s.TemplatePath = cfg.TemplatePath
		}
		if s.StaticPath == "" {
			s.StaticPath = cfg.StaticPath
		}
	}

	return &cfg, nil
}
