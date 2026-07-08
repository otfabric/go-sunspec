// SPDX-License-Identifier: MIT

package sunspec

import (
	"fmt"

	"github.com/otfabric/go-sunspec/registry"
)

// DecodeModel decodes a register slice into a DecodedModel using the given schema.
func DecodeModel(regs []uint16, meta *registry.ModelMeta, instanceAddr uint16) (*DecodedModel, error) {
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
		dm.Warnings = append(dm.Warnings,
			fmt.Sprintf("register slice too short for fixed block: have %d, need %d", len(regs), fixedLen))
		return dm, fmt.Errorf("%w: register slice too short for fixed block", ErrDecode)
	}

	if meta.FixedBlock != nil {
		fb, warns := decodeBlock(regs[:fixedLen], meta.FixedBlock, 0)
		dm.FixedBlock = fb
		dm.Warnings = append(dm.Warnings, warns...)
	}

	repeatLen := meta.RepeatingLength()
	if repeatLen > 0 && meta.RepeatingBlock != nil {
		remaining := regs[fixedLen:]
		instanceCount := len(remaining) / repeatLen
		for i := 0; i < instanceCount; i++ {
			start := i * repeatLen
			end := start + repeatLen
			rb, warns := decodeBlock(remaining[start:end], meta.RepeatingBlock, i+1)
			dm.RepeatingBlocks = append(dm.RepeatingBlocks, rb)
			dm.Warnings = append(dm.Warnings, warns...)
		}
		leftover := len(remaining) % repeatLen
		if leftover != 0 {
			dm.Warnings = append(dm.Warnings,
				fmt.Sprintf("repeating block: %d leftover registers (not a multiple of %d)", leftover, repeatLen))
		}
	}

	// resolve scale factors
	resolveSF(dm)

	return dm, nil
}

func decodeBlock(regs []uint16, gm *registry.GroupMeta, groupIndex int) (*DecodedBlock, []string) {
	block := &DecodedBlock{GroupIndex: groupIndex}
	var warnings []string
	for _, pm := range gm.Points {
		if pm.Offset+pm.Size > len(regs) {
			warnings = append(warnings,
				fmt.Sprintf("point %s: offset %d+size %d exceeds register slice length %d",
					pm.Name, pm.Offset, pm.Size, len(regs)))
			continue
		}
		slice := regs[pm.Offset : pm.Offset+pm.Size]
		dp, warn := decodePoint(slice, &pm)
		block.Points = append(block.Points, dp)
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	return block, warnings
}
