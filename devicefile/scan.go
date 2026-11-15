// SPDX-License-Identifier: MIT

package devicefile

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// maxListLen is the most elements a list in a point value may hold: no point
// holds more than 65535 registers, so a longer list never fits one. Longer
// lists are refused before their elements are decoded.
const maxListLen = 0xFFFF

// scanner walks JSON text that encoding/json has already found valid, so it
// does not check the syntax again. It decodes point values the way a
// json.Decoder with UseNumber does, without a decoder (and its buffer) per
// "points" object and without growing slices element by element.
type scanner struct {
	data []byte
	pos  int
}

// next skips white space and returns the byte that follows it.
func (s *scanner) next() byte {
	for s.pos < len(s.data) {
		switch c := s.data[s.pos]; c {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return c
		}
	}
	return 0
}

// expect skips white space and the byte that follows it, which the valid
// text guarantees to be c.
func (s *scanner) expect(c byte) {
	if s.next() == c {
		s.pos++
	}
}

// stringEnd returns the index after the closing quote of the string at pos.
func (s *scanner) stringEnd() int {
	i := s.pos + 1
	for {
		switch s.data[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
}

// str reads the string at pos (after white space), as encoding/json reads
// it: escapes resolved, invalid UTF-8 replaced by U+FFFD.
func (s *scanner) str() string {
	s.next()
	start, end := s.pos, s.stringEnd()
	s.pos = end
	raw := s.data[start:end]
	plain := true
	for _, c := range raw[1 : len(raw)-1] {
		if c == '\\' || c >= utf8.RuneSelf {
			plain = false
			break
		}
	}
	if plain {
		return string(raw[1 : len(raw)-1])
	}
	var out string
	// A valid JSON string always unmarshals.
	_ = json.Unmarshal(raw, &out)
	return out
}

// skip moves past the value at pos (after white space).
func (s *scanner) skip() {
	depth := 0
	for {
		switch c := s.next(); c {
		case '"':
			s.pos = s.stringEnd()
		case '[', '{':
			depth++
			s.pos++
		case ']', '}':
			depth--
			s.pos++
		case ',', ':':
			s.pos++
			continue
		default:
			for s.pos < len(s.data) && !isDelim(s.data[s.pos]) {
				s.pos++
			}
		}
		if depth == 0 {
			return
		}
	}
}

// isDelim reports whether c ends a number or a literal.
func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ']', '}', ':':
		return true
	}
	return false
}

// count returns the number of elements of the list or members of the object
// at pos (after white space), leaving pos where it was.
func (s *scanner) count() int {
	saved := s.pos
	s.pos++ // '[' or '{'
	n := 0
	if c := s.next(); c != ']' && c != '}' {
		for {
			n++
			if s.next() == '"' && s.data[saved] == '{' {
				// An object member: the key, then the value.
				s.skip()
				s.expect(':')
			}
			s.skip()
			if s.next() != ',' {
				break
			}
			s.pos++
		}
	}
	s.pos = saved
	return n
}

// value decodes the value at pos (after white space) as a json.Decoder with
// UseNumber decodes it into an interface value.
func (s *scanner) value() (any, error) {
	switch c := s.next(); c {
	case '"':
		return s.str(), nil
	case 'n':
		s.pos += len("null")
		return nil, nil
	case 't':
		s.pos += len("true")
		return true, nil
	case 'f':
		s.pos += len("false")
		return false, nil
	case '[':
		n := s.count()
		if n > maxListLen {
			return nil, fmt.Errorf("a list of %d values: no point holds more than %d registers", n, maxListLen)
		}
		list := make([]any, 0, n)
		s.pos++
		for range n {
			v, err := s.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
			s.next()
			s.pos++ // ',' or ']'
		}
		if n == 0 {
			s.expect(']')
		}
		return list, nil
	case '{':
		n := s.count()
		if n > maxListLen {
			return nil, fmt.Errorf("an object of %d members: no point holds more than %d registers", n, maxListLen)
		}
		obj := make(map[string]any, n)
		s.pos++
		for range n {
			key := s.str()
			s.expect(':')
			v, err := s.value()
			if err != nil {
				return nil, err
			}
			obj[key] = v
			s.next()
			s.pos++ // ',' or '}'
		}
		if n == 0 {
			s.expect('}')
		}
		return obj, nil
	default:
		start := s.pos
		for s.pos < len(s.data) && !isDelim(s.data[s.pos]) {
			s.pos++
		}
		return json.Number(s.data[start:s.pos]), nil
	}
}

