//go:build windows

package install

import (
	"os"
	"os/exec"
)

// preserveCurrent renames the running program to oldPath. Windows locks a
// running executable against deletion and overwriting, but allows it to
// be renamed, which is what makes in-place updates possible at all.
func preserveCurrent(exe, oldPath string) error {
	return os.Rename(exe, oldPath)
}

// Restart starts the program now at exe as a new process with the same
// arguments, then exits this one. Windows has no exec that replaces the
// current process image.
func Restart(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
