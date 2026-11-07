package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nod/ntx/internal/config"
)

// hashAnchor is the full line applyGenConfigSubstitutions anchors the site
// hash substitution on. Declared here so a change to the sample's wording
// that does not update the anchor fails loudly instead of silently emitting a
// config with an empty admin_password_hash.
const hashAnchor = `admin_password_hash = ""   # required: bcrypt hash`

// realBcryptHash is a genuine bcrypt hash (of "testpass"); config.Load
// bcrypt-validates every admin_password_hash, so TestGenConfigOutputLoads
// needs a well-formed one.
const realBcryptHash = `$2a$10$XKhLm60v72htK/lAO26kDuWPXh.3DovbWhmC/tN.E1UIzH0D.moZ.`

// TestGenConfigSubstitutionsDoNotLeakIntoGlobalHash guards against the
// credential leak described in task-10 addendum Ruling 7: a naive
// strings.ReplaceAll on "admin_password_hash = \"\"" also matches
// inside the sample's commented "# global_admin_password_hash = \"\""
// line, since the former is a substring of the latter. That would
// produce a generated config where uncommenting the global line
// silently grants all-sites admin access using what the operator
// believed was one site's password.
func TestGenConfigSubstitutionsDoNotLeakIntoGlobalHash(t *testing.T) {
	const hash = "$2a$10$abcdefghijklmnopqrstuv"
	const secret = "some-random-session-secret-value"

	out := applyGenConfigSubstitutions(sampleConfig, secret, hash)

	if !strings.Contains(out, `admin_password_hash = "`+hash+`"`) {
		t.Errorf("expected site's admin_password_hash to contain the generated hash, got:\n%s", out)
	}

	matches := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "global_admin_password_hash") {
			matches++
			if !strings.Contains(line, `global_admin_password_hash = ""`) {
				t.Errorf("global_admin_password_hash line was modified, want it to stay empty: %q", line)
			}
		}
	}
	if matches != 1 {
		t.Errorf("expected exactly 1 line mentioning global_admin_password_hash, found %d -- the check verified nothing if this is 0", matches)
	}
}

// TestGenConfigHashAnchorStillMatchesSample keeps the Ruling 7 anchoring
// honest from the other direction: the anchor must occur exactly once in the
// sample, and must not be a substring of the commented global-admin line (the
// substring relationship is precisely what made the naive target unsafe).
func TestGenConfigHashAnchorStillMatchesSample(t *testing.T) {
	if n := strings.Count(sampleConfig, hashAnchor); n != 1 {
		t.Fatalf("expected the anchored site hash line to appear exactly once in sampleConfig, found %d -- "+
			"gen-config -p would stop substituting the hash", n)
	}
	for _, line := range strings.Split(sampleConfig, "\n") {
		if strings.Contains(line, "global_admin_password_hash") && strings.Contains(line, hashAnchor) {
			t.Errorf("the anchor matches the commented global-admin line, which would leak the site's "+
				"password hash into the all-sites setting: %q", line)
		}
	}
}

// TestGenConfigOutputLoads is the first-run check: `ntx gen-config -p >
// ntx.toml && ntx serve` must produce a config that boots. Host routing is
// mandatory now, and an unmatched Host 404s indistinguishably from a route
// 404, so the sample's site_url has to be the host an operator actually
// browses on their first run.
func TestGenConfigOutputLoads(t *testing.T) {
	out := applyGenConfigSubstitutions(sampleConfig, strings.Repeat("s", 32), realBcryptHash)

	f := filepath.Join(t.TempDir(), "ntx.toml")
	if err := os.WriteFile(f, []byte(out), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("gen-config output must load: %v\n%s", err, out)
	}
	if len(cfg.Sites) != 1 {
		t.Fatalf("expected 1 site in the sample config, got %d", len(cfg.Sites))
	}
	if cfg.Sites[0].SiteURL != "localhost" {
		t.Errorf("sample site_url should be %q so the first run resolves on localhost, got %q",
			"localhost", cfg.Sites[0].SiteURL)
	}
	if cfg.GlobalAdminPasswordHash != "" {
		t.Errorf("sample config must leave global_admin_password_hash unset, got %q", cfg.GlobalAdminPasswordHash)
	}
}

// TestReadPasswordLineRejectsEmptyInput guards against the bug found in
// fix round 1: hash-password's non-terminal branch used to accept a
// closed or empty stdin (bufio.Reader.ReadString returns "" with
// io.EOF, which the old guard let through), silently hashing the empty
// string and printing a well-formed bcrypt hash with no indication that
// nothing was actually read.
func TestReadPasswordLineRejectsEmptyInput(t *testing.T) {
	_, err := readPasswordLine(strings.NewReader(""))
	if err == nil {
		t.Error("expected an error for empty stdin, got nil")
	}
}

func TestReadPasswordLineAcceptsNewlineTerminatedPassword(t *testing.T) {
	pw, err := readPasswordLine(strings.NewReader("testpass\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pw != "testpass" {
		t.Errorf("expected pw = %q, got %q", "testpass", pw)
	}
}
