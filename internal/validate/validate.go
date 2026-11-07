// Package validate holds the naming rules shared by config, the site store
// and themes: site keys and theme names, hostnames, and section/page slugs.
package validate

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// keyRe: a key names a directory (a site's `ntx generate` output, a theme
// folder), so it must be one lowercase path segment -- no traversal, no
// separators, and no two names that collide on a case-insensitive
// filesystem.
var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Key validates a site key or a theme name.
func Key(s string) error {
	if !keyRe.MatchString(s) {
		return fmt.Errorf("%q must be lowercase letters, digits, '-' or '_', starting with a letter or digit", s)
	}
	return nil
}

// Hostname requires a bare hostname. Routing compares it against the
// request's Host header with any port stripped, so a scheme, port or path
// would make the site unreachable.
func Hostname(h string) error {
	switch {
	case h == "":
		return fmt.Errorf("hostname must be set")
	case strings.Contains(h, "://"):
		return fmt.Errorf("hostname %q must be a bare hostname, without a scheme (e.g. \"example.com\")", h)
	case strings.ContainsAny(h, "/ \t\r\n"):
		return fmt.Errorf("hostname %q must be a bare hostname, without a path or whitespace", h)
	}
	if _, _, err := net.SplitHostPort(h); err == nil {
		return fmt.Errorf("hostname %q must be a bare hostname, without a port: the port is ignored when matching the Host header", h)
	}
	return nil
}

var segmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Segment validates a single URL path segment used as a section or page
// slug. These end up in template names and in `ntx generate` output paths,
// so anything that could traverse ("..", "a/b") is rejected.
func Segment(s string) error {
	if !segmentRe.MatchString(s) {
		return fmt.Errorf("slug %q must be a single path segment of letters, digits, '.', '-' or '_'", s)
	}
	return nil
}

// ReservedSectionSlugs are path prefixes the server routes itself. A
// section named after one of these would be shadowed by (or shadow) a
// built-in route.
var ReservedSectionSlugs = map[string]bool{
	"blog":   true,
	"static": true,
	"admin":  true,
	"about":  true,
	"auth":   true,
	"invite": true,
}

// SectionSlug rejects a section slug that is not a single plain path
// segment, or that collides with a built-in route.
func SectionSlug(slug string) error {
	if err := Segment(slug); err != nil {
		return fmt.Errorf("section %w", err)
	}
	if ReservedSectionSlugs[slug] {
		return fmt.Errorf("section slug %q is reserved for a built-in route", slug)
	}
	return nil
}

// Email checks an address that the caller has already trimmed and
// lowercased. It is deliberately loose: the provider verifies the mailbox;
// this only keeps obvious typos and junk out of the users table.
func Email(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("email must be set")
	case len(s) > 254:
		return fmt.Errorf("email %q is too long", s)
	case strings.IndexFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0:
		return fmt.Errorf("email %q must not contain spaces or control characters", s)
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") || !strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("%q is not an email address", s)
	}
	return nil
}
