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
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
	"github.com/otfabric/go-sunspec/registry"
	"github.com/otfabric/go-sunspec/testutil"
)

// labelLessModelID is a vendor-range model ID registered only for these tests,
// with a schema that has a name but no label.
const labelLessModelID = 64901

func init() {
	registry.Register(&registry.ModelMeta{
		ID:   labelLessModelID,
		Name: "label_less",
		Group: &registry.GroupMeta{Name: "label_less", Count: 1, Length: 3, Points: []registry.PointMeta{
			{Name: "ID", Type: "uint16", Size: 1, Offset: 0},
			{Name: "L", Type: "uint16", Size: 1, Offset: 1},
			{Name: "V", Type: "uint16", Size: 1, Offset: 2},
		}},
	})
}

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

// serve starts a fixture server for handler and returns a connected client
// that is closed when the test ends.
func serve(t *testing.T, handler *testutil.SunSpecHandler) *modbus.Client {
	t.Helper()
	client, cleanup := testutil.StartServerClient(t, handler, freePort(t))
	t.Cleanup(cleanup)
	return client
}

// testCtx returns a context that bounds one test's I/O.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// seq returns n registers holding start, start+1, ...
func seq(start, n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = uint16(start + i)
	}
	return out
}

// commonModel returns a valid, mostly empty Common model (ID 1).
func commonModel() testutil.FixtureModel {
	regs := make([]uint16, 66)
	copy(regs, testutil.StringToRegisters("Acme", 16))
	return testutil.FixtureModel{ID: 1, Length: 66, Registers: regs}
}

func at(base uint16) *DiscoverOptions {
	return &DiscoverOptions{BaseAddresses: []uint16{base}}
}

func TestNewDeviceNilDiscovery(t *testing.T) {
	d := NewDevice(nil, 3, nil)
	if d == nil {
		t.Fatal("NewDevice returned nil")
	}
	if d.UnitID != 3 || d.Discovery != nil || d.RegType != modbus.HoldingRegister {
		t.Errorf("device = %+v", d)
	}
	if got := d.ModelByID(1); got != nil {
		t.Errorf("ModelByID on a device without discovery = %+v, want nil", got)
	}
	results, err := d.ReadAll(context.Background())
	if results != nil || err != nil {
		t.Errorf("ReadAll on a device without discovery = %v, %v; want nil, nil", results, err)
	}
	if _, err := d.ReadModelByID(context.Background(), 1); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("ReadModelByID = %v, want ErrUnknownModel", err)
	}
}

func TestNewDeviceCopiesRegType(t *testing.T) {
	disc := &DiscoveryResult{RegType: modbus.InputRegister, Models: []ModelInstance{
		{Header: ModelHeader{ID: 1}}, {Header: ModelHeader{ID: 160, StartAddress: 10}}, {Header: ModelHeader{ID: 160, StartAddress: 20}},
	}}
	d := NewDevice(nil, 9, disc)
	if d.RegType != modbus.InputRegister || d.UnitID != 9 || d.Discovery != disc || d.Client != nil {
		t.Errorf("device = %+v", d)
	}
	got := d.ModelByID(160)
	if got != &disc.Models[1] {
		t.Errorf("ModelByID(160) = %+v, want a pointer to the first instance in the discovery result", got)
	}
	if d.ModelByID(2) != nil {
		t.Error("ModelByID of an absent model must be nil")
	}
}

func TestNilClientIsAnErrorNotAPanic(t *testing.T) {
	ctx := context.Background()

	res, err := Detect(ctx, nil, nil)
	if res != nil || !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("Detect(nil client) = %v, %v", res, err)
	}
	dev, err := Discover(ctx, nil, nil)
	if dev != nil || !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("Discover(nil client) = %v, %v", dev, err)
	}
	dev, err = Open(ctx, nil, nil)
	if dev != nil || !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("Open(nil client) = %v, %v", dev, err)
	}

	d := NewDevice(nil, 1, &DiscoveryResult{Models: []ModelInstance{{Header: ModelHeader{ID: 1, Length: 66, StartAddress: 2}, Name: "Common"}}})
	dm, err := d.ReadModel(ctx, d.Discovery.Models[0])
	if !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Fatalf("ReadModel(nil client) error = %v", err)
	}
	if dm == nil || dm.ModelID != 1 || dm.Name != "Common" || len(dm.Warnings) != 1 || dm.RawRegisters != nil {
		t.Errorf("ReadModel(nil client) model = %+v", dm)
	}
	if _, err := d.ReadPoint(ctx, d.Discovery.Models[0], "Mn"); !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("ReadPoint(nil client) error = %v", err)
	}
	results, err := d.ReadAll(ctx)
	if len(results) != 1 || !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("ReadAll(nil client) = %v, %v", results, err)
	}
}

