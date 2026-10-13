// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// ctlSchema is a small model with read-only and writable points of several
// shapes, used to test the Server.
func ctlSchema() *registry.ModelMeta {
	return pointSchema(64010,
		registry.PointMeta{Name: "Ro", Type: "uint16", Size: 1, Access: "R"},
		registry.PointMeta{Name: "Rw", Type: "uint16", Size: 1, Access: "RW"},
		registry.PointMeta{Name: "Wide", Type: "uint32", Size: 2, Access: "RW"},
		registry.PointMeta{Name: "Lim", Type: "int16", Size: 1, SF: "Lim_SF", Access: "RW"},
		registry.PointMeta{Name: "Lim_SF", Type: "sunssf", Size: 1, Access: "RW"},
		registry.PointMeta{Name: "Name", Type: "string", Size: 2, Access: "RW"},
	)
}

func ctlValues() *Values {
	return (&Values{}).Set("Ro", 11).Set("Rw", 22).Set("Wide", 70000).Set("Lim", 12.5).Set("Lim_SF", -1).Set("Name", "abcd")
}

// ctlServer serves, from base address 40000: the ctl model (header at 40002,
// data 40004..40011), a raw vendor model (header at 40012, data 40014..40016)
// and the curve model (header at 40017).
func ctlServer(t testing.TB, opts *ServerOptions) *Server {
	t.Helper()
	if opts == nil {
		opts = &ServerOptions{}
	}
	opts.BaseAddress = 40000
	curves := &Values{}
	curves.Add("Crv", (&Values{}).Set("X_SF", -1)).Add("Pt", (&Values{}).Set("X", 1.5).Set("Y", 3))
	srv, err := NewServer(opts,
		ServerModel{Schema: ctlSchema(), Values: ctlValues()},
		ServerModel{ID: 64999, Registers: []uint16{9, 8, 7}},
		ServerModel{Schema: curveSchema(), Values: curves},
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

func holdingRead(s *Server, addr, qty uint16) ([]uint16, error) {
	return s.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{UnitID: s.UnitID(), Addr: addr, Quantity: qty})
}

func holdingWrite(s *Server, addr uint16, regs ...uint16) error {
	_, err := s.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{
		UnitID: s.UnitID(), Addr: addr, Quantity: uint16(len(regs)), IsWrite: true, Args: regs, ClientAddr: "test-client",
	})
	return err
}

func TestServerLayout(t *testing.T) {
	srv := ctlServer(t, nil)
	if srv.UnitID() != 1 || srv.BaseAddress() != 40000 || srv.RegType() != modbus.HoldingRegister {
		t.Errorf("accessors: unit %d base %d type %d", srv.UnitID(), srv.BaseAddress(), srv.RegType())
	}
	wantModels := []ModelHeader{
		{ID: 64010, Length: 8, StartAddress: 40002, EndAddress: 40011, NextAddress: 40012},
		{ID: 64999, Length: 3, StartAddress: 40012, EndAddress: 40016, NextAddress: 40017},
		{ID: 64002, Length: 9, StartAddress: 40017, EndAddress: 40027, NextAddress: 40028},
	}
	if got := srv.Models(); !reflect.DeepEqual(got, wantModels) {
		t.Errorf("Models() = %+v, want %+v", got, wantModels)
	}

	want := []uint16{
		0x5375, 0x6E53,
		64010, 8, 11, 22, 1, 0x1170, 125, 0xFFFF, 0x6162, 0x6364,
		64999, 3, 9, 8, 7,
		64002, 9, 1, 1, 0x8000, 0xFFFF, 0xFFFF, 15, 3, 0xFFFF, 0xFFFF,
		0xFFFF, 0,
	}
	// The whole map in one read, spanning every model.
	got, err := holdingRead(srv, 40000, uint16(len(want)))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("whole map = %v, %v\nwant %v", got, err, want)
	}
	// Every sub-range is served; every range that leaves the map is not.
	for lo := 0; lo < len(want); lo++ {
		for hi := lo + 1; hi <= len(want); hi++ {
			got, err := holdingRead(srv, uint16(40000+lo), uint16(hi-lo))
			if err != nil || !reflect.DeepEqual(got, want[lo:hi]) {
				t.Fatalf("read %d..%d = %v, %v", lo, hi, got, err)
			}
		}
	}
	for _, r := range [][2]uint16{{39999, 1}, {39999, 5}, {40000 + uint16(len(want)), 1}, {40000 + uint16(len(want)) - 1, 2}, {0, 4}, {65535, 1}, {40000, 0}, {40000, 125}} {
		if _, err := holdingRead(srv, r[0], r[1]); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("read %d+%d: err = %v, want illegal data address", r[0], r[1], err)
		}
	}

	// A device without models serves the marker and the end marker, at the
	// defaults of a nil options value.
	empty, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := holdingRead(empty, 0, 4); err != nil || !reflect.DeepEqual(got, []uint16{0x5375, 0x6E53, 0xFFFF, 0}) {
		t.Errorf("empty device = %v, %v", got, err)
	}
	if len(empty.Models()) != 0 || empty.UnitID() != 1 {
		t.Errorf("empty device: %d models, unit %d", len(empty.Models()), empty.UnitID())
	}
}

