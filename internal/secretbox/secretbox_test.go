package secretbox

import (
	"bytes"
	"errors"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	a, err := Seal(key(1), "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal(key(1), "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical; the nonce isn't random")
	}
	for _, sealed := range [][]byte{a, b} {
		got, err := Open(key(1), sealed)
		if err != nil || got != "hunter2" {
			t.Fatalf("Open = %q, %v", got, err)
		}
	}
}

func TestOpenWrongKey(t *testing.T) {
	sealed, err := Seal(key(1), "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key(2), sealed); !errors.Is(err, ErrOpen) {
		t.Fatalf("Open with the wrong key = %v, want ErrOpen", err)
	}
	if _, err := Open(key(1), sealed[:5]); !errors.Is(err, ErrOpen) {
		t.Fatalf("Open of a truncated value = %v, want ErrOpen", err)
	}
}