func TestReadRegistersChunking(t *testing.T) {
	const base = 1000
	h := &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{}}
	for i := 0; i < 400; i++ {
		h.Registers[uint16(base+i)] = uint16(i + 1)
	}
	client := serve(t, h)
	ctx := testCtx(t)

	// Every chunk boundary: one request, exactly two full requests, a
	// trailing single register, three requests.
	for _, n := range []int{1, 124, 125, 126, 250, 251, 375, 400} {
		got, err := readRegisters(ctx, client, 1, base, uint16(n), modbus.HoldingRegister)
		if err != nil {
			t.Fatalf("read %d registers: %v", n, err)
		}
		if !reflect.DeepEqual(got, seq(1, n)) {
			t.Errorf("read %d registers: got %d values, first/last = %v/%v", n, len(got), got[0], got[len(got)-1])
		}
	}

	// A range that starts mid-way lines up its chunks on the start address.
	got, err := readRegisters(ctx, client, 1, base+100, 300, modbus.HoldingRegister)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, seq(101, 300)) {
		t.Errorf("offset read returned wrong data: first=%d last=%d len=%d", got[0], got[len(got)-1], len(got))
	}
}

func TestReadRegistersPartialRead(t *testing.T) {
	const base = 1000
	h := &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{}}
	for i := 0; i < 300; i++ {
		h.Registers[uint16(base+i)] = uint16(i + 1)
	}
	client := serve(t, h)
	ctx := testCtx(t)

	// The third chunk (registers 250..349) runs past the mapped range.
	got, err := readRegisters(ctx, client, 1, base, 350, modbus.HoldingRegister)
	if !errors.Is(err, ErrPartialRead) {
		t.Fatalf("err = %v, want ErrPartialRead", err)
	}
	if !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("err = %v, want it to wrap the Modbus exception too", err)
	}
	if !strings.Contains(err.Error(), "read at 1000+250") {
		t.Errorf("err = %q, want it to locate the failing chunk", err)
	}
	if !reflect.DeepEqual(got, seq(1, 250)) {
		t.Errorf("partial result has %d registers, want the first 250", len(got))
	}

	// A failure in the very first chunk of a long read is partial as well,
	// with nothing read.
	got, err = readRegisters(ctx, client, 1, 5000, 200, modbus.HoldingRegister)
	if !errors.Is(err, ErrPartialRead) || !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("err = %v, want ErrPartialRead wrapping ErrIllegalDataAddress", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d registers, want none", len(got))
	}

	// A read that fits in one request reports the Modbus error unwrapped.
	got, err = readRegisters(ctx, client, 1, 5000, 10, modbus.HoldingRegister)
	if !errors.Is(err, modbus.ErrIllegalDataAddress) || errors.Is(err, ErrPartialRead) {
		t.Errorf("single-request err = %v, want plain ErrIllegalDataAddress", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d registers, want none", len(got))
	}
}

// Regression: a multi-request read whose first chunk ends exactly at register
// 65535 used to wrap its second chunk around to address 0 and return those
// registers as if they followed.
func TestReadRegistersRejectsAddressSpaceOverflow(t *testing.T) {
	const start = 65536 - 125
	h := &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{}}
	for i := 0; i < 125; i++ {
		h.Registers[uint16(start+i)] = 0xAAAA
	}
	for i := 0; i < 125; i++ {
		h.Registers[uint16(i)] = 0xBBBB
	}
	client := serve(t, h)
	ctx := testCtx(t)

	for _, tc := range []struct {
		addr, qty uint16
	}{
		{start, 126}, {start, 250}, {65535, 2}, {65000, 600}, {2, 65535},
	} {
		got, err := readRegisters(ctx, client, 1, tc.addr, tc.qty, modbus.HoldingRegister)
		if !errors.Is(err, modbus.ErrUnexpectedParameters) {
			t.Errorf("read %d+%d: err = %v, want ErrUnexpectedParameters", tc.addr, tc.qty, err)
		}
		if errors.Is(err, ErrPartialRead) {
			t.Errorf("read %d+%d: rejected range reported as a partial read", tc.addr, tc.qty)
		}
		if got != nil {
			t.Errorf("read %d+%d: got %d registers, want none", tc.addr, tc.qty, len(got))
		}
	}

	// The last registers of the address space remain readable.
	got, err := readRegisters(ctx, client, 1, start, 125, modbus.HoldingRegister)
	if err != nil || len(got) != 125 || got[124] != 0xAAAA {
		t.Errorf("read up to register 65535: %d registers, err %v", len(got), err)
	}
}

