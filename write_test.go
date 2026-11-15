// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// writeTarget serves srv over TCP and discovers it, with the test schemas of
// ctlServer attached to the instances the registry does not know.
func writeTarget(t testing.TB, srv *Server, conf modbus.Config) (*Device, *Server) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dev, err := Discover(ctx, client, &DiscoverOptions{UnitID: srv.UnitID(), RegType: srv.RegType()})
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

func servedPoint(t testing.TB, srv *Server, index int, name string) any {
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

// A device whose model holds a register past what its schema describes: the
// encoder cannot reproduce the model, so nothing is written.
func TestWritePointModelLongerThanSchema(t *testing.T) {
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000}, ServerModel{ID: 64010, Registers: []uint16{11, 22, 0, 7, 125, 0xFFFF, 0x6162, 0x6364, 99}})
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := writeTarget(t, srv, modbus.Config{})
	if _, err := dev.WritePoint(testCtx(t), dev.Discovery.Models[0], "Rw", 5); !errors.Is(err, ErrPointNotWritable) {
		t.Errorf("error %v, want ErrPointNotWritable", err)
	}
	if got, _ := srv.Model(0); got.RawRegisters[1] != 22 {
		t.Errorf("Rw changed to %d", got.RawRegisters[1])
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

// TestWritePointScaleFactor: an engineering value is written only when the
// device reports the point's scale factor, in the read before the write, as a
// usable value: implemented and within -10..10. Otherwise WritePoint refuses
// before anything is sent, and the point's registers can still be written as
// []uint16.
func TestWritePointScaleFactor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sf     any     // Lim_SF on the device; nil: not implemented
		value  float64 // the engineering value written to Lim
		raw    int16   // what Lim holds after the write
		reason string  // of the refusal; "" for a value that is written
	}{
		{"-10, the lowest", -10, 1e-7, 1000, ""},
		{"-1", -1, 7.5, 75, ""},
		{"0", 0, 7, 7, ""},
		{"10, the highest", 10, 3e10, 3, ""},
		{"not implemented", nil, 7.5, 0, "reports as not implemented"},
		{"11", []uint16{11}, 7.5, 0, "reports as 11, outside -10..10"},
		{"-11", []uint16{0xFFF5}, 7.5, 0, "reports as -11, outside -10..10"},
		{"32767", []uint16{0x7FFF}, 7.5, 0, "reports as 32767, outside -10..10"},
		{"-32767", []uint16{0x8001}, 7.5, 0, "reports as -32767, outside -10..10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vals := ctlValues().Set("Lim", []uint16{5}).Set("Lim_SF", tc.sf)
			srv, err := NewServer(&ServerOptions{BaseAddress: 40000}, ServerModel{Schema: ctlSchema(), Values: vals})
			if err != nil {
				t.Fatal(err)
			}
			// The shim counts the write requests that reach the device.
			sh := newShim(srv)
			addr, _ := startModbus(t, sh)
			dev, err := Discover(testCtx(t), dial(t, addr, 2*time.Second), at(40000))
			if err != nil {
				t.Fatal(err)
			}
			inst := dev.Discovery.Models[0]
			inst.Schema, inst.SchemaKnown = ctlSchema(), true

			p, err := dev.WritePoint(testCtx(t), inst, "Lim", tc.value)
			if tc.reason == "" {
				if err != nil || p.RawValue != tc.raw || sh.writes.Load() != 1 {
					t.Fatalf("WritePoint(Lim, %v): point %+v, error %v, %d writes sent; want raw %d from one write",
						tc.value, p, err, sh.writes.Load(), tc.raw)
				}
				if got := servedPoint(t, srv, 0, "Lim"); got != tc.raw {
					t.Fatalf("the device holds %v, want %d", got, tc.raw)
				}
				return
			}
			if !errors.Is(err, ErrPointNotWritable) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("WritePoint(Lim, %v): point %+v, error %v; want ErrPointNotWritable saying %q", tc.value, p, err, tc.reason)
			}
			if n := sh.writes.Load(); n != 0 {
				t.Fatalf("%d writes were sent for a value that was refused", n)
			}
			if got := servedPoint(t, srv, 0, "Lim"); got != int16(5) {
				t.Fatalf("the device holds %v after a refused write, want 5", got)
			}
			// The point's registers are written as they are, whatever the
			// scale factor.
			p, err = dev.WritePoint(testCtx(t), inst, "Lim", []uint16{75})
			if err != nil || p.RawValue != int16(75) || p.ScaledValue != nil || sh.writes.Load() != 1 {
				t.Fatalf("WritePoint(Lim, []uint16{75}): point %+v, error %v, %d writes sent; want raw 75, unscaled, from one write",
					p, err, sh.writes.Load())
			}
			// So is a nil value, which sets the point to its not-implemented
			// value and needs no scale factor.
			p, err = dev.WritePoint(testCtx(t), inst, "Lim", nil)
			if err != nil || p.Implemented || sh.writes.Load() != 2 {
				t.Fatalf("WritePoint(Lim, nil): point %+v, error %v, %d writes sent; want a point that is not implemented, from a second write",
					p, err, sh.writes.Load())
			}
		})
	}
}

