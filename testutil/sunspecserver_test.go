// SPDX-License-Identifier: MIT

package testutil

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
)

// freePort returns a TCP port on 127.0.0.1 that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}

func TestStringToRegisters(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		regCount int
		want     []uint16
	}{
		{"even length", "ABCD", 2, []uint16{0x4142, 0x4344}},
		{"odd length is NUL padded", "ABC", 2, []uint16{0x4142, 0x4300}},
		{"shorter than the field", "A", 3, []uint16{0x4100, 0, 0}},
		{"empty string", "", 2, []uint16{0, 0}},
		{"longer than the field is truncated", "ABCDEF", 2, []uint16{0x4142, 0x4344}},
		{"zero registers", "ABC", 0, []uint16{}},
		{"high bytes", "\xff\x01", 1, []uint16{0xFF01}},
	}
	for _, tc := range tests {
		got := StringToRegisters(tc.s, tc.regCount)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: StringToRegisters(%q, %d) = %#v, want %#v", tc.name, tc.s, tc.regCount, got, tc.want)
		}
	}
}

func TestItoa(t *testing.T) {
	for _, n := range []int{0, 1, 9, 10, 502, 15020, 65535, 1234567890} {
		if got, want := itoa(n), strconv.Itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestNewSunSpecFixtureLayout(t *testing.T) {
	h := NewSunSpecFixture(100, 5,
		FixtureModel{ID: 1, Length: 3, Registers: []uint16{11, 12, 13}},
		// Fewer registers than the announced length: the rest stays unmapped.
		FixtureModel{ID: 64000, Length: 4, Registers: []uint16{21, 22}},
		// A model without data.
		FixtureModel{ID: 64001, Length: 2},
	)
	if h.UnitID != 5 {
		t.Errorf("UnitID = %d, want 5", h.UnitID)
	}
	want := map[uint16]uint16{
		100: 0x5375, 101: 0x6E53, // "SunS"
		102: 1, 103: 3, 104: 11, 105: 12, 106: 13,
		107: 64000, 108: 4, 109: 21, 110: 22, // 111, 112 unmapped
		113: 64001, 114: 2, // 115, 116 unmapped
		117: 0xFFFF, 118: 0, // end marker
	}
	if !reflect.DeepEqual(h.Registers, want) {
		t.Errorf("Registers = %v\nwant %v", h.Registers, want)
	}
}

func TestNewSunSpecFixtureWithoutModels(t *testing.T) {
	h := NewSunSpecFixture(0, 1)
	want := map[uint16]uint16{
		0: gmsunspec.MarkerReg0, 1: gmsunspec.MarkerReg1,
		2: gmsunspec.EndModelID, 3: gmsunspec.EndModelLength,
	}
	if !reflect.DeepEqual(h.Registers, want) {
		t.Errorf("Registers = %v, want %v", h.Registers, want)
	}
}

func TestHandlerHoldingRegisters(t *testing.T) {
	h := &SunSpecHandler{UnitID: 3, Registers: map[uint16]uint16{10: 100, 11: 101, 12: 102, 0xFFFF: 7, 0: 8}}
	ctx := context.Background()

	tests := []struct {
		name    string
		req     modbus.HoldingRegistersRequest
		want    []uint16
		wantErr error
	}{
		{"read range", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 10, Quantity: 3}, []uint16{100, 101, 102}, nil},
		{"read single", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 11, Quantity: 1}, []uint16{101}, nil},
		{"zero quantity", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 500, Quantity: 0}, []uint16{}, nil},
		{"range starts unmapped", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 9, Quantity: 2}, nil, modbus.ErrIllegalDataAddress},
		{"range ends unmapped", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 12, Quantity: 2}, nil, modbus.ErrIllegalDataAddress},
		{"entirely unmapped", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 500, Quantity: 1}, nil, modbus.ErrIllegalDataAddress},
		{"other unit", modbus.HoldingRegistersRequest{UnitID: 4, Addr: 10, Quantity: 1}, nil, modbus.ErrIllegalFunction},
		{"write", modbus.HoldingRegistersRequest{UnitID: 3, Addr: 10, Quantity: 1, IsWrite: true, Args: []uint16{1}}, nil, modbus.ErrIllegalFunction},
	}
	for _, tc := range tests {
		req := tc.req
		got, err := h.HandleHoldingRegisters(ctx, &req)
		if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}
	// A rejected write leaves the register map alone.
	if h.Registers[10] != 100 {
		t.Errorf("register 10 = %d after a rejected write, want 100", h.Registers[10])
	}
}