// Regression: StartAddress+2 was computed in uint16, so a model header at the
// very end of the address space was read from address 0 or 1.
func TestReadModelRejectsHeaderAtEndOfAddressSpace(t *testing.T) {
	h := &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{0: 0x1111, 1: 0x2222, 2: 0x3333}}
	client := serve(t, h)
	d := NewDevice(client, 1, &DiscoveryResult{})

	for _, start := range []uint16{0xFFFE, 0xFFFF} {
		inst := ModelInstance{Header: ModelHeader{ID: 64000, Length: 1, StartAddress: start}, Name: "edge"}
		dm, err := d.ReadModel(testCtx(t), inst)
		if !errors.Is(err, modbus.ErrUnexpectedParameters) {
			t.Fatalf("start %#x: err = %v, want ErrUnexpectedParameters", start, err)
		}
		if dm == nil || len(dm.RawRegisters) != 0 || len(dm.Warnings) != 1 {
			t.Errorf("start %#x: model = %+v, want no registers and one warning", start, dm)
		}
	}

	// The highest header address that still has a data register works.
	h.Registers[0xFFFF] = 0x4444
	dm, err := d.ReadModel(testCtx(t), ModelInstance{Header: ModelHeader{ID: 64000, Length: 1, StartAddress: 0xFFFD}})
	if err != nil {
		t.Fatalf("model ending at 0xFFFF: %v", err)
	}
	if !reflect.DeepEqual(dm.RawRegisters, []uint16{0x4444}) {
		t.Errorf("RawRegisters = %#v", dm.RawRegisters)
	}
}

func TestReadAllContinuesPastFailingModel(t *testing.T) {
	// Model 64950 announces 200 registers but only 150 exist, so its second
	// read request fails. The model after it must still be read.
	broken := testutil.FixtureModel{ID: 64950, Length: 200, Registers: seq(1, 150)}
	tail := testutil.FixtureModel{ID: labelLessModelID, Length: 1, Registers: []uint16{42}}
	client := serve(t, testutil.NewSunSpecFixture(40000, 1, commonModel(), broken, tail))
	ctx := testCtx(t)

	dev, err := Discover(ctx, client, at(40000))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	results, err := dev.ReadAll(ctx)
	if !errors.Is(err, ErrPartialRead) || !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Fatalf("ReadAll err = %v, want ErrPartialRead wrapping ErrIllegalDataAddress", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want one per model (3)", len(results))
	}

	if results[0].ModelID != 1 || results[0].Group == nil || len(results[0].Warnings) != 0 {
		t.Errorf("model before the failure not decoded: %+v", results[0])
	}

	bad := results[1]
	if bad.ModelID != 64950 || bad.Name != "unknown_64950" || bad.InstanceAddress != 40070 {
		t.Errorf("failed model identity = %+v", bad)
	}
	if !reflect.DeepEqual(bad.RawRegisters, seq(1, 125)) {
		t.Errorf("failed model has %d registers, want the 125 of the first request", len(bad.RawRegisters))
	}
	if len(bad.Warnings) != 1 || !strings.HasPrefix(bad.Warnings[0], "read error: ") {
		t.Errorf("failed model warnings = %q", bad.Warnings)
	}
	if bad.Group != nil || bad.Schema != nil {
		t.Errorf("failed model must not be decoded: %+v", bad)
	}

	good := results[2]
	if good.ModelID != labelLessModelID || good.Name != "label_less" {
		t.Errorf("model after the failure = %+v", good)
	}
	if v := pointByName(t, good.Group, "V"); v.RawValue != uint16(42) {
		t.Errorf("V = %v, want 42", v.RawValue)
	}
	// The header registers are prepended so offsets match the schema.
	if !reflect.DeepEqual(good.RawRegisters, []uint16{labelLessModelID, 1, 42}) {
		t.Errorf("RawRegisters = %v, want ID, L and the data", good.RawRegisters)
	}

	// The first error wins when several models fail.
	dev.Discovery.Models = append(dev.Discovery.Models, ModelInstance{Header: ModelHeader{ID: 7, Length: 1, StartAddress: 0xFFFF}})
	results, err = dev.ReadAll(ctx)
	if len(results) != 4 || !errors.Is(err, ErrPartialRead) {
		t.Errorf("ReadAll with two failures: %d results, err %v", len(results), err)
	}
}

