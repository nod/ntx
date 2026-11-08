// Package secretbox encrypts small secrets (provider client secrets) for
// storage with AES-256-GCM. A sealed value is nonce || ciphertext.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// ErrOpen means a sealed value didn't decrypt: the key changed, or the value
// is corrupt.
var ErrOpen = errors.New("secretbox: can't decrypt (wrong key or corrupt value)")

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext under a 32-byte key with a random nonce.
func Seal(key []byte, plaintext string) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts a value from Seal. Any failure to authenticate is ErrOpen.
func Open(key, sealed []byte) (string, error) {
	g, err := gcm(key)
	if err != nil {
		return "", err
	}
	if len(sealed) < g.NonceSize() {
		return "", ErrOpen
	}
	plain, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
	if err != nil {
		return "", ErrOpen
	}
	return string(plain), nil
}