// TestWritePointScaleFactorOfTheSchema: a literal scale factor is the
// schema's and always usable; a scale factor point that the schema names but
// the model does not hold is not.
func TestWritePointScaleFactorOfTheSchema(t *testing.T) {
	schema := pointSchema(64030,
		registry.PointMeta{Name: "Lit", Type: "int16", Size: 1, SF: "-2", Access: "RW"},
		registry.PointMeta{Name: "Odd", Type: "int16", Size: 1, SF: "Gone_SF", Access: "RW"},
	)
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{Schema: schema, Values: (&Values{}).Set("Lit", 0.25).Set("Odd", 3)})
	if err != nil {
		t.Fatal(err)
	}
	sh := newShim(srv)
	addr, _ := startModbus(t, sh)
	dev, err := Discover(testCtx(t), dial(t, addr, 2*time.Second), at(40000))
	if err != nil {
		t.Fatal(err)
	}
	inst := dev.Discovery.Models[0]
	inst.Schema, inst.SchemaKnown = schema, true

	if p, err := dev.WritePoint(testCtx(t), inst, "Lit", 1.5); err != nil || p.RawValue != int16(150) {
		t.Fatalf("WritePoint(Lit, 1.5): point %+v, error %v; want raw 150", p, err)
	}
	before := sh.writes.Load()
	_, err = dev.WritePoint(testCtx(t), inst, "Odd", 1.5)
	if !errors.Is(err, ErrPointNotWritable) || !strings.Contains(err.Error(), "Gone_SF, which the model as read does not hold") {
		t.Fatalf("WritePoint(Odd, 1.5): error %v; want ErrPointNotWritable naming Gone_SF", err)
	}
	if n := sh.writes.Load() - before; n != 0 {
		t.Fatalf("%d writes were sent for a value that was refused", n)
	}
	if got := servedPoint(t, srv, 0, "Odd"); got != int16(3) {
		t.Fatalf("the device holds %v after a refused write, want 3", got)
	}
}

// shimTarget serves srv behind a shim that counts the write requests and
// returns the device discovered at 40000 with schema attached to its first
// model.
func shimTarget(t *testing.T, srv *Server, schema *registry.ModelMeta) (*Device, ModelInstance, *shim) {
	t.Helper()
	sh := newShim(srv)
	addr, _ := startModbus(t, sh)
	dev, err := Discover(testCtx(t), dial(t, addr, 2*time.Second), at(40000))
	if err != nil {
		t.Fatal(err)
	}
	inst := dev.Discovery.Models[0]
	inst.Schema, inst.SchemaKnown = schema, true
	return dev, inst, sh
}

// TestWritePointUndersizedCountPoint: a schema whose count point has fewer
// registers than its type needs is refused with a *EncodeError, before
// anything is sent; the model as read gives that point as its registers.
func TestWritePointUndersizedCountPoint(t *testing.T) {
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000}, ServerModel{ID: 64060, Registers: []uint16{5, 100}})
	if err != nil {
		t.Fatal(err)
	}
	dev, inst, sh := shimTarget(t, srv, undersizedCountSchema("uint32", 1))
	_, err = dev.WritePoint(testCtx(t), inst, "W", 7)
	if ee, ok := errors.AsType[*EncodeError](err); !ok || ee.PointName != "NPt" {
		t.Fatalf("WritePoint(W, 7): error %v, want a *EncodeError for NPt", err)
	}
	if n := sh.writes.Load(); n != 0 {
		t.Errorf("%d writes were sent", n)
	}
}