func TestReadModelFailures(t *testing.T) {
	// Model 1 announces only 10 data registers: fewer than its schema needs.
	short := testutil.FixtureModel{ID: 1, Length: 10, Registers: seq(1, 10)}
	// A second model 1 whose data is not readable at all.
	missing := testutil.FixtureModel{ID: 1, Length: 66}
	client := serve(t, testutil.NewSunSpecFixture(100, 1, short, missing))
	ctx := testCtx(t)

	dev, err := Discover(ctx, client, at(100))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(dev.Discovery.Models) != 2 {
		t.Fatalf("got %d models, want 2", len(dev.Discovery.Models))
	}

	dm, err := dev.ReadModel(ctx, dev.Discovery.Models[0])
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrDecode) {
		t.Fatalf("short model: err = %v, want *DecodeError", err)
	}
	if de.ModelID != 1 {
		t.Errorf("DecodeError.ModelID = %d, want 1", de.ModelID)
	}
	if dm == nil || dm.Group != nil || len(dm.RawRegisters) != 12 || dm.RawRegisters[0] != 1 || dm.RawRegisters[1] != 10 {
		t.Errorf("short model = %+v, want only the 12 raw registers", dm)
	}

	// ReadModelByID picks the first instance, so it sees the same failure.
	if _, err := dev.ReadModelByID(ctx, 1); !errors.Is(err, ErrDecode) {
		t.Errorf("ReadModelByID err = %v, want ErrDecode", err)
	}
	// ReadPoint surfaces the read error and no point.
	if dp, err := dev.ReadPoint(ctx, dev.Discovery.Models[0], "Mn"); dp != nil || !errors.Is(err, ErrDecode) {
		t.Errorf("ReadPoint on short model = %v, %v", dp, err)
	}

	dm, err = dev.ReadModel(ctx, dev.Discovery.Models[1])
	if !errors.Is(err, modbus.ErrIllegalDataAddress) || errors.Is(err, ErrPartialRead) {
		t.Fatalf("unreadable model: err = %v, want plain ErrIllegalDataAddress", err)
	}
	if dm == nil || dm.ModelID != 1 || len(dm.RawRegisters) != 0 || len(dm.Warnings) != 1 {
		t.Errorf("unreadable model = %+v", dm)
	}
}

func TestReadPointWithoutSchema(t *testing.T) {
	unknown := testutil.FixtureModel{ID: 64960, Length: 2, Registers: []uint16{7, 8}}
	client := serve(t, testutil.NewSunSpecFixture(0, 1, unknown))
	ctx := testCtx(t)

	dev, err := Discover(ctx, client, at(0))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	inst := dev.Discovery.Models[0]
	if inst.SchemaKnown || inst.DecodingSupported || inst.Schema != nil || inst.Name != "unknown_64960" {
		t.Errorf("instance = %+v", inst)
	}
	if want := []string{"model 64960: no local schema definition"}; !reflect.DeepEqual(dev.Discovery.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", dev.Discovery.Warnings, want)
	}
	dp, err := dev.ReadPoint(ctx, inst, "ID")
	if dp != nil || !errors.Is(err, ErrPointNotFound) {
		t.Errorf("ReadPoint on a model without schema = %v, %v; want ErrPointNotFound", dp, err)
	}

	// A schema flag without a schema pointer is treated as "no schema".
	inst.SchemaKnown = true
	dm, err := dev.ReadModel(ctx, inst)
	if err != nil || dm.Group != nil || !reflect.DeepEqual(dm.RawRegisters, []uint16{7, 8}) || len(dm.Warnings) != 1 {
		t.Errorf("ReadModel = %+v, %v", dm, err)
	}
}

