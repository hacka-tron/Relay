package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hacka-tron/relay/internal/manifest"
)

func TestKeygenWritesUsableKeys(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"keygen", "--out", dir}, &out); err != nil {
		t.Fatal(err)
	}

	privText, err := os.ReadFile(filepath.Join(dir, "signing.key"))
	if err != nil {
		t.Fatal(err)
	}
	pubText, err := os.ReadFile(filepath.Join(dir, "pubkey.txt"))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := manifest.ParsePrivateKey(string(privText))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := manifest.ParsePublicKey(string(pubText))
	if err != nil {
		t.Fatal(err)
	}

	m := manifest.Manifest{Version: "1.0.0", OS: "linux", Arch: "amd64",
		SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	if err := manifest.Sign(priv, &m); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(pub, m); err != nil {
		t.Fatal("keys written by keygen do not match:", err)
	}

	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, "signing.key"))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("signing.key mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestKeygenRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"keygen", "--out", dir}, &out); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "signing.key"))

	if err := run([]string{"keygen", "--out", dir}, &out); err == nil {
		t.Fatal("second keygen overwrote signing.key without --force")
	}
	same, _ := os.ReadFile(filepath.Join(dir, "signing.key"))
	if !bytes.Equal(first, same) {
		t.Fatal("signing.key changed despite the refusal")
	}

	if err := run([]string{"keygen", "--out", dir, "--force"}, &out); err != nil {
		t.Fatal(err)
	}
	replaced, _ := os.ReadFile(filepath.Join(dir, "signing.key"))
	if bytes.Equal(first, replaced) {
		t.Fatal("--force did not replace signing.key")
	}
}

// Rotating a key that had loose permissions must not leave the new key
// readable by other users.
func TestKeygenForceTightensMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes")
	}
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"keygen", "--out", dir}, &out); err != nil {
		t.Fatal(err)
	}
	priv := filepath.Join(dir, "signing.key")
	if err := os.Chmod(priv, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"keygen", "--out", dir, "--force"}, &out); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(priv)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("after --force signing.key mode = %o, want 600", info.Mode().Perm())
	}
}

func TestUnknownCommand(t *testing.T) {
	if err := run([]string{"frobnicate"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown command accepted")
	}
	if err := run(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("no command accepted")
	}
}