func TestHandlerRejectsOtherFunctions(t *testing.T) {
	h := NewSunSpecFixture(0, 1)
	ctx := context.Background()

	if got, err := h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: 1, Quantity: 1}); got != nil || !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("HandleCoils = %v, %v; want ErrIllegalFunction", got, err)
	}
	if got, err := h.HandleDiscreteInputs(ctx, &modbus.DiscreteInputsRequest{UnitID: 1, Quantity: 1}); got != nil || !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("HandleDiscreteInputs = %v, %v; want ErrIllegalFunction", got, err)
	}
	if got, err := h.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{UnitID: 1, Quantity: 1}); got != nil || !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("HandleInputRegisters = %v, %v; want ErrIllegalFunction", got, err)
	}
	// The handlers ignore their arguments entirely.
	if _, err := h.HandleCoils(ctx, nil); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("HandleCoils(nil request) = %v", err)
	}
}

func TestStartServerClientRoundTrip(t *testing.T) {
	model := FixtureModel{ID: 64000, Length: 3, Registers: []uint16{7, 8, 9}}
	h := NewSunSpecFixture(40000, 2, model)
	port := freePort(t)
	client, cleanup := StartServerClient(t, h, port)
	if client == nil || cleanup == nil {
		t.Fatal("StartServerClient returned a nil client or cleanup function")
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The whole fixture, marker to end marker, in one read.
	got, err := client.ReadRegisters(ctx, 2, 40000, 9, modbus.HoldingRegister)
	if err != nil {
		t.Fatalf("ReadRegisters: %v", err)
	}
	want := []uint16{0x5375, 0x6E53, 64000, 3, 7, 8, 9, 0xFFFF, 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("registers = %#v, want %#v", got, want)
	}

	// The handler's exceptions travel over the wire as Modbus exceptions.
	if _, err := client.ReadRegisters(ctx, 2, 40000, 10, modbus.HoldingRegister); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read past the fixture: err = %v, want ErrIllegalDataAddress", err)
	}
	if _, err := client.ReadRegisters(ctx, 1, 40000, 2, modbus.HoldingRegister); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("read with another unit ID: err = %v, want ErrIllegalFunction", err)
	}
	if _, err := client.ReadRegisters(ctx, 2, 40000, 2, modbus.InputRegister); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("read input registers: err = %v, want ErrIllegalFunction", err)
	}
	if err := client.WriteRegister(ctx, 2, 40004, 1); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("write: err = %v, want ErrIllegalFunction", err)
	}
	if _, err := client.ReadCoils(ctx, 2, 0, 1); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("read coils: err = %v, want ErrIllegalFunction", err)
	}
	if _, err := client.ReadDiscreteInputs(ctx, 2, 0, 1); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("read discrete inputs: err = %v, want ErrIllegalFunction", err)
	}

	// The fixture is a SunSpec device as far as go-modbus is concerned.
	res, err := gmsunspecDiscover(ctx, client, 2, 40000)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !res.Detection.Detected || len(res.Models) != 2 || res.Models[0].ID != 64000 || !res.Models[1].IsEndModel {
		t.Errorf("discovery = %+v", res)
	}

	// A second client fits (the server accepts two).
	second, err := modbus.New(modbus.Config{URL: "tcp://127.0.0.1:" + strconv.Itoa(port)})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Open(); err != nil {
		t.Fatalf("second client: %v", err)
	}
	if v, err := second.ReadRegister(ctx, 2, 40004, modbus.HoldingRegister); err != nil || v != 7 {
		t.Errorf("second client read = %d, %v; want 7", v, err)
	}
	_ = second.Close()

	// After cleanup the server is gone; calling cleanup again is harmless.
	cleanup()
	cleanup()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Error("server still accepts connections after cleanup")
	}
	if _, err := client.ReadRegisters(ctx, 2, 40000, 2, modbus.HoldingRegister); err == nil {
		t.Error("client still works after cleanup")
	}
}

// reader adapts a modbus client to the go-modbus SunSpec helpers.
type reader struct{ c *modbus.Client }

func (r reader) ReadRawBytes(ctx context.Context, unitID uint8, addr, byteCount uint16, regType gmsunspec.RegType) ([]byte, error) {
	return r.c.ReadRegisterBytes(ctx, unitID, addr, byteCount, regType)
}

func gmsunspecDiscover(ctx context.Context, c *modbus.Client, unitID uint8, base uint16) (*gmsunspec.DiscoveryResult, error) {
	return gmsunspec.Discover(ctx, reader{c}, &gmsunspec.Options{UnitID: unitID, BaseAddresses: []uint16{base}})
}
