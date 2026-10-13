// SPDX-License-Identifier: MIT

package sunspec

// End-to-end tests: the library's own Server, behind a real go-modbus TCP
// server on loopback, read by the library's own client path.
//
// Feature matrix (feature -> test). Tests named TestIntegration..., TestRead...,
// TestDiscover... and TestDetect... are the older fixture-based tests in
// integration_test.go and device_test.go.
//
//	Detect: marker found at each default base address      TestE2EDetect
//	Detect: no marker -> ErrNotSunSpec with attempts        TestE2EDetect
//	Detect: every probe fails -> joined Modbus errors       TestE2EDiscoverOptionsUnitID
//	Discover / Open: chain, schemas, headers                TestE2EDiscoverLayout
//	DiscoverOptions nil -> defaults                         TestE2EDiscoverLayout
//	DiscoverOptions.UnitID (match, mismatch, zero)          TestE2EDiscoverOptionsUnitID
//	Server silent to other units (ErrNoResponse)            TestE2EDiscoverOptionsUnitID
//	DiscoverOptions.RegType (holding, input, mismatch)      TestE2EDiscoverOptionsRegType
//	DiscoverOptions.BaseAddresses (custom, empty, miss)     TestE2EDiscoverOptionsBaseAddresses
//	DiscoverOptions.MaxModels (cut chain + warning)         TestE2EDiscoverOptionsLimits
//	DiscoverOptions.MaxAddressSpan (limit exceeded)         TestE2EDiscoverOptionsLimits
//	ReadAll: every registry model, every point type         TestE2EReadAllRegistryModels
//	Scale factors: named, literal, outer scope, unset       TestE2EReadAllRegistryModels, TestE2EScaleFactorCases
//	Nested and repeating groups, count points               TestE2EReadAllRegistryModels, TestE2ENestedGroups
//	Not-implemented values of every type                    TestE2EReadAllRegistryModels (sparse values)
//	ReadModel / ReadModelByID / ModelByID, duplicate IDs    TestE2EReadModelAndPoint
//	ReadPoint: top level, repeating group, missing          TestE2EReadModelAndPoint
//	Vendor / unknown model as raw registers                 TestE2EVendorModels
//	Model longer than 125 registers (request splitting)     TestE2EVendorModels, TestE2EReadAllRegistryModels
//	Read spanning several models in one request             TestE2ESpanningRead
//	Live updates seen by the next read                      TestE2ELiveUpdates
//	Client writes to RW points (FC6, FC16), OnWrite         TestE2EClientWrites
//	Rejected writes (read-only, structure, hook, FC22/23)   TestE2EClientWrites
//	ErrUnknownModel, ErrPointNotFound                       TestE2EReadModelAndPoint
//	ErrPartialRead (later chunk fails)                      TestE2EErrorPaths
//	*DecodeError (model shorter than its schema)            TestE2EErrorPaths
//	ErrModelChanged (chain changed after discovery)         TestE2EErrorPaths
//	ErrModelChainInvalid                                    TestE2EErrorPaths
//	Unmapped address -> exception 2                         TestE2EErrorPaths
//	Closed client, cancelled context                        TestE2EErrorPaths
//	Nil client                                              TestNilClientIsAnErrorNotAPanic
//	Server.Serve lifecycle                                  TestServerServe
//	Faults on the wire and misbehaving devices              chaos_test.go
//	Concurrency                                             stress_test.go

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// e2eStart serves handler and returns a connected client.
func e2eStart(t testing.TB, handler modbus.RequestHandler) *modbus.Client {
	t.Helper()
	addr, _ := startModbus(t, handler)
	return dial(t, addr, 5*time.Second)
}

func TestE2EDetect(t *testing.T) {
	ctx := testCtx(t)
	for _, base := range []uint16{0, 40000, 50000} {
		d := newServed(t, 1, &ServerOptions{BaseAddress: base}, 1, 103)
		client := e2eStart(t, d.srv)
		res, err := Detect(ctx, client, nil)
		if err != nil {
			t.Fatalf("base %d: Detect: %v", base, err)
		}
		if !res.Detected || res.BaseAddress != base || res.UnitID != 1 || res.RegType != modbus.HoldingRegister ||
			res.Marker != [2]uint16{0x5375, 0x6E53} {
			t.Errorf("base %d: result = %+v", base, res)
		}
		if last := res.Attempts[len(res.Attempts)-1]; !last.Matched || last.BaseAddress != base {
			t.Errorf("base %d: last attempt = %+v", base, last)
		}
	}

	// A device that is readable at the probed address but holds no marker.
	d := newServed(t, 1, &ServerOptions{BaseAddress: 100}, 1)
	client := e2eStart(t, d.srv)
	// Address 102 holds the first model header, not "SunS".
	res, err := Detect(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{102, 7000}})
	if !errors.Is(err, ErrNotSunSpec) {
		t.Fatalf("Detect without marker: err = %v", err)
	}
	if res == nil || res.Detected || len(res.Attempts) != 2 || res.Attempts[0].Error != nil || res.Attempts[1].Error == nil {
		t.Errorf("result = %+v", res)
	}
	if _, err := Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{102, 7000}}); !errors.Is(err, ErrNotSunSpec) {
		t.Errorf("Discover without marker: err = %v", err)
	}
}

