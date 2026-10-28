package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hacka-tron/relay/internal/install"
	"github.com/hacka-tron/relay/internal/manifest"
)

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
		return fmt.Errorf("updater: version %s was rolled back before, skipping", m.Version)
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
		c.notify("rejected", "refused v"+m.Version+": "+err.Error())
		return err
	}
	c.Log.Info("downloaded and verified", "new_version", m.Version)
	c.notify("downloaded", "downloaded v"+m.Version+", size and sha256 match")
	if err := install.SelfTest(path, m.Version); err != nil {
		os.Remove(path)
		c.notify("rejected", "refused v"+m.Version+": self-test failed")
		return err
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

// restart is install.Restart unless a test replaced it.
func (c *Client) restart(exe string) error {
	if c.Restart != nil {
		return c.Restart(exe)
	}
	return install.Restart(exe)
}
