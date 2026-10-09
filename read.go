// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"

	"github.com/otfabric/go-modbus"
)

// maxRegistersPerRead is the Modbus limit on registers per read request.
const maxRegistersPerRead = 125

// readRegisters reads a contiguous register range, splitting into 125-register
// chunks as required by the Modbus protocol.
//
// A range that does not fit in the 16-bit register address space is rejected
// before any I/O. When a chunk after the first fails, the registers read so
// far are returned together with an error wrapping ErrPartialRead and the
// Modbus error.
func readRegisters(ctx context.Context, client *modbus.Client, unitID uint8, addr uint16, quantity uint16, regType modbus.RegType) ([]uint16, error) {
	if client == nil {
		return nil, errNilClient
	}
	if uint32(addr)+uint32(quantity) > 0x10000 {
		return nil, fmt.Errorf("sunspec: register range %d+%d exceeds the 16-bit address space: %w",
			addr, quantity, modbus.ErrUnexpectedParameters)
	}
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
