// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

// memDevice is a hostile device: a register map that answers holding
// register reads with whatever it holds, without any check of its own. A read
// that touches an address outside the map is answered with exception 2.
type memDevice struct {
	mu   sync.Mutex
	base uint16
	regs []uint16
	// delay is waited before each answer, or until the request's context
	// ends.
	delay atomic.Int64
	// onRead, when not nil, is called with the number of the read (from 1)
	// before it is answered.
	onRead func(n int64)
	reads  atomic.Int64
}

func (d *memDevice) HandleCoils(context.Context, *modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (d *memDevice) HandleDiscreteInputs(context.Context, *modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (d *memDevice) HandleInputRegisters(context.Context, *modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalDataAddress
}

func (d *memDevice) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req.IsWrite {
		return nil, modbus.ErrIllegalFunction
	}
	n := d.reads.Add(1)
	if d.onRead != nil {
		d.onRead(n)
	}
	if delay := time.Duration(d.delay.Load()); delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, modbus.ErrServerDeviceFailure
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	lo := int(req.Addr) - int(d.base)
	hi := lo + int(req.Quantity)
	if lo < 0 || hi > len(d.regs) {
		return nil, modbus.ErrIllegalDataAddress
	}
	return append([]uint16(nil), d.regs[lo:hi]...), nil
}

// sunsMap returns a register map at base 40000: the marker, then the given
// registers.
func sunsMap(regs ...uint16) *memDevice {
	return &memDevice{base: 40000, regs: append([]uint16{0x5375, 0x6E53}, regs...)}
}

// hostileClient serves d on loopback and returns a client with a short
// timeout.
func hostileClient(t testing.TB, d *memDevice) (*modbus.Client, *modbus.Server) {
	t.Helper()
	addr, ms := startModbus(t, d)
	return dial(t, addr, 2*time.Second), ms
}

func hostileDiscover(t *testing.T, client *modbus.Client) (*Device, error) {
	t.Helper()
	return bounded(t, "Discover", func(ctx context.Context) (*Device, error) {
		return Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{40000}})
	})
}

// A marker that is almost "SunS", or missing, is no SunSpec device; a marker
// whose chain cannot be read is not reported as one either.
func TestHostileMarker(t *testing.T) {
	t.Cleanup(leakCheck(t))
	for _, tc := range []struct {
		name string
		regs []uint16
	}{
		{"SunT", []uint16{0x5375, 0x6E54, 1, 66}},
		{"swapped", []uint16{0x6E53, 0x5375, 1, 66}},
		{"byte order", []uint16{0x7553, 0x536E, 1, 66}},
		{"all ones", []uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF}},
		{"all zero", []uint16{0, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := hostileClient(t, &memDevice{base: 40000, regs: tc.regs})
			if _, err := hostileDiscover(t, client); !errors.Is(err, ErrNotSunSpec) {
				t.Errorf("Discover err = %v, want ErrNotSunSpec", err)
			}
			det, err := Detect(context.Background(), client, &DiscoverOptions{BaseAddresses: []uint16{40000}})
			if !errors.Is(err, ErrNotSunSpec) || det == nil || det.Detected {
				t.Errorf("Detect = %+v, %v", det, err)
			}
		})
	}
	// Only the marker: the chain runs into unmapped registers.
	client, _ := hostileClient(t, sunsMap())
	if dev, err := hostileDiscover(t, client); err == nil || dev != nil || errors.Is(err, ErrNotSunSpec) {
		t.Errorf("marker only: %v, %v", dev, err)
	}
	// Half a marker.
	client, _ = hostileClient(t, &memDevice{base: 40000, regs: []uint16{0x5375}})
	if dev, err := hostileDiscover(t, client); err == nil || dev != nil {
		t.Errorf("half a marker: %v, %v", dev, err)
	}
}

