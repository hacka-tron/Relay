package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hacka-tron/relay/internal/install"
	"github.com/hacka-tron/relay/internal/manifest"
)

// MaxAttempts is how many times a signed release that fails its own checks
// (size, hash or self-test) is tried, the first try and two retries,
// before it is skipped for good. A newer version still installs.
const MaxAttempts = 3

// ErrSkipped means the version was given up on earlier, either after
// MaxAttempts failed attempts or after it was rolled back for crashing.
var ErrSkipped = errors.New("updater: version skipped")

// Apply installs a manifest that Check already verified: download next to
// the executable, self-test the new binary, swap it in, record that the
// swap is pending confirmation, and restart into it. On success it does
// not return. Every failure before the swap leaves the current program
// untouched; a failure after the swap is handled by install.Startup on the
// next run.
func (c *Client) Apply(ctx context.Context, m manifest.Manifest, exe string) error {
	st, err := install.LoadState(exe)
	if err != nil {
		return err
	}
	if st.Skips(m.Version) {
		return ErrSkipped
	}
	if st.Pending != nil {
		return fmt.Errorf("updater: version %s is installed but not yet confirmed healthy, waiting", st.Pending.Version)
	}
	unlock, err := install.Lock(exe)
	if err != nil {
		return err
	}
	defer unlock()

	c.notify("available", "update available: v"+m.Version)
	path, err := c.Download(ctx, m, filepath.Dir(exe))
	if err != nil {
		return c.failed(exe, m.Version, err)
	}
	c.Log.Info("downloaded and verified", "new_version", m.Version)
	c.notify("downloaded", "downloaded v"+m.Version+", size and sha256 match")
	if err := install.SelfTest(path, m.Version); err != nil {
		os.Remove(path)
		return c.failed(exe, m.Version, badRelease(err))
	}
	// Mark the update pending before the swap, not after: if the process
	// dies between the two steps the new binary must still be watched for
	// crash loops. A failed swap clears the mark again.
	st.Pending = &install.Pending{Version: m.Version}
	if err := st.Save(exe); err != nil {
		os.Remove(path)
		return err
	}
	if err := install.Swap(exe, path); err != nil {
		os.Remove(path)
		st.Pending = nil
		if saveErr := st.Save(exe); saveErr != nil {
			c.Log.Error("clear pending after failed swap", "err", saveErr)
		}
		return err
	}
	c.Log.Info("installed, restarting", "new_version", m.Version)
	c.notify("installed", "installed v"+m.Version+", restarting")
	// Restart replaces this process, so deferred calls never run; release
	// the lock by hand first or the new version would find it held.
	unlock()
	return c.restart(exe)
}

// failed counts a failed attempt at version. When the release itself was
// at fault MaxAttempts times, the version is added to the skip list, so it
// is never downloaded again, and the history says so.
func (c *Client) failed(exe, version string, err error) error {
	var bad *badReleaseError
	if !errors.As(err, &bad) {
		return err // the network, not the release: just retry next time
	}
	if c.attempts == nil {
		c.attempts = map[string]int{}
	}
	c.attempts[version]++
	c.notify("rejected", "refused v"+version+": "+err.Error())
	if c.attempts[version] < MaxAttempts {
		return err
	}
	st, loadErr := install.LoadState(exe)
	if loadErr != nil {
		return loadErr
	}
	st.Skip = append(st.Skip, version)
	if saveErr := st.Save(exe); saveErr != nil {
		return saveErr
	}
	c.notify("gaveup", fmt.Sprintf("gave up on v%s after %d failed attempts", version, MaxAttempts))
	return fmt.Errorf("updater: gave up on version %s: %w", version, err)
}

// Recorded reports whether err was already reported through Notify, so a
// caller does not record it twice.
func (c *Client) Recorded(err error) bool {
	var bad *badReleaseError
	return errors.As(err, &bad)
}

// restart is install.Restart unless a test replaced it.
func (c *Client) restart(exe string) error {
	if c.Restart != nil {
		return c.Restart(exe)
	}
	return install.Restart(exe)
}
