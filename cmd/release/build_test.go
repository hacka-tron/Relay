package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hacka-tron/relay/internal/manifest"
)

// TestBuildProducesSignedRelease really runs go build for the host
// platform, then checks that release.json is consistent with the binary.
func TestBuildProducesSignedRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build")
	}
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"keygen", "--out", dir}, &out); err != nil {
		t.Fatal(err)
	}
	target := runtime.GOOS + "/" + runtime.GOARCH
	err := run([]string{"build", "--version", "1.2.3", "--key", filepath.Join(dir, "signing.key"),
		"--out", filepath.Join(dir, "releases"), "--targets", target, "--src", "../app"}, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	b, err := os.ReadFile(filepath.Join(dir, "releases", "1.2.3", "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rel manifest.Release
	if err := json.Unmarshal(b, &rel); err != nil {
		t.Fatal(err)
	}
	m, ok := rel.Target(runtime.GOOS, runtime.GOARCH)
	if !ok || len(rel.Targets) != 1 {
		t.Fatalf("targets: %+v", rel.Targets)
	}
	pubText, _ := os.ReadFile(filepath.Join(dir, "pubkey.txt"))
	pub, _ := manifest.ParsePublicKey(string(pubText))
	if err := manifest.Verify(pub, m); err != nil {
		t.Fatal("release.json signature:", err)
	}
	bin := filepath.Join(dir, "releases", "1.2.3", m.URL)
	sum, size, err := hashFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if sum != m.SHA256 || size != m.Size {
		t.Fatal("release.json hash or size does not match the binary")
	}
	// The built binary knows its version and key.
	got, err := exec.Command(bin, "--selftest").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "1.2.3" {
		t.Fatalf("selftest printed %q", got)
	}

	// promote makes it visible.
	if err := run([]string{"promote", "--version", "1.2.3", "--percent", "25", "--releases", filepath.Join(dir, "releases")}, &out); err != nil {
		t.Fatal(err)
	}
	rc, _ := os.ReadFile(filepath.Join(dir, "releases", "rollout.json"))
	if !strings.Contains(string(rc), `"version": "1.2.3"`) || !strings.Contains(string(rc), `"rollout_percent": 25`) {
		t.Fatalf("rollout.json: %s", rc)
	}
}

// compile then sign, as CI does it on separate machines, must produce the
// same kind of signed release as build.
func TestCompileThenSign(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build")
	}
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"keygen", "--out", dir}, &out); err != nil {
		t.Fatal(err)
	}
	rels := filepath.Join(dir, "releases")
	target := runtime.GOOS + "/" + runtime.GOARCH
	if err := run([]string{"compile", "--version", "2.0.0", "--pubkey", filepath.Join(dir, "pubkey.txt"),
		"--out", rels, "--targets", target, "--src", "../app"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	// A stray file next to the binaries (the macOS zip) must be ignored.
	os.WriteFile(filepath.Join(rels, "2.0.0", "Relay-macos-arm64.zip"), []byte("zip"), 0o644)
	if err := run([]string{"sign", "--version", "2.0.0", "--key", filepath.Join(dir, "signing.key"), "--out", rels}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(rels, "2.0.0", "release.json"))
	var rel manifest.Release
	json.Unmarshal(b, &rel)
	pubText, _ := os.ReadFile(filepath.Join(dir, "pubkey.txt"))
	pub, _ := manifest.ParsePublicKey(string(pubText))
	if len(rel.Targets) != 1 || manifest.Verify(pub, rel.Targets[0]) != nil {
		t.Fatalf("release.json: %s", b)
	}
}

func TestCheckServerURL(t *testing.T) {
	for _, ok := range []string{"", "https://relay.example.run.app", "https://x.example/"} {
		if err := checkServerURL(ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://plain.example", "https://", "https://a b", "relay.example", "https://x.example/path", "https://u:p@x.example", "https://x.example?a=1", "https://x.example#f"} {
		if checkServerURL(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseBinaryName(t *testing.T) {
	cases := map[string]bool{
		"app-linux-amd64": true, "app-windows-amd64.exe": true, "app-darwin-arm64": true,
		"app-linux-amd64.exe": false, "app-windows-amd64": false, "Relay-macos-arm64.zip": false,
		"release.json": false, "app-linux": false,
	}
	for name, want := range cases {
		if _, _, ok := parseBinaryName(name); ok != want {
			t.Errorf("%s: ok=%v want %v", name, ok, want)
		}
	}
}

func TestBuildRejectsBadVersionAndMissingKey(t *testing.T) {
	var out bytes.Buffer
	for _, v := range []string{"latest", "1.2", "v1.2.3", "../x", "1.2.3+build"} {
		if err := run([]string{"build", "--version", v}, &out); err == nil {
			t.Fatalf("accepted version %q", v)
		}
	}
	t.Setenv("UPDATER_SIGNING_KEY", "")
	if err := run([]string{"build", "--version", "1.0.0", "--key", filepath.Join(t.TempDir(), "nope")}, &out); err == nil {
		t.Fatal("built without a key")
	}
}

func TestPromoteValidatesInput(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	if err := run([]string{"promote", "--version", "1.0.0", "--releases", dir}, &out); err == nil {
		t.Fatal("promoted a version that was never built")
	}
	os.MkdirAll(filepath.Join(dir, "1.0.0"), 0o755)
	os.WriteFile(filepath.Join(dir, "1.0.0", "release.json"), []byte("{}"), 0o644)
	bad := [][]string{
		{"--version", "1.0.0", "--channel", "", "--releases", dir},
		{"--version", "1.0.0", "--percent", "101", "--releases", dir},
		{"--version", "1.0.0", "--percent", "-1", "--releases", dir},
		{"--version", "1.0", "--releases", dir},
	}
	for _, args := range bad {
		if err := run(append([]string{"promote"}, args...), &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run([]string{"promote", "--version", "1.0.0", "--percent", "0", "--releases", dir}, &out); err != nil {
		t.Fatal(err)
	}
}