func TestServerRequestRouting(t *testing.T) {
	ctx := context.Background()
	srv := ctlServer(t, &ServerOptions{UnitID: 7})
	if _, err := srv.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, Addr: 40000, Quantity: 2}); !errors.Is(err, modbus.ErrGWTargetFailedToRespond) {
		t.Errorf("other unit, holding: %v", err)
	}
	if _, err := srv.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{UnitID: 1, Addr: 40000, Quantity: 2}); !errors.Is(err, modbus.ErrGWTargetFailedToRespond) {
		t.Errorf("other unit, input: %v", err)
	}
	if got, err := holdingRead(srv, 40000, 2); err != nil || got[0] != 0x5375 {
		t.Errorf("own unit: %v %v", got, err)
	}
	if _, err := srv.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{UnitID: 7, Addr: 40000, Quantity: 2}); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("input registers of a holding device: %v", err)
	}
	if _, err := srv.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: 7}); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("coils: %v", err)
	}
	if _, err := srv.HandleDiscreteInputs(ctx, &modbus.DiscreteInputsRequest{UnitID: 7}); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("discrete inputs: %v", err)
	}
	if _, err := srv.HandleHoldingRegisters(ctx, nil); !errors.Is(err, modbus.ErrServerDeviceFailure) {
		t.Errorf("nil holding request: %v", err)
	}
	if _, err := srv.HandleInputRegisters(ctx, nil); !errors.Is(err, modbus.ErrServerDeviceFailure) {
		t.Errorf("nil input request: %v", err)
	}

	// A device served in input registers: readable there, nothing in the
	// holding registers, and therefore not writable.
	in := ctlServer(t, &ServerOptions{RegType: modbus.InputRegister})
	if in.RegType() != modbus.InputRegister {
		t.Errorf("RegType() = %d", in.RegType())
	}
	if got, err := in.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{UnitID: 1, Addr: 40002, Quantity: 3}); err != nil || !reflect.DeepEqual(got, []uint16{64010, 8, 11}) {
		t.Errorf("input read = %v, %v", got, err)
	}
	if _, err := holdingRead(in, 40000, 2); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("holding read of an input device: %v", err)
	}
	if err := holdingWrite(in, 40005, 1); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("write to an input device: %v", err)
	}
}

