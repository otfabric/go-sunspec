// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

// modbusExceptions are the errors a Server may answer a request with.
var modbusExceptions = []error{
	modbus.ErrIllegalFunction, modbus.ErrIllegalDataAddress, modbus.ErrIllegalDataValue,
	modbus.ErrServerDeviceFailure, modbus.ErrNoResponse, modbus.ErrGWTargetFailedToRespond,
}

func isException(err error) bool {
	for _, e := range modbusExceptions {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// FuzzServerRequest sends arbitrary holding register requests, as a hostile
// client may, to the ctl test device. The Server must not panic; it answers
// with a Modbus exception or with exactly the registers of its map; a refused
// write changes nothing, an accepted one changes only the registers written
// and leaves a map whose models still decode.
func FuzzServerRequest(f *testing.F) {
	f.Add(uint8(1), uint16(40000), uint16(10), false, []byte{})
	f.Add(uint8(1), uint16(40005), uint16(1), true, []byte{0, 33})
	f.Add(uint8(1), uint16(40006), uint16(4), true, []byte{0, 2, 0, 3, 0, 77, 0xFF, 0xFE})
	f.Add(uint8(1), uint16(40009), uint16(1), true, []byte{0x7F, 0xFF})
	f.Add(uint8(1), uint16(0xFFFF), uint16(0xFFFF), false, []byte{})
	f.Add(uint8(0), uint16(40005), uint16(1), true, []byte{0, 1})
	f.Add(uint8(1), uint16(40005), uint16(3), true, []byte{0, 1})
	f.Add(uint8(1), uint16(40000), uint16(0), false, []byte{})

	f.Fuzz(func(t *testing.T, unit uint8, addr, quantity uint16, write bool, data []byte) {
		srv := ctlServer(t, nil)
		snapshot := func() []uint16 {
			srv.mu.RLock()
			defer srv.mu.RUnlock()
			return append([]uint16(nil), srv.image...)
		}
		before := snapshot()
		req := &modbus.HoldingRegistersRequest{UnitID: unit, Addr: addr, Quantity: quantity, IsWrite: write, ClientAddr: "fuzz"}
		if write {
			req.Args = make([]uint16, len(data)/2)
			for i := range req.Args {
				req.Args[i] = binary.BigEndian.Uint16(data[2*i:])
			}
		}
		regs, err := srv.HandleHoldingRegisters(context.Background(), req)
		after := snapshot()
		if err != nil {
			if !isException(err) {
				t.Fatalf("not a Modbus exception: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("a refused request changed the map")
			}
			return
		}
		lo := int(addr) - int(srv.base)
		if !write {
			if len(regs) != int(quantity) || lo < 0 || !reflect.DeepEqual(regs, before[lo:lo+len(regs)]) {
				t.Fatalf("read %d+%d = %v", addr, quantity, regs)
			}
			return
		}
		for i := range after {
			inside := i >= lo && i < lo+len(req.Args)
			if inside && after[i] != req.Args[i-lo] || !inside && after[i] != before[i] {
				t.Fatalf("write %d+%d: register %d is %d", addr, len(req.Args), i, after[i])
			}
		}
		for i := range srv.Models() {
			if _, err := srv.Model(i); err != nil {
				t.Fatalf("model %d after the write: %v", i, err)
			}
		}
		// The values held for the model encode again.
		if err := srv.Update(0, nil); err != nil {
			t.Fatalf("Update after the write: %v", err)
		}
	})
}

// FuzzDiscover serves arbitrary registers behind the "SunS" marker at base
// address 40000 and discovers and reads them. Nothing may panic or hang, and
// a device that is found lies inside what was served.
func FuzzDiscover(f *testing.F) {
	f.Add([]byte{0, 1, 0, 66})
	f.Add(registerBytes([]uint16{64900, 2, 0, 0, 0xFFFF, 0}))
	f.Add(registerBytes([]uint16{64900, 0, 0xFFFF, 0}))
	f.Add(registerBytes([]uint16{64900, 0xFFFF, 0xFFFF, 0}))
	f.Add(registerBytes([]uint16{103, 5, 1, 2, 3, 4, 5, 0xFFFF, 0}))
	f.Add(registerBytes([]uint16{64030, 2, 5, 0x7FFF, 0xFFFF, 0}))

	d := &memDevice{base: 40000}
	client, _ := hostileClient(f, d)
	f.Fuzz(func(t *testing.T, data []byte) {
		regs := []uint16{0x5375, 0x6E53}
		for i := 0; i+1 < len(data) && len(regs) < 4096; i += 2 {
			regs = append(regs, binary.BigEndian.Uint16(data[i:]))
		}
		d.mu.Lock()
		d.regs = regs
		d.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dev, err := Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{40000}})
		if err != nil {
			if dev != nil {
				t.Fatal("a device next to an error")
			}
			if ctx.Err() != nil {
				t.Fatalf("Discover hung: %v", err)
			}
			return
		}
		end := 40000 + len(regs)
		for _, m := range dev.Discovery.Models {
			if int(m.Header.StartAddress) < 40002 || int(m.Header.StartAddress)+int(m.Header.Length)+2 > end {
				t.Fatalf("model %+v outside the %d registers served", m.Header, len(regs))
			}
		}
		results, _ := dev.ReadAll(ctx)
		if ctx.Err() != nil {
			t.Fatal("ReadAll hung")
		}
		if len(results) != len(dev.Discovery.Models) {
			t.Fatalf("%d results for %d models", len(results), len(dev.Discovery.Models))
		}
	})
}
