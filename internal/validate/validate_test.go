package validate

import (
	"strings"
	"testing"
)

func TestKey(t *testing.T) {
	for _, k := range []string{"../www", "a/b", `a\b`, "..", ".", "Blog", " a", "a b", ".hidden", ""} {
		if Key(k) == nil {
			t.Errorf("Key(%q): expected error", k)
		}
	}
	for _, k := range []string{"mysite", "my-site", "my_site", "site2", "default", "2nd"} {
		if err := Key(k); err != nil {
			t.Errorf("Key(%q): unexpected error: %v", k, err)
		}
	}
}

func TestHostname(t *testing.T) {
	for _, h := range []string{"", "https://example.com", "http://example.com", "example.com:8080", "example.com/blog", "example.com/", "exa mple.com"} {
		if Hostname(h) == nil {
			t.Errorf("Hostname(%q): expected error", h)
		}
	}
	for _, h := range []string{"localhost", "example.com", "Example.COM", "sub.example.co.uk", "127.0.0.1", "::1"} {
		if err := Hostname(h); err != nil {
			t.Errorf("Hostname(%q): unexpected error: %v", h, err)
		}
	}
}

func TestSectionSlugReserved(t *testing.T) {
	for _, s := range []string{"blog", "static", "admin", "about"} {
		if SectionSlug(s) == nil {
			t.Errorf("SectionSlug(%q): expected error", s)
		}
	}
	if err := SectionSlug("projects"); err != nil {
		t.Errorf("SectionSlug(projects): %v", err)
	}
}

func TestReservedIncludesAuthAndInvite(t *testing.T) {
	for _, s := range []string{"auth", "invite"} {
		if SectionSlug(s) == nil {
			t.Errorf("SectionSlug(%q): expected error", s)
		}
	}
}

func TestSegmentRejectsTraversal(t *testing.T) {
	for _, s := range []string{"..", "../x", "a/b", "", "{page}", "*", "a b"} {
		if Segment(s) == nil {
			t.Errorf("Segment(%q): expected error", s)
		}
	}
	for _, s := range []string{"one", "v1.2", "Blog"} {
		if err := Segment(s); err != nil {
			t.Errorf("Segment(%q): unexpected error: %v", s, err)
		}
	}
}

func TestEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last+tag@sub.example.com"} {
		if err := Email(ok); err != nil {
			t.Errorf("Email(%q) = %v, want nil", ok, err)
		}
	}
	long := strings.Repeat("a", 246) + "@bb.co.uk" // 255 bytes
	for _, bad := range []string{"", "a", "a@", "@b.co", "a@b", "a@@b.co", "a b@c.co", "a@b.co\n", long} {
		if err := Email(bad); err == nil {
			t.Errorf("Email(%q) = nil, want an error", bad)
		}
	}
}
