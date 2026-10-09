// SPDX-License-Identifier: MIT

// Package testutil provides an in-process SunSpec Modbus TCP fixture for
// tests: a register map builder, a read-only request handler serving it, and
// a helper that starts a server with a connected client.
package testutil

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
)

// FixtureModel defines one model's register data for a test fixture.
type FixtureModel struct {
	// ID is the SunSpec model ID written to the model header.
	ID uint16
	// Length is the model length written to the model header: the number of
	// data registers, excluding the two header registers. The next model
	// header is placed Length registers after this model's data starts.
	Length uint16
	// Registers holds the data registers that follow the header. It may be
	// shorter than Length, which leaves the remaining addresses unmapped so
	// that reading them fails with an illegal data address exception.
	Registers []uint16
}

// SunSpecHandler implements modbus.RequestHandler for testing. It serves
// holding register reads from a sparse register map and rejects everything
// else.
//
// The handler does not lock Registers: populate the map before the server
// starts and do not modify it while requests may be served.
type SunSpecHandler struct {
	// Registers maps a zero-based register address to its value. Reading an
	// address that is not in the map fails with modbus.ErrIllegalDataAddress.
	Registers map[uint16]uint16
	// UnitID is the only Modbus unit ID the handler answers.
	UnitID uint8
}

// HandleCoils rejects every coil request with modbus.ErrIllegalFunction.
func (h *SunSpecHandler) HandleCoils(_ context.Context, _ *modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

// HandleDiscreteInputs rejects every discrete input request with
// modbus.ErrIllegalFunction.
func (h *SunSpecHandler) HandleDiscreteInputs(_ context.Context, _ *modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

// HandleHoldingRegisters serves holding register reads from Registers.
//
// It returns modbus.ErrIllegalFunction for a write or for a unit ID other
// than UnitID, and modbus.ErrIllegalDataAddress when any requested address is
// not in Registers.
func (h *SunSpecHandler) HandleHoldingRegisters(_ context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req.UnitID != h.UnitID {
		return nil, modbus.ErrIllegalFunction
	}
	if req.IsWrite {
		return nil, modbus.ErrIllegalFunction
	}

	res := make([]uint16, req.Quantity)
	for i := uint16(0); i < req.Quantity; i++ {
		v, ok := h.Registers[req.Addr+i]
		if !ok {
			return nil, modbus.ErrIllegalDataAddress
		}
		res[i] = v
	}
	return res, nil
}

// HandleInputRegisters rejects every input register request with
// modbus.ErrIllegalFunction.
func (h *SunSpecHandler) HandleInputRegisters(_ context.Context, _ *modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

// NewSunSpecFixture builds a register map with SunSpec marker, model headers, and data.
//
// The "SunS" marker is written at baseAddr, followed by each model (header,
// then data) in the given order and the end-of-chain marker (0xFFFF, 0). The
// returned handler answers only unitID. Addresses are not checked for
// overflow of the 16-bit address space.
func NewSunSpecFixture(baseAddr uint16, unitID uint8, models ...FixtureModel) *SunSpecHandler {
	h := &SunSpecHandler{
		Registers: make(map[uint16]uint16),
		UnitID:    unitID,
	}

	addr := baseAddr

	// Write SunS marker
	h.Registers[addr] = gmsunspec.MarkerReg0
	h.Registers[addr+1] = gmsunspec.MarkerReg1
	addr += 2

	for _, m := range models {
		// Model header: ID, Length
		h.Registers[addr] = m.ID
		h.Registers[addr+1] = m.Length
		addr += 2

		// Model data
		for i, r := range m.Registers {
			h.Registers[addr+uint16(i)] = r
		}
		addr += m.Length
	}

	// End model marker
	h.Registers[addr] = gmsunspec.EndModelID
	h.Registers[addr+1] = gmsunspec.EndModelLength

	return h
}

// StartServerClient creates a test server/client pair. Returns cleanup function.
//
// The server listens on tcp://127.0.0.1:<port> and accepts up to two clients,
// one of which is the returned, already opened client. Any failure to start
// the server or connect fails the test immediately with t.Fatalf. The caller
// must call the cleanup function, which closes the client and stops the
// server; calling it more than once is harmless.
func StartServerClient(t *testing.T, handler *SunSpecHandler, port int) (*modbus.Client, func()) {
	t.Helper()

	url := "tcp://127.0.0.1:" + itoa(port)

	server, err := modbus.NewServer(&modbus.ServerConfig{
		URL:        url,
		MaxClients: 2,
	}, handler)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	client, err := modbus.New(modbus.Config{URL: url})
	if err != nil {
		_ = server.Stop()
		t.Fatalf("failed to create client: %v", err)
	}
	if err := client.Open(); err != nil {
		_ = server.Stop()
		t.Fatalf("failed to open client: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		_ = server.Stop()
	}
	return client, cleanup
}

// itoa formats a non-negative integer in decimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf) - 1
	for n > 0 {
		buf[i] = byte('0' + n%10)
		n /= 10
		i--
	}
	return string(buf[i+1:])
}

// StringToRegisters encodes a string into big-endian registers, padded with NULs.
//
// The result always has regCount registers: s is truncated to regCount*2
// bytes when it is longer. This is the SunSpec "string" point encoding.
func StringToRegisters(s string, regCount int) []uint16 {
	buf := make([]byte, regCount*2)
	copy(buf, []byte(s))
	regs := make([]uint16, regCount)
	for i := range regs {
		regs[i] = binary.BigEndian.Uint16(buf[i*2 : i*2+2])
	}
	return regs
}
