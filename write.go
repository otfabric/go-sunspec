// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"fmt"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// ErrPointNotWritable indicates a write the client refuses before sending
// anything: a point whose schema access is not "RW", the count point of a
// group (it defines the layout), a point of a model without a schema or
// whose device holds more registers than the schema describes, a device
// served in input registers, which Modbus cannot write, and (since v0.11.0)
// an engineering value for a point whose scale factor the device does not
// report as a usable value.
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
// A point that is not "RW" in the schema, a count point (of a group at any
// depth of the model, as EncodeModel sees it), a model without a
// schema and a Device on input registers fail with ErrPointNotWritable, and
// a name that is not a point of the top-level group with ErrPointNotFound,
// before anything is sent. A value the encoder refuses fails with its
// *EncodeError. A device that refuses the write returns its Modbus
// exception (modbus.ErrIllegalDataAddress, say).
//
// A point whose schema names a scale factor point is written only when the
// device reports that scale factor, in the read before the write, as a usable
// value: implemented and within -10..10. Otherwise the engineering value
// cannot be converted to the device's registers, and WritePoint fails with
// ErrPointNotWritable, saying why, before anything is sent. A []uint16 value
// is not scaled and is written as the point's registers whatever the scale
// factor, and so is a nil value, which sets the point to its not-implemented
// value; before v0.11.0 every value was then written unscaled. An enum
// symbol name and a list of bitfield symbol names are raw values, never
// scaled, and are written whatever the scale factor as well.
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
	var sf string
	found, symbol := false, false
	for i := range top.Points {
		pm := &top.Points[i]
		if pm.Name != name {
			continue
		}
		if pm.Access != "RW" {
			return nil, fmt.Errorf("%w: %s of model %d has access %q", ErrPointNotWritable, name, inst.Header.ID, pm.Access)
		}
		if countsGroups(top, name) {
			// Also the count of a group nested further in, as the encoder
			// sees it.
			return nil, fmt.Errorf("%w: %s of model %d counts the instances of a group", ErrPointNotWritable, name, inst.Header.ID)
		}
		size, offset, sf, found = pm.Size, pm.Offset, pm.SF, true
		symbol = isSymbol(pm, value)
		break
	}
	if !found {
		return nil, fmt.Errorf("%w: no top-level point %q in model %d", ErrPointNotFound, name, inst.Header.ID)
	}

	dm, err := d.ReadModel(ctx, inst)
	if err != nil {
		return nil, err
	}
	if _, raw := value.([]uint16); !raw && !symbol && value != nil && sf != "" {
		if reason := unusableSF(dm, sf); reason != "" {
			return nil, fmt.Errorf("%w: %s of model %d is scaled by %s, %s; give its registers as []uint16 to write them unscaled",
				ErrPointNotWritable, name, inst.Header.ID, sf, reason)
		}
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

// isSymbol reports whether value is a symbol name of an enum point or a list
// of symbol names of a bitfield point: a raw value, which is never scaled.
func isSymbol(pm *registry.PointMeta, value any) bool {
	switch pm.Type {
	case "enum16", "enum32":
		_, ok := value.(string)
		return ok
	case "bitfield16", "bitfield32", "bitfield64":
		_, ok := value.([]string)
		return ok
	default:
		return false
	}
}

// unusableSF says why the scale factor sf of a top-level point of dm cannot
// scale a value, or returns "" when it can: a literal exponent, or a sunssf
// point of the top-level group that is implemented and within -10..10. It
// resolves the scale factor as the decoder does for a point that is
// implemented, whatever the point itself holds now.
func unusableSF(dm *DecodedModel, sf string) string {
	if _, ok := literalSF(sf); ok {
		return ""
	}
	if dm.Group == nil || dm.Group.Point(sf) == nil {
		return "which the model as read does not hold"
	}
	v, ok := (&scope{group: dm.Group}).scaleFactor(sf)
	switch {
	case !ok:
		return "which the device reports as not implemented"
	case !validScaleFactor(v):
		return fmt.Sprintf("which the device reports as %d, outside %d..%d", v, minScaleFactor, maxScaleFactor)
	default:
		return ""
	}
}