// TestWritePointNestedCountPoint: a top-level point that counts a group
// nested one level further in defines the layout as well and is refused.
func TestWritePointNestedCountPoint(t *testing.T) {
	inner := &registry.GroupMeta{Name: "Inner", CountPoint: "N", Length: 1,
		Points: []registry.PointMeta{{Name: "V", Type: "uint16", Size: 1, Offset: 0, Access: "RW"}}}
	outer := &registry.GroupMeta{Name: "Outer", Count: 1, Length: 1, Groups: []*registry.GroupMeta{inner},
		Points: []registry.PointMeta{{Name: "O", Type: "uint16", Size: 1, Offset: 0, Access: "RW"}}}
	schema := &registry.ModelMeta{ID: 64032, Name: "m", Group: &registry.GroupMeta{
		Name: "m", Count: 1, Length: 4, Groups: []*registry.GroupMeta{outer},
		Points: append(boundsHeader(),
			registry.PointMeta{Name: "N", Type: "uint16", Size: 1, Offset: 2, Access: "RW"},
			registry.PointMeta{Name: "W", Type: "uint16", Size: 1, Offset: 3, Access: "RW"},
		),
	}}
	vals := (&Values{}).Set("W", 1)
	vals.Add("Outer", (&Values{}).Set("O", 2)).Add("Inner", (&Values{}).Set("V", 3))
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000}, ServerModel{Schema: schema, Values: vals})
	if err != nil {
		t.Fatal(err)
	}
	dev, inst, sh := shimTarget(t, srv, schema)
	// The count it holds already: the layout would not change, and the
	// point is refused all the same.
	if _, err := dev.WritePoint(testCtx(t), inst, "N", 1); !errors.Is(err, ErrPointNotWritable) {
		t.Fatalf("WritePoint(N, 1): error %v, want ErrPointNotWritable", err)
	}
	if n := sh.writes.Load(); n != 0 {
		t.Errorf("%d writes were sent for the count point", n)
	}
	if p, err := dev.WritePoint(testCtx(t), inst, "W", 5); err != nil || p.RawValue != uint16(5) {
		t.Errorf("WritePoint(W, 5): %+v, %v", p, err)
	}
}

// TestWritePointSymbolsWithoutScaleFactor: enum and bitfield symbol names are
// raw values, written whatever the point's scale factor reads; a number for
// the same point is still refused.
func TestWritePointSymbolsWithoutScaleFactor(t *testing.T) {
	syms := []registry.SymbolMeta{{Name: "OFF", Value: 0}, {Name: "ON", Value: 1}, {Name: "B2", Value: 2}}
	schema := pointSchema(64031,
		registry.PointMeta{Name: "E", Type: "enum16", Size: 1, SF: "X_SF", Access: "RW", Symbols: syms},
		registry.PointMeta{Name: "F", Type: "bitfield16", Size: 1, SF: "X_SF", Access: "RW", Symbols: syms},
		registry.PointMeta{Name: "X_SF", Type: "sunssf", Size: 1, Access: "R"},
	)
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{Schema: schema, Values: (&Values{}).Set("E", 0).Set("F", 0)})
	if err != nil {
		t.Fatal(err)
	}
	dev, inst, sh := shimTarget(t, srv, schema)
	ctx := testCtx(t)
	if p, err := dev.WritePoint(ctx, inst, "E", "ON"); err != nil || p.RawValue != uint16(1) {
		t.Errorf("WritePoint(E, ON): %+v, %v", p, err)
	}
	if p, err := dev.WritePoint(ctx, inst, "F", []string{"B2", "ON"}); err != nil || p.RawValue != uint16(0b110) {
		t.Errorf("WritePoint(F, [B2 ON]): %+v, %v", p, err)
	}
	if _, err := dev.WritePoint(ctx, inst, "E", 1); !errors.Is(err, ErrPointNotWritable) {
		t.Errorf("WritePoint(E, 1): error %v, want ErrPointNotWritable", err)
	}
	if _, err := dev.WritePoint(ctx, inst, "F", "ON"); !errors.Is(err, ErrPointNotWritable) {
		t.Errorf("WritePoint(F, ON): error %v, want ErrPointNotWritable", err)
	}
	if n := sh.writes.Load(); n != 2 {
		t.Errorf("%d writes were sent, want 2", n)
	}
}
