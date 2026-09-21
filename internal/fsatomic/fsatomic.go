// Package fsatomic writes files so readers never observe partial content: the
// payload lands in a same-directory temporary file, is flushed to disk, and is
// renamed over the target, which is atomic on supported filesystems.
package fsatomic

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path with the given permissions via a
// temporary file in the same directory, fsync, rename, and a final directory
// fsync. A failed write removes the temporary file and leaves any existing
// target untouched.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".fsatomic-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
