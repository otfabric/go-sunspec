// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"

	"github.com/otfabric/go-modbus"
)

const maxRegistersPerRead = 125

// readRegisters reads a contiguous register range, splitting into 125-register
// chunks as required by the Modbus protocol.
func readRegisters(ctx context.Context, client *modbus.Client, unitID uint8, addr uint16, quantity uint16, regType modbus.RegType) ([]uint16, error) {
	if quantity <= maxRegistersPerRead {
		return client.ReadRegisters(ctx, unitID, addr, quantity, regType)
	}

	result := make([]uint16, 0, quantity)
	remaining := quantity
	offset := uint16(0)

	for remaining > 0 {
		chunk := remaining
		if chunk > maxRegistersPerRead {
			chunk = maxRegistersPerRead
		}
		regs, err := client.ReadRegisters(ctx, unitID, addr+offset, chunk, regType)
		if err != nil {
			return result, fmt.Errorf("%w: read at %d+%d: %w", ErrPartialRead, addr, offset, err)
		}
		result = append(result, regs...)
		offset += chunk
		remaining -= chunk
	}

	return result, nil
}
