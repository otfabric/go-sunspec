// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// writeTarget serves srv over TCP and discovers it, with the test schemas of
// ctlServer attached to the instances the registry does not know.
func writeTarget(t *testing.T, srv *Server, conf modbus.Config) (*Device, *Server) {
	t.Helper()
	addr, _ := startModbus(t, srv)
	conf.URL, conf.Logger = "tcp://"+addr, modbus.NopLogger()
	if conf.Timeout == 0 {
		conf.Timeout = 2 * time.Second
	}
	client, err := modbus.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	dev, err := Discover(testCtx(t), client, &DiscoverOptions{UnitID: srv.UnitID(), RegType: srv.RegType()})
	if err != nil {
		t.Fatal(err)
	}
	for i := range dev.Discovery.Models {
		switch m := &dev.Discovery.Models[i]; m.Header.ID {
		case 64010:
			m.Schema, m.SchemaKnown = ctlSchema(), true
		case 64002:
			m.Schema, m.SchemaKnown = curveSchema(), true
		}
	}
	return dev, srv
}

func servedPoint(t *testing.T, srv *Server, index int, name string) any {
	t.Helper()
	dm, err := srv.Model(index)
	if err != nil {
		t.Fatal(err)
	}
	return dm.Point(name).RawValue
}

func TestWritePoint(t *testing.T) {
	dev, srv := writeTarget(t, ctlServer(t, nil), modbus.Config{})
	ctx := testCtx(t)
	ctl := dev.Discovery.Models[0]

	for _, c := range []struct {
		point string
		value any
		raw   any
	}{
		{"Rw", 99, uint16(99)},
		{"Wide", uint32(123456), uint32(123456)},
		{"Lim", 3.7, int16(37)}, // engineering value, Lim_SF is -1
		{"Name", "xy", "xy"},
		{"Rw", []uint16{7}, uint16(7)}, // raw registers
	} {
		p, err := dev.WritePoint(ctx, ctl, c.point, c.value)
		if err != nil {
			t.Fatalf("WritePoint(%s, %v): %v", c.point, c.value, err)
		}
		if p.Name != c.point || p.RawValue != c.raw {
			t.Errorf("WritePoint(%s, %v) reads back %v (%T), want %v", c.point, c.value, p.RawValue, p.RawValue, c.raw)
		}
		if got := servedPoint(t, srv, 0, c.point); got != c.raw {
			t.Errorf("after WritePoint(%s), the device holds %v", c.point, got)
		}
	}
	if got := servedPoint(t, srv, 0, "Ro"); got != uint16(11) {
		t.Errorf("Ro changed to %v", got)
	}
	if got := servedPoint(t, srv, 0, "Lim_SF"); got != int16(-1) {
		t.Errorf("Lim_SF changed to %v", got)
	}
}

func TestWritePointOfRegistryModel(t *testing.T) {
	vals := (&Values{}).Set("WMaxLimPct", 50.0).Set("WMaxLimPct_SF", -1).Set("WMaxLim_Ena", "DISABLED")
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000}, ServerModel{ID: 123, Values: vals})
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := writeTarget(t, srv, modbus.Config{})
	inst := *dev.ModelByID(123)
	p, err := dev.WritePoint(testCtx(t), inst, "WMaxLimPct", 75.0)
	if err != nil {
		t.Fatal(err)
	}
	if p.RawValue != uint16(750) || p.ScaledValue == nil || *p.ScaledValue != 75 {
		t.Errorf("WMaxLimPct reads back raw %v, scaled %v", p.RawValue, p.ScaledValue)
	}
	if p, err = dev.WritePoint(testCtx(t), inst, "WMaxLim_Ena", "ENABLED"); err != nil || p.RawValue != uint16(1) {
		t.Errorf("WMaxLim_Ena: %v, %v", p, err)
	}
}