func TestE2EDiscoverLayout(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 2, &ServerOptions{BaseAddress: 40000}, 1, 103, 160, 1)
	client := e2eStart(t, d.srv)

	for name, discover := range map[string]func(context.Context, *modbus.Client, *DiscoverOptions) (*Device, error){
		"Discover": Discover, "Open": Open,
	} {
		dev, err := discover(ctx, client, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dev.UnitID != 1 || dev.RegType != modbus.HoldingRegister || dev.Client != client {
			t.Errorf("%s: device = %+v", name, dev)
		}
		disc := dev.Discovery
		if disc.BaseAddress != 40000 || disc.RegType != modbus.HoldingRegister || len(disc.Warnings) != 0 || disc.Raw == nil {
			t.Errorf("%s: discovery = %+v", name, disc)
		}
		// What the client found is what the server laid out.
		want := d.srv.Models()
		if len(disc.Models) != len(want) {
			t.Fatalf("%s: %d models discovered, want %d", name, len(disc.Models), len(want))
		}
		for i, inst := range disc.Models {
			if inst.Header != want[i] {
				t.Errorf("%s: model %d header = %+v, want %+v", name, i, inst.Header, want[i])
			}
			meta := registry.ByID(want[i].ID)
			if !inst.SchemaKnown || !inst.DecodingSupported || inst.Schema != meta || inst.Name != meta.Label {
				t.Errorf("%s: model %d instance = %+v", name, i, inst)
			}
		}
	}
}

func TestE2EDiscoverOptionsUnitID(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 3, &ServerOptions{BaseAddress: 40000, UnitID: 5}, 1, 101)
	client := e2eStart(t, d.srv)

	dev, err := Discover(ctx, client, &DiscoverOptions{UnitID: 5})
	if err != nil {
		t.Fatalf("matching unit: %v", err)
	}
	if dev.UnitID != 5 {
		t.Errorf("UnitID = %d", dev.UnitID)
	}
	results, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.checkAll(t, results)

	// Another unit does not answer: every probe fails, which is an error and
	// not "not a SunSpec device". Unit 0 selects unit 1.
	for _, unit := range []uint8{0, 1, 6} {
		res, err := Detect(ctx, client, &DiscoverOptions{UnitID: unit})
		if !errors.Is(err, modbus.ErrGWTargetFailedToRespond) || errors.Is(err, ErrNotSunSpec) || res != nil {
			t.Errorf("unit %d: Detect = %+v, %v", unit, res, err)
		}
		if _, err := Discover(ctx, client, &DiscoverOptions{UnitID: unit}); !errors.Is(err, modbus.ErrGWTargetFailedToRespond) {
			t.Errorf("unit %d: Discover err = %v", unit, err)
		}
	}
	// A device reading with the wrong unit fails the same way.
	dev.UnitID = 9
	if _, err := dev.ReadAll(ctx); !errors.Is(err, modbus.ErrGWTargetFailedToRespond) {
		t.Errorf("ReadAll with the wrong unit: %v", err)
	}

	// A device that stays silent to other units: the client times out, and
	// the connection keeps working for the right unit.
	quiet := newServed(t, 3, &ServerOptions{BaseAddress: 40000, UnitID: 5, SilentToOtherUnits: true}, 1)
	addr, _ := startModbus(t, quiet.srv)
	qc := dial(t, addr, 100*time.Millisecond)
	if _, err := qc.ReadRegisters(context.Background(), 6, 40000, 2, modbus.HoldingRegister); !errors.Is(err, modbus.ErrRequestTimedOut) {
		t.Errorf("silent device, other unit: err = %v, want a timeout", err)
	}
	if _, err := Detect(context.Background(), qc, &DiscoverOptions{UnitID: 6, BaseAddresses: []uint16{40000}}); err == nil || errors.Is(err, ErrNotSunSpec) {
		t.Errorf("Detect on a silent unit: %v", err)
	}
	if dev, err := Discover(ctx, qc, &DiscoverOptions{UnitID: 5}); err != nil || len(dev.Discovery.Models) != 1 {
		t.Errorf("silent device, own unit: %v", err)
	}
}

func TestE2EDiscoverOptionsRegType(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 4, &ServerOptions{BaseAddress: 0, RegType: modbus.InputRegister}, 1, 103, 160)
	client := e2eStart(t, d.srv)

	dev, err := Discover(ctx, client, &DiscoverOptions{RegType: modbus.InputRegister})
	if err != nil {
		t.Fatalf("input registers: %v", err)
	}
	if dev.RegType != modbus.InputRegister || dev.Discovery.RegType != modbus.InputRegister {
		t.Errorf("RegType = %d / %d", dev.RegType, dev.Discovery.RegType)
	}
	results, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.checkAll(t, results)

	// The same device has nothing in its holding registers.
	if _, err := Discover(ctx, client, nil); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("holding registers of an input device: %v", err)
	}
	// An invalid register type is refused before any I/O.
	if _, err := Discover(ctx, client, &DiscoverOptions{RegType: 7}); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("invalid RegType: %v", err)
	}
}

