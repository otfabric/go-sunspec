// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-modbus/batch"
)

// ModelResult is the outcome of reading one model instance with ReadModels.
type ModelResult struct {
	// Instance is the instance as given to ReadModels.
	Instance ModelInstance
	// Model is what ReadModel returns for the instance, partial registers
	// included. It is nil when the instance was not read because the batch
	// stopped before it.
	Model *DecodedModel
	// Err is the instance's error as ReadModel returns it (ErrModelChanged,
	// ErrPartialRead, a *DecodeError, a Modbus error), or one wrapping
	// batch.ErrNotAttempted and the reason the batch stopped. Nil on success.
	Err error
}

// ReadModels reads and decodes the given instances, in fewer requests than
// ReadModel when they are adjacent in the model chain, as the instances of
// Discovery.Models are. It returns one ModelResult per instance, in input
// order. The error is nil when every instance succeeded; otherwise it wraps
// the first failure in input order, and each ModelResult carries its own.
// An empty insts returns nil, nil.
//
// Adjacent instances share a request up to the Modbus limit of 125
// registers; no register outside the instances is read. Every request
// includes the header of each instance it serves, which is checked as
// ReadModel checks it. An instance whose header and data fit in one request
// (Length of 123 or less) is never split, so its values and scale factors
// come from one response. A longer instance gets requests of its own and
// fails with ErrPartialRead, keeping the registers read, when one of them
// fails. An instance given twice is read once and decoded for each. The requests are made one
// after another; they are not an atomic snapshot of the device.
//
// A Modbus exception fails only its request. When that request served several
// instances, as from a device that refuses reads across model boundaries,
// each of them is read again on its own, so every instance gets the result
// ReadModel would give it. A timeout or transport error ends the batch: the
// instances after it are not read. A cancelled or expired context ends it too.
//
// ReadModels uses the function codes of ReadFunctionCodes only.
func (d *Device) ReadModels(ctx context.Context, insts []ModelInstance) ([]ModelResult, error) {
	if len(insts) == 0 {
		return nil, nil
	}
	results := make([]ModelResult, len(insts))
	for i, inst := range insts {
		results[i].Instance = inst
	}
	if d.Client == nil {
		for i, inst := range insts {
			results[i].Model, results[i].Err = d.ReadModel(ctx, inst)
		}
		return results, firstFailure(results)
	}

	fc := modbus.FCReadHoldingRegisters
	if d.RegType == modbus.InputRegister {
		fc = modbus.FCReadInputRegisters
	}
	// One batch item per distinct instance: header and data registers.
	type key struct{ addr, id, length uint16 }
	slot := map[key]int{}
	var items []batch.Item
	itemOf := make([]int, len(insts))
	for i, inst := range insts {
		h := inst.Header
		if int(h.StartAddress)+int(h.Length)+2 > 0xFFFF+1 || int(h.Length)+2 > 0xFFFF {
			// ReadModel rejects it before any I/O.
			itemOf[i] = -1
			results[i].Model, results[i].Err = d.ReadModel(ctx, inst)
			continue
		}
		k := key{h.StartAddress, h.ID, h.Length}
		j, ok := slot[k]
		if !ok {
			j = len(items)
			slot[k] = j
			items = append(items, batch.Item{Name: strconv.Itoa(j), UnitID: d.UnitID, FC: fc, Addr: h.StartAddress, Count: h.Length + 2})
		}
		itemOf[i] = j
	}
	if len(items) == 0 {
		return results, firstFailure(results)
	}
	plan, err := batch.Compile(items, batch.Options{})
	if err != nil {
		return nil, fmt.Errorf("sunspec: plan model reads: %w", err)
	}
	res, _ := plan.Execute(ctx, d.Client) // every item carries its own outcome

	// Instances that shared a request a Modbus exception failed are read on
	// their own.
	alone := make([]bool, len(items))
	for _, w := range res.Windows {
		var exc *modbus.ExceptionError
		if w.State == batch.Failed && len(w.Items) > 1 && errors.As(w.Err, &exc) {
			for _, j := range w.Items {
				alone[j] = true
			}
		}
	}

	for i, inst := range insts {
		switch j := itemOf[i]; {
		case j < 0:
		case alone[j]:
			results[i].Model, results[i].Err = d.ReadModel(ctx, inst)
		default:
			results[i].Model, results[i].Err = itemModel(inst, res.Items[j])
		}
	}
	return results, firstFailure(results)
}

// itemModel turns the batch result of an instance's item into what ReadModel
// returns for the same registers and error.
func itemModel(inst ModelInstance, ir batch.ItemResult) (*DecodedModel, error) {
	switch ir.State {
	case batch.OK:
		return decodeRead(inst, slices.Clone(ir.Registers), nil)
	case batch.NotAttempted:
		return nil, fmt.Errorf("sunspec: model %d at address %d: %w", inst.Header.ID, inst.Header.StartAddress, ir.Err)
	default:
		err := ir.Err
		var we *batch.WindowError
		if errors.As(err, &we) {
			err = we.Err
		}
		// As readRange: a model needing several requests reports a failure
		// in any of them as a partial read.
		if int(inst.Header.Length)+2 > modbus.MaxReadRegisters {
			err = fmt.Errorf("%w: read at %d+%d: %w", ErrPartialRead, inst.Header.StartAddress, len(ir.Registers), err)
		}
		return decodeRead(inst, slices.Clone(ir.Registers), err)
	}
}

// firstFailure returns nil when every result succeeded, and otherwise an
// error wrapping the first failure in result order.
func firstFailure(results []ModelResult) error {
	failed := 0
	var first error
	for _, r := range results {
		if r.Err != nil {
			failed++
			if first == nil {
				first = r.Err
			}
		}
	}
	if first == nil {
		return nil
	}
	return fmt.Errorf("sunspec: %d of %d models failed: %w", failed, len(results), first)
}
