// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-sunspec"
)

// FuzzParse parses arbitrary text as a device file, which is what a file
// handed to a program is, and builds a server from what parses.
//
// Parse must not panic, must report failures as ErrSyntax, and what it accepts
// must survive being written and parsed again. Server must not panic either
// and must report a file it cannot use with the errors File.Server documents.
func FuzzParse(f *testing.F) {
	seeds, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range seeds {
		data, err := os.ReadFile(seed)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"unit_id":0,"base_address":40000,"models":[]}`))
	f.Add([]byte(`{"models":[{"id":1,"registers":[1,2]},{"id":1,"points":{"Mn":"x","Mn":"y"}}]}`))
	f.Add([]byte(`not json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(data)
		if err != nil {
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("error does not match ErrSyntax: %v", err)
			}
			if file != nil {
				t.Fatal("a file together with an error")
			}
			return
		}

		first, err := file.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		again, err := Parse(first)
		if err != nil {
			t.Fatalf("what Marshal wrote does not parse: %v\n%s", err, first)
		}
		second, err := again.Marshal()
		if err != nil {
			t.Fatalf("Marshal of the parsed file: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("written twice, the file differs:\n%s\n%s", first, second)
		}

		if _, err := file.Server(nil); err != nil &&
			!errors.Is(err, ErrInvalid) && !errors.Is(err, sunspec.ErrEncode) && !errors.Is(err, sunspec.ErrModelLayout) {
			t.Fatalf("Server: error of no documented kind: %v", err)
		}
	})
}
