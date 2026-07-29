// SPDX-License-Identifier: MIT

// Package registry holds compiled SunSpec model schemas and lookup helpers.
//
// The registry is populated by generated init functions. After package
// initialization it is safe for concurrent reads (ByID, Known, All, Count).
// Callers must not call Register after init.
package registry

type ModelMeta struct {
	ID             uint16
	Name           string
	Label          string
	Desc           string
	FixedBlock     *GroupMeta
	RepeatingBlock *GroupMeta
}

func (m *ModelMeta) FixedLength() int {
	if m.FixedBlock == nil {
		return 0
	}
	return m.FixedBlock.Length
}

func (m *ModelMeta) RepeatingLength() int {
	if m.RepeatingBlock == nil {
		return 0
	}
	return m.RepeatingBlock.Length
}

type GroupMeta struct {
	Name      string
	Label     string
	Length    int
	Repeating bool
	Points    []PointMeta
}

type PointMeta struct {
	Name        string
	Label       string
	Desc        string
	Type        string
	Size        int
	Offset      int
	SF          string // scale factor point name reference, or literal as string
	SFLiteral   int    // literal scale factor exponent (valid when SFIsLiteral)
	SFIsLiteral bool   // true if SF is a literal exponent, not a point reference
	Units       string
	Access      string
	Mandatory   bool
	Static      bool
	Symbols     []SymbolMeta
}

type SymbolMeta struct {
	Name  string
	Value int
	Label string
}

var models map[uint16]*ModelMeta

func Register(m *ModelMeta) {
	if models == nil {
		models = make(map[uint16]*ModelMeta)
	}
	models[m.ID] = m
}

func ByID(id uint16) *ModelMeta {
	return models[id]
}

func Known(id uint16) bool {
	return models[id] != nil
}

func All() map[uint16]*ModelMeta {
	out := make(map[uint16]*ModelMeta, len(models))
	for k, v := range models {
		out[k] = v
	}
	return out
}

func Count() int {
	return len(models)
}
