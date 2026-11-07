package clientip

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func prefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// run sends one request through Middleware and returns what IP and Scheme
// reported inside the handler.
func run(t *testing.T, trusted []netip.Prefix, peer string, headers map[string]string) (ip, scheme string) {
	t.Helper()
	h := Middleware(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, scheme = IP(r), Scheme(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return ip, scheme
}

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		peer    string
		xff     string
		trusted []string
		want    string
	}{
		{"no proxy configured", "203.0.113.5:1234", "1.2.3.4", nil, "203.0.113.5"},
		{"trusted proxy", "127.0.0.1:9", "1.2.3.4", []string{"127.0.0.1/32"}, "1.2.3.4"},
		{"spoofed leftmost entry", "127.0.0.1:9", "6.6.6.6, 1.2.3.4", []string{"127.0.0.1/32"}, "1.2.3.4"},
		{"proxy chain", "127.0.0.1:9", "1.2.3.4, 10.0.0.2", []string{"127.0.0.1/32", "10.0.0.0/8"}, "1.2.3.4"},
		{"untrusted peer ignores header", "198.51.100.7:9", "1.2.3.4", []string{"127.0.0.1/32"}, "198.51.100.7"},
		{"trusted proxy, no header", "127.0.0.1:9", "", []string{"127.0.0.1/32"}, "127.0.0.1"},
		{"ipv6 peer", "[::1]:9", "2001:db8::1", []string{"::1/128"}, "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := map[string]string{}
			if tc.xff != "" {
				h["X-Forwarded-For"] = tc.xff
			}
			got, _ := run(t, prefixes(t, tc.trusted...), tc.peer, h)
			if got != tc.want {
				t.Errorf("IP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScheme(t *testing.T) {
	trusted := prefixes(t, "127.0.0.1/32")
	if _, s := run(t, trusted, "127.0.0.1:9", map[string]string{"X-Forwarded-Proto": "https"}); s != "https" {
		t.Errorf("trusted proxy saying https: got %q", s)
	}
	if _, s := run(t, trusted, "198.51.100.7:9", map[string]string{"X-Forwarded-Proto": "https"}); s != "http" {
		t.Errorf("untrusted peer saying https: got %q", s)
	}
	if _, s := run(t, trusted, "127.0.0.1:9", map[string]string{"X-Forwarded-Proto": "gopher"}); s != "http" {
		t.Errorf("unknown scheme from trusted proxy: got %q", s)
	}
}