func TestE2EDiscoverOptionsBaseAddresses(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 5, &ServerOptions{BaseAddress: 1234}, 1, 101)
	client := e2eStart(t, d.srv)

	// Not at a default base address.
	if _, err := Discover(ctx, client, nil); err == nil || errors.Is(err, ErrNotSunSpec) {
		// Every default probe hits an unmapped address: a Modbus error.
		t.Errorf("default base addresses: err = %v, want the probe errors", err)
	}
	dev, err := Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{40000, 1234}})
	if err != nil {
		t.Fatalf("custom base address: %v", err)
	}
	if dev.Discovery.BaseAddress != 1234 || len(dev.Discovery.Raw.Detection.Attempts) != 2 {
		t.Errorf("discovery = %+v", dev.Discovery)
	}
	results, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.checkAll(t, results)

	if _, err := Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{}}); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("empty base address list: %v", err)
	}
}

func TestE2EDiscoverOptionsLimits(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 6, &ServerOptions{BaseAddress: 40000}, 1, 101, 120, 121, 122)
	client := e2eStart(t, d.srv)
	layout := d.srv.Models()

	// MaxModels cuts the chain; the missing end marker is a warning.
	dev, err := Discover(ctx, client, &DiscoverOptions{MaxModels: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Discovery.Models) != 3 || len(dev.Discovery.Warnings) != 1 {
		t.Errorf("MaxModels 3: %d models, warnings %q", len(dev.Discovery.Models), dev.Discovery.Warnings)
	}
	results, err := dev.ReadAll(ctx)
	if err != nil || len(results) != 3 {
		t.Fatalf("ReadAll of the cut chain: %d models, %v", len(results), err)
	}
	for i, dm := range results {
		d.checkModel(t, i, dm)
	}
	// Exactly enough for the models and the end marker: no warning.
	dev, err = Discover(ctx, client, &DiscoverOptions{MaxModels: 6})
	if err != nil || len(dev.Discovery.Models) != 5 || len(dev.Discovery.Warnings) != 0 {
		t.Errorf("MaxModels 6: %v, %+v", err, dev)
	}

	// MaxAddressSpan: the chain may reach exactly that far from the base.
	span := layout[4].NextAddress - 40000
	if _, err := Discover(ctx, client, &DiscoverOptions{MaxAddressSpan: span}); err != nil {
		t.Errorf("MaxAddressSpan %d: %v", span, err)
	}
	if _, err := Discover(ctx, client, &DiscoverOptions{MaxAddressSpan: span - 1}); !errors.Is(err, ErrModelChainLimitExceeded) {
		t.Errorf("MaxAddressSpan %d: err = %v", span-1, err)
	}
	if _, err := Discover(ctx, client, &DiscoverOptions{MaxAddressSpan: 10}); !errors.Is(err, ErrModelChainLimitExceeded) {
		t.Errorf("MaxAddressSpan 10: err = %v", err)
	}
}

// TestE2EReadAllRegistryModels serves every model of the registry with
// generated values, in a few devices, and reads them all back.
func TestE2EReadAllRegistryModels(t *testing.T) {
	ctx := testCtx(t)
	ids := registryIDs()
	const perDevice = 40
	points, long := 0, 0
	for start, seed := 0, int64(100); start < len(ids); start, seed = start+perDevice, seed+1 {
		end := start + perDevice
		if end > len(ids) {
			end = len(ids)
		}
		d := newServed(t, seed, &ServerOptions{BaseAddress: 0}, ids[start:end]...)
		client := e2eStart(t, d.srv)
		dev, err := Discover(ctx, client, nil)
		if err != nil {
			t.Fatalf("models %d..%d: Discover: %v", ids[start], ids[end-1], err)
		}
		results, err := dev.ReadAll(ctx)
		if err != nil {
			t.Fatalf("models %d..%d: ReadAll: %v", ids[start], ids[end-1], err)
		}
		d.checkAll(t, results)
		for i, dm := range results {
			points += d.gens[i].check(t, dm.Group, d.metas[i].Group.Name)
			if len(dm.RawRegisters) > 127 {
				long++
			}
		}
	}
	if points < 3000 || long < 3 {
		t.Errorf("%d points compared, %d models needed more than one request", points, long)
	}
}

