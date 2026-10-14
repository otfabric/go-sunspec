// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	// DefaultUnitID is the Modbus unit ID of a device file without "unit_id".
	DefaultUnitID = 1

	// DefaultBaseAddress is where a device file without "base_address" puts
	// the SunSpec marker.
	DefaultBaseAddress = 40000
)

// File is a device file: the JSON document that describes a SunSpec device.
type File struct {
	// UnitID is the Modbus unit ID the device answers; DefaultUnitID when
	// nil.
	UnitID *uint8 `json:"unit_id,omitempty"`
	// BaseAddress is the register address of the "SunS" marker;
	// DefaultBaseAddress when nil.
	BaseAddress *uint16 `json:"base_address,omitempty"`
	// Models lists the device's models in chain order.
	Models []Model `json:"models"`
}

// Model is one model of a device file: given by point values, which are
// encoded with the model's schema, or by its raw data registers.
type Model struct {
	// ID is the SunSpec model ID. It is a pointer so that a model without
	// "id" can be told from model 0; a model without ID is an error when the
	// file is turned into a device.
	ID *uint16 `json:"id"`
	// Name is the model's name. It is written for the reader's benefit and
	// ignored when the file is used.
	Name string `json:"name,omitempty"`
	// Group holds the points and nested groups of the model's top-level
	// group; in JSON its members are members of the model.
	Group
	// Registers holds the data registers that follow the model's ID and
	// length registers. It replaces points and groups, and is the only way
	// to describe a model without a schema.
	Registers []uint16 `json:"registers,omitempty"`
}

// Group holds the values of one group instance.
type Group struct {
	// Points holds the point values by name.
	Points Points `json:"points,omitempty"`
	// Groups maps the name of a nested group to its instances. A nil
	// instance is an empty one.
	Groups map[string][]*Group `json:"groups,omitempty"`
}

// Point is one point of a group in a device file.
type Point struct {
	// Name is the SunSpec point name.
	Name string
	// Value is, after parsing, what encoding/json makes of the JSON value
	// with UseNumber: a json.Number, a string, a []interface{}, nil, a bool
	// or a map. For writing it is any value json.Marshal accepts. Nil stands
	// for a point that is not implemented.
	Value interface{}
}

// Points is the list of point values of a group instance. In JSON it is one
// object, name to value, that keeps the order of its members, so that a
// written file lists the points in schema order; a point given twice is an
// error when it is read.
type Points []Point

// MarshalJSON writes the points as one object, in order.
func (l Points) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range l {
		if i > 0 {
			buf.WriteByte(',')
		}
		// A string always marshals.
		name, _ := json.Marshal(e.Name)
		value, err := json.Marshal(e.Value)
		if err != nil {
			return nil, fmt.Errorf("point %s: %w", e.Name, err)
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON reads an object of point values, keeping its order.
func (l *Points) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		*l = nil
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New(`"points" must be an object of point names and values`)
	}
	var out Points
	seen := make(map[string]bool)
	for dec.More() {
		// An object's keys are strings: the decoder guarantees it.
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name := tok.(string)
		if seen[name] {
			return fmt.Errorf("point %s is given twice", name)
		}
		seen[name] = true
		var value interface{}
		if err := dec.Decode(&value); err != nil {
			return err
		}
		out = append(out, Point{Name: name, Value: value})
	}
	*l = out
	return nil
}

// New returns a device file for the given unit ID and base address, without
// models. Written as it is, it has an empty "models" list.
func New(unitID uint8, baseAddress uint16) *File {
	return &File{UnitID: &unitID, BaseAddress: &baseAddress, Models: []Model{}}
}

// Load reads and parses the device file at path. A file that cannot be read
// gives the error of the operating system; a file that cannot be parsed the
// error of Parse, prefixed with the path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

// Parse parses a device file. Unknown members are errors: a misspelt
// "points" must not silently describe an empty model. So is a point given
// twice, and anything after the document.
//
// Every error is a *SyntaxError, with the line and column for malformed JSON
// and for a member of the wrong type. Parse checks the form of the file
// only: whether the models, points and values exist and fit is checked by
// ServerModels and Server.
func Parse(data []byte) (*File, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file File
	if err := dec.Decode(&file); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxErr):
			// The offset counts the bytes read, the offending one included.
			line, col := position(data, syntaxErr.Offset-1)
			return nil, &SyntaxError{Line: line, Column: col, Err: err}
		case errors.As(err, &typeErr):
			line, col := position(data, typeErr.Offset)
			return nil, &SyntaxError{Line: line, Column: col, Err: err}
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return nil, &SyntaxError{Err: fmt.Errorf("the file ends before the JSON document does: %w", err)}
		}
		return nil, &SyntaxError{Err: err}
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, &SyntaxError{Err: errors.New("data after the end of the JSON document")}
	}
	return &file, nil
}

// position converts a byte offset in data to a line and a column, both
// counting from 1.
func position(data []byte, offset int64) (line, col int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	if offset < 0 {
		offset = 0
	}
	line, col = 1, 1
	for _, b := range data[:offset] {
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// Marshal returns the file as JSON indented by two spaces and ended by a
// newline: the form in which device files are written. Points come in the
// order of their list, the groups of an instance sorted by name.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write writes the file to w in the form Marshal returns. Nothing is written
// when the file cannot be marshalled.
func (f *File) Write(w io.Writer) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// WriteFile writes the file to path in the form Marshal returns, creating
// the file with mode 0644 or replacing its content.
func (f *File) WriteFile(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