func TestDiscoverDefaults(t *testing.T) {
	// Marker at 40000: with nil options the default base addresses are
	// probed in order, so the unreadable address 0 is tried first.
	client := serve(t, testutil.NewSunSpecFixture(40000, 1, commonModel()))
	ctx := testCtx(t)

	for name, open := range map[string]func(context.Context, *modbus.Client, *DiscoverOptions) (*Device, error){
		"Discover": Discover, "Open": Open,
	} {
		dev, err := open(ctx, client, nil)
		if err != nil {
			t.Fatalf("%s(nil options): %v", name, err)
		}
		if dev.Client != client || dev.UnitID != 1 || dev.RegType != modbus.HoldingRegister {
			t.Errorf("%s: device = %+v", name, dev)
		}
		disc := dev.Discovery
		if disc.BaseAddress != 40000 || disc.RegType != modbus.HoldingRegister || len(disc.Warnings) != 0 {
			t.Errorf("%s: discovery = %+v", name, disc)
		}
		if len(disc.Models) != 1 {
			t.Fatalf("%s: got %d models, want 1 (the end marker is not a model)", name, len(disc.Models))
		}
		m := disc.Models[0]
		want := ModelHeader{ID: 1, Length: 66, StartAddress: 40002, EndAddress: 40069, NextAddress: 40070}
		if m.Header != want {
			t.Errorf("%s: header = %+v, want %+v", name, m.Header, want)
		}
		if m.Name != "Common" || !m.SchemaKnown || !m.DecodingSupported || m.Schema != registry.ByID(1) {
			t.Errorf("%s: instance = %+v", name, m)
		}
		// The raw result keeps what the enriched one drops.
		if disc.Raw == nil || len(disc.Raw.Models) != 2 || !disc.Raw.Models[1].IsEndModel {
			t.Errorf("%s: Raw = %+v, want the model and the end marker", name, disc.Raw)
		}
		if n := len(disc.Raw.Detection.Attempts); n != 2 {
			t.Errorf("%s: %d detection attempts, want 2 (address 0, then 40000)", name, n)
		}
	}
}

func TestDiscoverUnitID(t *testing.T) {
	client := serve(t, testutil.NewSunSpecFixture(0, 7, commonModel()))
	ctx := testCtx(t)

	dev, err := Discover(ctx, client, &DiscoverOptions{UnitID: 7, BaseAddresses: []uint16{0}})
	if err != nil {
		t.Fatalf("Discover(unit 7): %v", err)
	}
	if dev.UnitID != 7 {
		t.Errorf("UnitID = %d, want 7", dev.UnitID)
	}
	if _, err := dev.ReadModelByID(ctx, 1); err != nil {
		t.Errorf("ReadModelByID with unit 7: %v", err)
	}

	// Unit ID 0 means unit 1, which this device does not answer.
	dev, err = Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{0}})
	if dev != nil || !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("Discover(unit 0 -> 1) = %v, %v; want the device's exception", dev, err)
	}
}

func TestDiscoverZeroUnitIDSelectsUnitOne(t *testing.T) {
	client := serve(t, testutil.NewSunSpecFixture(0, 1, commonModel()))
	dev, err := Discover(testCtx(t), client, &DiscoverOptions{BaseAddresses: []uint16{0}})
	if err != nil {
		t.Fatal(err)
	}
	if dev.UnitID != 1 {
		t.Errorf("UnitID = %d, want 1", dev.UnitID)
	}
}

func TestDiscoverMarkerOnly(t *testing.T) {
	client := serve(t, testutil.NewSunSpecFixture(50000, 1))
	ctx := testCtx(t)

	dev, err := Discover(ctx, client, at(50000))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(dev.Discovery.Models) != 0 || dev.Discovery.BaseAddress != 50000 {
		t.Errorf("discovery = %+v, want no models", dev.Discovery)
	}
	results, err := dev.ReadAll(ctx)
	if results != nil || err != nil {
		t.Errorf("ReadAll = %v, %v; want nil, nil", results, err)
	}
	if _, err := dev.ReadModelByID(ctx, 1); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("ReadModelByID = %v, want ErrUnknownModel", err)
	}
}

