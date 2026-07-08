// SPDX-License-Identifier: MIT

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
	ID        uint16
	Length    uint16
	Registers []uint16
}

// SunSpecHandler implements modbus.RequestHandler for testing.
type SunSpecHandler struct {
	Registers map[uint16]uint16
	UnitID    uint8
}

func (h *SunSpecHandler) HandleCoils(_ context.Context, _ *modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *SunSpecHandler) HandleDiscreteInputs(_ context.Context, _ *modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

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

func (h *SunSpecHandler) HandleInputRegisters(_ context.Context, _ *modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

// NewSunSpecFixture builds a register map with SunSpec marker, model headers, and data.
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
func StringToRegisters(s string, regCount int) []uint16 {
	buf := make([]byte, regCount*2)
	copy(buf, []byte(s))
	regs := make([]uint16, regCount)
	for i := range regs {
		regs[i] = binary.BigEndian.Uint16(buf[i*2 : i*2+2])
	}
	return regs
}
