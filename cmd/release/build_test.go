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

func TestBuildRejectsBadVersionAndMissingKey(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"build", "--version", "latest"}, &out); err == nil {
		t.Fatal("accepted non semver version")
	}
	t.Setenv("UPDATER_SIGNING_KEY", "")
	if err := run([]string{"build", "--version", "1.0.0", "--key", filepath.Join(t.TempDir(), "nope")}, &out); err == nil {
		t.Fatal("built without a key")
	}
}

func TestPromoteRequiresBuiltRelease(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"promote", "--version", "1.0.0", "--releases", t.TempDir()}, &out); err == nil {
		t.Fatal("promoted a version that was never built")
	}
}
