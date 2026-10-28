package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The test binary doubles as the "new program" for SelfTest: when this
// variable is set it prints the version and exits before any tests run.
func TestMain(m *testing.M) {
	if v := os.Getenv("INSTALL_TEST_VERSION"); v != "" {
		fmt.Println(v)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeExe writes a pretend program and returns its path.
func fakeExe(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSwapReplacesAndKeepsOld(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "app", "v1")
	nb := fakeExe(t, dir, "update-1.tmp", "v2")
	os.Chmod(nb, 0o644) // downloads are not executable; Swap must fix that

	if err := Swap(exe, nb); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "v2" || read(t, oldPath(exe)) != "v1" {
		t.Fatal("swap did not put v2 in place and v1 aside")
	}
	if _, err := os.Stat(nb); !os.IsNotExist(err) {
		t.Fatal("temp download still exists after swap")
	}
	if info, _ := os.Stat(exe); runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatal("new program is not executable")
	}
}

func TestRollbackRestoresOld(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "app", "v1")
	if err := Swap(exe, fakeExe(t, dir, "new", "v2")); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(exe); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "v1" || read(t, badPath(exe)) != "v2" {
		t.Fatal("rollback did not restore v1 and set v2 aside")
	}
	if err := Rollback(exe); err == nil {
		t.Fatal("second rollback with no .old should fail")
	}
}

// Simulate the program starting repeatedly after an update and never
// confirming: the third start must roll back and skip the version.
func TestCrashLoopRollsBackAndSkips(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "app", "v1")
	if err := Swap(exe, fakeExe(t, dir, "new", "v2")); err != nil {
		t.Fatal(err)
	}
	if err := (State{Pending: &Pending{Version: "2.0.0"}}).Save(exe); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= MaxStartAttempts; i++ {
		rolled, err := Startup(exe)
		if err != nil || rolled {
			t.Fatalf("start %d: rolled=%v err=%v", i, rolled, err)
		}
		if read(t, exe) != "v2" {
			t.Fatalf("start %d rolled back early", i)
		}
	}
	rolled, err := Startup(exe)
	if err != nil || !rolled {
		t.Fatalf("final start: rolled=%v err=%v, want rollback", rolled, err)
	}
	if read(t, exe) != "v1" {
		t.Fatal("rollback did not restore v1")
	}
	st, _ := LoadState(exe)
	if st.Pending != nil || !st.Skips("2.0.0") {
		t.Fatalf("state after rollback: %+v", st)
	}
	// The next normal start clears the .bad leftover.
	if _, err := Startup(exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(badPath(exe)); !os.IsNotExist(err) {
		t.Fatal(".bad not cleaned on next start")
	}
}

func TestConfirmClearsMarkerAndOld(t *testing.T) {
	dir := t.TempDir()
	exe := fakeExe(t, dir, "app", "v1")
	if err := Swap(exe, fakeExe(t, dir, "new", "v2")); err != nil {
		t.Fatal(err)
	}
	(State{Pending: &Pending{Version: "2.0.0"}}).Save(exe)
	if _, err := Startup(exe); err != nil {
		t.Fatal(err)
	}
	if err := Confirm(exe); err != nil {
		t.Fatal(err)
	}
	st, _ := LoadState(exe)
	if st.Pending != nil {
		t.Fatal("pending not cleared")
	}
	if _, err := os.Stat(oldPath(exe)); !os.IsNotExist(err) {
		t.Fatal(".old not removed")
	}
	// A later start is a normal one: no rollback, no error.
	if rolled, err := Startup(exe); rolled || err != nil {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
	if read(t, exe) != "v2" {
		t.Fatal("confirmed version was replaced")
	}
}

func TestStartupWithoutStateIsNoop(t *testing.T) {
	exe := fakeExe(t, t.TempDir(), "app", "v1")
	if rolled, err := Startup(exe); rolled || err != nil {
		t.Fatalf("rolled=%v err=%v", rolled, err)
	}
}

func TestSelfTest(t *testing.T) {
	// Copy the test binary to a non executable file, as a download would be.
	self, _ := os.Executable()
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "update-1.tmp")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INSTALL_TEST_VERSION", "1.2.0")
	if err := SelfTest(path, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	self = path
	if err := SelfTest(self, "9.9.9"); err == nil {
		t.Fatal("wrong version passed self-test")
	}
	if err := SelfTest(filepath.Join(t.TempDir(), "missing"), "1.2.0"); err == nil {
		t.Fatal("missing binary passed self-test")
	}
}

func TestLock(t *testing.T) {
	exe := fakeExe(t, t.TempDir(), "app", "v1")
	unlock, err := Lock(exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(exe); err == nil {
		t.Fatal("second lock succeeded while first held")
	}
	unlock()
	unlock, err = Lock(exe)
	if err != nil {
		t.Fatal("lock not released:", err)
	}
	unlock()

	// A stale lock from a crashed run is taken over.
	os.WriteFile(lockPath(exe), nil, 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(lockPath(exe), old, old)
	if _, err := Lock(exe); err != nil {
		t.Fatal("stale lock not taken over:", err)
	}
}