func TestNewServerErrors(t *testing.T) {
	big := make([]uint16, 30000)
	tests := []struct {
		name   string
		opts   *ServerOptions
		models []ServerModel
		want   error
	}{
		{"unknown model without registers", nil, []ServerModel{{ID: 64999}}, ErrUnknownModel},
		{"values and registers", nil, []ServerModel{{ID: 1, Values: &Values{}, Registers: []uint16{1}}}, ErrEncode},
		{"schema of another model", nil, []ServerModel{{ID: 5, Schema: ctlSchema()}}, ErrEncode},
		{"end marker ID", nil, []ServerModel{{ID: 0xFFFF, Registers: []uint16{1}}}, ErrModelLayout},
		{"no data registers", nil, []ServerModel{{ID: 64999, Registers: []uint16{}}}, ErrModelLayout},
		{"schema without data", nil, []ServerModel{{Schema: pointSchema(64011)}}, ErrModelLayout},
		{"too many registers", nil, []ServerModel{{ID: 64999, Registers: make([]uint16, 65536)}}, ErrModelLayout},
		{"chain past the address space", nil, []ServerModel{{ID: 64998, Registers: big}, {ID: 64999, Registers: big}, {ID: 64997, Registers: big}}, ErrModelLayout},
		{"model past the address space", &ServerOptions{BaseAddress: 65500}, []ServerModel{{ID: 1}}, ErrModelLayout},
		{"no room for the end marker", &ServerOptions{BaseAddress: 65528}, []ServerModel{{ID: 64999, Registers: []uint16{1, 2, 3}}}, ErrModelLayout},
		{"markers past the address space", &ServerOptions{BaseAddress: 65534}, nil, ErrModelLayout},
		{"bad value", nil, []ServerModel{{ID: 1}, {ID: 1, Values: (&Values{}).Set("DA", 70000)}}, ErrValueOutOfRange},
		{"unknown point", nil, []ServerModel{{ID: 1, Values: (&Values{}).Set("Nope", 1)}}, ErrPointNotFound},
		{"register type", &ServerOptions{RegType: 9}, nil, modbus.ErrUnexpectedParameters},
	}
	for _, tc := range tests {
		srv, err := NewServer(tc.opts, tc.models...)
		if !errors.Is(err, tc.want) || srv != nil {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	// The error says which model of the chain it concerns.
	_, err := NewServer(nil, ServerModel{ID: 1}, ServerModel{ID: 1, Values: (&Values{}).Set("DA", 70000)})
	var ee *EncodeError
	if !errors.As(err, &ee) || ee.PointName != "DA" || !strings.Contains(err.Error(), "model 1 of the chain") {
		t.Errorf("err = %v", err)
	}

	// The tightest fit: a device that ends exactly at address 65535.
	srv, err := NewServer(&ServerOptions{BaseAddress: 65527}, ServerModel{ID: 64999, Registers: []uint16{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := holdingRead(srv, 65527, 9); err != nil || got[8] != 0 || got[7] != 0xFFFF {
		t.Errorf("device ending at 65535: %v %v", got, err)
	}
	if _, err := holdingRead(srv, 65535, 2); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read past 65535: %v", err)
	}
	// The longest model there is.
	if _, err := NewServer(nil, ServerModel{ID: 64999, Registers: make([]uint16, 65530)}); err != nil {
		t.Errorf("65530-register model at base 0: %v", err)
	}
}

func TestServerCopiesItsInput(t *testing.T) {
	vals := ctlValues()
	raw := []uint16{9, 8, 7}
	srv, err := NewServer(nil, ServerModel{Schema: ctlSchema(), Values: vals}, ServerModel{ID: 64999, Registers: raw})
	if err != nil {
		t.Fatal(err)
	}
	vals.Set("Ro", 99)
	raw[0] = 99
	if got, _ := holdingRead(srv, 4, 1); got[0] != 11 {
		t.Errorf("Ro = %d after the caller changed its Values", got[0])
	}
	if got, _ := holdingRead(srv, 14, 1); got[0] != 9 {
		t.Errorf("raw register = %d after the caller changed its slice", got[0])
	}
	if err := srv.SetPoint(0, "Rw", 23); err != nil {
		t.Fatal(err)
	}
	if got, _ := holdingRead(srv, 4, 2); got[0] != 11 || got[1] != 23 {
		t.Errorf("after SetPoint: %v", got)
	}
}

func TestServerUpdates(t *testing.T) {
	srv := ctlServer(t, nil)
	point := func(index int, name string) *DecodedPoint {
		t.Helper()
		dm, err := srv.Model(index)
		if err != nil {
			t.Fatalf("Model(%d): %v", index, err)
		}
		p := dm.Point(name)
		if p == nil {
			t.Fatalf("model %d has no point %s", index, name)
		}
		return p
	}

	// SetPoint: one value, scaled, the rest untouched.
	if err := srv.SetPoint(0, "Lim", -3.2); err != nil {
		t.Fatal(err)
	}
	if p := point(0, "Lim"); p.RawValue != int16(-32) || *p.ScaledValue != -3.2 {
		t.Errorf("Lim = %+v", p)
	}
	if p := point(0, "Ro"); p.RawValue != uint16(11) {
		t.Errorf("Ro = %+v", p)
	}
	// A nil value makes a point not implemented.
	if err := srv.SetPoint(0, "Ro", nil); err != nil {
		t.Fatal(err)
	}
	if p := point(0, "Ro"); p.Implemented {
		t.Errorf("Ro = %+v, want not implemented", p)
	}

	// Update: several points, nested ones included, in one step.
	err := srv.Update(2, func(v *Values) error {
		pt := v.Groups["Crv"][0].Groups["Pt"][0]
		pt.Set("X", 2.5).Set("Y", 4)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := point(2, "X"); *p.ScaledValue != 2.5 {
		t.Errorf("X = %+v", p)
	}
	if err := srv.Update(0, nil); err != nil {
		t.Errorf("Update with a nil function: %v", err)
	}

	// SetValues replaces everything.
	if err := srv.SetValues(0, (&Values{}).Set("Rw", 5)); err != nil {
		t.Fatal(err)
	}
	if got, _ := holdingRead(srv, 40004, 8); !reflect.DeepEqual(got, []uint16{0xFFFF, 5, 0xFFFF, 0xFFFF, 0x8000, 0x8000, 0, 0}) {
		t.Errorf("after SetValues: %v", got)
	}
	if err := srv.SetValues(0, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := holdingRead(srv, 40005, 1); got[0] != 0xFFFF {
		t.Errorf("after SetValues(nil): %v", got)
	}

	// SetRegisters on the raw model, and on a model with a schema.
	if err := srv.SetRegisters(1, []uint16{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	dm, err := srv.Model(1)
	if err != nil || dm.Group != nil || dm.Name != "unknown_64999" || !reflect.DeepEqual(dm.RawRegisters, []uint16{1, 2, 3}) ||
		len(dm.Warnings) != 1 || dm.InstanceAddress != 40012 {
		t.Errorf("raw model = %+v, %v", dm, err)
	}
	if err := srv.SetRegisters(0, []uint16{1, 2, 0, 3, 40, 0xFFFE, 0x4142, 0}); err != nil {
		t.Fatal(err)
	}
	if p := point(0, "Lim"); *p.ScaledValue != 0.4 {
		t.Errorf("Lim after SetRegisters = %+v", p)
	}
	// The values follow the registers: an update keeps what was set.
	if err := srv.SetPoint(0, "Rw", 6); err != nil {
		t.Fatal(err)
	}
	if got, _ := holdingRead(srv, 40004, 8); !reflect.DeepEqual(got, []uint16{1, 6, 0, 3, 40, 0xFFFE, 0x4142, 0}) {
		t.Errorf("after SetRegisters and SetPoint: %v", got)
	}
	if dm, _ := srv.Model(0); dm.InstanceAddress != 40002 || dm.ModelID != 64010 {
		t.Errorf("Model(0) = %+v", dm)
	}
}

func TestServerUpdateOfModelWithoutValues(t *testing.T) {
	// A model given without values can be filled in later.
	srv, err := NewServer(nil, ServerModel{ID: 101})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SetPoint(0, "W", 5); err != nil {
		t.Fatal(err)
	}
	if dm, _ := srv.Model(0); dm.Point("W").RawValue != int16(5) || dm.Point("A").Implemented {
		t.Errorf("model = %+v", dm.Group.Points[:3])
	}
}

func TestServerSetRegistersKeepsStructure(t *testing.T) {
	// Two counted groups of the same shape: swapping their counts keeps the
	// length of the model but moves its points.
	group := func(name, count string) *registry.GroupMeta {
		return &registry.GroupMeta{Name: name, CountPoint: count, Length: 1,
			Points: []registry.PointMeta{{Name: "V", Type: "uint16", Size: 1, Access: "RW"}}}
	}
	meta := pointSchema(64012,
		registry.PointMeta{Name: "NA", Type: "uint16", Size: 1},
		registry.PointMeta{Name: "NB", Type: "uint16", Size: 1})
	meta.Group.Groups = []*registry.GroupMeta{group("A", "NA"), group("B", "NB")}
	v := &Values{}
	v.Add("A", (&Values{}).Set("V", 1))
	v.Add("A", (&Values{}).Set("V", 2))
	v.Add("B", (&Values{}).Set("V", 3))
	srv, err := NewServer(nil, ServerModel{Schema: meta, Values: v})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SetRegisters(0, []uint16{1, 2, 1, 2, 3}); !errors.Is(err, ErrModelLayout) {
		t.Errorf("swapped counts: err = %v, want ErrModelLayout", err)
	}
	if err := srv.SetRegisters(0, []uint16{2, 1, 7, 8, 9}); err != nil {
		t.Errorf("same structure: %v", err)
	}
	if got, _ := holdingRead(srv, 4, 5); !reflect.DeepEqual(got, []uint16{2, 1, 7, 8, 9}) {
		t.Errorf("registers = %v", got)
	}
	// The second instance of A stays writable where it is.
	if err := holdingWrite(srv, 7, 80); err != nil {
		t.Errorf("write after SetRegisters: %v", err)
	}
}

func TestServerWriteToRegisterWithoutPoint(t *testing.T) {
	// A schema whose group is one register longer than its points: that
	// register belongs to no point and cannot be written.
	meta := pointSchema(64013, registry.PointMeta{Name: "V", Type: "uint16", Size: 1, Access: "RW"})
	meta.Group.Length++
	srv, err := NewServer(nil, ServerModel{Schema: meta, Values: (&Values{}).Set("V", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := holdingWrite(srv, 4, 2); err != nil {
		t.Errorf("write to the point: %v", err)
	}
	for _, regs := range [][]uint16{{3, 4}, {5}} {
		addr := uint16(6 - len(regs))
		if err := holdingWrite(srv, addr, regs...); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("write of %d registers at %d: %v", len(regs), addr, err)
		}
	}
}

func TestServerUpdateErrorsChangeNothing(t *testing.T) {
	srv := ctlServer(t, nil)
	before, _ := holdingRead(srv, 40000, 30)
	boom := errors.New("boom")

	checks := []struct {
		name string
		err  error
		want error
	}{
		{"index too high", srv.SetPoint(3, "Rw", 1), ErrUnknownModel},
		{"negative index", srv.SetValues(-1, nil), ErrUnknownModel},
		{"Model index", func() error { _, err := srv.Model(9); return err }(), ErrUnknownModel},
		{"SetRegisters index", srv.SetRegisters(9, nil), ErrUnknownModel},
		{"value out of range", srv.SetPoint(0, "Rw", 70000), ErrValueOutOfRange},
		{"unknown point", srv.SetPoint(0, "Nope", 1), ErrPointNotFound},
		{"function error", srv.Update(0, func(v *Values) error { v.Set("Rw", 1); return boom }), boom},
		{"values for a raw model", srv.SetPoint(1, "Rw", 1), ErrEncode},
		{"more instances", srv.Update(2, func(v *Values) error { v.Add("Crv", nil); return nil }), ErrModelLayout},
		{"fewer instances", srv.SetValues(2, nil), ErrModelLayout},
		{"filling group grows", srv.Update(2, func(v *Values) error { v.Add("Rep", nil); return nil }), ErrModelLayout},
		{"SetRegisters length", srv.SetRegisters(1, []uint16{1, 2}), ErrModelLayout},
		{"SetRegisters length with schema", srv.SetRegisters(0, make([]uint16, 9)), ErrModelLayout},
		// The curve model's registers with its counts changed.
		{"SetRegisters structure", srv.SetRegisters(2, []uint16{2, 1, 0x8000, 0xFFFF, 0xFFFF, 15, 3, 0xFFFF, 0xFFFF}), ErrModelLayout},
		{"SetRegisters count gone", srv.SetRegisters(2, []uint16{0xFFFF, 1, 0x8000, 0xFFFF, 0xFFFF, 15, 3, 0xFFFF, 0xFFFF}), ErrModelLayout},
	}
	for _, c := range checks {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, c.err, c.want)
		}
	}
	after, _ := holdingRead(srv, 40000, 30)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("failed updates changed the map:\n%v\n%v", before, after)
	}
	// And the values behind it: the next update starts from the old ones.
	if err := srv.SetPoint(0, "Ro", 12); err != nil {
		t.Fatal(err)
	}
	if got, _ := holdingRead(srv, 40004, 2); got[0] != 12 || got[1] != 22 {
		t.Errorf("after the failed updates: %v", got)
	}
}

func TestServerWrites(t *testing.T) {
	var seen []*ServerWrite
	reject := false
	srv := ctlServer(t, &ServerOptions{OnWrite: func(_ context.Context, w *ServerWrite) error {
		seen = append(seen, w)
		if reject {
			return modbus.ErrIllegalDataValue
		}
		return nil
	}})
	// ctl data: 40004 Ro, 40005 Rw, 40006-7 Wide, 40008 Lim, 40009 Lim_SF,
	// 40010-11 Name.
	if err := holdingWrite(srv, 40005, 33); err != nil {
		t.Fatalf("write Rw: %v", err)
	}
	if err := holdingWrite(srv, 40006, 0x0002, 0x0003, 77, 0xFFFE); err != nil {
		t.Fatalf("write Wide, Lim and Lim_SF: %v", err)
	}
	if got, _ := holdingRead(srv, 40004, 8); !reflect.DeepEqual(got, []uint16{11, 33, 2, 3, 77, 0xFFFE, 0x6162, 0x6364}) {
		t.Errorf("after the writes: %v", got)
	}
	if len(seen) != 2 {
		t.Fatalf("OnWrite called %d times", len(seen))
	}
	w := seen[1]
	if w.ClientAddr != "test-client" || w.ModelIndex != 0 || w.ModelID != 64010 || w.Address != 40006 ||
		!reflect.DeepEqual(w.Registers, []uint16{2, 3, 77, 0xFFFE}) {
		t.Errorf("ServerWrite = %+v", w)
	}
	if len(w.Points) != 3 || w.Points[0].Name != "Wide" || w.Points[0].RawValue != uint32(0x00020003) ||
		w.Points[1].Name != "Lim" || *w.Points[1].ScaledValue != 0.77 || w.Points[2].Name != "Lim_SF" {
		t.Errorf("ServerWrite.Points = %+v", w.Points)
	}
	// The model shows the state after the write, the other points included.
	if p := w.Model.Point("Rw"); p.RawValue != uint16(33) {
		t.Errorf("ServerWrite.Model Rw = %+v", p)
	}

	// The written values are the model's values from now on: an update of
	// another point keeps them, with the new scale factor.
	if err := srv.SetPoint(0, "Ro", 12); err != nil {
		t.Fatal(err)
	}
	if got, _ := holdingRead(srv, 40004, 8); !reflect.DeepEqual(got, []uint16{12, 33, 2, 3, 77, 0xFFFE, 0x6162, 0x6364}) {
		t.Errorf("after a write and an update: %v", got)
	}
	if dm, _ := srv.Model(0); *dm.Point("Lim").ScaledValue != 0.77 {
		t.Errorf("Lim = %v", *dm.Point("Lim").ScaledValue)
	}

	// A client may write a not-implemented value; the point becomes unset.
	if err := holdingWrite(srv, 40005, 0xFFFF); err != nil {
		t.Fatal(err)
	}
	if dm, _ := srv.Model(0); dm.Point("Rw").Implemented {
		t.Error("Rw still implemented after writing 0xFFFF")
	}

	// A nested, writable point of the curve model: X of the first point of
	// the first curve, at 40017+2+3+2 = 40024.
	if err := holdingWrite(srv, 40024, 25); err != nil {
		t.Fatalf("write nested X: %v", err)
	}
	if last := seen[len(seen)-1]; last.ModelIndex != 2 || last.Points[0].Name != "X" || *last.Points[0].ScaledValue != 2.5 || last.Points[0].RegisterOffset != 7 {
		t.Errorf("nested write = %+v", last.Points)
	}

	// A refused write changes nothing and passes the hook's error on.
	before, _ := holdingRead(srv, 40000, 30)
	reject = true
	if err := holdingWrite(srv, 40005, 44); !errors.Is(err, modbus.ErrIllegalDataValue) {
		t.Errorf("rejected write: %v", err)
	}
	reject = false
	calls := len(seen)

	refused := []struct {
		name string
		addr uint16
		regs []uint16
	}{
		{"read-only point", 40004, []uint16{1}},
		{"read-only and writable point", 40004, []uint16{1, 2}},
		{"first half of a wide point", 40006, []uint16{1}},
		{"second half of a wide point", 40007, []uint16{1}},
		{"wide point and half of nothing", 40005, []uint16{1, 2}},
		{"half of a string", 40010, []uint16{1}},
		{"marker", 40000, []uint16{1, 2}},
		{"model ID", 40002, []uint16{1}},
		{"model length", 40003, []uint16{1}},
		{"across two models", 40010, []uint16{1, 2, 3}},
		{"raw model", 40014, []uint16{1}},
		{"count point with RW access", 40019, []uint16{1}},
		{"read-only count point", 40020, []uint16{1}},
		{"read-only nested point", 40025, []uint16{1}},
		{"writable point and the end marker", 40027, []uint16{1, 2}},
		{"end marker", 40028, []uint16{1}},
		{"past the map", 40030, []uint16{1}},
		{"before the map", 39999, []uint16{1}},
		{"nothing", 40005, nil},
	}
	for _, r := range refused {
		if err := holdingWrite(srv, r.addr, r.regs...); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("write to %s: err = %v, want illegal data address", r.name, err)
		}
	}
	if len(seen) != calls {
		t.Errorf("OnWrite was called for %d refused writes", len(seen)-calls)
	}
	if after, _ := holdingRead(srv, 40000, 30); !reflect.DeepEqual(before, after) {
		t.Errorf("refused writes changed the map:\n%v\n%v", before, after)
	}

	// Without a hook writes are simply accepted.
	plain := ctlServer(t, nil)
	if err := holdingWrite(plain, 40010, 0x4142, 0x4300); err != nil {
		t.Fatal(err)
	}
	if dm, _ := plain.Model(0); dm.Point("Name").RawValue != "ABC" {
		t.Errorf("Name = %v", dm.Point("Name").RawValue)
	}
}

// listenAddr returns a loopback address that was free a moment ago.
func listenAddr(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

// dial opens a client to addr that is closed when the test ends.
func dial(t testing.TB, addr string, timeout time.Duration) *modbus.Client {
	t.Helper()
	client, err := modbus.New(modbus.Config{URL: "tcp://" + addr, Timeout: timeout, DialTimeout: 2 * time.Second, Logger: modbus.NopLogger()})
	if err != nil {
		t.Fatalf("modbus.New: %v", err)
	}
	var openErr error
	for i := 0; i < 50; i++ {
		if openErr = client.Open(); openErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if openErr != nil {
		t.Fatalf("client.Open: %v", openErr)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestServerServe(t *testing.T) {
	srv := ctlServer(t, nil)
	addr := listenAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, &modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()})
	}()

	client := dial(t, addr, 2*time.Second)
	dev, err := Discover(testCtx(t), client, at(40000))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(dev.Discovery.Models) != 3 {
		t.Errorf("%d models discovered", len(dev.Discovery.Models))
	}

	// A second listener on the same address fails to start.
	if err := srv.Serve(context.Background(), &modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()}); err == nil {
		t.Error("Serve on an address in use must fail")
	}
	if err := srv.Serve(context.Background(), &modbus.ServerConfig{URL: "bogus://x"}); !errors.Is(err, modbus.ErrConfigurationError) {
		t.Errorf("Serve with an invalid URL: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
	// The listener is gone.
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = c.Close()
		t.Error("the address still accepts connections after Serve returned")
	}
}
