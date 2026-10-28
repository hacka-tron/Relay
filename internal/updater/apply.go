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
	unlock, err := install.Lock(exe)
	if err != nil {
		return err
	}
	defer unlock()

	path, err := c.Download(ctx, m, filepath.Dir(exe))
	if err != nil {
		return err
	}
	c.Log.Info("downloaded and verified", "new_version", m.Version)
	if err := install.SelfTest(path, m.Version); err != nil {
		os.Remove(path)
		return err
	}
	if err := install.Swap(exe, path); err != nil {
		os.Remove(path)
		return err
	}
	st.Pending = &install.Pending{Version: m.Version}
	if err := st.Save(exe); err != nil {
		return err
	}
	c.Log.Info("installed, restarting", "new_version", m.Version)
	// Restart replaces this process, so deferred calls never run; release
	// the lock by hand first or the new version would find it held.
	unlock()
	return install.Restart(exe)
}
