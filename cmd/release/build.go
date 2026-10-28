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
	"runtime"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/hacka-tron/relay/internal/atomicfile"
	"github.com/hacka-tron/relay/internal/manifest"
)

// defaultTargets are the platforms every release is built for.
const defaultTargets = "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"

// A release is made in two steps so that CI can compile each platform on
// its own machine (the desktop window links against each OS's native web
// engine) while only one job ever sees the private key:
//
//	compile  builds binaries into releases/<version>, using the public key
//	sign     hashes and signs every binary there and writes release.json
//
// build does both in one go for local use. None of them touch
// rollout.json: building a release and offering it to clients are
// separate, deliberate steps.

// build compiles every target and signs the result.
func build(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(out)
	version := fs.String("version", "", "version to build, for example 1.2.0 (required)")
	keyFile := fs.String("key", "signing.key", "private key file, used when UPDATER_SIGNING_KEY is not set")
	outDir := fs.String("out", "releases", "releases directory")
	targets := fs.String("targets", defaultTargets, "comma separated os/arch pairs")
	src := fs.String("src", "./cmd/app", "package to build")
	desktop := fs.Bool("desktop", false, "build the native window (needs each target's native toolchain)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkVersion(*version); err != nil {
		return err
	}
	priv, err := loadPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	pub := strings.TrimSpace(manifest.EncodeKey(priv.Public().(ed25519.PublicKey)))
	if err := compileTargets(*version, *targets, pub, *src, *outDir, *desktop, out); err != nil {
		return err
	}
	return signDir(*version, *outDir, priv, out)
}

// compile builds binaries with the public key from pubkey.txt baked in.
// It never needs the private key.
func compile(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	fs.SetOutput(out)
	version := fs.String("version", "", "version to build (required)")
	outDir := fs.String("out", "releases", "releases directory")
	targets := fs.String("targets", defaultTargets, "comma separated os/arch pairs")
	src := fs.String("src", "./cmd/app", "package to build")
	pubFile := fs.String("pubkey", "pubkey.txt", "public key to embed")
	desktop := fs.Bool("desktop", false, "build the native window (needs each target's native toolchain)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkVersion(*version); err != nil {
		return err
	}
	b, err := os.ReadFile(*pubFile)
	if err != nil {
		return err
	}
	if _, err := manifest.ParsePublicKey(string(b)); err != nil {
		return err
	}
	return compileTargets(*version, *targets, strings.TrimSpace(string(b)), *src, *outDir, *desktop, out)
}

// sign hashes and signs every binary in releases/<version>.
func sign(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(out)
	version := fs.String("version", "", "version to sign (required)")
	keyFile := fs.String("key", "signing.key", "private key file, used when UPDATER_SIGNING_KEY is not set")
	outDir := fs.String("out", "releases", "releases directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkVersion(*version); err != nil {
		return err
	}
	priv, err := loadPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	return signDir(*version, *outDir, priv, out)
}

func compileTargets(version, targets, pub, src, outDir string, desktop bool, out io.Writer) error {
	dir := filepath.Join(outDir, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, target := range strings.Split(targets, ",") {
		goos, goarch, ok := strings.Cut(strings.TrimSpace(target), "/")
		if !ok {
			return fmt.Errorf("bad target %q, want os/arch", target)
		}
		path := filepath.Join(dir, binaryName(goos, goarch))
		fmt.Fprintf(out, "building %s\n", path)
		if err := goBuild(goos, goarch, version, pub, src, path, desktop, out); err != nil {
			return err
		}
	}
	return nil
}

// signDir signs every app-<os>-<arch> binary in releases/<version> and
// writes release.json. Other files there (the macOS .zip) are left alone.
func signDir(version, outDir string, priv ed25519.PrivateKey, out io.Writer) error {
	dir := filepath.Join(outDir, version)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	rel := manifest.Release{Version: version}
	for _, e := range entries {
		goos, goarch, ok := parseBinaryName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		sum, size, err := hashFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		m := manifest.Manifest{Version: version, OS: goos, Arch: goarch, URL: e.Name(), SHA256: sum, Size: size}
		if err := manifest.Sign(priv, &m); err != nil {
			return err
		}
		rel.Targets = append(rel.Targets, m)
	}
	if len(rel.Targets) == 0 {
		return fmt.Errorf("no app-<os>-<arch> binaries in %s", dir)
	}
	b, err := json.MarshalIndent(rel, "", "  ")
	if err != nil {
		return err
	}
	relPath := filepath.Join(dir, "release.json")
	if err := atomicfile.Write(relPath, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (%d targets)\n", relPath, len(rel.Targets))
	return nil
}

func binaryName(goos, goarch string) string {
	name := "app-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// parseBinaryName is the inverse of binaryName.
func parseBinaryName(name string) (goos, goarch string, ok bool) {
	parts := strings.Split(strings.TrimSuffix(name, ".exe"), "-")
	if len(parts) != 3 || parts[0] != "app" || (parts[1] == "windows") != strings.HasSuffix(name, ".exe") {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// goBuild runs the Go toolchain for one platform. The version and public
// key are set through the linker so the same source produces every build.
// A desktop build adds the native window, which needs cgo and the OS's
// web engine on macOS and Linux; on Windows it is pure Go.
func goBuild(goos, goarch, version, pubkey, src, outPath string, desktop bool, out io.Writer) error {
	ldflags := "-s -w -X main.version=" + version + " -X main.publicKey=" + pubkey
	env := append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	args := []string{"build", "-trimpath"}
	if desktop {
		tags := "desktop,production"
		switch goos {
		case "darwin":
			if runtime.GOOS != "darwin" {
				return errors.New("a macOS desktop build must run on macOS")
			}
			arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[goarch]
			env = append(env, "CGO_ENABLED=1", "CC=clang -arch "+arch, "MACOSX_DEPLOYMENT_TARGET=13.0",
				"CGO_LDFLAGS=-framework UniformTypeIdentifiers")
		case "linux":
			if runtime.GOOS != "linux" || runtime.GOARCH != goarch {
				return errors.New("a Linux desktop build must run on Linux of the same architecture")
			}
			env = append(env, "CGO_ENABLED=1")
			tags += ",webkit2_41"
		case "windows":
			ldflags += " -H windowsgui" // no console window
		}
		args = append(args, "-tags", tags)
	}
	args = append(args, "-ldflags", ldflags, "-o", outPath, src)
	cmd := exec.Command("go", args...)
	cmd.Env = env
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

// checkVersion requires a full MAJOR.MINOR.PATCH version, optionally with a
// prerelease tag, written without a leading v. Semver's own rules keep path
// characters out, so the version is safe to use as a directory name.
func checkVersion(v string) error {
	if !semver.IsValid("v"+v) || semver.Canonical("v"+v) != "v"+v {
		return fmt.Errorf("version %q must look like 1.2.3 or 1.2.3-rc1", v)
	}
	return nil
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
