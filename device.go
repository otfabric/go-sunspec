// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"
)

// ReadAll reads and decodes all discovered models with known schema.
// Unknown models are included with raw registers only.
func (d *Device) ReadAll(ctx context.Context) ([]*DecodedModel, error) {
	var results []*DecodedModel
	var firstErr error

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
func (d *Device) ReadModel(ctx context.Context, inst ModelInstance) (*DecodedModel, error) {
	// StartAddress points to the model header (ID register); data starts 2 registers later.
	dataAddr := inst.Header.StartAddress + 2
	regs, err := readRegisters(ctx, d.Client, d.UnitID, dataAddr, inst.Header.Length, d.RegType)
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

// ReadModelByID reads the first instance of the given model ID.
func (d *Device) ReadModelByID(ctx context.Context, modelID uint16) (*DecodedModel, error) {
	inst := d.ModelByID(modelID)
	if inst == nil {
		return nil, fmt.Errorf("%w: model %d not found in discovery", ErrUnknownModel, modelID)
	}
	return d.ReadModel(ctx, *inst)
}

// ReadPoint reads a single named point from a model instance.
func (d *Device) ReadPoint(ctx context.Context, inst ModelInstance, pointName string) (*DecodedPoint, error) {
	dm, err := d.ReadModel(ctx, inst)
	if err != nil {
		return nil, err
	}

	// Search fixed block
	if dm.FixedBlock != nil {
		for i := range dm.FixedBlock.Points {
			if dm.FixedBlock.Points[i].Name == pointName {
				return &dm.FixedBlock.Points[i], nil
			}
		}
	}

	// Search repeating blocks
	for _, rb := range dm.RepeatingBlocks {
		for i := range rb.Points {
			if rb.Points[i].Name == pointName {
				return &rb.Points[i], nil
			}
		}
	}

	return nil, fmt.Errorf("%w: point %q in model %d", ErrPointNotFound, pointName, inst.Header.ID)
}
