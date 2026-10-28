package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/hacka-tron/relay/internal/manifest"
)

// defaultTargets are the platforms every release is built for.
const defaultTargets = "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"

// build compiles the app for each target with the version and public key
// baked in, then hashes and signs each binary and writes release.json.
// It never touches rollout.json: building a release and offering it to
// clients are separate, deliberate steps.
func build(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(out)
	version := fs.String("version", "", "version to build, for example 1.2.0 (required)")
	keyFile := fs.String("key", "signing.key", "private key file, used when UPDATER_SIGNING_KEY is not set")
	outDir := fs.String("out", "releases", "releases directory")
	targets := fs.String("targets", defaultTargets, "comma separated os/arch pairs")
	src := fs.String("src", "./cmd/app", "package to build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !semver.IsValid("v" + *version) {
		return fmt.Errorf("--version %q is not a valid semantic version", *version)
	}
	priv, err := loadPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	pub := manifest.EncodeKey(priv.Public().(ed25519.PublicKey))

	dir := filepath.Join(*outDir, *version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rel := manifest.Release{Version: *version}
	for _, target := range strings.Split(*targets, ",") {
		goos, goarch, ok := strings.Cut(strings.TrimSpace(target), "/")
		if !ok {
			return fmt.Errorf("bad target %q, want os/arch", target)
		}
		name := "app-" + goos + "-" + goarch
		if goos == "windows" {
			name += ".exe"
		}
		path := filepath.Join(dir, name)
		fmt.Fprintf(out, "building %s\n", path)
		if err := goBuild(goos, goarch, *version, strings.TrimSpace(pub), *src, path, out); err != nil {
			return err
		}
		sum, size, err := hashFile(path)
		if err != nil {
			return err
		}
		m := manifest.Manifest{Version: *version, OS: goos, Arch: goarch, URL: name, SHA256: sum, Size: size}
		if err := manifest.Sign(priv, &m); err != nil {
			return err
		}
		rel.Targets = append(rel.Targets, m)
	}

	b, err := json.MarshalIndent(rel, "", "  ")
	if err != nil {
		return err
	}
	relPath := filepath.Join(dir, "release.json")
	if err := os.WriteFile(relPath, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (%d targets)\n", relPath, len(rel.Targets))
	return nil
}

// goBuild runs the Go toolchain for one platform. The version and public
// key are set through the linker so the same source produces every build.
func goBuild(goos, goarch, version, pubkey, src, outPath string, out io.Writer) error {
	ldflags := "-s -w -X main.version=" + version + " -X main.publicKey=" + pubkey
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", outPath, src)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build %s/%s: %w", goos, goarch, err)
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// loadPrivateKey prefers the environment, which is how CI passes secrets,
// and falls back to a file for local use.
func loadPrivateKey(keyFile string) (ed25519.PrivateKey, error) {
	if env := os.Getenv("UPDATER_SIGNING_KEY"); env != "" {
		return manifest.ParsePrivateKey(env)
	}
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, errors.New("no signing key: set UPDATER_SIGNING_KEY or pass --key")
	}
	return manifest.ParsePrivateKey(string(b))
}
