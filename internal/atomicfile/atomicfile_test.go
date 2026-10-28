package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func isWindows() bool { return runtime.GOOS == "windows" }

func TestWriteReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestWriteUsesRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	if err := Write(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 && !isWindows() {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}
