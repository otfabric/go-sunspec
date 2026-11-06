// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"fmt"

	"github.com/otfabric/go-modbus"
)

// ErrPointNotWritable indicates a write the client refuses before sending
// anything: a point whose schema access is not "RW", the count point of a
// group (it defines the layout), a point of a model without a schema or
// whose device holds more registers than the schema describes, or a device
// served in input registers, which Modbus cannot write.
var ErrPointNotWritable = errors.New("sunspec: point not writable")

// WritePoint writes one top-level point of a model instance and returns the
// point as the device reports it afterwards.
//
// It is the only method of this package that sends a write. It reads the
// model first, so that the header is checked (ErrModelChanged) and the
// point's scale factor is the device's current one, and encodes value as
// EncodeModel does: the engineering value for a scaled point, an enum symbol
// name, a list of bitfield symbol names, a string, or []uint16 with the
// point's exact registers. Then it writes the point's registers, and only
// those, with Write Multiple Registers (FC 16), and reads the model again.
//
// A point that is not "RW" in the schema, a count point, a model without a
// schema and a Device on input registers fail with ErrPointNotWritable, and
// a name that is not a point of the top-level group with ErrPointNotFound,
// before anything is sent. A value the encoder refuses fails with its
// *EncodeError. A device that refuses the write returns its Modbus
// exception (modbus.ErrIllegalDataAddress, say).
//
// A client built with AllowedFunctionCodes: ReadFunctionCodes() cannot
// write: WritePoint then fails with modbus.ErrFunctionCodeNotAllowed.
func (d *Device) WritePoint(ctx context.Context, inst ModelInstance, name string, value any) (*DecodedPoint, error) {
	if d == nil || d.Client == nil {
		return nil, errNilClient
	}
	if d.RegType == modbus.InputRegister {
		return nil, fmt.Errorf("%w: model %d is served in input registers", ErrPointNotWritable, inst.Header.ID)
	}
	if !inst.SchemaKnown || inst.Schema == nil || inst.Schema.Group == nil {
		return nil, fmt.Errorf("%w: model %d has no schema", ErrPointNotWritable, inst.Header.ID)
	}
	top := inst.Schema.Group
	var size, offset int
	found := false
	for i := range top.Points {
		pm := &top.Points[i]
		if pm.Name != name {
			continue
		}
		if pm.Access != "RW" {
			return nil, fmt.Errorf("%w: %s of model %d has access %q", ErrPointNotWritable, name, inst.Header.ID, pm.Access)
		}
		for _, g := range top.Groups {
			if g.CountPoint == name {
				return nil, fmt.Errorf("%w: %s of model %d counts the instances of %s", ErrPointNotWritable, name, inst.Header.ID, g.Name)
			}
		}
		size, offset, found = pm.Size, pm.Offset, true
		break
	}
	if !found {
		return nil, fmt.Errorf("%w: no top-level point %q in model %d", ErrPointNotFound, name, inst.Header.ID)
	}

	dm, err := d.ReadModel(ctx, inst)
	if err != nil {
		return nil, err
	}
	values := dm.Values()
	values.Set(name, value)
	regs, err := EncodeModel(inst.Schema, values)
	if err != nil {
		return nil, err
	}
	if len(regs) != int(inst.Header.Length)+2 {
		// A device whose model is longer than its schema describes (data
		// past the last group instance): the encoder cannot reproduce what
		// the device holds, so the layout of the write is not known.
		return nil, fmt.Errorf("%w: model %d encodes to %d registers, the device has %d",
			ErrPointNotWritable, inst.Header.ID, len(regs), int(inst.Header.Length)+2)
	}
	if err := d.Client.WriteRegisters(ctx, d.UnitID, mustUint16(int(inst.Header.StartAddress)+offset), regs[offset:offset+size]); err != nil {
		return nil, fmt.Errorf("sunspec: write %s of model %d: %w", name, inst.Header.ID, err)
	}
	return d.ReadPoint(ctx, inst, name)
}
