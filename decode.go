// SPDX-License-Identifier: MIT

package sunspec

import (
	"fmt"

	"github.com/otfabric/go-sunspec/registry"
)

// DecodeModel decodes a register slice into a DecodedModel using the given schema.
// It performs no I/O.
//
// regs must hold the whole model starting at its header: regs[0] is the ID
// register, regs[1] the length register, and the data follows. The registers
// are consumed in the order of the schema's group tree: the top-level group's
// own points, then every instance of each nested group, depth first. How often
// a nested group occurs comes from the schema: a fixed count, the value of a
// count point decoded earlier, or as many instances as the remaining registers
// hold. instanceAddr is only copied to DecodedModel.InstanceAddress.
//
// The returned model's RawRegisters aliases regs; it is not copied. Problems
// that leave the rest of the model usable (an unsupported point type, a group
// that does not fit in the registers, registers left over at the end) are
// reported in Warnings, not as an error.
//
// DecodeModel returns a *DecodeError (matching ErrDecode) when meta is nil or
// has no group, in which case the model is nil too, or when regs is shorter
// than the top-level group's own points, in which case the returned model
// holds only the raw registers and a warning.
func DecodeModel(regs []uint16, meta *registry.ModelMeta, instanceAddr uint16) (*DecodedModel, error) {
	if meta == nil {
		return nil, &DecodeError{Message: "nil model schema", Err: ErrDecode}
	}
	if meta.Group == nil {
		return nil, &DecodeError{ModelID: meta.ID, Message: "model schema has no group", Err: ErrDecode}
	}
	dm := &DecodedModel{
		ModelID:         meta.ID,
		Name:            meta.Label,
		InstanceAddress: instanceAddr,
		Schema:          meta,
		RawRegisters:    regs,
	}
	if dm.Name == "" {
		dm.Name = meta.Name
	}

	fixedLen := meta.FixedLength()
	if len(regs) < fixedLen {
		msg := fmt.Sprintf("register slice too short for the model's fixed points: have %d, need %d", len(regs), fixedLen)
		dm.Warnings = append(dm.Warnings, msg)
		return dm, &DecodeError{
			ModelID: meta.ID,
			Message: msg,
			Err:     ErrDecode,
		}
	}

	d := &decoder{regs: regs}
	dm.Group = d.group(meta.Group, 0, meta.Group.Name, nil)
	if left := len(regs) - d.pos; left > 0 {
		d.warnf("%d registers left over after the last group", left)
	}
	dm.Warnings = append(dm.Warnings, d.warnings...)

	return dm, nil
}

// decoder walks a model's group tree over its registers.
type decoder struct {
	regs     []uint16
	pos      int // index in regs of the next register to consume
	warnings []string
}

func (d *decoder) warnf(format string, args ...interface{}) {
	d.warnings = append(d.warnings, fmt.Sprintf(format, args...))
}

// scope is the chain of group instances enclosing the points being decoded:
// count points and scale factor points are looked up innermost first.
type scope struct {
	group  *DecodedGroup
	parent *scope
}

// lookup returns the nearest enclosing point with the given name, or nil.
func (s *scope) lookup(name string) *DecodedPoint {
	for ; s != nil; s = s.parent {
		if p := s.group.Point(name); p != nil {
			return p
		}
	}
	return nil
}

// group decodes one instance of gm at the current position: its own points,
// then the instances of its nested groups. It returns nil, without consuming
// anything, when the registers do not hold the group's own points. path names
// the instance in warnings.
func (d *decoder) group(gm *registry.GroupMeta, index int, path string, parent *scope) *DecodedGroup {
	if gm.Length < 0 || d.pos+gm.Length > len(d.regs) {
		return nil
	}
	g := &DecodedGroup{Name: gm.Name, Index: index}
	own := d.regs[d.pos : d.pos+gm.Length]
	for i := range gm.Points {
		pm := &gm.Points[i]
		if pm.Offset < 0 || pm.Size < 0 {
			d.warnf("%s: point %s: invalid offset %d or size %d in schema", path, pm.Name, pm.Offset, pm.Size)
			continue
		}
		if pm.Offset+pm.Size > len(own) {
			d.warnf("%s: point %s: offset %d+size %d exceeds the group length %d",
				path, pm.Name, pm.Offset, pm.Size, len(own))
			continue
		}
		dp, warn := decodePoint(own[pm.Offset:pm.Offset+pm.Size], pm)
		dp.RegisterOffset = d.pos + pm.Offset
		g.Points = append(g.Points, dp)
		if warn != "" {
			d.warnf("%s: %s", path, warn)
		}
	}
	d.pos += gm.Length

	sc := &scope{group: g, parent: parent}
	// Scale factors are resolved before the nested groups are decoded, so
	// that this group's points are complete when they serve as scope.
	applySF(g, sc)

	for _, child := range gm.Groups {
		d.instances(child, g, path, sc)
	}
	return g
}

// instances decodes every instance of the nested group child and appends them
// to parent.
func (d *decoder) instances(child *registry.GroupMeta, parent *DecodedGroup, path string, sc *scope) {
	childPath := path + "/" + child.Name
	count, fill := 0, false
	switch {
	case child.CountPoint != "":
		n, ok := countValue(sc.lookup(child.CountPoint))
		if !ok {
			d.warnf("%s: count point %s is missing or not implemented, no instances decoded", childPath, child.CountPoint)
			return
		}
		count = n
	case child.Count == 0:
		fill = true
	default:
		count = child.Count
	}

	for i := 1; fill || i <= count; i++ {
		if fill && d.pos >= len(d.regs) {
			return
		}
		before := d.pos
		g := d.group(child, i, fmt.Sprintf("%s[%d]", childPath, i), sc)
		if g == nil {
			if fill {
				// The leftover is reported once, by DecodeModel.
				return
			}
			d.warnf("%s: registers end before instance %d of %d", childPath, i, count)
			return
		}
		parent.Groups = append(parent.Groups, g)
		if d.pos == before {
			// A group without registers cannot be counted by filling, and
			// repeating it would never end.
			if fill || i < count {
				d.warnf("%s: group occupies no registers, decoded once", childPath)
			}
			return
		}
	}
}

// countValue returns the value of a count point. ok is false for a nil,
// unimplemented or non-integer point.
func countValue(p *DecodedPoint) (n int, ok bool) {
	if p == nil || !p.Implemented {
		return 0, false
	}
	switch v := p.RawValue.(type) {
	case uint16:
		return int(v), true
	case int16:
		if v < 0 {
			return 0, false
		}
		return int(v), true
	case uint32:
		return int(v), true
	default:
		return 0, false
	}
}
