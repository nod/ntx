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

type Config struct {
	StaticPath        string    `mapstructure:"static_path"`
	DBPath            string    `mapstructure:"db_path"`
	Port              int       `mapstructure:"port"`
	TemplatePath      string    `mapstructure:"template_path"`
	AboutSlug         string    `mapstructure:"about_slug"`
	AdminPasswordHash string    `mapstructure:"admin_password_hash"`
	SessionSecret     string    `mapstructure:"session_secret"`
	Dev               bool      `mapstructure:"dev"`
	SiteName          string    `mapstructure:"site_name"`
	SiteURL           string    `mapstructure:"site_url"`
	SiteDescription   string    `mapstructure:"site_description"`
	Sections          []Section `mapstructure:"section"`
}

func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	v.SetDefault("static_path", "./static")
	v.SetDefault("db_path", "./ntx.db")
	v.SetDefault("port", 8080)
	v.SetDefault("template_path", "./templates")
	v.SetDefault("about_slug", "about")
	v.SetDefault("dev", false)
	v.SetDefault("site_name", "ntx")
	v.SetDefault("site_url", "localhost")
	v.SetDefault("site_description", "")

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
	if cfg.AdminPasswordHash == "" {
		return nil, fmt.Errorf("admin_password_hash must be set (run: ntx hash-password)")
	}

	return &cfg, nil
}