// Every refusal of the client comes before anything is sent: the device is
// unchanged, and a read-only client would have refused a write anyway.
func TestWritePointRefusals(t *testing.T) {
	dev, srv := writeTarget(t, ctlServer(t, nil), modbus.Config{})
	ctx := testCtx(t)
	ctl, raw, curves := dev.Discovery.Models[0], dev.Discovery.Models[1], dev.Discovery.Models[2]
	changed := ctl
	changed.Header.Length++

	for _, c := range []struct {
		name  string
		inst  ModelInstance
		point string
		value any
		want  error
	}{
		{"read-only point", ctl, "Ro", 1, ErrPointNotWritable},
		{"ID register", ctl, "ID", 1, ErrPointNotWritable},
		{"model without schema", raw, "X", 1, ErrPointNotWritable},
		{"count point", curves, "NCrv", 1, ErrPointNotWritable},
		{"unknown point", ctl, "Nope", 1, ErrPointNotFound},
		{"point of a group", curves, "En", 1, ErrPointNotFound},
		{"value out of range", ctl, "Rw", 70000, ErrValueOutOfRange},
		{"not implemented value", ctl, "Rw", 65535, ErrValueOutOfRange},
		{"wrong type", ctl, "Rw", "text", ErrEncode},
		{"model changed", changed, "Rw", 1, ErrModelChanged},
	} {
		if _, err := dev.WritePoint(ctx, c.inst, c.point, c.value); !errors.Is(err, c.want) {
			t.Errorf("%s: error %v, want %v", c.name, err, c.want)
		}
	}
	if got := servedPoint(t, srv, 0, "Rw"); got != uint16(22) {
		t.Errorf("Rw changed to %v", got)
	}
	if got := servedPoint(t, srv, 0, "Ro"); got != uint16(11) {
		t.Errorf("Ro changed to %v", got)
	}

	var nilDev *Device
	if _, err := nilDev.WritePoint(ctx, ctl, "Rw", 1); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("nil Device: %v", err)
	}
	if _, err := NewDevice(nil, 1, dev.Discovery).WritePoint(ctx, ctl, "Rw", 1); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("nil client: %v", err)
	}
	noSchema := ctl
	noSchema.Schema = &registry.ModelMeta{ID: 64010}
	if _, err := dev.WritePoint(ctx, noSchema, "Rw", 1); !errors.Is(err, ErrPointNotWritable) {
		t.Errorf("schema without group: %v", err)
	}
}

func TestWritePointRefusedByTheDevice(t *testing.T) {
	refuse := func(context.Context, *ServerWrite) error { return modbus.ErrIllegalDataValue }
	dev, srv := writeTarget(t, ctlServer(t, &ServerOptions{OnWrite: refuse}), modbus.Config{})
	if _, err := dev.WritePoint(testCtx(t), dev.Discovery.Models[0], "Rw", 5); !errors.Is(err, modbus.ErrIllegalDataValue) {
		t.Errorf("error %v, want the device's exception", err)
	}
	if got := servedPoint(t, srv, 0, "Rw"); got != uint16(22) {
		t.Errorf("Rw changed to %v", got)
	}
}

func TestWritePointWithReadOnlyClient(t *testing.T) {
	dev, srv := writeTarget(t, ctlServer(t, nil), modbus.Config{AllowedFunctionCodes: ReadFunctionCodes()})
	if _, err := dev.WritePoint(testCtx(t), dev.Discovery.Models[0], "Rw", 5); !errors.Is(err, modbus.ErrFunctionCodeNotAllowed) {
		t.Errorf("error %v, want ErrFunctionCodeNotAllowed", err)
	}
	if got := servedPoint(t, srv, 0, "Rw"); got != uint16(22) {
		t.Errorf("Rw changed to %v", got)
	}
}

func TestWritePointInputRegisters(t *testing.T) {
	dev, _ := writeTarget(t, ctlServer(t, &ServerOptions{RegType: modbus.InputRegister}), modbus.Config{})
	if _, err := dev.WritePoint(testCtx(t), dev.Discovery.Models[0], "Rw", 5); !errors.Is(err, ErrPointNotWritable) {
		t.Errorf("error %v, want ErrPointNotWritable", err)
	}
}

// A device that goes away between the write and the read back.
func TestWritePointReadBackFails(t *testing.T) {
	srv := ctlServer(t, nil)
	sh := newShim(srv)
	addr, _ := startModbus(t, sh)
	client := dial(t, addr, time.Second)
	dev, err := Discover(testCtx(t), client, &DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	inst := dev.Discovery.Models[0]
	inst.Schema, inst.SchemaKnown = ctlSchema(), true
	sh.setFilter(func(_, _ uint16, regs []uint16, err error) ([]uint16, error) {
		if sh.writes.Load() > 0 {
			return nil, modbus.ErrServerDeviceFailure
		}
		return regs, err
	})
	if _, err := dev.WritePoint(testCtx(t), inst, "Rw", 5); !errors.Is(err, modbus.ErrServerDeviceFailure) || sh.writes.Load() != 1 {
		t.Errorf("error %v after %d writes", err, sh.writes.Load())
	}
	if got := servedPoint(t, srv, 0, "Rw"); got != uint16(5) {
		t.Errorf("the write did not land: %v", got)
	}
}
