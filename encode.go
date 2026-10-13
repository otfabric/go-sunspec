// SPDX-License-Identifier: MIT

package sunspec

import (
	"fmt"

	"github.com/otfabric/go-sunspec/registry"
)

// maxModelRegisters is the longest model the 16-bit length register can
// announce, including the two header registers.
const maxModelRegisters = 0xFFFF + 2

// Values holds the values of one group instance of a model: the group's own
// points by name and the instances of the groups nested inside it. It is the
// input of EncodeModel and of the Server, and the write-side counterpart of
// DecodedGroup.
//
// A point that is absent from Points, or whose value is nil, is encoded as the
// SunSpec "not implemented" value of its type. The value of a present point
// may be:
//
//   - any Go integer or float type, or a json.Number, for a numeric point
//     (int16 … uint64, acc16 … acc64, count, sunssf, enum, bitfield, pad,
//     float32, float64). For a point with a scale factor this is the
//     engineering value, see below.
//   - a string for a string, ipaddr ("192.168.1.10"), ipv6addr ("fe80::1") or
//     eui48 ("00:11:22:33:44:55") point.
//   - a string naming one of the schema's symbols for an enum16 or enum32
//     point, and a []string naming the bits to set for a bitfield point.
//   - a []uint16 with exactly as many elements as the point has registers,
//     for any point: the registers are used as they are.
//
// # Scale factors
//
// A point whose schema names a scale factor takes the engineering value: the
// encoder divides it by 10^sf and stores the nearest integer, halves rounded
// away from zero. The scale factor is the value given for the sunssf point of
// that name in the same Values or, failing that, in an enclosing one (or the
// literal exponent of the schema). So {"W": 1234.5, "W_SF": -1} stores 12345
// in W. When the sunssf point is not set at any level the value is stored
// unscaled, which is how DecodeModel reads such a point. Unscaled integer
// points round a fractional value the same way.
//
// A value that does not fit the point's type after scaling, or that would
// collide with the type's "not implemented" value (for example 65535 for a
// uint16), is rejected with an error matching ErrValueOutOfRange; nothing is
// wrapped or clamped. Accumulators accept 0 although it reads back as not
// implemented.
//
// # Groups
//
// Groups maps the name of a nested group to its instances in register order.
// The number of instances that is encoded follows the schema
// (registry.GroupMeta):
//
//   - CountPoint set: the value given for that count point, or, when the count
//     point is not set, the largest number of instances given for any group
//     that is counted by it. The count point itself is encoded with that
//     number, so an unset count point reads back as that number, not as "not
//     implemented". Groups with fewer instances are completed with instances
//     whose points are all not implemented; more instances than an explicit
//     count is an error.
//   - Count > 0: exactly that many, completed the same way.
//   - Count == 0: as many as given.
//
// A nil *Values, as a model's values or as an instance, is an instance without
// any value.
type Values struct {
	// Points maps a point name of the group to its value.
	Points map[string]any
	// Groups maps the name of a nested group to its instances.
	Groups map[string][]*Values
}

// Set stores value for the named point and returns v, so that calls can be
// chained. It allocates Points when needed.
func (v *Values) Set(name string, value any) *Values {
	if v.Points == nil {
		v.Points = make(map[string]any)
	}
	v.Points[name] = value
	return v
}

// Add appends one instance of the named nested group and returns that
// instance. A nil inst adds an empty instance.
func (v *Values) Add(group string, inst *Values) *Values {
	if inst == nil {
		inst = &Values{}
	}
	if v.Groups == nil {
		v.Groups = make(map[string][]*Values)
	}
	v.Groups[group] = append(v.Groups[group], inst)
	return inst
}

