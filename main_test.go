package main

import (
	"strings"
	"testing"
)

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