func TestE2EScaleFactorCases(t *testing.T) {
	ctx := testCtx(t)
	// Model 103: A_SF given, V_SF left unset, a literal-free model; the ctl
	// and curve schemas add a writable and a nested scale factor.
	inverter := (&Values{}).Set("A", 12.34).Set("A_SF", -2).Set("PhVphA", 2301).Set("W", 5000).Set("W_SF", 1).Set("Hz", 50.01).Set("Hz_SF", -2)
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{ID: 103, Values: inverter},
		ServerModel{ID: 64020, Values: (&Values{}).Set("Lit", -1.234).Set("Zero", 77).Set("Zero_SF", 0)},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := e2eStart(t, srv)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	results, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		raw    any
		scaled *float64
		sf     *int16
	}
	f := func(v float64) *float64 { return &v }
	s := func(v int16) *int16 { return &v }
	check := func(dm *DecodedModel, name string, w want) {
		t.Helper()
		p := dm.Point(name)
		if p == nil || p.RawValue != w.raw || !reflect.DeepEqual(p.ScaledValue, w.scaled) || !reflect.DeepEqual(p.SFRawValue, w.sf) {
			t.Errorf("%s = %+v, want raw %v scaled %v sf %v", name, p, w.raw, w.scaled, w.sf)
		}
	}
	check(results[0], "A", want{uint16(1234), f(12.34), s(-2)})
	check(results[0], "W", want{int16(500), f(5000), s(1)})
	check(results[0], "Hz", want{uint16(5001), f(50.01), s(-2)})
	// V_SF is not implemented: the value is raw and unscaled on both sides.
	check(results[0], "PhVphA", want{uint16(2301), nil, nil})
	check(results[1], "Lit", want{int32(-1234), f(-1.234), s(-3)})
	check(results[1], "Zero", want{uint16(77), f(77), s(0)})
}

func TestE2ENestedGroups(t *testing.T) {
	ctx := testCtx(t)
	// DER volt-var (705): curves holding points, both counted by points of
	// the top-level group, with scale factors two levels up.
	vv := (&Values{}).Set("Ena", 1).Set("V_SF", -2).Set("DeptRef_SF", -1).Set("NPt", 3)
	for c := 0; c < 2; c++ {
		crv := vv.Add("Crv", (&Values{}).Set("ActPt", 2).Set("ReadOnly", "RW"))
		crv.Add("Pt", (&Values{}).Set("V", 0.92+float64(c)).Set("Var", 30.0))
		crv.Add("Pt", (&Values{}).Set("V", 1.08+float64(c)).Set("Var", -30.0))
	}
	custom := &Values{}
	custom.Set("X_SF", -1)
	custom.Add("Crv", nil).Add("Pt", (&Values{}).Set("X", 0.5))
	custom.Add("Rep", (&Values{}).Set("B", 1))
	custom.Add("Rep", (&Values{}).Set("B", 2))
	custom.Add("Rep", (&Values{}).Set("B", 3))
	srv, err := NewServer(&ServerOptions{BaseAddress: 0},
		ServerModel{ID: 705, Values: vv}, ServerModel{ID: 64002, Values: custom})
	if err != nil {
		t.Fatal(err)
	}
	client := e2eStart(t, srv)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	results, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, dm := range results {
		if len(dm.Warnings) != 0 {
			t.Errorf("model %d: warnings %v", dm.ModelID, dm.Warnings)
		}
	}
	top := results[0].Group
	if top.Point("NCrv").RawValue != uint16(2) || top.Point("NPt").RawValue != uint16(3) {
		t.Errorf("counts = %v, %v", top.Point("NCrv").RawValue, top.Point("NPt").RawValue)
	}
	curves := top.GroupsNamed("Crv")
	if len(curves) != 2 {
		t.Fatalf("%d curves", len(curves))
	}
	for c, crv := range curves {
		pts := crv.GroupsNamed("Pt")
		if len(pts) != 3 || crv.Index != c+1 {
			t.Fatalf("curve %d: %d points, index %d", c+1, len(pts), crv.Index)
		}
		if v := pts[1].Point("V"); v.ScaledValue == nil || *v.ScaledValue != 1.08+float64(c) {
			t.Errorf("curve %d point 2 V = %+v", c+1, v)
		}
		if v := pts[0].Point("Var"); v.ScaledValue == nil || *v.ScaledValue != 30 {
			t.Errorf("curve %d point 1 Var = %+v", c+1, v)
		}
		// The third point was not given: completed, not implemented.
		if pts[2].Point("V").Implemented || pts[2].Index != 3 {
			t.Errorf("curve %d point 3 = %+v", c+1, pts[2])
		}
		if !reflect.DeepEqual(crv.Point("ReadOnly").Symbols, []string{"RW"}) {
			t.Errorf("curve %d ReadOnly = %+v", c+1, crv.Point("ReadOnly"))
		}
	}
	// ReadPoint finds the first instance of a nested point.
	p, err := dev.ReadPoint(ctx, dev.Discovery.Models[0], "V")
	if err != nil || *p.ScaledValue != 0.92 {
		t.Errorf("ReadPoint(V) = %+v, %v", p, err)
	}
	reps := results[1].Group.GroupsNamed("Rep")
	if len(reps) != 3 || reps[2].Point("B").RawValue != uint32(3) || len(results[1].Group.GroupsNamed("Fix")) != 2 {
		t.Errorf("custom model groups: %d Rep, %d Fix", len(reps), len(results[1].Group.GroupsNamed("Fix")))
	}
}

