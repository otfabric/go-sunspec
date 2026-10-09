// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/json"
	"math"

	"github.com/otfabric/go-sunspec/registry"
)

// DecodedModel holds the decoded output of a single model instance.
type DecodedModel struct {
	// ModelID is the SunSpec model ID.
	ModelID uint16
	// Name is the schema label (or name), or "unknown_<ID>" for a model
	// without a schema.
	Name string
	// InstanceAddress is the register address of the model header (the ID
	// register) on the device.
	InstanceAddress uint16
	// Schema is the schema the model was decoded with. It is nil when the
	// model was not decoded (no schema, or the read failed). It is shared
	// with the registry and must not be modified.
	Schema *registry.ModelMeta
	// Group is the decoded top-level group: the model's own points and,
	// nested inside it, the instances of its groups. It is nil when the model
	// was not decoded (no schema, the read failed, or too few registers).
	Group *DecodedGroup
	// RawRegisters holds the registers the model was decoded from. For a
	// decoded model this includes the ID and L header registers at index 0
	// and 1; for a model without a schema, or one whose read failed, it holds
	// only the data registers that were read.
	RawRegisters []uint16
	// Warnings holds human-readable notes about anything that could not be
	// read or decoded. A model can carry warnings without an error.
	Warnings []string
}

// DecodedGroup holds one decoded instance of a group: its own points and the
// instances of the groups nested inside it.
//
// A simple model has one top-level group and nothing else. A model with a
// repeating part (the phases of a meter, the trackers of an MPPT extension,
// the strings of a battery) has one nested instance per repetition in Groups.
// Curve models nest further: each curve instance holds its point instances.
type DecodedGroup struct {
	// Name is the group name from the schema, for example "Crv" or "Pt".
	// For the top-level group it is the model's name.
	Name string
	// Index is the 1-based number of this instance among the instances of the
	// same group within its parent. It is 0 for the top-level group.
	Index int
	// Points holds the group's own decoded points in schema order.
	Points []DecodedPoint
	// Groups holds the instances of the nested groups in register order:
	// all instances of the first nested group, then all of the second, and
	// so on. It is nil for a group without nested instances.
	Groups []*DecodedGroup
}

// Point returns the group's own point with the given name, or nil. Nested
// groups are not searched.
func (g *DecodedGroup) Point(name string) *DecodedPoint {
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

// FindPoint returns the first point with the given name in the group or, depth
// first in register order, in the groups nested inside it. It returns nil when
// there is none.
func (g *DecodedGroup) FindPoint(name string) *DecodedPoint {
	if g == nil {
		return nil
	}
	if p := g.Point(name); p != nil {
		return p
	}
	for _, c := range g.Groups {
		if p := c.FindPoint(name); p != nil {
			return p
		}
	}
	return nil
}

// GroupsNamed returns the nested instances with the given group name, in
// order. Deeper levels are not searched.
func (g *DecodedGroup) GroupsNamed(name string) []*DecodedGroup {
	if g == nil {
		return nil
	}
	var out []*DecodedGroup
	for _, c := range g.Groups {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// Walk calls fn for the group and then, depth first in register order, for
// every group nested inside it.
func (g *DecodedGroup) Walk(fn func(*DecodedGroup)) {
	if g == nil {
		return
	}
	fn(g)
	for _, c := range g.Groups {
		c.Walk(fn)
	}
}

// Point returns the first point with the given name in the model: in the
// top-level group or, failing that, in the nested groups in register order.
// It returns nil when the model was not decoded or has no such point.
func (m *DecodedModel) Point(name string) *DecodedPoint {
	if m == nil {
		return nil
	}
	return m.Group.FindPoint(name)
}

// DecodedPoint holds a single decoded point value.
type DecodedPoint struct {
	// Name is the point name from the schema, for example "W".
	Name string
	// Type is the SunSpec point type from the schema, for example "int16",
	// "acc32", "enum16", "sunssf" or "string".
	Type string
	// RawValue is the unscaled value. Its dynamic type follows Type: int16
	// (int16, sunssf), uint16 (uint16, count, acc16, enum16, bitfield16,
	// pad), int32, uint32 (uint32, acc32, enum32, bitfield32), int64, uint64
	// (uint64, acc64, bitfield64), float32, float64, string (string, ipaddr,
	// ipv6addr, eui48), or []uint16 holding a copy of the raw registers for
	// an unsupported type or a point with too few registers.
	RawValue interface{}
	// ScaledValue is RawValue multiplied by 10^SFRawValue. It is nil when the
	// point has no scale factor, is not implemented, is not numeric, or its
	// scale factor could not be resolved.
	ScaledValue *float64
	// Units is the unit from the schema (for example "W" or "Hz"), or empty.
	Units string
	// SFName is the scale factor reference from the schema: the name of a
	// sunssf point, a literal exponent such as "-2", or empty for none.
	SFName string
	// SFRawValue is the resolved scale factor exponent, or nil when the point
	// has no scale factor or it could not be resolved (for example because
	// the device reports the sunssf point as not implemented).
	SFRawValue *int16
	// RegisterOffset is the offset of the point's first register from the
	// model's ID register, that is its index in DecodedModel.RawRegisters.
	RegisterOffset int
	// RegisterCount is the number of registers the point occupies.
	RegisterCount int
	// Implemented is false when the device reports the SunSpec "not
	// implemented" value for the point's type (for example 0x8000 for int16,
	// 0xFFFF for uint16, 0 for accumulators, NaN for floats, all NUL for
	// strings, the all-zero address for ipaddr and ipv6addr, and
	// FF:FF:FF:FF:FF:FF for eui48). RawValue then
	// holds that sentinel and must not be used as a measurement.
	Implemented bool
	// Symbols holds the names of the active symbols: at most one for an enum
	// whose value is listed in the schema, one per set bit that the schema
	// names for a bitfield. It is nil for other types.
	Symbols []string
}

// MarshalJSON encodes the point with the same field names as the struct.
// JSON has no representation for NaN or infinity, so a float RawValue or a
// ScaledValue that is not a finite number is encoded as null. This is what a
// float point the device reports as not implemented looks like (Implemented is
// false and RawValue is NaN); without it, encoding a model with such a point fails.
func (p DecodedPoint) MarshalJSON() ([]byte, error) {
	// plain has the fields of DecodedPoint but not this method.
	type plain DecodedPoint
	out := plain(p)
	switch v := out.RawValue.(type) {
	case float32:
		if !isFinite(float64(v)) {
			out.RawValue = nil
		}
	case float64:
		if !isFinite(v) {
			out.RawValue = nil
		}
	}
	if out.ScaledValue != nil && !isFinite(*out.ScaledValue) {
		out.ScaledValue = nil
	}
	return json.Marshal(out)
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