// A chain whose end marker is missing, cut at the end of the map or broken by
// a zero length, yields no device.
func TestHostileChainEnds(t *testing.T) {
	t.Cleanup(leakCheck(t))
	for _, tc := range []struct {
		name string
		regs []uint16
		want error
	}{
		{"no end marker", []uint16{64900, 2, 0, 0}, nil},
		{"end marker cut", []uint16{64900, 2, 0, 0, 0xFFFF}, nil},
		{"zero length", []uint16{64900, 0, 0xFFFF, 0}, ErrModelChainInvalid},
		{"length past the map", []uint16{64900, 1000, 0, 0, 0xFFFF, 0}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := hostileClient(t, sunsMap(tc.regs...))
			dev, err := hostileDiscover(t, client)
			if err == nil || dev != nil {
				t.Fatalf("Discover = %v, %v", dev, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Model headers whose length does not match the model's definition: too short
// for the fixed points is a DecodeError with the raw registers kept, longer is
// decoded with a warning.
func TestHostileLengthMismatch(t *testing.T) {
	t.Cleanup(leakCheck(t))
	short := make([]uint16, 5)
	long := make([]uint16, 70) // model 1 has 66 data registers
	regs := append([]uint16{103, 5}, short...)
	regs = append(regs, 1, 70)
	regs = append(regs, long...)
	regs = append(regs, 0xFFFF, 0)
	client, _ := hostileClient(t, sunsMap(regs...))
	dev, err := hostileDiscover(t, client)
	if err != nil {
		t.Fatal(err)
	}
	results, err := bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) { return dev.ReadAll(ctx) })
	var dec *DecodeError
	if !errors.As(err, &dec) || !errors.Is(err, ErrDecode) || dec.ModelID != 103 {
		t.Fatalf("ReadAll err = %v", err)
	}
	if len(results) != 2 || results[0].Group != nil || len(results[0].RawRegisters) != 7 {
		t.Fatalf("model 103 = %+v", results[0])
	}
	if results[1].Group == nil || len(results[1].Warnings) == 0 {
		t.Errorf("model 1 = %+v", results[1])
	}
}

// The longest model the address space holds, from base address 0, is read in
// bounded time and memory; one register more and there is no device.
func TestHostileHugeModel(t *testing.T) {
	t.Cleanup(leakCheck(t))
	// Marker 0-1, header 2-3, data from 4, end marker after it.
	const maxLen = 0x10000 - 4 - 2
	d := &memDevice{regs: make([]uint16, 0x10000)}
	d.regs[0], d.regs[1] = 0x5375, 0x6E53
	set := func(length int) {
		d.mu.Lock()
		defer d.mu.Unlock()
		clear(d.regs[2:])
		d.regs[2], d.regs[3] = 64777, uint16(length)
		if 4+length+1 < len(d.regs) {
			d.regs[4+length], d.regs[4+length+1] = 0xFFFF, 0
		}
	}
	client, _ := hostileClient(t, d)
	discover := func() (*Device, error) {
		return bounded(t, "Discover", func(ctx context.Context) (*Device, error) {
			return Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{0}})
		})
	}
	set(maxLen)
	dev, err := discover()
	if err != nil {
		t.Fatal(err)
	}
	before := d.reads.Load()
	dm, err := bounded(t, "ReadModel", func(ctx context.Context) (*DecodedModel, error) {
		return dev.ReadModel(ctx, dev.Discovery.Models[0])
	})
	if err != nil || len(dm.RawRegisters) != maxLen {
		t.Fatalf("ReadModel: %d registers, %v", len(dm.RawRegisters), err)
	}
	// 65532 registers at 125 per request.
	if n := d.reads.Load() - before; n != (maxLen+2+124)/125 {
		t.Errorf("%d requests", n)
	}
	for _, length := range []int{maxLen + 1, 0xFFFF} {
		set(length)
		if dev, err := discover(); err == nil || dev != nil {
			t.Errorf("length %d: Discover = %v, %v", length, dev, err)
		}
	}
}

// Regression, over the wire: a scale factor outside -10..10 left W at +Inf.
func TestHostileScaleFactorOverWire(t *testing.T) {
	// Model 64030 (sfSchema) is not in the registry: model 1 cannot carry
	// a scale factor, so a vendor model with a schema set by hand is read.
	client, _ := hostileClient(t, sunsMap(64030, 2, 5, 0x7FFF, 0xFFFF, 0))
	dev, err := hostileDiscover(t, client)
	if err != nil {
		t.Fatal(err)
	}
	inst := dev.Discovery.Models[0]
	inst.Schema, inst.SchemaKnown = sfSchema(), true
	dm, err := dev.ReadModel(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	if w := dm.Point("W"); w.ScaledValue != nil || w.RawValue != int16(5) {
		t.Errorf("W = %+v", w)
	}
	if _, err := json.Marshal(dm); err != nil {
		t.Error(err)
	}
}

// A device that answers slowly: a context deadline ends Discover and ReadModel
// promptly, without leaking goroutines.
func TestHostileSlowDevice(t *testing.T) {
	t.Cleanup(leakCheck(t))
	d := sunsMap(1, 66)
	d.regs = append(d.regs, make([]uint16, 66)...)
	d.regs = append(d.regs, 0xFFFF, 0)
	client, ms := hostileClient(t, d)
	dev, err := hostileDiscover(t, client)
	if err != nil {
		t.Fatal(err)
	}
	d.delay.Store(int64(time.Second))
	for _, call := range []func(ctx context.Context) error{
		func(ctx context.Context) error { _, err := Discover(ctx, client, nil); return err },
		func(ctx context.Context) error { _, err := dev.ReadModel(ctx, dev.Discovery.Models[0]); return err },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		start := time.Now()
		err := call(ctx)
		cancel()
		if err == nil {
			t.Error("no error from a device slower than the deadline")
		}
		if el := time.Since(start); el > 900*time.Millisecond {
			t.Errorf("returned after %v, deadline 100ms", el)
		}
	}
	_ = client.Close()
	_ = ms.Stop()
}

// A device that goes away in the middle of the chain walk: Discover fails
// with an error and no device.
func TestHostileDeviceDropsMidDiscovery(t *testing.T) {
	t.Cleanup(leakCheck(t))
	// Ten vendor models of two registers.
	var regs []uint16
	for i := range 10 {
		regs = append(regs, 64910+uint16(i), 2, 0, 0)
	}
	regs = append(regs, 0xFFFF, 0)
	d := sunsMap(regs...)
	var once sync.Once
	stopped := make(chan struct{})
	var ms atomic.Pointer[modbus.Server]
	d.onRead = func(n int64) {
		if n == 4 {
			once.Do(func() {
				go func() { _ = ms.Load().Stop(); close(stopped) }()
			})
		}
	}
	client, srv := hostileClient(t, d)
	ms.Store(srv)
	dev, err := hostileDiscover(t, client)
	if err == nil || dev != nil {
		t.Errorf("Discover = %v, %v", dev, err)
	}
	<-stopped
	_ = client.Close()
}
