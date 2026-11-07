package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/spf13/viper"
	"golang.org/x/crypto/bcrypt"
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

	if cfg.GlobalAdminPasswordHash != "" {
		if err := checkBcryptHash(cfg.GlobalAdminPasswordHash); err != nil {
			return nil, fmt.Errorf("global_admin_password_hash: %w", err)
		}
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
		if err := checkBcryptHash(s.AdminPasswordHash); err != nil {
			return nil, fmt.Errorf("site %q: admin_password_hash: %w", s.Key, err)
		}
		if err := checkSiteKey(s.Key); err != nil {
			return nil, fmt.Errorf("site %d: %w", i, err)
		}
		if err := checkSiteURL(s.SiteURL); err != nil {
			return nil, fmt.Errorf("site %q: %w", s.Key, err)
		}
		for _, sec := range s.Sections {
			if err := CheckSectionSlug(sec.Slug); err != nil {
				return nil, fmt.Errorf("site %q: %w", s.Key, err)
			}
		}
		if keys[s.Key] {
			return nil, fmt.Errorf("duplicate site key %q", s.Key)
		}
		keys[s.Key] = true
		// Routing lowercases the Host header, so site_urls that differ only
		// in case are the same routing key.
		host := strings.ToLower(s.SiteURL)
		if urls[host] {
			return nil, fmt.Errorf("duplicate site_url %q (site_url matching ignores case)", s.SiteURL)
		}
		urls[host] = true

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

// checkBcryptHash rejects anything that is not a well-formed bcrypt hash.
// A truncated or mis-pasted hash is otherwise invisible until someone tries
// to log in, at which point every attempt fails with "Invalid password" and
// no amount of retyping the password helps. bcrypt.Cost does the parse for
// us: it only succeeds on a hash bcrypt could actually compare against.
func checkBcryptHash(hash string) error {
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return fmt.Errorf("not a valid bcrypt hash (run: ntx hash-password): %w", err)
	}
	return nil
}

// siteKeyRe: a key names the site's `ntx generate` output directory, so it
// must be one lowercase path segment -- no traversal, no separators, and no
// two keys that collide on a case-insensitive filesystem.
var siteKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func checkSiteKey(key string) error {
	if !siteKeyRe.MatchString(key) {
		return fmt.Errorf("key %q must be lowercase letters, digits, '-' or '_', starting with a letter or digit", key)
	}
	return nil
}

// checkSiteURL requires a bare hostname. Routing compares site_url against
// the request's Host header with any port stripped, so a scheme, port or
// path in site_url would make the site unreachable: it boots fine and 404s
// on every request.
func checkSiteURL(u string) error {
	switch {
	case strings.Contains(u, "://"):
		return fmt.Errorf("site_url %q must be a bare hostname, without a scheme (e.g. \"example.com\")", u)
	case strings.ContainsAny(u, "/ \t\r\n"):
		return fmt.Errorf("site_url %q must be a bare hostname, without a path or whitespace", u)
	}
	if _, _, err := net.SplitHostPort(u); err == nil {
		return fmt.Errorf("site_url %q must be a bare hostname, without a port: the port is ignored when matching the Host header", u)
	}
	return nil
}

// reservedSectionSlugs are path prefixes the server routes itself. Every
// site's section slugs are registered on one shared router, so a section
// named after one of these would take over that route on every site.
var reservedSectionSlugs = map[string]bool{
	"blog":   true,
	"static": true,
	"admin":  true,
	"about":  true,
}

var sectionSlugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// CheckSectionSlug rejects a section slug that is not a single plain path
// segment, or that collides with a built-in route.
func CheckSectionSlug(slug string) error {
	if !sectionSlugRe.MatchString(slug) {
		return fmt.Errorf("section slug %q must be a single path segment of letters, digits, '.', '-' or '_'", slug)
	}
	if reservedSectionSlugs[slug] {
		return fmt.Errorf("section slug %q is reserved for a built-in route", slug)
	}
	return nil
}