// UnmarshalJSON reads an object of point values, keeping its order. A list
// longer than 65535 values, which no point holds, is refused.
func (l *Points) UnmarshalJSON(data []byte) error {
	// encoding/json passes valid text; a direct call may not.
	if !json.Valid(data) {
		// Text that is not valid always gives an error.
		return json.Unmarshal(data, new(any))
	}
	s := &scanner{data: data}
	switch s.next() {
	case 'n':
		*l = nil
		return nil
	case '{':
	default:
		return errors.New(`"points" must be an object of point names and values`)
	}
	n := s.count()
	if n == 0 {
		*l = nil
		return nil
	}
	out := make(Points, 0, n)
	seen := make(map[string]struct{}, n)
	s.pos++
	for range n {
		name := s.str()
		if _, ok := seen[name]; ok {
			return fmt.Errorf("point %s is given twice", name)
		}
		seen[name] = struct{}{}
		s.expect(':')
		value, err := s.value()
		if err != nil {
			return fmt.Errorf("point %s: %w", name, err)
		}
		out = append(out, Point{Name: name, Value: value})
		s.next()
		s.pos++ // ',' or '}'
	}
	*l = out
	return nil
}

// Members of the objects of a device file, by kind of object.
var (
	fileMembers  = []string{"unit_id", "base_address", "models"}
	modelMembers = []string{"id", "name", "points", "groups", "registers"}
	groupMembers = []string{"points", "groups"}
)

// checkMembers checks what encoding/json lets through in data, a device file
// that decoded: a member given twice (encoding/json keeps the last), a
// member whose name differs from the format's in case only (encoding/json
// matches names regardless of case), and a nested group named twice in one
// "groups" object (encoding/json keeps the last instances). It returns a
// *SyntaxError with the position of the offending name.
func checkMembers(data []byte) error {
	s := &scanner{data: data}
	return s.object(fileMembers)
}

// object checks the members of the object at pos, whose names are names,
// and walks into "models" and "groups". A null is no object.
func (s *scanner) object(names []string) error {
	if s.next() == 'n' {
		s.skip()
		return nil
	}
	s.pos++
	var given uint
	for s.next() != '}' {
		at := s.pos
		key := s.str()
		i := 0
		for i < len(names) && names[i] != key {
			i++
		}
		if i == len(names) {
			// Decoding succeeded, so the name is one of names but for case.
			return s.errorAt(at, fmt.Errorf("member %q: member names are case-sensitive", key))
		}
		if given&(1<<i) != 0 {
			return s.errorAt(at, fmt.Errorf("member %q is given twice", key))
		}
		given |= 1 << i
		s.expect(':')
		var err error
		switch key {
		case "models":
			err = s.list(modelMembers)
		case "groups":
			err = s.groups()
		default:
			s.skip()
		}
		if err != nil {
			return err
		}
		if s.next() == ',' {
			s.pos++
		}
	}
	s.pos++
	return nil
}

// list checks the objects of the list at pos; null elements and a null list
// are left alone.
func (s *scanner) list(names []string) error {
	if s.next() == 'n' {
		s.skip()
		return nil
	}
	s.pos++
	for s.next() != ']' {
		if err := s.object(names); err != nil {
			return err
		}
		if s.next() == ',' {
			s.pos++
		}
	}
	s.pos++
	return nil
}

// groups checks the "groups" object at pos: each nested group named once,
// and its instances.
func (s *scanner) groups() error {
	if s.next() == 'n' {
		s.skip()
		return nil
	}
	s.pos++
	var seen map[string]struct{}
	for s.next() != '}' {
		at := s.pos
		name := s.str()
		if _, ok := seen[name]; ok {
			return s.errorAt(at, fmt.Errorf("group %s is given twice", name))
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		seen[name] = struct{}{}
		s.expect(':')
		if err := s.list(groupMembers); err != nil {
			return err
		}
		if s.next() == ',' {
			s.pos++
		}
	}
	s.pos++
	return nil
}

// errorAt returns a *SyntaxError for the text at offset.
func (s *scanner) errorAt(offset int, err error) error {
	line, col := position(s.data, int64(offset))
	return &SyntaxError{Line: line, Column: col, Err: err}
}
