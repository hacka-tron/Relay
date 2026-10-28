// Command release is the developer and CI tool for shipping a version.
//
//	release keygen [--out DIR] [--force]   write signing.key and pubkey.txt
//
// Later phases add "release build", which cross compiles, hashes, signs and
// writes releases/<version>/release.json.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hacka-tron/relay/internal/manifest"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// run dispatches to a subcommand. It takes args and an output writer so
// tests can call it without touching the real process.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: release <keygen> [flags]")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:], out)
	default:
		return fmt.Errorf("unknown command %q (want keygen)", args[0])
	}
}

// keygen writes a new key pair. It refuses to overwrite an existing
// signing.key unless --force is given, because losing the private key means
// no existing client can ever accept another update.
func keygen(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("out", ".", "directory to write signing.key and pubkey.txt into")
	force := fs.Bool("force", false, "overwrite an existing signing.key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	privPath := filepath.Join(*dir, "signing.key")
	pubPath := filepath.Join(*dir, "pubkey.txt")
	if _, err := os.Stat(privPath); err == nil && !*force {
		return fmt.Errorf("%s already exists; pass --force to replace it", privPath)
	}

	pub, priv, err := manifest.GenerateKey()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	// 0600: the private key is readable by the owner only. WriteFile only
	// applies the mode when it creates the file, so with --force an existing
	// looser mode would survive; Chmod makes it 0600 either way.
	if err := os.WriteFile(privPath, []byte(manifest.EncodeKey(priv)), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(privPath, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, []byte(manifest.EncodeKey(pub)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (private, keep secret)\nwrote %s (public, embed in client)\n", privPath, pubPath)
	return nil
}
