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

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "global_admin_password_hash") {
			if !strings.Contains(line, `global_admin_password_hash = ""`) {
				t.Errorf("global_admin_password_hash line was modified, want it to stay empty: %q", line)
			}
		}
	}
}