// Clone returns a deep copy of v: maps, instances and slice values are
// copied. A nil v yields nil.
func (v *Values) Clone() *Values {
	if v == nil {
		return nil
	}
	out := &Values{}
	if v.Points != nil {
		out.Points = make(map[string]any, len(v.Points))
		for k, val := range v.Points {
			switch s := val.(type) {
			case []uint16:
				val = append([]uint16(nil), s...)
			case []string:
				val = append([]string(nil), s...)
			}
			out.Points[k] = val
		}
	}
	if v.Groups != nil {
		out.Groups = make(map[string][]*Values, len(v.Groups))
		for k, insts := range v.Groups {
			cp := make([]*Values, len(insts))
			for i, inst := range insts {
				cp[i] = inst.Clone()
			}
			out.Groups[k] = cp
		}
	}
	return out
}

// point returns the value given for a point and whether it is set (present
// and not nil).
func (v *Values) point(name string) (any, bool) {
	if v == nil {
		return nil, false
	}
	val, ok := v.Points[name]
	if !ok || val == nil {
		return nil, false
	}
	return val, true
}

// instances returns the instances given for a nested group.
func (v *Values) instances(group string) []*Values {
	if v == nil {
		return nil
	}
	return v.Groups[group]
}

// EncodeModel builds the registers of one model from its schema and values.
// It is the inverse of DecodeModel and performs no I/O.
//
// The result holds the whole model starting at its header: index 0 is the ID
// register (meta.ID), index 1 the length register (the number of registers
// that follow it), then the data in the order of the schema's group tree.
// Passing it to DecodeModel with the same schema yields the given values.
// The ID and length registers are set by the encoder and cannot be given.
//
// How values are given, scaled, rounded and checked, and how many instances
// of a nested group are encoded, is described on Values. A nil values encodes
// a model in which every point is not implemented.
//
// EncodeModel returns a *EncodeError, matching ErrEncode, that names the
// group instance and point at fault. It additionally matches
// ErrValueOutOfRange for a value that does not fit its point and
// ErrPointNotFound for a name in Values.Points that the schema's group does
// not have.
func EncodeModel(meta *registry.ModelMeta, values *Values) ([]uint16, error) {
	regs, _, err := encodeModel(meta, values)
	return regs, err
}

// pointSlot locates one point of an encoded model.
type pointSlot struct {
	// offset is the index of the point's first register from the model's ID
	// register.
	offset int
	meta   *registry.PointMeta
	// writable is true for a point a Modbus client may write: its schema
	// access is "RW" and it is neither a header register nor the count point
	// of a group.
	writable bool
}

// encodeModel is EncodeModel that also returns where each point was placed.
func encodeModel(meta *registry.ModelMeta, values *Values) ([]uint16, []pointSlot, error) {
	if meta == nil {
		return nil, nil, &EncodeError{Message: "nil model schema"}
	}
	if meta.Group == nil {
		return nil, nil, &EncodeError{ModelID: meta.ID, Message: "model schema has no group"}
	}
	if meta.Group.Length < 2 {
		return nil, nil, &EncodeError{ModelID: meta.ID, Message: "model schema has no ID and length registers"}
	}
	e := &encoder{modelID: meta.ID}
	if err := e.group(meta.Group, values, meta.Group.Name, nil); err != nil {
		return nil, nil, err
	}
	e.regs[0] = meta.ID
	e.regs[1] = uint16(len(e.regs) - 2)
	return e.regs, e.slots, nil
}

// encoder appends the registers of a model's group tree.
type encoder struct {
	modelID uint16
	regs    []uint16
	slots   []pointSlot
}

// encScope is the chain of group instances enclosing the points being
// encoded, innermost first: scale factors and counts are looked up along it.
type encScope struct {
	meta   *registry.GroupMeta
	values *Values
	// counts holds the instance count resolved for each count point of this
	// group instance.
	counts map[string]int
	parent *encScope
}

// scaleFactor returns the value of the nearest enclosing sunssf point with
// the given name that is set, as the decoder resolves it.
func (s *encScope) scaleFactor(name string) (sf int16, ok bool, err error) {
	for ; s != nil; s = s.parent {
		pm := s.meta.Point(name)
		if pm == nil || pm.Type != "sunssf" {
			continue
		}
		val, set := s.values.point(name)
		if !set {
			continue
		}
		regs, err := encodeValue(pm, val, nil)
		if err != nil {
			return 0, false, err
		}
		if len(regs) == 0 || regs[0] == 0x8000 {
			continue
		}
		return int16(regs[0]), true, nil
	}
	return 0, false, nil
}

