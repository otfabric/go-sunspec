// SPDX-License-Identifier: MIT

package devicefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// renameFile is os.Rename; tests replace it to make the last step of
// writeFile fail.
var renameFile = os.Rename

// readFile reads at most MaxFileSize bytes of the regular file at path.
func readFile(path string) ([]byte, error) {
	// Checked before opening, so that a named pipe is never opened: that
	// would block until something writes to it.
	if err := checkRegular(path, os.Stat); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readLimited(f, path)
}

// readLimited reads r to its end, or fails with ErrTooLarge once it has
// given more than MaxFileSize bytes. What was opened may not be what was
// checked, and a file can grow while it is read: it never reads more than the
// limit and one byte.
func readLimited(r io.Reader, path string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("%s: more than %d bytes: %w", path, MaxFileSize, ErrTooLarge)
	}
	return data, nil
}

// checkRegular returns an error matching ErrNotRegular when path exists and,
// as stat reports it, is not a regular file. A path that does not exist
// passes; the error of the open or rename that follows reports it.
func checkRegular(path string, stat func(string) (os.FileInfo, error)) error {
	if fi, err := stat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: %s: %w", path, fi.Mode().Type(), ErrNotRegular)
	}
	return nil
}

// writeFile replaces path with data atomically: a temporary file of mode 0600
// in the same directory, synced, then renamed over path.
func writeFile(path string, data []byte) (err error) {
	if err := checkRegular(path, os.Lstat); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameFile(tmp.Name(), path)
	}
	return err
}
