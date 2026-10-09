// SPDX-License-Identifier: MIT

// Package registry holds compiled SunSpec model schemas and lookup helpers.
//
// The registry is populated by generated init functions. After package
// initialization it is safe for concurrent reads (ByID, Known, All, Count).
// Callers must not call Register after init.
package registry

// ModelMeta is the compiled schema of one SunSpec model: its identity and the
// layout of its fixed and repeating blocks.
//
// Schemas returned by ByID and All are shared by every caller and must be
// treated as read-only.
type ModelMeta struct {
	// ID is the SunSpec model ID, for example 1 (Common) or 103 (three phase
	// inverter).
	ID uint16
	// Name is the machine name of the model's top-level group, for example
	// "inverter_three_phase".
	Name string
	// Label is the human-readable name, for example "Inverter (Three
	// Phase)". Generated schemas fall back to Name when the model definition
	// has no label.
	Label string
	// Desc is the description from the model definition, or empty.
	Desc string
	// Group is the model's top-level group: its own points, starting with
	// the ID and L header points, and the groups nested inside it. It is nil
	// only for a model definition without a group.
	Group *GroupMeta
}

// FixedLength returns the number of registers occupied by the top-level
// group's own points, including the two header registers (ID and L), or 0
// when the model has no group. Nested groups follow these registers.
func (m *ModelMeta) FixedLength() int {
	if m == nil || m.Group == nil {
		return 0
	}
	return m.Group.Length
}

// GroupMeta describes a group: a run of consecutive points, followed by the
// instances of its nested groups.
//
// On the wire one instance of a group is its own points (Length registers)
// followed, for each entry of Groups in order, by all instances of that nested
// group. How often a nested group occurs within its parent is given by its
// Count and CountPoint.
type GroupMeta struct {
	// Name is the machine name of the group, for example "Crv" or "Pt".
	Name string
	// Label is the human-readable name of the group, or empty.
	Label string
	// Desc is the description from the model definition, or empty.
	Desc string
	// Type is the group type from the model definition: "group", or "sync"
	// for a group whose points must be read and written together.
	Type string
	// Count is the number of instances of the group within its parent when
	// CountPoint is empty. 0 means the group repeats for as long as registers
	// remain in the model. It is 1 for the top-level group.
	Count int
	// CountPoint is the name of the point that holds the number of
	// instances, or empty when Count applies. The point belongs to an
	// enclosing group: for example "NPt" in the top-level group of a curve
	// model, or "ActPt"-style counters next to the nested group.
	CountPoint string
	// Length is the number of registers occupied by the group's own points
	// in one instance: the sum of the sizes of Points. Nested groups are not
	// included.
	Length int
	// Points lists the group's own points in register order.
	Points []PointMeta
	// Groups lists the nested groups in register order, or is nil.
	Groups []*GroupMeta
}

// Repeating reports whether the group can occur other than exactly once
// within its parent: its count comes from a point, or is a fixed number other
// than 1.
func (g *GroupMeta) Repeating() bool {
	return g != nil && (g.CountPoint != "" || g.Count != 1)
}

// Group returns the nested group with the given name, or nil.
func (g *GroupMeta) Group(name string) *GroupMeta {
	if g == nil {
		return nil
	}
	for _, c := range g.Groups {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Point returns the group's own point with the given name, or nil. Nested
// groups are not searched.
func (g *GroupMeta) Point(name string) *PointMeta {
	if g == nil {
		return nil
	}
	for i := range g.Points {
		if g.Points[i].Name == name {
			return &g.Points[i]
		}
	}
	return nil
}

// PointMeta describes one point (a named value) within a group.
type PointMeta struct {
	// Name is the point name, unique within its group, for example "W".
	Name string
	// Label is the human-readable name, or empty.
	Label string
	// Desc is the description from the model definition, or empty.
	Desc string
	// Type is the SunSpec point type: one of int16, uint16, count, acc16,
	// enum16, bitfield16, pad, sunssf, int32, uint32, acc32, enum32,
	// bitfield32, float32, int64, uint64, acc64, bitfield64, float64, string,
	// ipaddr, ipv6addr or eui48.
	Type string
	// Size is the number of registers the point occupies (at least 1).
	Size int
	// Offset is the index of the point's first register within one instance
	// of its group. In the top-level group offset 0 is the model's ID register.
	Offset int
	// SF is the scale factor: the name of a sunssf point in the same group or
	// an enclosing one,
	// a literal exponent written as a decimal string (for example "-2"), or
	// empty when the point is unscaled.
	SF string
	// SFLiteral is the literal scale factor exponent. It is only valid when
	// SFIsLiteral is true.
	SFLiteral int
	// SFIsLiteral is true when SF is a literal exponent rather than a point
	// reference.
	SFIsLiteral bool
	// Units is the unit of the scaled value, for example "W", or empty.
	Units string
	// Access is "R" for a read-only point or "RW" for a writable one.
	Access string
	// Mandatory is true when the SunSpec specification requires a device to
	// implement the point.
	Mandatory bool
	// Static is true when the value does not change during normal operation.
	Static bool
	// Symbols lists the named values of an enum point or the named bits of a
	// bitfield point. It is nil for other types and for vendor-defined enums.
	Symbols []SymbolMeta
}

// SymbolMeta names one value of an enum point or one bit of a bitfield point.
type SymbolMeta struct {
	// Name is the symbol name, for example "MPPT".
	Name string
	// Value is the enum value, or for a bitfield the zero-based bit position.
	Value int
	// Label is the human-readable name, or empty.
	Label string
}

// models maps model ID to schema. It is written only during init.
var models map[uint16]*ModelMeta

// Register adds m to the registry, replacing any schema already registered
// for m.ID. A nil m is ignored.
//
// Register is not safe for concurrent use and exists for the generated init
// function; it must not be called once package initialization has finished.
// The registry keeps m itself, not a copy.
func Register(m *ModelMeta) {
	if m == nil {
		return
	}
	if models == nil {
		models = make(map[uint16]*ModelMeta)
	}
	models[m.ID] = m
}

// ByID returns the schema for the given model ID, or nil when the registry
// has none. The result is shared and must not be modified.
func ByID(id uint16) *ModelMeta {
	return models[id]
}

// Known reports whether the registry has a schema for the given model ID.
func Known(id uint16) bool {
	return models[id] != nil
}

// All returns every registered schema keyed by model ID. The map is a fresh
// copy the caller may modify; the schemas it points to are shared and must
// not be modified.
func All() map[uint16]*ModelMeta {
	out := make(map[uint16]*ModelMeta, len(models))
	for k, v := range models {
		out[k] = v
	}
	return out
}

// Count returns the number of registered schemas.
func Count() int {
	return len(models)
}