// count returns the instance count resolved for the nearest enclosing count
// point with the given name. Every group that has a point of that name above
// a group it counts has resolved it before its nested groups are encoded.
func (s *encScope) count(name string) (int, bool) {
	for ; s != nil; s = s.parent {
		if n, ok := s.counts[name]; ok {
			return n, true
		}
	}
	return 0, false
}

func (e *encoder) errorf(path string, pm *registry.PointMeta, err error, format string, args ...any) *EncodeError {
	ee := &EncodeError{ModelID: e.modelID, Group: path, Message: fmt.Sprintf(format, args...), Err: err}
	if pm != nil {
		ee.PointName = pm.Name
		ee.PointType = pm.Type
	}
	return ee
}

// group appends one instance of gm: its own points, then the instances of its
// nested groups. path names the instance in errors.
func (e *encoder) group(gm *registry.GroupMeta, vals *Values, path string, parent *encScope) error {
	top := parent == nil
	if vals != nil {
		for name := range vals.Points {
			if gm.Point(name) == nil {
				return &EncodeError{ModelID: e.modelID, Group: path, PointName: name,
					Message: "the group has no point of that name", Err: ErrPointNotFound}
			}
		}
		for name := range vals.Groups {
			if gm.Group(name) == nil {
				return e.errorf(path, nil, nil, "the group has no nested group %q", name)
			}
		}
	}
	if gm.Length < 0 || len(e.regs)+gm.Length > maxModelRegisters {
		return e.errorf(path, nil, nil, "model exceeds %d registers", maxModelRegisters)
	}

	sc := &encScope{meta: gm, values: vals, parent: parent}
	base := len(e.regs)
	e.regs = append(e.regs, make([]uint16, gm.Length)...)

	for i := range gm.Points {
		pm := &gm.Points[i]
		if pm.Offset < 0 || pm.Size < 0 || pm.Offset+pm.Size > gm.Length {
			return e.errorf(path, pm, nil, "offset %d+size %d exceeds the group length %d in schema", pm.Offset, pm.Size, gm.Length)
		}
		val, set := vals.point(pm.Name)
		header := top && pm.Offset < 2
		counts := countsGroups(gm, pm.Name)
		switch {
		case header:
			if set {
				return e.errorf(path, pm, nil, "the ID and length registers are set by the encoder")
			}
			continue
		case counts:
			if !countable(pm.Type) {
				return e.errorf(path, pm, nil, "a point of this type cannot count a group")
			}
			n := maxInstances(gm, vals, pm.Name)
			derived := true
			if set {
				given, implemented, err := countFromValue(pm, val)
				if err != nil {
					return e.errorf(path, pm, err, "count point: %v", err)
				}
				if n > given {
					return e.errorf(path, pm, ErrValueOutOfRange,
						"count is %d but %d instances are given", given, n)
				}
				// A count given as its raw not-implemented registers stays
				// that way: it reads back as no instances.
				n, derived = given, implemented
			}
			if sc.counts == nil {
				sc.counts = make(map[string]int)
			}
			sc.counts[pm.Name] = n
			if derived {
				val, set = n, true
			}
		}

		dst := e.regs[base+pm.Offset : base+pm.Offset+pm.Size]
		if set {
			var sf *int16
			if pm.SF != "" {
				v, ok, err := scaleFactorFor(pm, sc)
				if err != nil {
					return e.errorf(path, pm, err, "scale factor %s: %v", pm.SF, err)
				}
				if ok {
					sf = &v
				}
			}
			regs, err := encodeValue(pm, val, sf)
			if err != nil {
				return e.errorf(path, pm, err, "%v", err)
			}
			copy(dst, regs)
		} else if err := encodeNotImplemented(pm, dst); err != nil {
			return e.errorf(path, pm, err, "%v", err)
		}
		e.slots = append(e.slots, pointSlot{
			offset:   base + pm.Offset,
			meta:     pm,
			writable: pm.Access == "RW" && !counts,
		})
	}

	for _, child := range gm.Groups {
		insts := vals.instances(child.Name)
		childPath := path + "/" + child.Name
		count := len(insts)
		switch {
		case child.CountPoint != "":
			n, ok := sc.count(child.CountPoint)
			if !ok {
				return e.errorf(childPath, nil, nil, "count point %s is not in an enclosing group of the schema", child.CountPoint)
			}
			count = n
		case child.Count > 0:
			if len(insts) > child.Count {
				return e.errorf(childPath, nil, ErrValueOutOfRange,
					"%d instances are given but the group occurs %d times", len(insts), child.Count)
			}
			count = child.Count
		}
		for i := 0; i < count; i++ {
			var inst *Values
			if i < len(insts) {
				inst = insts[i]
			}
			before := len(e.regs)
			if err := e.group(child, inst, fmt.Sprintf("%s[%d]", childPath, i+1), sc); err != nil {
				return err
			}
			if len(e.regs) == before {
				// A group without registers cannot repeat; the decoder
				// reads it once as well.
				break
			}
		}
	}
	return nil
}

