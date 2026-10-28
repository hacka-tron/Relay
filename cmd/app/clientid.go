package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// clientID returns a stable random identifier for this installation,
// created on first run and stored in the user's config directory. The
// server uses it to bucket clients into staged rollouts, nothing more.
func clientID() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(base, "relay", "client-id")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	id := newUUID()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// newUUID returns a random version 4 UUID string.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
