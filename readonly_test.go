// SPDX-License-Identifier: MIT

package sunspec

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

func TestReadFunctionCodes(t *testing.T) {
	want := []modbus.FunctionCode{modbus.FCReadHoldingRegisters, modbus.FCReadInputRegisters}
	got := ReadFunctionCodes()
	if !reflect.DeepEqual(got, want) || got[0] != 0x03 || got[1] != 0x04 {
		t.Fatalf("ReadFunctionCodes() = %v, want %v", got, want)
	}
	// Each call returns its own slice.
	got[0] = modbus.FCWriteMultipleRegisters
	if again := ReadFunctionCodes(); !reflect.DeepEqual(again, want) {
		t.Errorf("after changing a returned slice: %v", again)
	}
}

// readOnlyClient connects a client that sends ReadFunctionCodes only.
func readOnlyClient(t *testing.T, addr string) *modbus.Client {
	t.Helper()
	client, err := modbus.New(modbus.Config{
		URL: "tcp://" + addr, Timeout: 5 * time.Second, DialTimeout: 2 * time.Second, Logger: modbus.NopLogger(),
		AllowedFunctionCodes: ReadFunctionCodes(),
	})
	if err != nil {
		t.Fatalf("modbus.New: %v", err)
	}
	if err := client.Open(); err != nil {
		t.Fatalf("client.Open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestReadOnlyClient runs every client path of the package through a client
// that refuses all function codes but ReadFunctionCodes: none of them is
// refused, so none sends another code, for holding and for input registers.
func TestReadOnlyClient(t *testing.T) {
	for name, regType := range map[string]modbus.RegType{"holding": modbus.HoldingRegister, "input": modbus.InputRegister} {
		t.Run(name, func(t *testing.T) {
			ctx := testCtx(t)
			// Model 802 is longer than one request: the read is split.
			d := newServed(t, 7, &ServerOptions{BaseAddress: 40000, RegType: regType}, 1, 103, 160, 123, 802)
			addr, _ := startModbus(t, d.srv)
			client := readOnlyClient(t, addr)
			opts := &DiscoverOptions{RegType: regType}

			det, err := Detect(ctx, client, opts)
			if err != nil || !det.Detected {
				t.Fatalf("Detect: %+v, %v", det, err)
			}
			dev, err := Open(ctx, client, opts)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			results, err := dev.ReadAll(ctx)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			d.checkAll(t, results)
			dm, err := dev.ReadModelByID(ctx, 103)
			if err != nil {
				t.Fatalf("ReadModelByID: %v", err)
			}
			d.checkModel(t, 1, dm)
			if _, err := dev.ReadPoint(ctx, dev.Discovery.Models[0], "Mn"); err != nil {
				t.Errorf("ReadPoint: %v", err)
			}

			// The same client cannot write, and the device is not asked.
			before, err := dev.ReadModelByID(ctx, 123)
			if err != nil {
				t.Fatal(err)
			}
			ctl := dev.ModelByID(123).Header.StartAddress
			if err := client.WriteRegister(ctx, 1, ctl+5, 505); !errors.Is(err, modbus.ErrFunctionCodeNotAllowed) {
				t.Errorf("WriteRegister = %v, want ErrFunctionCodeNotAllowed", err)
			}
			if err := client.WriteRegisters(ctx, 1, ctl+5, []uint16{1, 2}); !errors.Is(err, modbus.ErrFunctionCodeNotAllowed) {
				t.Errorf("WriteRegisters = %v, want ErrFunctionCodeNotAllowed", err)
			}
			after, err := dev.ReadModelByID(ctx, 123)
			if err != nil || !reflect.DeepEqual(before.RawRegisters, after.RawRegisters) {
				t.Errorf("the device changed after refused writes (err %v)", err)
			}
		})
	}
}