func TestDiscoverSchemaWithoutLabelUsesName(t *testing.T) {
	m := testutil.FixtureModel{ID: labelLessModelID, Length: 1, Registers: []uint16{5}}
	client := serve(t, testutil.NewSunSpecFixture(0, 1, m))
	dev, err := Discover(testCtx(t), client, at(0))
	if err != nil {
		t.Fatal(err)
	}
	if got := dev.Discovery.Models[0].Name; got != "label_less" {
		t.Errorf("Name = %q, want the schema name", got)
	}
	if len(dev.Discovery.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none for a known model", dev.Discovery.Warnings)
	}
}

func TestDiscoverErrors(t *testing.T) {
	marker := func(base uint16, extra map[uint16]uint16) *testutil.SunSpecHandler {
		h := &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{
			base: gmsunspec.MarkerReg0, base + 1: gmsunspec.MarkerReg1,
		}}
		for k, v := range extra {
			h.Registers[k] = v
		}
		return h
	}

	tests := []struct {
		name    string
		handler *testutil.SunSpecHandler
		opts    *DiscoverOptions
		want    error
		notWant error
	}{
		{
			name:    "readable but no marker",
			handler: &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{0: 1, 1: 2}},
			opts:    at(0),
			want:    ErrNotSunSpec,
		},
		{
			name:    "no marker at any default address",
			handler: &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{40000: 0x5375, 40001: 0, 1: 0, 2: 0}},
			opts:    nil,
			want:    ErrNotSunSpec,
		},
		{
			name:    "nothing readable",
			handler: &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{}},
			opts:    at(0),
			want:    modbus.ErrIllegalDataAddress,
			notWant: ErrNotSunSpec,
		},
		{
			name:    "chain without end marker",
			handler: marker(0, map[uint16]uint16{2: 64000, 3: 1, 4: 9}),
			opts:    at(0),
			want:    modbus.ErrIllegalDataAddress,
			notWant: ErrNotSunSpec,
		},
		{
			name:    "zero length model that is not the end marker",
			handler: marker(0, map[uint16]uint16{2: 64000, 3: 0}),
			opts:    at(0),
			want:    ErrModelChainInvalid,
		},
		{
			name:    "chain longer than MaxAddressSpan",
			handler: testutil.NewSunSpecFixture(0, 1, commonModel(), commonModel()),
			opts:    &DiscoverOptions{BaseAddresses: []uint16{0}, MaxAddressSpan: 100},
			want:    ErrModelChainLimitExceeded,
		},
		{
			name:    "empty base address list",
			handler: testutil.NewSunSpecFixture(0, 1),
			opts:    &DiscoverOptions{BaseAddresses: []uint16{}},
			want:    modbus.ErrUnexpectedParameters,
		},
		{
			name:    "invalid register type",
			handler: testutil.NewSunSpecFixture(0, 1),
			opts:    &DiscoverOptions{BaseAddresses: []uint16{0}, RegType: modbus.RegType(99)},
			want:    modbus.ErrUnexpectedParameters,
		},
		{
			name:    "input registers not served",
			handler: testutil.NewSunSpecFixture(0, 1),
			opts:    &DiscoverOptions{BaseAddresses: []uint16{0}, RegType: modbus.InputRegister},
			want:    modbus.ErrIllegalFunction,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := serve(t, tc.handler)
			dev, err := Discover(testCtx(t), client, tc.opts)
			if dev != nil {
				t.Errorf("device = %+v, want nil", dev)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if tc.notWant != nil && errors.Is(err, tc.notWant) {
				t.Errorf("err = %v, must not match %v", err, tc.notWant)
			}
		})
	}
}

