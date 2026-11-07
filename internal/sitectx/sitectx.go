// Package sitectx carries the request's resolved config.Site through
// context, so handlers, session auth, and routing agree on "which site
// is this?" without handlers importing server (which would be a cycle).
package sitectx

import (
	"context"
	"net/http"

	"github.com/nod/ntx/internal/config"
)

type ctxKey struct{}

// With returns a copy of r with site attached to its context.
func With(r *http.Request, site *config.Site) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, site))
}

// From returns the site attached to r's context, or nil if none was set.
func From(r *http.Request) *config.Site {
	site, _ := r.Context().Value(ctxKey{}).(*config.Site)
	return site
}
