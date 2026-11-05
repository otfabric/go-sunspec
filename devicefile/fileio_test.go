// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Regression: Load read any path whole, so /dev/zero or a huge file
// exhausted memory, and /dev/null read as an empty document.
func TestLoadRefusesNonRegularFiles(t *testing.T) {
	paths := []string{t.TempDir(), os.DevNull}
	if _, err := os.Stat("/dev/zero"); err == nil {
		paths = append(paths, "/dev/zero")
	}
	for _, p := range paths {
		if f, err := Load(p); !errors.Is(err, ErrNotRegular) || f != nil {
			t.Errorf("Load(%s) = %v, %v; want ErrNotRegular", p, f, err)
		}
	}
	// A symbolic link to a device file is read.
	dir := t.TempDir()
	target := filepath.Join(dir, "device.json")
	if err := os.WriteFile(target, []byte(exampleDevice), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Logf("no symbolic links here: %v", err)
	} else if _, err := Load(link); err != nil {
		t.Errorf("Load through a link: %v", err)
	}
}

func TestLoadRefusesLargeFiles(t *testing.T) {
	dir := t.TempDir()
	sized := func(name string, size int64) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		// Sparse: no disk space is used.
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := Load(sized("big.json", MaxFileSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a file of MaxFileSize+1 bytes: %v", err)
	}
	// The limit itself is read, and then not a device file.
	if _, err := Load(sized("limit.json", MaxFileSize)); !errors.Is(err, ErrSyntax) {
		t.Errorf("a file of MaxFileSize bytes: %v", err)
	}
}

func writeTestFile(t *testing.T) (*File, []byte) {
	t.Helper()
	file, err := Parse([]byte(exampleDevice))
	if err != nil {
		t.Fatal(err)
	}
	want, err := file.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return file, want
}

// Regression: WriteFile followed a symbolic link at the destination, also a
// dangling one, and wrote wherever it pointed.
func TestWriteFileRefusesLinksAndNonRegular(t *testing.T) {
	file, _ := writeTestFile(t)
	dir := t.TempDir()
	if err := file.WriteFile(dir); !errors.Is(err, ErrNotRegular) {
		t.Errorf("WriteFile to a directory: %v", err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link.json": victim, "dangling.json": filepath.Join(dir, "nowhere")} {
		link := filepath.Join(dir, name)
		if err := os.Symlink(target, link); err != nil {
			t.Logf("no symbolic links here: %v", err)
			continue
		}
		if err := file.WriteFile(link); !errors.Is(err, ErrNotRegular) {
			t.Errorf("WriteFile to %s: %v", name, err)
		}
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Errorf("the link target was written: %q", data)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nowhere")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dangling link target was created: %v", err)
	}
	assertNoTemp(t, dir, 3)
}

// Regression: WriteFile truncated the file before writing it, so a failure
// lost the old content. It now replaces the file in one rename, with mode
// 0600 whatever the old file had.
func TestWriteFileIsAtomic(t *testing.T) {
	file, want := writeTestFile(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "device.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	fail := errors.New("rename failed")
	renameFile = func(string, string) error { return fail }
	err := file.WriteFile(path)
	renameFile = os.Rename
	if !errors.Is(err, fail) {
		t.Errorf("WriteFile with a failing rename: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Errorf("a failed WriteFile changed the file to %q", data)
	}
	assertNoTemp(t, dir, 1)

	if err := file.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, want) {
		t.Errorf("WriteFile wrote %q, %v", data, err)
	}
	if fi, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after WriteFile: %v, %v", fi.Mode(), err)
	}
	assertNoTemp(t, dir, 1)
}

// assertNoTemp fails when dir holds other than n entries: a temporary file
// was left behind.
func assertNoTemp(t *testing.T, dir string, n int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%s holds %v", dir, names)
	}
}

func TestReadLimited(t *testing.T) {
	if _, err := readLimited(failingReader{}, "x"); err == nil || err.Error() != "disk gone" {
		t.Errorf("a failing read: %v", err)
	}
	// A file that grows past the limit while it is read.
	if _, err := readLimited(io.LimitReader(zeroReader{}, MaxFileSize+2), "x"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a growing file: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk gone") }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
