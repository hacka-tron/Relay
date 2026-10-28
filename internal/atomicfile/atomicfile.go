// Package atomicfile writes a file so that readers see either the old
// content or the new content, never a partial write.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write writes data to a temp file in the same directory and renames it
// over path. Rename is atomic on every supported OS, so a crash midway
// leaves the previous file intact.
func Write(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(name, perm)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
