// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"

	"github.com/otfabric/go-modbus"
)

// ReadAll reads and decodes all discovered models with known schema.
// Unknown models are included with raw registers only.
//
// ReadAll does not stop at the first failure: it returns one entry per
// discovered model, in discovery order, together with the first error it
// met. The entry of a model that failed carries the failure in its Warnings
// and whatever registers could be read. A Device without models returns nil,
// nil.
func (d *Device) ReadAll(ctx context.Context) ([]*DecodedModel, error) {
	var results []*DecodedModel
	var firstErr error

	if d.Discovery == nil {
		return nil, nil
	}

	for _, inst := range d.Discovery.Models {
		dm, err := d.ReadModel(ctx, inst)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if dm != nil {
			results = append(results, dm)
		}
	}

	return results, firstErr
}

// ReadModel reads and decodes a specific model instance.
//
// It reads the model's two header registers at inst.Header.StartAddress and
// the inst.Header.Length data registers that follow, in as many Modbus
// requests as needed, and decodes them with inst.Schema. An instance without
// a schema yields a DecodedModel holding only RawRegisters and a warning,
// with a nil error.
//
// The header registers are compared with inst.Header. A device that no longer
// reports that model ID and length at that address has changed its model
// chain since discovery (a firmware update, a reconfiguration, a different
// device behind the same address), and decoding its registers with the old
// layout would yield values that look valid and are not: ReadModel then
// returns an error matching ErrModelChanged. Discover again to continue.
//
// On failure ReadModel returns a non-nil DecodedModel next to the error: for
// a read error it holds the data registers read so far (ErrPartialRead when
// a later chunk of a long model failed, otherwise the Modbus error as is);
// for a changed model it holds the data registers that were read, undecoded;
// for a device that reports fewer registers than the schema's fixed block
// needs it holds the raw registers and the error is a *DecodeError.
func (d *Device) ReadModel(ctx context.Context, inst ModelInstance) (*DecodedModel, error) {
	// StartAddress points to the model header (ID register); data starts 2 registers later.
	var (
		regs []uint16
		err  error
	)
	if dataAddr := uint32(inst.Header.StartAddress) + 2; dataAddr > 0xFFFF {
		err = fmt.Errorf("sunspec: model %d at address %d has no data registers within the 16-bit address space: %w",
			inst.Header.ID, inst.Header.StartAddress, modbus.ErrUnexpectedParameters)
	} else {
		regs, err = readRange(ctx, d.Client, d.UnitID, inst.Header.StartAddress, int(inst.Header.Length)+2, d.RegType)
	}
	return decodeRead(inst, regs, err)
}

// decodeRead checks and decodes what was read of inst: regs holds its header
// and data registers, or the part of them read before err.
func decodeRead(inst ModelInstance, regs []uint16, err error) (*DecodedModel, error) {
	// A successful read holds exactly the registers asked for; a short one is
	// treated like a header that does not match.
	total := int(inst.Header.Length) + 2
	if err == nil && (len(regs) != total || regs[0] != inst.Header.ID || regs[1] != inst.Header.Length) {
		err = fmt.Errorf("%w: address %d does not hold model %d of length %d (header read: %v)",
			ErrModelChanged, inst.Header.StartAddress, inst.Header.ID, inst.Header.Length, regs[:min(len(regs), 2)])
	}
	// What follows works on the data registers, as before the header was
	// read along.
	if len(regs) >= 2 {
		regs = regs[2:]
	} else {
		regs = nil
	}
	if err != nil {
		// Return partial result if we have some registers
		dm := &DecodedModel{
			ModelID:         inst.Header.ID,
			Name:            inst.Name,
			InstanceAddress: inst.Header.StartAddress,
			RawRegisters:    regs,
			Warnings:        []string{fmt.Sprintf("read error: %v", err)},
		}
		return dm, err
	}

	if !inst.SchemaKnown || inst.Schema == nil {
		return &DecodedModel{
			ModelID:         inst.Header.ID,
			Name:            inst.Name,
			InstanceAddress: inst.Header.StartAddress,
			RawRegisters:    regs,
			Warnings:        []string{"no schema available, returning raw registers only"},
		}, nil
	}

	// The SunSpec wire protocol's model header Length excludes the 2-register
	// header (ID + L), but the schema includes ID and L as the first two
	// points at offsets 0 and 1. Prepend them so the register slice matches
	// the schema layout.
	fullRegs := make([]uint16, len(regs)+2)
	fullRegs[0] = inst.Header.ID
	fullRegs[1] = inst.Header.Length
	copy(fullRegs[2:], regs)

	return DecodeModel(fullRegs, inst.Schema, inst.Header.StartAddress)
}

// ReadModelByID reads the first instance of the given model ID. It returns an
// error wrapping ErrUnknownModel when the device does not expose that model,
// and otherwise behaves like ReadModel.
func (d *Device) ReadModelByID(ctx context.Context, modelID uint16) (*DecodedModel, error) {
	inst := d.ModelByID(modelID)
	if inst == nil {
		return nil, fmt.Errorf("%w: model %d not found in discovery", ErrUnknownModel, modelID)
	}
	return d.ReadModel(ctx, *inst)
}

// ReadPoint reads a single named point from a model instance.
//
// The whole model is read and decoded, so the point's scale factor is
// resolved. The top-level group is searched first, then the nested group
// instances depth first in register order; the first point with a matching
// (case-sensitive) name wins, so for a point of a repeating group this is the
// one of the first instance. ReadPoint returns an error wrapping
// ErrPointNotFound when no decoded point has that name, which includes every
// point of a model without a schema, and the ReadModel error when the model
// cannot be read.
func (d *Device) ReadPoint(ctx context.Context, inst ModelInstance, pointName string) (*DecodedPoint, error) {
	dm, err := d.ReadModel(ctx, inst)
	if err != nil {
		return nil, err
	}
	if p := dm.Point(pointName); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("%w: point %q in model %d", ErrPointNotFound, pointName, inst.Header.ID)
}
