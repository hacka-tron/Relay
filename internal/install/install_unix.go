//go:build !windows

package install

import (
	"io"
	"os"
	"syscall"
)

// preserveCurrent copies the running program to oldPath. A copy rather
// than a rename means the program's path never stops existing, so there
// is no instant where a user or a supervisor could find it missing. The
// rename in Swap then replaces it atomically.
func preserveCurrent(exe, oldPath string) error {
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(oldPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// Restart replaces the current process image with the program now at exe,
// keeping the same PID, arguments and environment. It only returns on
// failure.
func Restart(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
