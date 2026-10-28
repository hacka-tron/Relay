package manifest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// Key files are one line of standard base64. signing.key holds the 64 byte
// Ed25519 private key (seed plus public half, as Go's ed25519 package
// represents it). pubkey.txt holds the 32 byte public key. Plain text keeps
// the demo easy to inspect; a production tool might wrap the private key in
// PEM or a KMS reference instead.

// GenerateKey makes a fresh Ed25519 key pair from the OS random source.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// EncodeKey turns either key into the one line file format.
func EncodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key) + "\n"
}

// ParsePublicKey reads the pubkey.txt format. It is also what the client
// uses on the embedded key string at startup.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := decodeKey(s)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key: want %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey reads the signing.key format.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := decodeKey(s)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key: want %d bytes, got %d", ed25519.PrivateKeySize, len(b))
	}
	return ed25519.PrivateKey(b), nil
}

func decodeKey(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}
