// Package updater is the client side of the update system: ask the server
// whether there is a newer version, download it within limits, and verify
// it. Nothing here touches the running executable; see package install.
package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"golang.org/x/mod/semver"

	"github.com/hacka-tron/relay/internal/manifest"
)

// DefaultMaxSize caps a download at 200 MB. A manifest claiming more than
// this is refused before any bytes are fetched.
const DefaultMaxSize = 200 << 20

// TempPattern is the name pattern of in-progress downloads, so startup
// cleanup can find leftovers from an interrupted run.
const TempPattern = "update-*.tmp"

// Client holds the facts the updater sends to the server and the key it
// trusts. Fill it once at startup.
type Client struct {
	ServerURL string // base URL of the update server, no trailing slash
	Channel   string
	ClientID  string
	Version   string // the version this process is running
	OS        string // runtime.GOOS; a field so tests can pretend
	Arch      string // runtime.GOARCH
	PublicKey ed25519.PublicKey
	HTTP      *http.Client // set a Timeout
	MaxSize   int64        // zero means DefaultMaxSize
	Log       *slog.Logger
	Restart   func(exe string) error // nil means install.Restart; tests override it
	// Notify, when set, is called at each step of an update so a UI can
	// show progress. It must return quickly.
	Notify func(step, detail string)
}

// notify calls Notify if one is set.
func (c *Client) notify(step, detail string) {
	if c.Notify != nil {
		c.Notify(step, detail)
	}
}

// Check asks the server for an update. It returns the verified manifest and
// true when there is one, false when the server says there is nothing, and
// an error for anything else. An update that fails verification is an
// error, not "nothing", so it gets logged.
func (c *Client) Check(ctx context.Context) (manifest.Manifest, bool, error) {
	var none manifest.Manifest
	q := url.Values{
		"client_id": {c.ClientID},
		"version":   {c.Version},
		"os":        {c.OS},
		"arch":      {c.Arch},
		"channel":   {c.Channel},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ServerURL+"/v1/update?"+q.Encode(), nil)
	if err != nil {
		return none, false, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return none, false, fmt.Errorf("updater: check: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return none, false, nil
	case http.StatusOK:
	default:
		return none, false, fmt.Errorf("updater: check: server returned %s", resp.Status)
	}
	var m manifest.Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return none, false, fmt.Errorf("updater: check: bad manifest: %w", err)
	}
	if err := c.verify(m); err != nil {
		return none, false, err
	}
	return m, true, nil
}

// verify is every check that can run before downloading, in order. The
// signature comes first because nothing else in the manifest can be
// trusted until it passes.
func (c *Client) verify(m manifest.Manifest) error {
	if err := manifest.Verify(c.PublicKey, m); err != nil {
		return err
	}
	if m.OS != c.OS || m.Arch != c.Arch {
		return fmt.Errorf("updater: manifest is for %s/%s, this machine is %s/%s", m.OS, m.Arch, c.OS, c.Arch)
	}
	if semver.Compare("v"+m.Version, "v"+c.Version) <= 0 {
		return fmt.Errorf("updater: refusing version %s, not newer than running %s", m.Version, c.Version)
	}
	u, err := url.Parse(m.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("updater: download url must be http or https: %q", m.URL)
	}
	if m.Size <= 0 || m.Size > c.maxSize() {
		return fmt.Errorf("updater: refusing download of %d bytes (limit %d)", m.Size, c.maxSize())
	}
	return nil
}

// Download fetches the binary described by m into a new file in dir and
// returns its path. dir should be the directory of the running executable
// so the later rename is on the same filesystem. The bytes are hashed as
// they are written; a size or hash mismatch deletes the file and returns
// an error, so a bad download never reaches disk under a usable name.
func (c *Client) Download(ctx context.Context, m manifest.Manifest, dir string) (string, error) {
	if err := c.verify(m); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("updater: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("updater: download: server returned %s", resp.Status)
	}

	f, err := os.CreateTemp(dir, TempPattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	hash := sha256.New()
	// Read at most Size+1 bytes: one extra so a too-large body is detected
	// rather than silently truncated to the right size.
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, m.Size+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n != m.Size {
		err = fmt.Errorf("updater: download: got %d bytes, manifest says %d", n, m.Size)
	}
	if err == nil && hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		err = errors.New("updater: download: sha256 does not match manifest")
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// CleanupTemp removes leftover partial downloads in dir from a previous run
// that was interrupted.
func CleanupTemp(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, TempPattern))
	for _, p := range matches {
		os.Remove(p)
	}
}

func (c *Client) maxSize() int64 {
	if c.MaxSize > 0 {
		return c.MaxSize
	}
	return DefaultMaxSize
}