func TestE2EReadModelAndPoint(t *testing.T) {
	ctx := testCtx(t)
	// Two inverter models of the same ID: lookups by ID find the first.
	d := newServed(t, 7, &ServerOptions{BaseAddress: 40000}, 1, 103, 160, 103)
	for index, watts := range map[int]int{1: 1111, 3: 2222} {
		if err := d.srv.Update(index, func(v *Values) error {
			v.Set("W", watts).Set("W_SF", 0)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	client := e2eStart(t, d.srv)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}

	inst := dev.ModelByID(103)
	if inst == nil || inst.Header != d.srv.Models()[1] {
		t.Fatalf("ModelByID(103) = %+v", inst)
	}
	if dev.ModelByID(999) != nil {
		t.Error("ModelByID(999) found something")
	}
	dm, err := dev.ReadModelByID(ctx, 103)
	if err != nil || dm.InstanceAddress != inst.Header.StartAddress || dm.Point("W").RawValue != int16(1111) {
		t.Errorf("ReadModelByID(103) = %+v, %v", dm, err)
	}
	// The second instance is reachable through its ModelInstance.
	dm, err = dev.ReadModel(ctx, dev.Discovery.Models[3])
	if err != nil || dm.Point("W").RawValue != int16(2222) {
		t.Errorf("ReadModel(second 103) = %v, %v", dm.Point("W"), err)
	}
	dm, err = dev.ReadModel(ctx, dev.Discovery.Models[2])
	if err != nil {
		t.Fatal(err)
	}
	d.checkModel(t, 2, dm)

	p, err := dev.ReadPoint(ctx, *inst, "W")
	if err != nil || p.RawValue != int16(1111) || *p.ScaledValue != 1111 || p.Units != "W" {
		t.Errorf("ReadPoint(W) = %+v, %v", p, err)
	}
	if _, err := dev.ReadPoint(ctx, *inst, "Nope"); !errors.Is(err, ErrPointNotFound) {
		t.Errorf("ReadPoint(Nope): %v", err)
	}
	if _, err := dev.ReadPoint(ctx, *inst, "w"); !errors.Is(err, ErrPointNotFound) {
		t.Errorf("ReadPoint is case sensitive: %v", err)
	}
	if _, err := dev.ReadModelByID(ctx, 999); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("ReadModelByID(999): %v", err)
	}
}

func TestE2EVendorModels(t *testing.T) {
	ctx := testCtx(t)
	long := seq(1, 400)
	srv, err := NewServer(&ServerOptions{BaseAddress: 50000},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "Acme")},
		ServerModel{ID: 64900, Registers: []uint16{0xDEAD, 0xBEEF}},
		ServerModel{ID: 64902, Registers: long},
		ServerModel{ID: 101},
	)
	if err != nil {
		t.Fatal(err)
	}
	sh := newShim(srv)
	client := e2eStart(t, sh)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Discovery.Warnings) != 2 {
		t.Errorf("discovery warnings = %q", dev.Discovery.Warnings)
	}
	if m := dev.Discovery.Models[1]; m.SchemaKnown || m.DecodingSupported || m.Schema != nil || m.Name != "unknown_64900" {
		t.Errorf("vendor model instance = %+v", m)
	}
	before := sh.reads.Load()
	results, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 68 + 4 + 402 + 52 registers: 1 + 1 + 4 + 1 requests.
	if n := sh.reads.Load() - before; n != 7 {
		t.Errorf("ReadAll used %d requests, want 7", n)
	}
	if dm := results[1]; dm.Group != nil || !reflect.DeepEqual(dm.RawRegisters, []uint16{0xDEAD, 0xBEEF}) || len(dm.Warnings) != 1 {
		t.Errorf("vendor model = %+v", dm)
	}
	if dm := results[2]; !reflect.DeepEqual(dm.RawRegisters, long) {
		t.Errorf("long vendor model: %d registers, first %v", len(dm.RawRegisters), dm.RawRegisters[:3])
	}
	if results[0].Point("Mn").RawValue != "Acme" || results[3].Point("W").Implemented {
		t.Errorf("neighbours of the vendor models: %+v %+v", results[0].Point("Mn"), results[3].Point("W"))
	}
	if _, err := dev.ReadPoint(ctx, dev.Discovery.Models[1], "X"); !errors.Is(err, ErrPointNotFound) {
		t.Errorf("ReadPoint on a vendor model: %v", err)
	}
	// Raw registers can be replaced while serving.
	if err := srv.SetRegisters(1, []uint16{1, 2}); err != nil {
		t.Fatal(err)
	}
	dm, err := dev.ReadModelByID(ctx, 64900)
	if err != nil || !reflect.DeepEqual(dm.RawRegisters, []uint16{1, 2}) {
		t.Errorf("after SetRegisters: %+v, %v", dm, err)
	}
}

