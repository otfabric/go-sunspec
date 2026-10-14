// SPDX-License-Identifier: MIT

package devicefile

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
	"github.com/otfabric/go-sunspec/testutil"
)

// testCtx returns a context that ends with the test, or after 30 seconds.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// connect serves handler over Modbus TCP on a loopback port until the test
// ends and returns a client connected to it.
func connect(t *testing.T, handler modbus.RequestHandler) *modbus.Client {
	t.Helper()
	var (
		ms   *modbus.Server
		addr string
		err  error
	)
	// The port is picked and released before the server binds it: retry
	// when something else took it in between.
	for attempt := 0; attempt < 10; attempt++ {
		var l net.Listener
		if l, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			t.Fatalf("find free port: %v", err)
		}
		addr = l.Addr().String()
		_ = l.Close()
		ms, err = modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()}, handler)
		if err != nil {
			t.Fatalf("modbus.NewServer: %v", err)
		}
		if err = ms.Start(); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() { _ = ms.Stop() })

	client, err := modbus.New(modbus.Config{
		URL: "tcp://" + addr, Timeout: 5 * time.Second, DialTimeout: 2 * time.Second, Logger: modbus.NopLogger(),
		// Reading a device to describe it takes no other function codes.
		AllowedFunctionCodes: sunspec.ReadFunctionCodes(),
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

// discover serves handler and discovers the device with the given unit ID
// behind it.
func discover(t *testing.T, handler modbus.RequestHandler, unitID uint8) *sunspec.Device {
	t.Helper()
	device, err := sunspec.Discover(testCtx(t), connect(t, handler), &sunspec.DiscoverOptions{UnitID: unitID})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return device
}

// readAll reads every model of a device. A device with a model that does not
// fit its schema reads with a decode error; the models are all there.
func readAll(t *testing.T, device *sunspec.Device) []*sunspec.DecodedModel {
	t.Helper()
	models, err := device.ReadAll(testCtx(t))
	if err != nil && !errors.Is(err, sunspec.ErrDecode) {
		t.Fatalf("read: %v", err)
	}
	return models
}

// asJSON returns v as indented JSON.
func asJSON(t *testing.T, v interface{}) string {
	t.Helper()
	text, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// decodedOnly returns the JSON of what was decoded from each model: its
// identity, groups and warnings, without the registers behind them.
func decodedOnly(t *testing.T, models []*sunspec.DecodedModel) string {
	t.Helper()
	type view struct {
		ModelID  uint16
		Name     string
		Address  uint16
		Group    *sunspec.DecodedGroup
		Warnings []string
		Raw      []uint16
	}
	var out []view
	for _, dm := range models {
		v := view{ModelID: dm.ModelID, Name: dm.Name, Address: dm.InstanceAddress, Group: dm.Group, Warnings: dm.Warnings}
		if dm.Group == nil {
			// Nothing was decoded: the registers are the values.
			v.Raw = dm.RawRegisters
		}
		out = append(out, v)
	}
	return asJSON(t, out)
}

// point returns the named point of a decoded group, failing the test when it
// is not there.
func point(t *testing.T, g *sunspec.DecodedGroup, name string) sunspec.DecodedPoint {
	t.Helper()
	if g != nil {
		for _, p := range g.Points {
			if p.Name == name {
				return p
			}
		}
	}
	t.Fatalf("point %s not found", name)
	return sunspec.DecodedPoint{}
}

// modelBuilder assembles the registers of a model from its schema, for
// fixture devices that do not go through the encoder.
type modelBuilder struct {
	t    *testing.T
	meta *registry.ModelMeta
	regs []uint16 // indexed by schema offset, header included
}

// unimplemented returns the SunSpec "not implemented" registers for a type.
func unimplemented(typ string, size int) []uint16 {
	out := make([]uint16, size)
	switch typ {
	case "int16", "sunssf", "pad", "int32", "int64":
		out[0] = 0x8000
	case "float32":
		out[0] = 0x7FC0
	case "float64":
		out[0] = 0x7FF8
	case "acc16", "acc32", "acc64", "string", "ipaddr", "ipv6addr":
		// all zero
	default:
		for i := range out {
			out[i] = 0xFFFF
		}
	}
	return out
}

// newModel starts a model with its top-level points and, for a model with
// one repeating group directly below the top level (MPPT modules, say), that
// many instances of it. Deeper structures are appended with add.
func newModel(t *testing.T, id uint16, instances int) *modelBuilder {
	t.Helper()
	meta := registry.ByID(id)
	if meta == nil || meta.Group == nil {
		t.Fatalf("no schema for model %d", id)
	}
	b := &modelBuilder{t: t, meta: meta}
	b.fill(meta.Group)
	for i := 0; i < instances; i++ {
		b.fill(meta.Group.Groups[0])
	}
	b.regs[0] = id
	return b
}

// fill appends one instance of g's own points, all not implemented, and
// returns the index of its first register.
func (b *modelBuilder) fill(g *registry.GroupMeta) int {
	base := len(b.regs)
	b.regs = append(b.regs, make([]uint16, g.Length)...)
	for _, p := range g.Points {
		copy(b.regs[base+p.Offset:], unimplemented(p.Type, p.Size))
	}
	return base
}

// add appends one instance of the nested group at path ("Crv" or "Crv/Pt")
// with the given point values. Instances must be added in register order.
func (b *modelBuilder) add(path string, vals map[string][]uint16) *modelBuilder {
	b.t.Helper()
	g := b.meta.Group
	for _, name := range strings.Split(path, "/") {
		if g = g.Group(name); g == nil {
			b.t.Fatalf("model %d has no group %s", b.meta.ID, path)
		}
	}
	base := b.fill(g)
	for name, v := range vals {
		b.put(base, g, name, v)
	}
	return b
}

func (b *modelBuilder) put(base int, g *registry.GroupMeta, name string, vals []uint16) *modelBuilder {
	b.t.Helper()
	for _, p := range g.Points {
		if p.Name == name {
			if len(vals) > p.Size {
				b.t.Fatalf("model %d point %s: %d registers do not fit in %d", b.meta.ID, name, len(vals), p.Size)
			}
			for i := 0; i < p.Size; i++ {
				b.regs[base+p.Offset+i] = 0
			}
			copy(b.regs[base+p.Offset:], vals)
			return b
		}
	}
	b.t.Fatalf("model %d has no point %s", b.meta.ID, name)
	return b
}

// set writes a top-level point.
func (b *modelBuilder) set(name string, vals ...uint16) *modelBuilder {
	b.t.Helper()
	return b.put(0, b.meta.Group, name, vals)
}

// str writes a top-level string point.
func (b *modelBuilder) str(name, s string) *modelBuilder {
	b.t.Helper()
	return b.set(name, testutil.StringToRegisters(s, (len(s)+1)/2)...)
}

// rep writes a point of instance i (zero-based) of the model's first nested
// group, as created by newModel.
func (b *modelBuilder) rep(i int, name string, vals ...uint16) *modelBuilder {
	b.t.Helper()
	g := b.meta.Group.Groups[0]
	return b.put(b.meta.FixedLength()+i*g.Length, g, name, vals)
}

func (b *modelBuilder) fixture() testutil.FixtureModel {
	b.regs[1] = uint16(len(b.regs) - 2)
	return testutil.FixtureModel{ID: b.meta.ID, Length: uint16(len(b.regs) - 2), Registers: b.regs[2:]}
}

const neg1, neg2 = 0xFFFF, 0xFFFE // sunssf -1 and -2

// r is shorthand for the registers of one point.
func r(regs ...uint16) []uint16 { return regs }

// inverter is a single phase inverter with two MPPT inputs and one vendor
// model without a schema, at base address 40000, unit 1.
func inverter(t *testing.T) *testutil.SunSpecHandler {
	t.Helper()
	common := newModel(t, 1, 0).
		str("Mn", "Acme").str("Md", "Inv-1").str("Vr", "1.2.3").str("SN", "SN001").set("DA", 1)
	inv := newModel(t, 101, 0).
		set("A", 1250).set("AphA", 1250).set("A_SF", neg2).
		set("PhVphA", 2305).set("V_SF", neg1).
		set("W", 0xFF38).set("W_SF", 1). // -200 * 10
		set("Hz", 5001).set("Hz_SF", neg2).
		set("WH", 0x0001, 0xE240).set("WH_SF", 0). // 123456
		set("TmpCab", 355).set("Tmp_SF", neg1).
		set("St", 4).
		set("Evt1", 0x0000, 0x0003).
		set("Evt2", 0, 0)
	mppt := newModel(t, 160, 2).
		set("DCA_SF", neg1).set("DCV_SF", 0).set("DCW_SF", 0).set("N", 2).
		rep(0, "ID", 1).rep(0, "IDStr", testutil.StringToRegisters("East", 2)...).rep(0, "DCA", 85).rep(0, "DCV", 400).rep(0, "DCW", 3400).rep(0, "DCSt", 4).
		rep(1, "ID", 2).rep(1, "IDStr", testutil.StringToRegisters("West", 2)...).rep(1, "DCA", 42).rep(1, "DCV", 395)
	vendor := testutil.FixtureModel{ID: 64950, Length: 3, Registers: []uint16{0x0001, 0x00FF, 0xABCD}}
	return testutil.NewSunSpecFixture(40000, 1, common.fixture(), inv.fixture(), mppt.fixture(), vendor)
}

// derDevice exposes two DER models with nested groups: Volt-Var (705: two
// curves of two points each) and AC Controls (704: four sibling groups).
func derDevice(t *testing.T) *testutil.SunSpecHandler {
	t.Helper()
	voltVar := newModel(t, 705, 0).
		set("Ena", 1).set("NPt", 2).set("NCrv", 2).set("V_SF", neg1).set("DeptRef_SF", neg2).set("RspTms_SF", 0).
		add("Crv", map[string][]uint16{"ActPt": r(2), "DeptRef": r(1), "VRef": r(1000), "RspTms": r(0, 5), "ReadOnly": r(1)}).
		add("Crv/Pt", map[string][]uint16{"V": r(920), "Var": r(3000)}).
		add("Crv/Pt", map[string][]uint16{"V": r(1080), "Var": r(0xF448)}). // -3000
		add("Crv", map[string][]uint16{"ActPt": r(1), "ReadOnly": r(0)}).
		add("Crv/Pt", map[string][]uint16{"V": r(1000), "Var": r(0)}).
		add("Crv/Pt", nil) // not in use
	controls := newModel(t, 704, 0).
		set("WMaxLimPctEna", 1).set("WMaxLimPct", 8050).set("WMaxLimPct_SF", neg2).set("PF_SF", neg2).
		add("PFWInj", map[string][]uint16{"PF": r(100), "Ext": r(1)}).
		add("PFWInjRvrt", map[string][]uint16{"PF": r(96), "Ext": r(0)}).
		add("PFWAbs", map[string][]uint16{"PF": r(97)}).
		add("PFWAbsRvrt", nil)
	return testutil.NewSunSpecFixture(40000, 1, newModel(t, 1, 0).str("Mn", "Acme").fixture(), voltVar.fixture(), controls.fixture())
}

// oddDevice has what a capture must fall back to raw registers for: a Common
// model that is one register shorter than its schema, an inverter with
// registers the schema does not know, a vendor model, and a model whose
// strings are padded with spaces instead of NULs.
func oddDevice(t *testing.T) *testutil.SunSpecHandler {
	t.Helper()
	short := newModel(t, 1, 0).str("Mn", "Acme").str("Md", "Old").fixture()
	short.Length, short.Registers = 65, short.Registers[:65]
	long := newModel(t, 101, 0).set("A", 1234).set("A_SF", neg2).set("W", 5000).set("W_SF", 0).fixture()
	long.Length, long.Registers = 53, append(long.Registers, 7, 8, 9)
	padded := newModel(t, 1, 0).fixture()
	copy(padded.Registers, testutil.StringToRegisters("Spacey          ", 16))
	return testutil.NewSunSpecFixture(0, 1, short, long,
		testutil.FixtureModel{ID: 64900, Length: 4, Registers: []uint16{0xDEAD, 0xBEEF, 0, 1}},
		padded,
		newModel(t, 160, 2).set("DCA_SF", neg1).set("N", 2).rep(0, "ID", 1).rep(0, "DCA", 85).rep(1, "ID", 2).fixture(),
	)
}

// brokenInverter exposes a Common model followed by a vendor model that
// announces 200 registers of which only 130 can be read.
func brokenInverter(t *testing.T) *testutil.SunSpecHandler {
	t.Helper()
	common := newModel(t, 1, 0).str("Mn", "Acme")
	regs := make([]uint16, 130)
	return testutil.NewSunSpecFixture(40000, 1, common.fixture(), testutil.FixtureModel{ID: 64950, Length: 200, Registers: regs})
}
