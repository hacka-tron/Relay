// Package atomicfile writes a file so that readers see either the old
// content or the new content, never a partial write.
package atomicfile

import (
	"crypto/rand"
	"os"
)

// Write writes data to a temp file beside path and renames it over path.
// Rename is atomic on every supported OS, so a crash midway leaves the
// previous file intact. The temp file is created with the final mode
// rather than changed afterwards, because some mounted filesystems (the
// Cloud Storage volume used in deployment, for one) refuse chmod.
func Write(path string, data []byte, perm os.FileMode) error {
	name := path + ".tmp-" + rand.Text()
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