func TestE2ESpanningRead(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 8, &ServerOptions{BaseAddress: 40000}, 1, 101)
	client := e2eStart(t, d.srv)
	layout := d.srv.Models()
	// From the last registers of model 1 over the header of model 101 into
	// its data, and the whole device up to the end marker.
	got, err := client.ReadRegisters(ctx, 1, layout[0].EndAddress-1, 6, modbus.HoldingRegister)
	if err != nil {
		t.Fatal(err)
	}
	if got[2] != 101 || got[3] != 50 {
		t.Errorf("spanning read = %v", got)
	}
	end := layout[1].NextAddress
	got, err = client.ReadRegisters(ctx, 1, 40000, end-40000+2, modbus.HoldingRegister)
	if err != nil || got[0] != 0x5375 || got[len(got)-2] != 0xFFFF || got[len(got)-1] != 0 {
		t.Errorf("whole device: %d registers, %v", len(got), err)
	}
	// One register further is unmapped: exception 2.
	if _, err := client.ReadRegisters(ctx, 1, 40000, end-40000+3, modbus.HoldingRegister); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read past the end marker: %v", err)
	}
	if _, err := client.ReadRegisters(ctx, 1, 39999, 2, modbus.HoldingRegister); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read before the marker: %v", err)
	}
	if _, err := client.ReadCoils(ctx, 1, 0, 1); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("coils: %v", err)
	}
}

func TestE2ELiveUpdates(t *testing.T) {
	ctx := testCtx(t)
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "Acme")},
		ServerModel{ID: 103, Values: (&Values{}).Set("W", 0).Set("W_SF", 1).Set("St", "OFF")},
		ServerModel{ID: 160, Values: func() *Values {
			v := (&Values{}).Set("DCW_SF", -1).Set("N", 2)
			v.Add("module", (&Values{}).Set("ID", 1).Set("DCW", 10.5))
			v.Add("module", (&Values{}).Set("ID", 2).Set("DCW", 20.5))
			return v
		}()},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := e2eStart(t, srv)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		watts := float64(i * 100)
		if err := srv.SetPoint(1, "W", watts); err != nil {
			t.Fatal(err)
		}
		if err := srv.Update(2, func(v *Values) error {
			v.Groups["module"][1].Set("DCW", watts/10)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		p, err := dev.ReadPoint(ctx, *dev.ModelByID(103), "W")
		if err != nil || *p.ScaledValue != watts {
			t.Fatalf("round %d: W = %+v, %v", i, p, err)
		}
		dm, err := dev.ReadModelByID(ctx, 160)
		if err != nil {
			t.Fatal(err)
		}
		mods := dm.Group.GroupsNamed("module")
		if *mods[1].Point("DCW").ScaledValue != watts/10 || *mods[0].Point("DCW").ScaledValue != 10.5 {
			t.Fatalf("round %d: module DCW = %v, %v", i, *mods[0].Point("DCW").ScaledValue, *mods[1].Point("DCW").ScaledValue)
		}
	}
	// A symbolic value, a point made not implemented, and a whole new set.
	if err := srv.SetPoint(1, "St", "MPPT"); err != nil {
		t.Fatal(err)
	}
	if err := srv.SetPoint(1, "W", nil); err != nil {
		t.Fatal(err)
	}
	dm, err := dev.ReadModelByID(ctx, 103)
	if err != nil || !reflect.DeepEqual(dm.Point("St").Symbols, []string{"MPPT"}) || dm.Point("W").Implemented {
		t.Errorf("St = %+v, W = %+v, %v", dm.Point("St"), dm.Point("W"), err)
	}
	if err := srv.SetValues(0, (&Values{}).Set("Md", "X1")); err != nil {
		t.Fatal(err)
	}
	dm, err = dev.ReadModelByID(ctx, 1)
	if err != nil || dm.Point("Mn").Implemented || dm.Point("Md").RawValue != "X1" {
		t.Errorf("after SetValues: Mn %+v Md %+v, %v", dm.Point("Mn"), dm.Point("Md"), err)
	}
	// The layout did not move.
	if again, err := Discover(ctx, client, nil); err != nil || !reflect.DeepEqual(again.Discovery.Models, dev.Discovery.Models) {
		t.Errorf("layout changed by updates: %v", err)
	}
}

