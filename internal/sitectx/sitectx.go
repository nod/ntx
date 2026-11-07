// Package sitectx carries what host resolution decided about a request --
// which site it is for, or that it is on the platform host -- so handlers,
// session auth and routing agree without importing server.
package sitectx

import (
	"context"
	"net/http"

	"github.com/nod/ntx/internal/models"
)

type siteKey struct{}
type platformKey struct{}

// With returns a copy of r with site attached.
func With(r *http.Request, site *models.Site) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), siteKey{}, site))
}

// From returns the request's site, or nil if it has none (an unknown host
// never gets this far; the platform host has no site).
func From(r *http.Request) *models.Site {
	site, _ := r.Context().Value(siteKey{}).(*models.Site)
	return site
}

// WithPlatform marks r as addressed to the platform hostname.
func WithPlatform(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), platformKey{}, true))
}

// IsPlatform reports whether r is on the platform hostname.
func IsPlatform(r *http.Request) bool {
	v, _ := r.Context().Value(platformKey{}).(bool)
	return v
}
