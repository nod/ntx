// Package clientip works out the real client address and scheme of a
// request. ntx runs behind a reverse proxy, so the TCP peer is usually the
// proxy; X-Forwarded-For and X-Forwarded-Proto are believed only when the
// peer is in the configured trusted_proxies.
package clientip

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type ctxKey struct{}

type info struct{ ip, scheme string }

// Middleware records the client IP and scheme for IP and Scheme.
func Middleware(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		a = a.Unmap()
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.RemoteAddr
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			ip, scheme := host, "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if peer, err := netip.ParseAddr(host); err == nil && isTrusted(peer) {
				ip = fromForwarded(r.Header.Values("X-Forwarded-For"), host, isTrusted)
				switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) {
				case "https":
					scheme = "https"
				case "http":
					scheme = "http"
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, info{ip, scheme})))
		})
	}
}

// fromForwarded walks X-Forwarded-For right to left, skipping our own
// trusted proxies: the first untrusted entry is the client. Entries to its
// left were supplied by the client and can't be believed. If every entry
// is trusted, the leftmost is the best answer available.
func fromForwarded(headers []string, peer string, isTrusted func(netip.Addr) bool) string {
	var entries []string
	for _, h := range headers {
		for _, e := range strings.Split(h, ",") {
			if e = strings.TrimSpace(e); e != "" {
				entries = append(entries, e)
			}
		}
	}
	if len(entries) == 0 {
		return peer
	}
	for i := len(entries) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(entries[i])
		if err != nil {
			// Garbage in the chain: stop at the last address we could vouch for.
			if i+1 < len(entries) {
				return entries[i+1]
			}
			return peer
		}
		if !isTrusted(a) {
			return a.Unmap().String()
		}
	}
	return entries[0]
}

// IP returns the client IP recorded by Middleware, or the raw peer address
// when Middleware didn't run.
func IP(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKey{}).(info); ok {
		return v.ip
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// Scheme returns "https" or "http" for the client's view of the request.
func Scheme(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKey{}).(info); ok {
		return v.scheme
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