// scaleFactorFor resolves the scale factor of pm: a literal exponent, or the
// nearest enclosing sunssf point that is set.
func scaleFactorFor(pm *registry.PointMeta, sc *encScope) (int16, bool, error) {
	if lit, ok := literalSF(pm.SF); ok {
		return lit, true, nil
	}
	return sc.scaleFactor(pm.SF)
}

// countsGroups reports whether the point named name of gm is the count point
// of a group nested, at any depth, inside gm.
func countsGroups(gm *registry.GroupMeta, name string) bool {
	for _, c := range gm.Groups {
		if c.CountPoint == name {
			return true
		}
		// A point of the same name further in hides this one from there on.
		if c.Point(name) == nil && countsGroups(c, name) {
			return true
		}
	}
	return false
}

// maxInstances returns the largest number of instances given in vals for any
// group that the point named name of gm counts.
func maxInstances(gm *registry.GroupMeta, vals *Values, name string) int {
	n := 0
	for _, c := range gm.Groups {
		insts := vals.instances(c.Name)
		if c.CountPoint == name && len(insts) > n {
			n = len(insts)
		}
		if c.Point(name) != nil {
			continue
		}
		for _, inst := range insts {
			if m := maxInstances(c, inst, name); m > n {
				n = m
			}
		}
	}
	return n
}

// countable reports whether the decoder can read a point of the given type
// as the count of a group: the 16-bit integer types and the unsigned 32-bit
// ones.
func countable(pointType string) bool {
	switch pointType {
	case "int16", "sunssf", "uint16", "count", "acc16", "enum16", "bitfield16", "pad",
		"uint32", "acc32", "enum32", "bitfield32":
		return true
	default:
		return false
	}
}

// countFromValue converts the value given for a count point, of a countable
// type, to a number of instances. implemented is false for a value the
// decoder reads as not implemented, which counts no instances: the point's
// raw not-implemented registers, or zero in an accumulator.
func countFromValue(pm *registry.PointMeta, val any) (n int, implemented bool, err error) {
	regs, err := encodeValue(pm, val, nil)
	if err != nil {
		return 0, false, err
	}
	if dp, _ := decodePoint(regs, pm); !dp.Implemented {
		return 0, false, nil
	}
	if minRegisters(pm.Type) == 1 {
		if pm.Type == "int16" || pm.Type == "sunssf" {
			if int16(regs[0]) < 0 {
				return 0, false, fmt.Errorf("negative count %d: %w", int16(regs[0]), ErrValueOutOfRange)
			}
		}
		return int(regs[0]), true, nil
	}
	count := uint32(regs[0])<<16 | uint32(regs[1])
	if count > maxModelRegisters {
		return 0, false, fmt.Errorf("count %d: %w", count, ErrValueOutOfRange)
	}
	return int(count), true, nil
}

