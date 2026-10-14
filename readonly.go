// SPDX-License-Identifier: MIT

package sunspec

import "github.com/otfabric/go-modbus"

// ReadFunctionCodes returns the Modbus function codes the client side of
// this package sends: Read Holding Registers (0x03) and Read Input Registers
// (0x04). Detect, Discover, Open and every Device method read registers and
// do nothing else.
//
// Give the list to modbus.Config.AllowedFunctionCodes (go-modbus v1.5.0 or
// later) for a client that cannot write: the client then refuses any other
// function code with modbus.ErrFunctionCodeNotAllowed before a byte is sent,
// whoever asks for it.
//
//	client, err := modbus.New(modbus.Config{
//		URL:                  "tcp://192.168.1.100:502",
//		AllowedFunctionCodes: sunspec.ReadFunctionCodes(),
//	})
//
// Each call returns a new slice.
func ReadFunctionCodes() []modbus.FunctionCode {
	return []modbus.FunctionCode{modbus.FCReadHoldingRegisters, modbus.FCReadInputRegisters}
}
