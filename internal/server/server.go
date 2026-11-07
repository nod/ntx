package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/handlers"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/sitectx"
)

func New(cfg *config.Config, posts *models.PostStore, session *auth.SessionManager) (*http.Server, error) {
	// handlers.New rejects sites that never went through
	// models.EnsureSites (ID still 0), so nothing below sees an unset id.
	h, err := handlers.New(cfg.Sites, posts, session, cfg.Dev, cfg.GlobalAdminPasswordHash)
	if err != nil {
		return nil, err
	}

	sites := make(map[string]*config.Site, len(cfg.Sites))
	for i := range cfg.Sites {
		sites[strings.ToLower(cfg.Sites[i].SiteURL)] = &cfg.Sites[i]
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(resolveSite(sites))

	r.Get("/", h.Home)
	r.Get("/about", func(w http.ResponseWriter, r *http.Request) {
		site := sitectx.From(r)
		http.Redirect(w, r, "/blog/"+site.AboutSlug, http.StatusFound)
	})
	r.Get("/blog", h.BlogIndex)
	r.Get("/blog/{slug}", h.BlogPost)

	// chi routes can't vary by Host, so we register one route per unique
	// section slug across all sites; which site's Section content applies
	// is decided per-request inside handlers.SectionPage via sitectx.
	// Because the router is shared, a slug that collides with a built-in
	// route would hijack it on every site -- refuse it here as well as in
	// config.Load, since callers can hand New a Config directly.
	sectionSlugs := map[string]bool{}
	for _, site := range cfg.Sites {
		for _, sec := range site.Sections {
			if err := config.CheckSectionSlug(sec.Slug); err != nil {
				return nil, fmt.Errorf("site %q: %w", site.Key, err)
			}
			sectionSlugs[sec.Slug] = true
		}
	}
	for slug := range sectionSlugs {
		slug := slug
		r.Get("/"+slug+"/{page}", func(w http.ResponseWriter, r *http.Request) {
			h.SectionPage(w, r, slug)
		})
	}

	// Static resolution happens at boot, not per request: one FileServer per
	// site, looked up by the site resolveSite already put on the request.
	staticServers := make(map[int64]http.Handler, len(cfg.Sites))
	for i := range cfg.Sites {
		staticServers[cfg.Sites[i].ID] = http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.Sites[i].StaticPath)))
	}
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site := sitectx.From(r)
		if site == nil {
			http.NotFound(w, r)
			return
		}
		fs, ok := staticServers[site.ID]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	}))

	r.Get("/admin/login", h.AdminLogin)
	r.Post("/admin/login", h.AdminLoginPost)

	r.Group(func(r chi.Router) {
		r.Use(session.Middleware(func(r *http.Request) (int64, bool) {
			// resolveSite 404s unmatched hosts before this runs; if it ever
			// didn't, Middleware fails closed on the false.
			site := sitectx.From(r)
			if site == nil {
				return 0, false
			}
			return site.ID, true
		}))
		r.Post("/admin/logout", h.AdminLogout)
		r.Get("/admin/posts", h.AdminPosts)
		r.Get("/admin/posts/new", h.AdminNewPost)
		r.Post("/admin/posts", h.AdminCreatePost)
		r.Get("/admin/posts/{id}/edit", h.AdminEditPost)
		r.Post("/admin/posts/{id}", h.AdminUpdatePost)
	})

	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "localhost"
	}

	return &http.Server{
		Addr:         fmt.Sprintf("%s:%d", bindAddr, cfg.Port),
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

// resolveSite matches the request's Host header (port stripped) against
// configured site_urls and attaches the match via sitectx. Unmatched
// hosts get a bare 404 -- no detail on which hostnames are configured.
func resolveSite(sites map[string]*config.Site) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if hostOnly, _, err := net.SplitHostPort(host); err == nil {
				host = hostOnly
			}
			site, ok := sites[strings.ToLower(host)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, sitectx.With(r, site))
		})
	}
}
