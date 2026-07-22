package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/nerdtx/ntx/internal/auth"
	"github.com/nerdtx/ntx/internal/config"
	"github.com/nerdtx/ntx/internal/handlers"
	"github.com/nerdtx/ntx/internal/models"
)

func New(cfg *config.Config, posts *models.PostStore, session *auth.SessionManager) (*http.Server, error) {
	h, err := handlers.New(cfg, posts, session)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	r.Get("/", h.Home)
	r.Get("/about", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/blog/"+cfg.AboutSlug, http.StatusFound)
	})
	r.Get("/blog", h.BlogIndex)
	r.Get("/blog/{slug}", h.BlogPost)

	for _, sec := range cfg.Sections {
		sec := sec
		r.Get("/"+sec.Slug+"/{page}", func(w http.ResponseWriter, r *http.Request) {
			h.SectionPage(w, r, sec)
		})
	}

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.StaticPath))))

	r.Get("/admin/login", h.AdminLogin)
	r.Post("/admin/login", h.AdminLoginPost)

	r.Group(func(r chi.Router) {
		r.Use(session.Middleware)
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
