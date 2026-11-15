// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// FuzzPointsUnmarshal checks Points.UnmarshalJSON against encoding/json:
// for any object it accepts, the values are those a json.Decoder with
// UseNumber gives, in the order of the text.
func FuzzPointsUnmarshal(f *testing.F) {
	for _, seed := range []string{
		`{}`, `null`, `{"a": 1}`, `{"b": [1, "x", null, true, false, -0.5e3], "a": {"k": [[]], "k": {}}}`,
		`{"A\\": "😀", "x": " \t[ ] "}`, "{\"s\": \"\xff\"}", `[1]`, `{"a": }`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var got Points
		err := got.UnmarshalJSON(data)
		if !json.Valid(data) {
			if err == nil {
				t.Fatalf("invalid text accepted: %q", data)
			}
			return
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var want any
		if dec.Decode(&want) != nil {
			t.Fatal("valid text does not decode")
		}
		obj, isObject := want.(map[string]any)
		if !isObject {
			if want == nil && (err != nil || got != nil) {
				t.Fatalf("null: %v, %v", got, err)
			}
			if want != nil && err == nil {
				t.Fatalf("%T accepted as points", want)
			}
			return
		}
		if err != nil {
			// Refused: a name given twice, or a list or object too large.
			return
		}
		if len(got) != len(obj) {
			t.Fatalf("%d points, encoding/json has %d", len(got), len(obj))
		}
		for _, p := range got {
			if v, ok := obj[p.Name]; !ok || !reflect.DeepEqual(v, p.Value) {
				t.Fatalf("point %q = %#v, encoding/json has %#v", p.Name, p.Value, v)
			}
		}
	})
}