// Values returns the model's decoded values in the form EncodeModel takes, so
// that EncodeModel(m.Schema, m.Values()) rebuilds a model that decodes to the
// same values. It returns nil for a model that was not decoded (Group is nil).
//
// Points the device reports as not implemented are left out, as are the ID
// and length registers and pad points holding the pad value. A point whose
// scale factor was resolved to a non-zero exponent is given as its
// engineering value (ScaledValue); every other point as its RawValue. Enum
// and bitfield points are given as numbers, not symbol names, so that values
// the schema does not name survive. Should an engineering value not convert
// back to the registers it was decoded from (a 64-bit value beyond the
// precision of a float64, an absurd scale factor, a string of only spaces),
// the point's registers are given instead, as a []uint16.
//
// Every decoded group instance is kept, also one without implemented points,
// and count points keep their value, so the model's length is preserved. A
// count point the device reports as not implemented is given as its
// registers, because leaving it out would encode it as a count of zero.
//
// For a model that decoded without warnings, decoding the result of
// EncodeModel(m.Schema, m.Values()) yields the same points as m holds.
func (m *DecodedModel) Values() *Values {
	if m == nil || m.Group == nil {
		return nil
	}
	var meta *registry.GroupMeta
	if m.Schema != nil {
		meta = m.Schema.Group
	}
	return m.groupValues(m.Group, meta, true)
}

// groupValues converts one decoded group instance. meta is its schema, or nil
// when the model carries none.
func (m *DecodedModel) groupValues(g *DecodedGroup, meta *registry.GroupMeta, top bool) *Values {
	v := &Values{}
	for i := range g.Points {
		p := &g.Points[i]
		if top && p.RegisterOffset < 2 {
			continue
		}
		counts := meta != nil && countsGroups(meta, p.Name)
		if val, ok := m.pointValue(p, counts); ok {
			v.Set(p.Name, val)
		}
	}
	for _, c := range g.Groups {
		v.Add(c.Name, m.groupValues(c, meta.Group(c.Name), false))
	}
	return v
}

// pointValue returns the value Values gives for p; ok is false for a point
// that is left out. counts says that p is the count point of a group.
func (m *DecodedModel) pointValue(p *DecodedPoint, counts bool) (val any, ok bool) {
	var regs []uint16
	if p.RegisterOffset >= 0 && p.RegisterCount > 0 && p.RegisterOffset+p.RegisterCount <= len(m.RawRegisters) {
		regs = m.RawRegisters[p.RegisterOffset : p.RegisterOffset+p.RegisterCount]
	}
	if !p.Implemented {
		// Left out, the count point of a group would be encoded as the
		// number of instances: zero, not "not implemented".
		if counts && regs != nil {
			return append([]uint16(nil), regs...), true
		}
		return nil, false
	}
	if p.Type == "pad" && len(regs) > 0 && regs[0] == 0x8000 {
		return nil, false
	}
	if raw, isRaw := p.RawValue.([]uint16); isRaw {
		return append([]uint16(nil), raw...), true
	}
	var sf *int16
	val = p.RawValue
	if p.SFRawValue != nil && *p.SFRawValue != 0 && p.ScaledValue != nil {
		sf, val = p.SFRawValue, *p.ScaledValue
	}
	if regs != nil {
		// Give the registers when the value would not read back the same.
		pm := &registry.PointMeta{Name: p.Name, Type: p.Type, Size: p.RegisterCount}
		back, err := encodeValue(pm, val, sf)
		if err != nil {
			return append([]uint16(nil), regs...), true
		}
		if again, _ := decodePoint(back, pm); again.RawValue != p.RawValue || !again.Implemented {
			return append([]uint16(nil), regs...), true
		}
	}
	return val, true
}
