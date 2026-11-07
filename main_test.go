package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nod/ntx/internal/config"
)

// realBcryptHash is a genuine bcrypt hash (of "testpass"); config.Load
// bcrypt-validates root_password_hash.
const realBcryptHash = `$2a$10$XKhLm60v72htK/lAO26kDuWPXh.3DovbWhmC/tN.E1UIzH0D.moZ.`

func genConfigFile(t *testing.T, rootHash string) string {
	t.Helper()
	secret, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "ntx.toml")
	if err := os.WriteFile(f, []byte(applyGenConfigSubstitutions(sampleConfig, secret, key, rootHash)), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGenConfigOutputLoads(t *testing.T) {
	cfg, err := config.Load(genConfigFile(t, realBcryptHash))
	if err != nil {
		t.Fatalf("gen-config output must load: %v", err)
	}
	if cfg.PlatformHostname != "localhost" {
		t.Errorf("sample platform_hostname should be localhost so a first run works locally, got %q", cfg.PlatformHostname)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Errorf("sample should trust the loopback proxy addresses, got %v", cfg.TrustedProxies)
	}
}

// Without -p the root hash stays empty, and Load must say what's missing.
func TestGenConfigWithoutPasswordFailsLoadOnRootHash(t *testing.T) {
	_, err := config.Load(genConfigFile(t, ""))
	if err == nil || !strings.Contains(err.Error(), "root_password_hash") {
		t.Fatalf("expected a root_password_hash error, got %v", err)
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
