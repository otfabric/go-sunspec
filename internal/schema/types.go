// SPDX-License-Identifier: MIT

package schema

import (
	"encoding/json"
	"fmt"
)

type ModelDef struct {
	ID    int      `json:"id"`
	Group GroupDef `json:"group"`
}

type GroupDef struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Desc   string     `json:"desc"`
	Type   string     `json:"type"`
	Count  RawCount   `json:"count"`
	Points []PointDef `json:"points"`
	Groups []GroupDef `json:"groups"`
}

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
	IsLiteral bool
	IsSet     bool
}

func (sf *RawSF) UnmarshalJSON(b []byte) error {
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

func (sf RawSF) String() string {
	if !sf.IsSet {
		return ""
	}
	if sf.IsLiteral {
		return fmt.Sprintf("%d", sf.IntVal)
	}
	return sf.Ref
}

type SymbolDef struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Label string `json:"label"`
	Desc  string `json:"desc"`
}

type RawCount struct {
	IntVal    int
	StringVal string
	IsString  bool
}

func (c *RawCount) UnmarshalJSON(b []byte) error {
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		c.IntVal = i
		c.IsString = false
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.StringVal = s
		c.IsString = true
		return nil
	}
	return fmt.Errorf("count: expected int or string, got %s", string(b))
}

func (c RawCount) IsRepeating() bool {
	if c.IsString {
		return true
	}
	return c.IntVal != 1
}
