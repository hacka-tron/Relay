package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hacka-tron/relay/internal/install"
	"github.com/hacka-tron/relay/internal/manifest"
)

// The test binary doubles as the downloaded program: with this variable
// set it prints the version and exits, which is what SelfTest expects.
func TestMain(m *testing.M) {
	if v := os.Getenv("UPDATER_TEST_VERSION"); v != "" {
		fmt.Println(v)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// applySetup serves the test binary itself as the "new version" so the
// self-test can really run it, and installs a fake current program.
func applySetup(t *testing.T) (*Client, string, *fakeServer) {
	t.Helper()
	pub, priv, _ := manifest.GenerateKey()
	fs := newFakeServer(t)
	self, _ := os.Executable()
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	fs.body = body
	fs.m = fs.signed(t, priv, "1.1.0", body)
	c := newClient(t, fs.URL, pub)
	dir := t.TempDir()
	exe := filepath.Join(dir, "app")
	if err := os.WriteFile(exe, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	return c, exe, fs
}

func TestApplyInstallsMarksPendingAndRestarts(t *testing.T) {
	c, exe, fs := applySetup(t)
	t.Setenv("UPDATER_TEST_VERSION", "1.1.0")
	restarted := ""
	c.Restart = func(e string) error { restarted = e; return nil }

	if err := c.Apply(context.Background(), fs.m, exe); err != nil {
		t.Fatal(err)
	}
	if restarted != exe {
		t.Fatal("Restart was not called with the executable")
	}
	if b, _ := os.ReadFile(exe); string(b) != string(fs.body) {
		t.Fatal("executable was not replaced")
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old program" {
		t.Fatal(".old does not hold the previous program")
	}
	st, _ := install.LoadState(exe)
	if st.Pending == nil || st.Pending.Version != "1.1.0" {
		t.Fatalf("pending not recorded: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "update.lock")); err == nil {
		t.Fatal("lock left behind after restart")
	}
}

func TestApplyFailedSelfTestLeavesEverythingUntouched(t *testing.T) {
	c, exe, fs := applySetup(t)
	t.Setenv("UPDATER_TEST_VERSION", "9.9.9") // binary reports the wrong version
	c.Restart = func(string) error { t.Fatal("restarted after failed self-test"); return nil }

	if err := c.Apply(context.Background(), fs.m, exe); err == nil {
		t.Fatal("Apply succeeded despite failed self-test")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old program" {
		t.Fatal("executable changed")
	}
	st, _ := install.LoadState(exe)
	if st.Pending != nil {
		t.Fatal("pending set although nothing was installed")
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	for _, e := range entries {
		if e.Name() != "app" {
			t.Fatalf("leftover file %s", e.Name())
		}
	}
}

func TestApplyRefusesSkippedAndUnconfirmedVersions(t *testing.T) {
	c, exe, fs := applySetup(t)
	c.Restart = func(string) error { t.Fatal("restarted"); return nil }

	(install.State{Skip: []string{"1.1.0"}}).Save(exe)
	if err := c.Apply(context.Background(), fs.m, exe); err == nil {
		t.Fatal("installed a version that was rolled back before")
	}
	(install.State{Pending: &install.Pending{Version: "1.0.5"}}).Save(exe)
	if err := c.Apply(context.Background(), fs.m, exe); err == nil {
		t.Fatal("installed while an earlier update was unconfirmed")
	}
}