func TestE2EClientWrites(t *testing.T) {
	ctx := testCtx(t)
	writes := make(chan *ServerWrite, 16)
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000, OnWrite: func(_ context.Context, w *ServerWrite) error {
		for _, p := range w.Points {
			if p.Name == "WMaxLimPct" && p.ScaledValue != nil && *p.ScaledValue > 100 {
				return modbus.ErrIllegalDataValue
			}
			if p.Name == "Conn_WinTms" && p.RawValue == uint16(666) {
				return errors.New("not a Modbus exception")
			}
		}
		writes <- w
		return nil
	}},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "Acme").Set("DA", 1)},
		ServerModel{ID: 123, Values: (&Values{}).Set("WMaxLimPct", 100).Set("WMaxLimPct_SF", -1).Set("Conn", "CONNECT")},
		ServerModel{ID: 64900, Registers: []uint16{1, 2, 3}},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := e2eStart(t, srv)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctl := dev.ModelByID(123).Header.StartAddress
	common := dev.ModelByID(1).Header.StartAddress

	// FC6: WMaxLimPct (offset 5) to 50.5 %.
	if err := client.WriteRegister(ctx, 1, ctl+5, 505); err != nil {
		t.Fatalf("WriteRegister: %v", err)
	}
	w := <-writes
	if w.ModelID != 123 || w.ModelIndex != 1 || w.Address != ctl+5 || len(w.Points) != 1 || *w.Points[0].ScaledValue != 50.5 || w.ClientAddr == "" {
		t.Errorf("OnWrite got %+v", w)
	}
	p, err := dev.ReadPoint(ctx, *dev.ModelByID(123), "WMaxLimPct")
	if err != nil || *p.ScaledValue != 50.5 {
		t.Errorf("WMaxLimPct = %+v, %v", p, err)
	}
	// FC16: three consecutive writable points (offsets 6..8).
	if err := client.WriteRegisters(ctx, 1, ctl+6, []uint16{10, 20, 30}); err != nil {
		t.Fatalf("WriteRegisters: %v", err)
	}
	if w := <-writes; len(w.Points) != 3 || w.Points[2].Name != "WMaxLimPct_RmpTms" {
		t.Errorf("OnWrite got %+v", w.Points)
	}
	// The server side sees the writes, and its own updates keep them.
	if err := srv.SetPoint(1, "Conn", "DISCONNECT"); err != nil {
		t.Fatal(err)
	}
	dm, err := dev.ReadModelByID(ctx, 123)
	if err != nil {
		t.Fatal(err)
	}
	if *dm.Point("WMaxLimPct").ScaledValue != 50.5 || dm.Point("WMaxLimPct_RvrtTms").RawValue != uint16(20) ||
		!reflect.DeepEqual(dm.Point("Conn").Symbols, []string{"DISCONNECT"}) {
		t.Errorf("after writes and an update: %+v", dm.Group.Points[:9])
	}
	local, err := srv.Model(1)
	if err != nil || *local.Point("WMaxLimPct").ScaledValue != 50.5 {
		t.Errorf("Server.Model after a write: %v", err)
	}
	// The writable point of the Common model.
	if err := client.WriteRegister(ctx, 1, common+66, 42); err != nil {
		t.Errorf("write DA: %v", err)
	}
	<-writes

	// Refused by the Server: exception 2, nothing changes.
	before, err := client.ReadRegisters(ctx, 1, 40000, 103, modbus.HoldingRegister)
	if err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"read-only scale factor":    func() error { return client.WriteRegister(ctx, 1, ctl+23, 0) },
		"read-only string":          func() error { return client.WriteRegisters(ctx, 1, common+2, []uint16{0x4142, 0x4344}) },
		"model ID":                  func() error { return client.WriteRegister(ctx, 1, ctl, 124) },
		"model length":              func() error { return client.WriteRegister(ctx, 1, ctl+1, 2) },
		"marker":                    func() error { return client.WriteRegister(ctx, 1, 40000, 0) },
		"writable into read-only":   func() error { return client.WriteRegisters(ctx, 1, ctl+22, []uint16{1, 0}) },
		"vendor model":              func() error { return client.WriteRegister(ctx, 1, dev.ModelByID(64900).Header.StartAddress+2, 9) },
		"end marker":                func() error { return client.WriteRegister(ctx, 1, dev.ModelByID(64900).Header.NextAddress, 9) },
		"unmapped address":          func() error { return client.WriteRegister(ctx, 1, 100, 9) },
		"across the model boundary": func() error { return client.WriteRegisters(ctx, 1, common+66, []uint16{1, 0x8000, 123}) },
	} {
		if err := write(); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("write to %s: err = %v, want exception 2", name, err)
		}
	}
	// Refused by the hook: its exception, or a device failure for any
	// other error.
	if err := client.WriteRegister(ctx, 1, ctl+5, 1001); !errors.Is(err, modbus.ErrIllegalDataValue) {
		t.Errorf("hook refusal: %v", err)
	}
	if err := client.WriteRegister(ctx, 1, ctl+2, 666); !errors.Is(err, modbus.ErrServerDeviceFailure) {
		t.Errorf("hook failure: %v", err)
	}
	// Function codes the Server does not implement.
	if err := client.MaskWriteRegister(ctx, 1, ctl+5, 0xFF00, 0x0001); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("mask write: %v", err)
	}
	if _, err := client.ReadWriteMultipleRegisters(ctx, 1, ctl, 2, ctl+5, []uint16{1}); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("read/write: %v", err)
	}
	if err := client.WriteCoil(ctx, 1, 0, true); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("write coil: %v", err)
	}
	after, err := client.ReadRegisters(ctx, 1, 40000, 103, modbus.HoldingRegister)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("refused writes changed the device: %v", err)
	}
	if len(writes) != 0 {
		t.Errorf("OnWrite accepted %d of the refused writes", len(writes))
	}
}

