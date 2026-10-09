// SPDX-License-Identifier: MIT

// Package schema parses SunSpec model definitions in the JSON format
// published at https://github.com/sunspec/models.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ModelDef is one SunSpec model definition file.
type ModelDef struct {
	// ID is the SunSpec model ID. ParseDir fills it from the file name or the
	// ID point when the JSON has no "id"; 0 means it could not be determined.
	ID int `json:"id"`
	// Group is the top-level group. It holds the model's own points and,
	// nested to any depth, the groups that follow them.
	Group GroupDef `json:"group"`
}

// GroupDef is a group of points, optionally with nested groups.
//
// Count applies to nested groups: it tells how often the group repeats within
// its parent.
type GroupDef struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Desc   string     `json:"desc"`
	Type   string     `json:"type"`
	Count  RawCount   `json:"count"`
	Points []PointDef `json:"points"`
	Groups []GroupDef `json:"groups"`
}

// PointDef is one point definition.
//
// Size is in registers. Mandatory is "M" for a mandatory point, Static is "S"
// for a static one, and Access is "RW" for a writable point; each is empty
// otherwise. Value holds the fixed value of a point as decoded by
// encoding/json (a float64 for numbers), or nil.
type PointDef struct {
	Name      string      `json:"name"`
	Label     string      `json:"label"`
	Desc      string      `json:"desc"`
	Type      string      `json:"type"`
	Size      int         `json:"size"`
	SF        RawSF       `json:"sf"`
	Units     string      `json:"units"`
	Access    string      `json:"access"`
	Mandatory string      `json:"mandatory"`
	Static    string      `json:"static"`
	Value     interface{} `json:"value"`
	Symbols   []SymbolDef `json:"symbols"`
}

// RawSF represents a scale factor reference that can be either a string
// (point name reference like "W_SF") or an integer (literal exponent like -2).
type RawSF struct {
	Ref       string // point name reference (when IsLiteral is false)
	IntVal    int    // literal scale factor exponent (when IsLiteral is true)
	IsLiteral bool   // true when the JSON value was a number
	IsSet     bool   // true when the point has an "sf" value at all
}

// UnmarshalJSON accepts a JSON string (point reference) or integer (literal
// exponent). A JSON null leaves sf unset; any other value is an error.
func (sf *RawSF) UnmarshalJSON(b []byte) error {
	if string(bytes.TrimSpace(b)) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		sf.Ref = s
		sf.IsLiteral = false
		sf.IsSet = true
		return nil
	}
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		sf.IntVal = i
		sf.IsLiteral = true
		sf.IsSet = true
		return nil
	}
	return fmt.Errorf("sf: expected string or int, got %s", string(b))
}

// String returns the point reference, the literal exponent in decimal, or an
// empty string when no scale factor is set.
func (sf RawSF) String() string {
	if !sf.IsSet {
		return ""
	}
	if sf.IsLiteral {
		return fmt.Sprintf("%d", sf.IntVal)
	}
	return sf.Ref
}

// SymbolDef names one value of an enum point or one bit position of a
// bitfield point.
type SymbolDef struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Label string `json:"label"`
	Desc  string `json:"desc"`
}

// RawCount is a group's repeat count, which is either an integer or the name
// of the point that holds the count at run time.
//
// In the model definitions a count of 0 means "as many instances as fit in
// the model length". A group without a "count" occurs exactly once, the
// default the SunSpec JSON schema gives it; IsSet tells the two apart.
type RawCount struct {
	IntVal    int    // fixed count (when IsString is false)
	StringVal string // name of the point holding the count (when IsString is true)
	IsString  bool   // true when the JSON value was a string
	IsSet     bool   // true when the group has a "count" value at all
}

// UnmarshalJSON accepts a JSON integer or string. A JSON null leaves the
// count unset; any other value is an error.
func (c *RawCount) UnmarshalJSON(b []byte) error {
	if string(bytes.TrimSpace(b)) == "null" {
		return nil
	}
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		c.IntVal = i
		c.IsString = false
		c.IsSet = true
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.StringVal = s
		c.IsString = true
		c.IsSet = true
		return nil
	}
	return fmt.Errorf("count: expected int or string, got %s", string(b))
}

// Fixed returns the number of times the group occurs when that is a fixed
// number: the integer count, or 1 for a group without a count. A result of 0
// means "as many instances as fit in the model length". It is meaningless when
// the count is a point reference (IsString).
func (c RawCount) Fixed() int {
	if !c.IsSet {
		return 1
	}
	return c.IntVal
}

// IsRepeating reports whether the group can occur other than exactly once:
// its count is a point reference or any integer but 1.
func (c RawCount) IsRepeating() bool {
	if c.IsString {
		return true
	}
	return c.Fixed() != 1
}
