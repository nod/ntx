package server

import (
	"fmt"
	"net/http"

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

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}, nil
}