func TestDiscoverMaxModels(t *testing.T) {
	small := func(id uint16) testutil.FixtureModel {
		return testutil.FixtureModel{ID: id, Length: 1, Registers: []uint16{id}}
	}
	client := serve(t, testutil.NewSunSpecFixture(0, 1, small(65001), small(65002), small(65003)))
	dev, err := Discover(testCtx(t), client, &DiscoverOptions{BaseAddresses: []uint16{0}, MaxModels: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Discovery.Models) != 2 || dev.Discovery.Models[1].Header.ID != 65002 {
		t.Errorf("models = %+v, want the first two", dev.Discovery.Models)
	}
	if len(dev.Discovery.Warnings) != 2 {
		t.Errorf("Warnings = %q, want one per unknown model", dev.Discovery.Warnings)
	}
}

func TestDetectResults(t *testing.T) {
	h := testutil.NewSunSpecFixture(40000, 1, commonModel())
	h.Registers[0] = 0x1234
	h.Registers[1] = 0x5678
	client := serve(t, h)
	ctx := testCtx(t)

	// nil options: the default addresses, in order, until the marker.
	res, err := Detect(ctx, client, nil)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !res.Detected || res.UnitID != 1 || res.BaseAddress != 40000 || res.RegType != modbus.HoldingRegister ||
		res.Marker != [2]uint16{SunSpecMarkerReg0, SunSpecMarkerReg1} {
		t.Errorf("result = %+v", res)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want 2", res.Attempts)
	}
	miss, hit := res.Attempts[0], res.Attempts[1]
	if miss.BaseAddress != 0 || miss.Matched || miss.Error != nil || miss.ErrorString != "" ||
		!reflect.DeepEqual(miss.Registers, []uint16{0x1234, 0x5678}) {
		t.Errorf("first attempt = %+v", miss)
	}
	if hit.BaseAddress != 40000 || !hit.Matched || !reflect.DeepEqual(hit.Registers, []uint16{0x5375, 0x6E53}) {
		t.Errorf("matching attempt = %+v", hit)
	}

	// A failed probe is recorded with its error; a readable non-marker makes
	// the overall verdict "not SunSpec" and keeps the attempts.
	res, err = Detect(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{300, 0}})
	if !errors.Is(err, ErrNotSunSpec) {
		t.Fatalf("err = %v, want ErrNotSunSpec", err)
	}
	if res == nil || res.Detected || len(res.Attempts) != 2 {
		t.Fatalf("result = %+v", res)
	}
	failed := res.Attempts[0]
	if !errors.Is(failed.Error, modbus.ErrIllegalDataAddress) || failed.ErrorString != failed.Error.Error() || failed.Registers != nil {
		t.Errorf("failed attempt = %+v", failed)
	}

	// Every probe failing is an I/O problem, not a verdict.
	res, err = Detect(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{300, 400}})
	if res != nil || !errors.Is(err, modbus.ErrIllegalDataAddress) || errors.Is(err, ErrNotSunSpec) {
		t.Errorf("Detect with only failing probes = %+v, %v", res, err)
	}

	// Invalid options are rejected.
	res, err = Detect(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{}})
	if res != nil || !errors.Is(err, modbus.ErrUnexpectedParameters) {
		t.Errorf("Detect with no base addresses = %+v, %v", res, err)
	}

	// A context that is already done stops before the first probe.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	res, err = Detect(cancelled, client, nil)
	if res != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Detect with cancelled context = %+v, %v", res, err)
	}
	dev, err := Discover(cancelled, client, nil)
	if dev != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Discover with cancelled context = %+v, %v", dev, err)
	}
}

func TestReExportedConstants(t *testing.T) {
	if SunSpecMarkerReg0 != 0x5375 || SunSpecMarkerReg1 != 0x6E53 {
		t.Errorf("marker = %#x %#x, want \"SunS\"", SunSpecMarkerReg0, SunSpecMarkerReg1)
	}
	if SunSpecEndModelID != 0xFFFF || SunSpecEndModelLength != 0 {
		t.Errorf("end model = %#x/%d", SunSpecEndModelID, SunSpecEndModelLength)
	}
	if len(SunSpecDefaultBaseAddresses) == 0 || SunSpecDefaultBaseAddresses[1] != 40000 {
		t.Errorf("default base addresses = %v", SunSpecDefaultBaseAddresses)
	}
	if !errors.Is(ErrModelChainInvalid, modbus.ErrSunSpecModelChainInvalid) ||
		!errors.Is(ErrModelChainLimitExceeded, modbus.ErrSunSpecModelChainLimitExceeded) {
		t.Error("chain errors must be the go-modbus sentinels")
	}
}