func TestE2EErrorPaths(t *testing.T) {
	ctx := testCtx(t)
	long := seq(1, 300)
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "Acme")},
		ServerModel{ID: 64902, Registers: long},
		// A known model that is shorter than its schema.
		ServerModel{ID: 103, Registers: seq(1, 10)},
		ServerModel{ID: 101, Values: (&Values{}).Set("W", 7)},
	)
	if err != nil {
		t.Fatal(err)
	}
	sh := newShim(srv)
	addr, _ := startModbus(t, sh)
	client := dial(t, addr, 5*time.Second)
	dev, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	layout := srv.Models()

	// DecodeError: fewer registers than the schema's fixed block. ReadAll
	// still returns every model.
	results, err := dev.ReadAll(ctx)
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrDecode) || de.ModelID != 103 {
		t.Fatalf("ReadAll err = %v, want a DecodeError for model 103", err)
	}
	if len(results) != 4 || results[2].Group != nil || len(results[2].RawRegisters) != 12 || results[3].Point("W").RawValue != int16(7) {
		t.Errorf("ReadAll results around the short model: %+v", results[2])
	}

	// ErrPartialRead: the third request of the long model fails.
	failFrom := layout[1].StartAddress + 250
	sh.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if addr >= failFrom && addr < layout[1].NextAddress {
			return nil, modbus.ErrServerDeviceBusy
		}
		return regs, err
	})
	dm, err := dev.ReadModelByID(ctx, 64902)
	if !errors.Is(err, ErrPartialRead) || !errors.Is(err, modbus.ErrServerDeviceBusy) {
		t.Errorf("partial read err = %v", err)
	}
	if dm == nil || !reflect.DeepEqual(dm.RawRegisters, long[:248]) || len(dm.Warnings) != 1 {
		t.Errorf("partial read model: %d registers", len(dm.RawRegisters))
	}
	// An exception on a single-request model is passed on as it is.
	sh.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		return nil, modbus.ErrIllegalDataAddress
	})
	if _, err := dev.ReadModelByID(ctx, 1); !errors.Is(err, modbus.ErrIllegalDataAddress) || errors.Is(err, ErrPartialRead) {
		t.Errorf("exception on a short model: %v", err)
	}
	sh.setFilter(nil)

	// ErrModelChanged: the device behind the address now has another chain.
	other, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "Other")},
		ServerModel{ID: 64902, Registers: seq(500, 299)},
		ServerModel{ID: 101, Values: (&Values{}).Set("W", 8)},
	)
	if err != nil {
		t.Fatal(err)
	}
	sh.setTarget(other)
	// Model 1 is where it was: still readable, with the new values.
	if dm, err := dev.ReadModelByID(ctx, 1); err != nil || dm.Point("Mn").RawValue != "Other" {
		t.Errorf("unchanged model after the swap: %v", err)
	}
	// Model 64902 changed its length and what followed it has moved: the
	// old addresses hold other models, or nothing at all.
	for id, want := range map[uint16]error{64902: ErrModelChanged, 103: ErrModelChanged, 101: modbus.ErrIllegalDataAddress} {
		dm, err := dev.ReadModelByID(ctx, id)
		if !errors.Is(err, want) {
			t.Errorf("model %d after the swap: err = %v, want %v", id, err, want)
		}
		if dm == nil || dm.Group != nil {
			t.Errorf("model %d after the swap must not be decoded: %+v", id, dm)
		}
	}
	if _, err := dev.ReadPoint(ctx, dev.Discovery.Models[2], "A"); !errors.Is(err, ErrModelChanged) {
		t.Errorf("ReadPoint after the swap: %v", err)
	}
	if _, err := dev.ReadAll(ctx); !errors.Is(err, ErrModelChanged) {
		t.Errorf("ReadAll after the swap: %v", err)
	}
	// Discovering again fixes it.
	dev2, err := Discover(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := dev2.ReadPoint(ctx, *dev2.ModelByID(101), "W"); err != nil || p.RawValue != int16(8) {
		t.Errorf("after rediscovery: %+v, %v", p, err)
	}

	// ErrModelChainInvalid: a model of length zero that is not the end
	// marker.
	sh.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if addr == 40002 && quantity == 2 {
			return []uint16{1, 0}, nil
		}
		return regs, err
	})
	if _, err := Discover(ctx, client, nil); !errors.Is(err, ErrModelChainInvalid) {
		t.Errorf("zero-length model: %v", err)
	}
	sh.setFilter(nil)

	// A cancelled context and a closed client.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Discover(cctx, client, nil); err == nil {
		t.Error("Discover with a cancelled context succeeded")
	}
	if _, err := dev2.ReadAll(cctx); err == nil {
		t.Error("ReadAll with a cancelled context succeeded")
	}
	_ = client.Close()
	if _, err := Discover(ctx, client, nil); err == nil || errors.Is(err, ErrNotSunSpec) {
		t.Errorf("Discover on a closed client: %v", err)
	}
	if _, err := dev2.ReadAll(ctx); err == nil {
		t.Error("ReadAll on a closed client succeeded")
	}
}
