// Package server assembles the HTTP server: host resolution, session
// loading, CSRF protection and security headers around the handlers' route
// table.
package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/clientip"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/handlers"
	"github.com/nod/ntx/internal/sitectx"
	"github.com/nod/ntx/internal/sitereg"
)

// WriteTimeout bounds how long a handler has to write its response,
// counted from when the request was read. Slow work in handlers (the
// sign-in provider) must finish well inside it; see
// handlers.DefaultProviderTimeout.
const WriteTimeout = 10 * time.Second

func New(cfg *config.Config, h *handlers.Handler, reg *sitereg.Registry, am *auth.Manager) *http.Server {
	r := chi.NewRouter()
	r.Use(clientip.Middleware(cfg.TrustedProxies))
	r.Use(requestLog)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(resolveHost(reg, cfg.PlatformHostname))
	r.Use(am.Middleware(func(r *http.Request) (int64, bool) {
		if site := sitectx.From(r); site != nil {
			return site.ID, true
		}
		return 0, false
	}))
	h.Mount(r)

	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "localhost"
	}

	return &http.Server{
		Addr: fmt.Sprintf("%s:%d", bindAddr, cfg.Port),
		// Every state-changing request must come from our own origin:
		// SameSite=Strict alone treats sibling subdomains as same-site.
		Handler:      http.NewCrossOriginProtection().Handler(r),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}
}

// requestLog logs one line per request with the real client IP (see
// clientip), so behind a proxy the log names the client, not the proxy.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("%s %s %s://%s%s %d %dB %s from %s",
			r.Method, r.Proto, clientip.Scheme(r), r.Host, logPath(r),
			status, ww.BytesWritten(), time.Since(start).Round(time.Microsecond), clientip.IP(r))
	})
}

// logPath is the request URI with secrets taken out: invite tokens (in the
// path), the provider's code and state (in the callback's query), and an
// invite token wrongly sent in /auth/start's query (it is only read from
// the POST body, but would still be logged).
func logPath(r *http.Request) string {
	switch p := r.URL.EscapedPath(); {
	case strings.HasPrefix(p, "/invite/"):
		return "/invite/[redacted]"
	case p == "/auth/callback", p == "/auth/start":
		return p
	}
	return r.URL.RequestURI()
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

// resolveHost matches the Host header (lowercased, port stripped) to a
// site, or to the platform hostname. Anything else gets a bare 404 that
// reveals nothing about which hostnames exist.
func resolveHost(reg *sitereg.Registry, platformHostname string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			host = strings.ToLower(host)
			switch site := reg.ByHost(host); {
			case site != nil:
				r = sitectx.With(r, site)
			case host == platformHostname:
				r = sitectx.WithPlatform(r)
			default:
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
