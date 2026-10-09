// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"

	"github.com/otfabric/go-sunspec/registry"
	"github.com/otfabric/go-sunspec/testutil"
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

// startDevice serves handler on a local port for the duration of the test and
// returns the URL to pass to --url.
//
// It does not use testutil.StartServerClient: that server accepts only two
// clients, and a connection the previous command just closed can still occupy
// a slot when the next command connects.
func startDevice(t *testing.T, handler *testutil.SunSpecHandler) string {
	t.Helper()
	url := "tcp://127.0.0.1:" + strconv.Itoa(freePort(t))
	server, err := modbus.NewServer(&modbus.ServerConfig{URL: url, MaxClients: 64}, handler)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return url
}

// result is what one CLI invocation produced.
type result struct {
	stdout string
	stderr string
	err    error
}

// runCLI executes the root command with args, as main would, and captures its
// output streams.
func runCLI(t *testing.T, args ...string) result {
	t.Helper()
	return runCLIContext(t, context.Background(), args...)
}

func runCLIContext(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// modelBuilder assembles the registers of one model instance from its schema.
// Every point starts out as "not implemented".
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

// inverter returns the fixture every command test runs against: a single
// phase inverter with two MPPT inputs and one vendor model without a schema,
// at base address 40000, unit 1.
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

// lines joins output lines, each terminated by a newline. It keeps trailing
// spaces (which tabwriter emits for empty last cells) visible in the source.
func lines(l ...string) string {
	return strings.Join(l, "\n") + "\n"
}

var (
	commonTable = lines(
		"=== Model 1: Common ===",
		"  [Fixed]",
		"    ID   uint16  1",
		"    L    uint16  66",
		"    Mn   string  Acme",
		"    Md   string  Inv-1",
		// Opt is all NUL on the device: not implemented, so not listed.
		"    Vr   string  1.2.3",
		"    SN   string  SN001",
		"    DA   uint16  1",
		"    Pad  pad     32768",
	)
	commonRaw = lines(
		"  Raw registers (68):",
		"    0000: 0001 0042 4163 6D65 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000",
		"    0016: 0000 0000 496E 762D 3100 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000",
		"    0032: 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000 312E 322E 3300 0000 0000 0000",
		"    0048: 0000 0000 534E 3030 3100 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000 0000",
		"    0064: 0000 0000 0001 8000",
	)
	inverterTable = lines(
		"=== Model 101: Inverter (Single Phase) ===",
		"  [Fixed]",
		"    ID      uint16      101",
		"    L       uint16      50",
		"    A       uint16      12.5 A",
		"    AphA    uint16      12.5 A",
		"    A_SF    sunssf      -2",
		"    PhVphA  uint16      230.5 V",
		"    V_SF    sunssf      -1",
		"    W       int16       -2000 W",
		"    W_SF    sunssf      1",
		"    Hz      uint16      50.01 Hz",
		"    Hz_SF   sunssf      -2",
		"    WH      acc32       123456 Wh",
		"    WH_SF   sunssf      0",
		"    TmpCab  int16       35.5 C",
		"    Tmp_SF  sunssf      -1",
		"    St      enum16      4 [MPPT]",
		"    Evt1    bitfield32  3 [GROUND_FAULT, DC_OVER_VOLT]",
		"    Evt2    bitfield32  0",
	)
	inverterRaw = lines(
		"  Raw registers (52):",
		"    0000: 0065 0032 04E2 04E2 FFFF FFFF FFFE FFFF FFFF FFFF 0901 FFFF FFFF FFFF FF38 0001",
		"    0016: 1389 FFFE 8000 8000 8000 8000 8000 8000 0001 E240 0000 FFFF 8000 FFFF 8000 8000",
		"    0032: 8000 0163 8000 8000 8000 FFFF 0004 FFFF 0000 0003 0000 0000 FFFF FFFF FFFF FFFF",
		"    0048: FFFF FFFF FFFF FFFF",
	)
	mpptTable = lines(
		"=== Model 160: Multiple MPPT Inverter Extension Model ===",
		"  [Fixed]",
		"    ID      uint16  160",
		"    L       uint16  48",
		"    DCA_SF  sunssf  -1",
		"    DCV_SF  sunssf  0",
		"    DCW_SF  sunssf  0",
		"    N       count   2",
		"    [module 1]",
		"      ID     uint16  1",
		"      IDStr  string  East",
		"      DCA    uint16  8.5 A",
		"      DCV    uint16  400 V",
		"      DCW    uint16  3400 W",
		"      DCSt   enum16  4 [MPPT]",
		"    [module 2]",
		"      ID     uint16  2",
		"      IDStr  string  West",
		"      DCA    uint16  4.2 A",
		"      DCV    uint16  395 V",
	)
	mpptRaw = lines(
		"  Raw registers (50):",
		"    0000: 00A0 0030 FFFF 0000 0000 8000 FFFF FFFF 0002 FFFF 0001 4561 7374 0000 0000 0000",
		"    0016: 0000 0000 0000 0055 0190 0D48 0000 0000 FFFF FFFF 8000 0004 FFFF FFFF 0002 5765",
		"    0032: 7374 0000 0000 0000 0000 0000 0000 002A 018B FFFF 0000 0000 FFFF FFFF 8000 FFFF",
		"    0048: FFFF FFFF",
	)
	vendorHeader  = "=== Model 64950: unknown_64950 ===\n"
	vendorRaw     = "  Raw registers (3):\n    0000: 0001 00FF ABCD\n"
	vendorWarning = "  WARNING: no schema available, returning raw registers only\n"

	// readTable is the output of "read"; every model ends with a blank line.
	readTable = commonTable + "\n" + inverterTable + "\n" + mpptTable + "\n" + vendorHeader + vendorWarning + "\n"
	// readRawTable is the output of "read --raw".
	readRawTable = commonTable + commonRaw + "\n" + inverterTable + inverterRaw + "\n" + mpptTable + mpptRaw + "\n" +
		vendorHeader + vendorRaw + vendorWarning + "\n"
)

// wantOK asserts a successful invocation with exactly the given stdout and
// nothing on stderr.
func wantOK(t *testing.T, r result, stdout string) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", r.err, r.stderr)
	}
	if r.stdout != stdout {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, stdout)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
}

// wantNoCommandOutput asserts that a failed invocation printed no command
// output. cobra follows the error with the command's usage text; that goes to
// stderr in the real binary (see TestMainExitCodes) but to the writer set with
// SetOut in these in-process runs, so a usage text on stdout is accepted.
func wantNoCommandOutput(t *testing.T, r result) {
	t.Helper()
	if r.stdout != "" && !strings.HasPrefix(r.stdout, "Usage:\n  sunspecctl ") {
		t.Errorf("stdout of a failed command:\n%s", r.stdout)
	}
}

// wantFail asserts a failed invocation whose error message is exactly msg and
// which cobra reported on stderr as "Error: <msg>".
func wantFail(t *testing.T, r result, msg string) {
	t.Helper()
	if r.err == nil {
		t.Fatalf("no error; stdout:\n%s", r.stdout)
	}
	if r.err.Error() != msg {
		t.Errorf("error = %q, want %q", r.err.Error(), msg)
	}
	if !strings.HasPrefix(r.stderr, "Error: "+msg+"\n") {
		t.Errorf("stderr does not start with the error message:\n%s", r.stderr)
	}
	wantNoCommandOutput(t, r)
}

// decodeJSON decodes stdout, which must hold exactly one JSON document.
func decodeJSON(t *testing.T, r result, v interface{}) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", r.err, r.stderr)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	if dec.More() {
		t.Errorf("stdout holds more than one JSON document:\n%s", r.stdout)
	}
	// printJSON indents with two spaces and ends with a newline.
	if !strings.HasSuffix(r.stdout, "\n") || !strings.Contains(r.stdout, "\n  ") {
		t.Errorf("JSON output is not indented:\n%s", r.stdout)
	}
}

// point returns the named own point of a decoded group.
func point(t *testing.T, b *sunspec.DecodedGroup, name string) sunspec.DecodedPoint {
	t.Helper()
	if b == nil {
		t.Fatalf("no group while looking for point %s", name)
	}
	for _, p := range b.Points {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("point %s not found", name)
	return sunspec.DecodedPoint{}
}

func TestDetect(t *testing.T) {
	url := startDevice(t, inverter(t))

	wantOK(t, runCLI(t, "detect", "--url", url), lines(
		"Detected:     true",
		"Unit ID:      1",
		"Base Address: 40000",
		"Reg Type:     0",
		"Marker:       0x5375 0x6E53",
		"Attempts:     2",
	))

	var det sunspec.DetectionResult
	r := runCLI(t, "detect", "--json", "--url", url)
	decodeJSON(t, r, &det)
	if !det.Detected || det.UnitID != 1 || det.BaseAddress != 40000 || det.RegType != modbus.HoldingRegister ||
		det.Marker != [2]uint16{0x5375, 0x6E53} || len(det.Attempts) != 2 {
		t.Errorf("detect --json = %+v", det)
	}
	if a := det.Attempts[0]; a.BaseAddress != 0 || a.Matched || a.ErrorString == "" {
		t.Errorf("first attempt = %+v, want a failed probe of address 0 with its error text", a)
	}
	if a := det.Attempts[1]; a.BaseAddress != 40000 || !a.Matched || a.ErrorString != "" {
		t.Errorf("second attempt = %+v", a)
	}
	if !strings.Contains(r.stdout, `"error": "`) {
		t.Errorf("the probe error is not in the JSON output:\n%s", r.stdout)
	}
}

func TestModels(t *testing.T) {
	url := startDevice(t, inverter(t))

	wantOK(t, runCLI(t, "models", "--url", url), lines(
		"ID     NAME                                    START  LENGTH  SCHEMA",
		"1      Common                                  40002  66      yes",
		"101    Inverter (Single Phase)                 40070  50      yes",
		"160    Multiple MPPT Inverter Extension Model  40122  48      yes",
		"64950  unknown_64950                           40172  3       no",
	))

	var disc sunspec.DiscoveryResult
	decodeJSON(t, runCLI(t, "--json", "models", "--url", url), &disc)
	if disc.BaseAddress != 40000 || len(disc.Models) != 4 {
		t.Fatalf("models --json: base %d, %d models", disc.BaseAddress, len(disc.Models))
	}
	wantHeaders := []sunspec.ModelHeader{
		{ID: 1, Length: 66, StartAddress: 40002, EndAddress: 40069, NextAddress: 40070},
		{ID: 101, Length: 50, StartAddress: 40070, EndAddress: 40121, NextAddress: 40122},
		{ID: 160, Length: 48, StartAddress: 40122, EndAddress: 40171, NextAddress: 40172},
		{ID: 64950, Length: 3, StartAddress: 40172, EndAddress: 40176, NextAddress: 40177},
	}
	for i, m := range disc.Models {
		if m.Header != wantHeaders[i] {
			t.Errorf("model %d header = %+v, want %+v", i, m.Header, wantHeaders[i])
		}
		if known := m.Header.ID != 64950; m.SchemaKnown != known || (m.Schema != nil) != known {
			t.Errorf("model %d: SchemaKnown=%v Schema set=%v", m.Header.ID, m.SchemaKnown, m.Schema != nil)
		}
	}
	if len(disc.Warnings) != 1 || !strings.Contains(disc.Warnings[0], "64950") {
		t.Errorf("Warnings = %q", disc.Warnings)
	}
}

func TestRead(t *testing.T) {
	url := startDevice(t, inverter(t))

	wantOK(t, runCLI(t, "read", "--url", url), readTable)
	wantOK(t, runCLI(t, "read", "--raw", "--url", url), readRawTable)

	var models []*sunspec.DecodedModel
	decodeJSON(t, runCLI(t, "read", "--json", "--url", url), &models)
	if len(models) != 4 {
		t.Fatalf("read --json returned %d models, want 4", len(models))
	}
	for i, id := range []uint16{1, 101, 160, 64950} {
		if models[i].ModelID != id {
			t.Errorf("model %d has ID %d, want %d", i, models[i].ModelID, id)
		}
	}
	if mn := point(t, models[0].Group, "Mn"); mn.RawValue != "Acme" {
		t.Errorf("Mn = %v", mn.RawValue)
	}
	w := point(t, models[1].Group, "W")
	if w.ScaledValue == nil || *w.ScaledValue != -2000 || w.SFRawValue == nil || *w.SFRawValue != 1 || w.Units != "W" {
		t.Errorf("W = %+v", w)
	}
	if aphB := point(t, models[1].Group, "AphB"); aphB.Implemented || aphB.ScaledValue != nil {
		t.Errorf("AphB = %+v, want not implemented (JSON lists every point)", aphB)
	}
	modules := models[2].Group.GroupsNamed("module")
	if len(modules) != 2 || modules[0].Index != 1 || modules[1].Index != 2 {
		t.Fatalf("MPPT model has %d module instances, want 2", len(modules))
	}
	if dca := point(t, modules[1], "DCA"); dca.ScaledValue == nil || *dca.ScaledValue != 4.2 {
		t.Errorf("second MPPT DCA = %+v", dca)
	}
	vendor := models[3]
	if vendor.Group != nil || len(vendor.RawRegisters) != 3 || vendor.RawRegisters[2] != 0xABCD || len(vendor.Warnings) != 1 {
		t.Errorf("vendor model = %+v", vendor)
	}
	// --raw makes no difference to JSON, which always carries the registers.
	if a, b := runCLI(t, "read", "--json", "--url", url), runCLI(t, "read", "--json", "--raw", "--url", url); a.stdout != b.stdout {
		t.Error("read --json and read --json --raw differ")
	}
}

func TestReadModel(t *testing.T) {
	url := startDevice(t, inverter(t))

	wantOK(t, runCLI(t, "read-model", "--id", "101", "--url", url), inverterTable+"\n")
	wantOK(t, runCLI(t, "read-model", "--id=160", "--raw", "--url", url), mpptTable+mpptRaw+"\n")
	wantOK(t, runCLI(t, "read-model", "--id", "64950", "--url", url), vendorHeader+vendorWarning+"\n")
	wantOK(t, runCLI(t, "read-model", "--id", "64950", "--raw", "--url", url), vendorHeader+vendorRaw+vendorWarning+"\n")

	var dm sunspec.DecodedModel
	decodeJSON(t, runCLI(t, "read-model", "--id", "101", "--json", "--url", url), &dm)
	if dm.ModelID != 101 || dm.Name != "Inverter (Single Phase)" || dm.InstanceAddress != 40070 || len(dm.RawRegisters) != 52 {
		t.Errorf("read-model --json = ID %d, %q at %d with %d registers", dm.ModelID, dm.Name, dm.InstanceAddress, len(dm.RawRegisters))
	}
	if st := point(t, dm.Group, "St"); len(st.Symbols) != 1 || st.Symbols[0] != "MPPT" {
		t.Errorf("St = %+v", st)
	}
	if dm.Schema == nil || dm.Schema.ID != 101 {
		t.Errorf("Schema = %+v", dm.Schema)
	}
}

func TestReadPoint(t *testing.T) {
	url := startDevice(t, inverter(t))

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			"scaled with units", []string{"--model", "101", "--point", "W"},
			lines("Point:   W", "Type:    int16", "Raw:     -200", "Scaled:  -2000", "Units:   W"),
		},
		{
			"enum with symbol and --raw", []string{"--model", "101", "--point", "St", "--raw"},
			lines("Point:   St", "Type:    enum16", "Raw:     4", "Symbols: MPPT", "Offset:  38", "Count:   1"),
		},
		{
			"bitfield with several symbols", []string{"--model", "101", "--point", "Evt1"},
			lines("Point:   Evt1", "Type:    bitfield32", "Raw:     3", "Symbols: GROUND_FAULT, DC_OVER_VOLT"),
		},
		{
			"string", []string{"--model", "1", "--point", "Mn"},
			lines("Point:   Mn", "Type:    string", "Raw:     Acme"),
		},
		{
			"two-register point with --raw", []string{"--model", "101", "--point", "WH", "--raw"},
			lines("Point:   WH", "Type:    acc32", "Raw:     123456", "Scaled:  123456", "Units:   Wh", "Offset:  24", "Count:   2"),
		},
		{
			"first repeating instance wins", []string{"--model", "160", "--point", "DCA"},
			lines("Point:   DCA", "Type:    uint16", "Raw:     85", "Scaled:  8.5", "Units:   A"),
		},
		{
			"fixed block is searched before repeating blocks", []string{"--model", "160", "--point", "ID"},
			lines("Point:   ID", "Type:    uint16", "Raw:     160"),
		},
		{
			"not implemented point is still printed", []string{"--model", "101", "--point", "AphB"},
			lines("Point:   AphB", "Type:    uint16", "Raw:     65535", "Units:   A"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantOK(t, runCLI(t, append([]string{"read-point", "--url", url}, tc.args...)...), tc.want)
		})
	}

	var dp sunspec.DecodedPoint
	decodeJSON(t, runCLI(t, "read-point", "--model", "101", "--point", "A", "--json", "--url", url), &dp)
	if dp.Name != "A" || dp.Type != "uint16" || dp.RawValue != float64(1250) || dp.ScaledValue == nil || *dp.ScaledValue != 12.5 ||
		dp.Units != "A" || dp.SFName != "A_SF" || dp.SFRawValue == nil || *dp.SFRawValue != -2 ||
		dp.RegisterOffset != 2 || dp.RegisterCount != 1 || !dp.Implemented {
		t.Errorf("read-point --json = %+v", dp)
	}
}

// pollHeader matches the line printed before each poll in table mode.
var pollHeader = regexp.MustCompile(`(?m)^--- poll (\d+) @ (\S+) ---\n`)

// splitPolls checks the poll headers (numbered from 1, RFC 3339 timestamps
// that do not lie in the future or the distant past) and returns the output
// that follows each of them.
func splitPolls(t *testing.T, stdout string) []string {
	t.Helper()
	heads := pollHeader.FindAllStringSubmatchIndex(stdout, -1)
	if len(heads) == 0 || heads[0][0] != 0 {
		t.Fatalf("output does not start with a poll header:\n%s", stdout)
	}
	var bodies []string
	for i, h := range heads {
		if n := stdout[h[2]:h[3]]; n != strconv.Itoa(i+1) {
			t.Errorf("poll header %d is numbered %s", i+1, n)
		}
		ts, err := time.Parse(time.RFC3339, stdout[h[4]:h[5]])
		if err != nil {
			t.Errorf("poll header %d: bad timestamp: %v", i+1, err)
		} else if age := time.Since(ts); age < -2*time.Second || age > time.Minute {
			t.Errorf("poll header %d: timestamp %s is %s old", i+1, ts, age)
		}
		end := len(stdout)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		bodies = append(bodies, stdout[h[1]:end])
	}
	return bodies
}

func TestPoll(t *testing.T) {
	url := startDevice(t, inverter(t))

	r := runCLI(t, "poll", "--count", "3", "--interval", "1ms", "--url", url)
	if r.err != nil || r.stderr != "" {
		t.Fatalf("poll: err %v, stderr %q", r.err, r.stderr)
	}
	bodies := splitPolls(t, r.stdout)
	if len(bodies) != 3 {
		t.Fatalf("got %d polls, want 3", len(bodies))
	}
	for i, body := range bodies {
		if body != readTable {
			t.Errorf("poll %d:\n%s\nwant:\n%s", i+1, body, readTable)
		}
	}

	r = runCLI(t, "poll", "--count", "1", "--raw", "--url", url)
	if bodies := splitPolls(t, r.stdout); r.err != nil || len(bodies) != 1 || bodies[0] != readRawTable {
		t.Errorf("poll --raw: err %v, output:\n%s", r.err, r.stdout)
	}

	// JSON mode prints one document per poll and no headers.
	r = runCLI(t, "poll", "--count", "2", "--interval", "1ms", "--json", "--url", url)
	if r.err != nil || r.stderr != "" {
		t.Fatalf("poll --json: err %v, stderr %q", r.err, r.stderr)
	}
	if strings.Contains(r.stdout, "--- poll") {
		t.Error("poll --json prints poll headers")
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	docs := 0
	for dec.More() {
		var models []*sunspec.DecodedModel
		if err := dec.Decode(&models); err != nil {
			t.Fatalf("document %d: %v", docs+1, err)
		}
		if len(models) != 4 || models[1].ModelID != 101 {
			t.Errorf("document %d has %d models", docs+1, len(models))
		}
		docs++
	}
	if docs != 2 {
		t.Errorf("poll --json --count 2 printed %d documents", docs)
	}
}

func TestPollModel(t *testing.T) {
	url := startDevice(t, inverter(t))

	r := runCLI(t, "poll-model", "--id", "160", "--count", "2", "--interval", "1ms", "--url", url)
	if r.err != nil || r.stderr != "" {
		t.Fatalf("poll-model: err %v, stderr %q", r.err, r.stderr)
	}
	bodies := splitPolls(t, r.stdout)
	if len(bodies) != 2 || bodies[0] != mpptTable+"\n" || bodies[1] != mpptTable+"\n" {
		t.Errorf("poll-model output:\n%s", r.stdout)
	}

	r = runCLI(t, "poll-model", "--id", "64950", "--count", "1", "--raw", "--url", url)
	if bodies := splitPolls(t, r.stdout); r.err != nil || len(bodies) != 1 || bodies[0] != vendorHeader+vendorRaw+vendorWarning+"\n" {
		t.Errorf("poll-model --raw: err %v, output:\n%s", r.err, r.stdout)
	}

	r = runCLI(t, "poll-model", "--id", "101", "--count", "2", "--interval", "1ms", "--json", "--url", url)
	if r.err != nil {
		t.Fatal(r.err)
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	docs := 0
	for dec.More() {
		var dm sunspec.DecodedModel
		if err := dec.Decode(&dm); err != nil {
			t.Fatalf("document %d: %v", docs+1, err)
		}
		if dm.ModelID != 101 || dm.Group == nil {
			t.Errorf("document %d = model %d", docs+1, dm.ModelID)
		}
		docs++
	}
	if docs != 2 || strings.Contains(r.stdout, "--- poll") {
		t.Errorf("poll-model --json printed %d documents:\n%s", docs, r.stdout)
	}
}

func TestPollPoint(t *testing.T) {
	url := startDevice(t, inverter(t))
	pointA := lines("Point:   A", "Type:    uint16", "Raw:     1250", "Scaled:  12.5", "Units:   A")

	r := runCLI(t, "poll-point", "--model", "101", "--point", "A", "--count", "2", "--interval", "1ms", "--url", url)
	if r.err != nil || r.stderr != "" {
		t.Fatalf("poll-point: err %v, stderr %q", r.err, r.stderr)
	}
	if bodies := splitPolls(t, r.stdout); len(bodies) != 2 || bodies[0] != pointA || bodies[1] != pointA {
		t.Errorf("poll-point output:\n%s", r.stdout)
	}

	r = runCLI(t, "poll-point", "--model", "101", "--point", "St", "--count", "1", "--raw", "--url", url)
	want := lines("Point:   St", "Type:    enum16", "Raw:     4", "Symbols: MPPT", "Offset:  38", "Count:   1")
	if bodies := splitPolls(t, r.stdout); r.err != nil || len(bodies) != 1 || bodies[0] != want {
		t.Errorf("poll-point --raw: err %v, output:\n%s", r.err, r.stdout)
	}

	r = runCLI(t, "poll-point", "--model", "160", "--point", "DCV", "--count", "3", "--interval", "1ms", "--json", "--url", url)
	if r.err != nil {
		t.Fatal(r.err)
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	docs := 0
	for dec.More() {
		var dp sunspec.DecodedPoint
		if err := dec.Decode(&dp); err != nil {
			t.Fatalf("document %d: %v", docs+1, err)
		}
		if dp.Name != "DCV" || dp.ScaledValue == nil || *dp.ScaledValue != 400 {
			t.Errorf("document %d = %+v", docs+1, dp)
		}
		docs++
	}
	if docs != 3 || strings.Contains(r.stdout, "--- poll") {
		t.Errorf("poll-point --json printed %d documents:\n%s", docs, r.stdout)
	}
}

// The default --count is 0: poll until cancelled.
func TestPollRunsUntilCancelled(t *testing.T) {
	url := startDevice(t, inverter(t))

	for _, args := range [][]string{
		{"poll"},
		{"poll-model", "--id", "1"},
		{"poll-point", "--model", "101", "--point", "W"},
	} {
		t.Run(args[0], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			root := newRootCmd()
			// Cancel from inside the command, once the third poll starts.
			out := &cancelOnWriter{trigger: "--- poll 3 @", cancel: cancel}
			var stderr bytes.Buffer
			root.SetOut(out)
			root.SetErr(&stderr)
			root.SetArgs(append(args, "--interval", "1ms", "--url", url))

			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("cancelled poll returned an error: %v\nstderr: %s", err, stderr.String())
				}
			case <-time.After(30 * time.Second):
				cancel()
				t.Fatal("poll did not stop after its context was cancelled")
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			// The poll in flight when the context ended may finish or not,
			// but no further poll starts.
			if n := len(splitPolls(t, out.buf.String())); n != 3 {
				t.Errorf("%d polls ran, want exactly 3", n)
			}
		})
	}
}

// cancelOnWriter collects output and cancels a context as soon as trigger has
// been written.
type cancelOnWriter struct {
	buf     bytes.Buffer
	trigger string
	cancel  context.CancelFunc
}

func (w *cancelOnWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.trigger) {
		w.cancel()
	}
	return n, err
}

// A context that ends while the loop waits for the next poll stops it at
// once, without an error and without sitting out the interval.
func TestPollStopsWaitingWhenContextEnds(t *testing.T) {
	url := startDevice(t, inverter(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	r := runCLIContext(t, ctx, "poll-model", "--id", "1", "--count", "5", "--interval", "1h", "--url", url)
	if r.err != nil {
		t.Errorf("err = %v, want nil for a cancelled poll", r.err)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("cancelled poll returned after %s", d)
	}
	// Whether the poll that was due when the context ended still ran is up
	// to the Modbus client; no later one may.
	if r.stdout != "" {
		if n := len(splitPolls(t, r.stdout)); n != 1 {
			t.Errorf("%d polls ran after cancellation, want at most 1", n)
		}
	}
}

// The interval is waited between polls, not before the first one.
func TestPollInterval(t *testing.T) {
	url := startDevice(t, inverter(t))

	start := time.Now()
	r := runCLI(t, "poll-point", "--model", "101", "--point", "W", "--count", "1", "--interval", "1h", "--url", url)
	if r.err != nil || len(splitPolls(t, r.stdout)) != 1 {
		t.Fatalf("err %v, output:\n%s", r.err, r.stdout)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("a single poll took %s; the interval must not delay the first poll", d)
	}

	const interval = 60 * time.Millisecond
	start = time.Now()
	r = runCLI(t, "poll-point", "--model", "101", "--point", "W", "--count", "3", "--interval", interval.String(), "--url", url)
	if r.err != nil || len(splitPolls(t, r.stdout)) != 3 {
		t.Fatalf("err %v, output:\n%s", r.err, r.stdout)
	}
	if d := time.Since(start); d < 2*interval {
		t.Errorf("3 polls took %s, want at least two intervals (%s)", d, 2*interval)
	}
}

// brokenInverter exposes a Common model followed by a vendor model that
// announces 200 registers of which only 130 can be read.
func brokenInverter(t *testing.T) *testutil.SunSpecHandler {
	t.Helper()
	common := newModel(t, 1, 0).str("Mn", "Acme")
	regs := make([]uint16, 130)
	return testutil.NewSunSpecFixture(40000, 1, common.fixture(), testutil.FixtureModel{ID: 64950, Length: 200, Registers: regs})
}

func TestPartialReadIsAWarning(t *testing.T) {
	url := startDevice(t, brokenInverter(t))
	const warning = "warning: partial read: sunspec: partial read: read at 40072+125: " +
		"Read Holding Registers (0x03): Illegal Data Address (0x02)\n"
	brokenModel := "=== Model 64950: unknown_64950 ===\n" +
		"  WARNING: read error: sunspec: partial read: read at 40072+125: " +
		"Read Holding Registers (0x03): Illegal Data Address (0x02)\n\n"

	// read: the models that could be read are printed, the command succeeds.
	r := runCLI(t, "read", "--url", url)
	if r.err != nil {
		t.Fatalf("read: %v", r.err)
	}
	if r.stderr != warning {
		t.Errorf("stderr = %q, want %q", r.stderr, warning)
	}
	if !strings.HasPrefix(r.stdout, "=== Model 1: Common ===\n") || !strings.HasSuffix(r.stdout, brokenModel) {
		t.Errorf("stdout:\n%s", r.stdout)
	}

	// read --json: stdout stays valid JSON, the warning goes to stderr.
	r = runCLI(t, "read", "--json", "--url", url)
	if r.err != nil || r.stderr != warning {
		t.Fatalf("read --json: err %v, stderr %q", r.err, r.stderr)
	}
	var models []*sunspec.DecodedModel
	if err := json.Unmarshal([]byte(r.stdout), &models); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if len(models) != 2 || len(models[1].RawRegisters) != 125 || len(models[1].Warnings) != 1 {
		t.Errorf("read --json returned %d models; broken model: %+v", len(models), models[len(models)-1])
	}

	// poll: warns on every iteration and keeps going.
	r = runCLI(t, "poll", "--count", "2", "--interval", "1ms", "--url", url)
	if r.err != nil {
		t.Fatalf("poll: %v", r.err)
	}
	if r.stderr != warning+warning {
		t.Errorf("stderr = %q, want the warning twice", r.stderr)
	}
	if bodies := splitPolls(t, r.stdout); len(bodies) != 2 || !strings.HasSuffix(bodies[1], brokenModel) {
		t.Errorf("stdout:\n%s", r.stdout)
	}

	// Reading the broken model on its own is an error.
	wantFail(t, runCLI(t, "read-model", "--id", "64950", "--url", url),
		"read model 64950: sunspec: partial read: read at 40072+125: Read Holding Registers (0x03): Illegal Data Address (0x02)")
	r = runCLI(t, "poll-model", "--id", "64950", "--count", "3", "--interval", "1ms", "--url", url)
	if r.err == nil || !errors.Is(r.err, sunspec.ErrPartialRead) || !errors.Is(r.err, modbus.ErrIllegalDataAddress) {
		t.Errorf("poll-model on the broken model: err = %v", r.err)
	}
	wantNoCommandOutput(t, r)
}

func TestUnknownModelAndPoint(t *testing.T) {
	url := startDevice(t, inverter(t))

	tests := []struct {
		args []string
		want string
		is   error
	}{
		{
			[]string{"read-model", "--id", "999"},
			"read model 999: sunspec: unknown model ID: model 999 not found in discovery", sunspec.ErrUnknownModel,
		},
		{
			[]string{"poll-model", "--id", "999", "--count", "2", "--interval", "1ms"},
			"read model 999: sunspec: unknown model ID: model 999 not found in discovery", sunspec.ErrUnknownModel,
		},
		{[]string{"read-point", "--model", "999", "--point", "W"}, "model 999 not found", nil},
		{[]string{"poll-point", "--model", "999", "--point", "W", "--count", "2"}, "model 999 not found", nil},
		{
			[]string{"read-point", "--model", "101", "--point", "Nope"},
			`read point: sunspec: point not found: point "Nope" in model 101`, sunspec.ErrPointNotFound,
		},
		{
			[]string{"poll-point", "--model", "101", "--point", "Nope", "--count", "2", "--interval", "1ms"},
			`read point: sunspec: point not found: point "Nope" in model 101`, sunspec.ErrPointNotFound,
		},
		{
			// Point names are case-sensitive.
			[]string{"read-point", "--model", "101", "--point", "w"},
			`read point: sunspec: point not found: point "w" in model 101`, sunspec.ErrPointNotFound,
		},
		{
			// A model without a schema has no named points.
			[]string{"read-point", "--model", "64950", "--point", "ID"},
			`read point: sunspec: point not found: point "ID" in model 64950`, sunspec.ErrPointNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			r := runCLI(t, append(tc.args, "--url", url)...)
			wantFail(t, r, tc.want)
			if tc.is != nil && !errors.Is(r.err, tc.is) {
				t.Errorf("err = %v, want it to wrap %v", r.err, tc.is)
			}
		})
	}
}

// deviceCommands lists one valid invocation of every command that talks to a
// device.
var deviceCommands = [][]string{
	{"detect"},
	{"models"},
	{"read"},
	{"read-model", "--id", "1"},
	{"read-point", "--model", "1", "--point", "Mn"},
	{"poll", "--count", "1"},
	{"poll-model", "--id", "1", "--count", "1"},
	{"poll-point", "--model", "1", "--point", "Mn", "--count", "1"},
}

func TestNotSunSpec(t *testing.T) {
	// Registers that can be read at the default base addresses, but no marker.
	h := &testutil.SunSpecHandler{UnitID: 1, Registers: map[uint16]uint16{0: 0, 1: 0, 2: 0, 40000: 0x5375, 40001: 0x0000}}
	url := startDevice(t, h)

	for _, args := range deviceCommands {
		t.Run(args[0], func(t *testing.T) {
			r := runCLI(t, append(args, "--url", url)...)
			want := "discover: sunspec: device is not SunSpec-enabled"
			if args[0] == "detect" {
				want = "detect: sunspec: device is not SunSpec-enabled"
			}
			wantFail(t, r, want)
			if !errors.Is(r.err, sunspec.ErrNotSunSpec) {
				t.Errorf("err = %v, want it to wrap ErrNotSunSpec", r.err)
			}
		})
	}
}

func TestConnectionRefused(t *testing.T) {
	url := "tcp://127.0.0.1:" + strconv.Itoa(freePort(t)) // nothing listens here

	for _, args := range deviceCommands {
		t.Run(args[0], func(t *testing.T) {
			r := runCLI(t, append(args, "--url", url, "--timeout", "2s")...)
			if r.err == nil {
				t.Fatalf("no error; stdout:\n%s", r.stdout)
			}
			if !strings.HasPrefix(r.err.Error(), "connect: ") {
				t.Errorf("err = %q, want a connect error", r.err)
			}
			if !strings.HasPrefix(r.stderr, "Error: connect: ") {
				t.Errorf("stderr = %q", r.stderr)
			}
			wantNoCommandOutput(t, r)
		})
	}
}

func TestBadURL(t *testing.T) {
	for _, args := range deviceCommands {
		r := runCLI(t, append(args, "--url", "bogus://nowhere")...)
		if r.err == nil || !strings.HasPrefix(r.err.Error(), "create client: ") {
			t.Errorf("%s: err = %v, want a create client error", args[0], r.err)
		}
		if !errors.Is(r.err, modbus.ErrConfigurationError) {
			t.Errorf("%s: err = %v, want it to wrap modbus.ErrConfigurationError", args[0], r.err)
		}
	}
}

func TestUnitID(t *testing.T) {
	h := inverter(t)
	h.UnitID = 7
	url := startDevice(t, h)

	wantOK(t, runCLI(t, "read-point", "--unit-id", "7", "--model", "101", "--point", "St", "--url", url),
		lines("Point:   St", "Type:    enum16", "Raw:     4", "Symbols: MPPT"))
	wantOK(t, runCLI(t, "detect", "--unit-id=7", "--url", url), lines(
		"Detected:     true", "Unit ID:      7", "Base Address: 40000", "Reg Type:     0",
		"Marker:       0x5375 0x6E53", "Attempts:     2"))

	// The default unit ID 1 is not answered by this device.
	r := runCLI(t, "models", "--url", url)
	if r.err == nil || !errors.Is(r.err, modbus.ErrIllegalFunction) || errors.Is(r.err, sunspec.ErrNotSunSpec) {
		t.Errorf("models with the wrong unit ID: err = %v", r.err)
	}
}

func TestBadFlagsAndArguments(t *testing.T) {
	tests := []struct {
		args []string
		want string // exact error message
	}{
		{[]string{"read-model"}, `required flag(s) "id" not set`},
		{[]string{"poll-model"}, `required flag(s) "id" not set`},
		{[]string{"read-point"}, `required flag(s) "model", "point" not set`},
		{[]string{"read-point", "--model", "1"}, `required flag(s) "point" not set`},
		{[]string{"poll-point", "--point", "W"}, `required flag(s) "model" not set`},
		{[]string{"read-model", "--id", "70000"}, `invalid argument "70000" for "--id" flag: strconv.ParseUint: parsing "70000": value out of range`},
		{[]string{"read-model", "--id", "abc"}, `invalid argument "abc" for "--id" flag: strconv.ParseUint: parsing "abc": invalid syntax`},
		{[]string{"read-model", "--id", "-1"}, `invalid argument "-1" for "--id" flag: strconv.ParseUint: parsing "-1": invalid syntax`},
		{[]string{"detect", "--unit-id", "256"}, `invalid argument "256" for "--unit-id" flag: strconv.ParseUint: parsing "256": value out of range`},
		{[]string{"detect", "--timeout", "soon"}, `invalid argument "soon" for "--timeout" flag: time: invalid duration "soon"`},
		{[]string{"poll", "--interval", "5"}, `invalid argument "5" for "--interval" flag: time: missing unit in duration "5"`},
		{[]string{"poll", "--count", "many"}, `invalid argument "many" for "--count" flag: strconv.ParseInt: parsing "many": invalid syntax`},
		{[]string{"detect", "--json=maybe"}, `invalid argument "maybe" for "--json" flag: strconv.ParseBool: parsing "maybe": invalid syntax`},
		{[]string{"detect", "--no-such-flag"}, `unknown flag: --no-such-flag`},
		{[]string{"read", "--id", "1"}, `unknown flag: --id`},
		{[]string{"read", "--count", "1"}, `unknown flag: --count`},
		{[]string{"frobnicate"}, `unknown command "frobnicate" for "sunspecctl"`},
		{[]string{"completion"}, `accepts 1 arg(s), received 0`},
		{[]string{"completion", "bash", "zsh"}, `accepts 1 arg(s), received 2`},
		{[]string{"completion", "tcsh"}, `invalid argument "tcsh" for "sunspecctl completion"`},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			// A URL nothing listens on: flag errors must come before any I/O.
			r := runCLI(t, append(tc.args, "--url", "tcp://127.0.0.1:1")...)
			wantFail(t, r, tc.want)
		})
	}
}

func TestFlagDefaults(t *testing.T) {
	// A previous command line must not leak into the next root command.
	if r := runCLI(t, "version", "--json", "--raw", "--unit-id", "9", "--timeout", "1s", "--url", "tcp://example.invalid:1"); r.err != nil {
		t.Fatal(r.err)
	}
	if !flagJSON || !flagRaw || flagUnitID != 9 || flagTimeout != time.Second || flagURL != "tcp://example.invalid:1" {
		t.Errorf("flags not parsed: json=%v raw=%v unit=%d timeout=%s url=%s", flagJSON, flagRaw, flagUnitID, flagTimeout, flagURL)
	}

	root := newRootCmd()
	if flagJSON || flagRaw || flagUnitID != 1 || flagTimeout != 10*time.Second || flagURL != "tcp://localhost:502" {
		t.Errorf("defaults: json=%v raw=%v unit=%d timeout=%s url=%s", flagJSON, flagRaw, flagUnitID, flagTimeout, flagURL)
	}
	if opts := discoverOpts(); opts.UnitID != 1 || opts.BaseAddresses != nil || opts.MaxModels != 0 {
		t.Errorf("discoverOpts() = %+v", opts)
	}

	var names []string
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	want := "completion detect models poll poll-model poll-point read read-model read-point version"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("commands = %q, want %q", got, want)
	}
	for _, c := range root.Commands() {
		if strings.HasPrefix(c.Name(), "poll") {
			if f := c.Flags().Lookup("interval"); f == nil || f.DefValue != "30s" {
				t.Errorf("%s: --interval default = %+v, want 30s", c.Name(), f)
			}
			if f := c.Flags().Lookup("count"); f == nil || f.DefValue != "0" {
				t.Errorf("%s: --count default = %+v, want 0", c.Name(), f)
			}
		}
	}
}

func TestHelp(t *testing.T) {
	r := runCLI(t, "--help")
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, want := range []string{
		"SunSpec Modbus tool for inspecting solar inverters and meters\n",
		"Usage:\n  sunspecctl [command]\n",
		"  detect      Detect SunSpec device presence\n",
		"  models      List discovered SunSpec models\n",
		"  read        Read and decode all models\n",
		"  read-model  Read and decode a specific model by ID\n",
		"  read-point  Read a single named point from a model\n",
		"  poll        Repeatedly read and decode all models\n",
		"  poll-model  Repeatedly read and decode a specific model by ID\n",
		"  poll-point  Repeatedly read a single named point from a model\n",
		"  completion  Generate shell completion script\n",
		"  version     Print the sunspecctl version\n",
		"      --json               Output as JSON\n",
		"      --raw                Include raw register hex in output\n",
		"      --timeout duration   Operation timeout (default 10s)\n",
		"      --unit-id uint8      Modbus unit ID (default 1)\n",
		"      --url string         Modbus device URL (default \"tcp://localhost:502\")\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("root help lacks %q:\n%s", want, r.stdout)
		}
	}
	// No arguments prints the same help.
	if bare := runCLI(t); bare.err != nil || bare.stdout != r.stdout {
		t.Errorf("running without arguments does not print the help (err %v)", bare.err)
	}

	r = runCLI(t, "poll-point", "--help")
	for _, want := range []string{
		"Repeatedly read a single named point from a model\n",
		"  sunspecctl poll-point [flags]\n",
		"      --count int           Number of polls (0 = infinite)\n",
		"      --interval duration   Interval between polls (e.g. 5s, 1m, 1h) (default 30s)\n",
		"      --model uint16        Model ID (required)\n",
		"      --point string        Point name (required)\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("poll-point help lacks %q:\n%s", want, r.stdout)
		}
	}

	r = runCLI(t, "read-model", "-h")
	if !strings.Contains(r.stdout, "      --id uint16   Model ID to read (required)\n") {
		t.Errorf("read-model help:\n%s", r.stdout)
	}
	r = runCLI(t, "completion", "--help")
	if !strings.Contains(r.stdout, "  source <(sunspecctl completion bash)\n") ||
		!strings.Contains(r.stdout, "  sunspecctl completion [bash|zsh|fish|powershell]\n") {
		t.Errorf("completion help:\n%s", r.stdout)
	}
}

func TestCompletion(t *testing.T) {
	headers := map[string]string{
		"bash":       "# bash completion for sunspecctl",
		"zsh":        "#compdef sunspecctl",
		"fish":       "# fish completion for sunspecctl",
		"powershell": "# powershell completion for sunspecctl",
	}
	for shell, header := range headers {
		t.Run(shell, func(t *testing.T) {
			r := runCLI(t, "completion", shell)
			if r.err != nil || r.stderr != "" {
				t.Fatalf("err %v, stderr %q", r.err, r.stderr)
			}
			if !strings.HasPrefix(r.stdout, header) {
				t.Errorf("script starts with %q, want %q", strings.SplitN(r.stdout, "\n", 2)[0], header)
			}
			if len(r.stdout) < 500 {
				t.Errorf("script is only %d bytes", len(r.stdout))
			}
		})
	}
	// The bash script knows the commands and flags of this tool.
	bash := runCLI(t, "completion", "bash").stdout
	for _, want := range []string{"poll-point", "read-model", "--unit-id", "--interval"} {
		if !strings.Contains(bash, want) {
			t.Errorf("bash completion does not mention %s", want)
		}
	}
}

// setBuildInfo overrides the link-time build metadata for one test.
func setBuildInfo(t *testing.T, v, tg, c, d string) {
	t.Helper()
	oldV, oldT, oldC, oldD := version, tag, commit, buildDate
	version, tag, commit, buildDate = v, tg, c, d
	t.Cleanup(func() { version, tag, commit, buildDate = oldV, oldT, oldC, oldD })
}

func TestVersion(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setBuildInfo(t, "dev", "", "", "")
		wantOK(t, runCLI(t, "version"), "sunspecctl dev\n")
		wantOK(t, runCLI(t, "version", "--json"), "{\n  \"version\": \"dev\"\n}\n")
	})
	t.Run("full build metadata", func(t *testing.T) {
		setBuildInfo(t, "v1.2.3-4-gabc1234", "v1.2.3", "abc1234", "2026-01-02T03:04:05Z")
		wantOK(t, runCLI(t, "version"), lines(
			"sunspecctl v1.2.3-4-gabc1234",
			"tag:       v1.2.3",
			"commit:    abc1234",
			"built:     2026-01-02T03:04:05Z",
		))
		var info map[string]string
		decodeJSON(t, runCLI(t, "--json", "version"), &info)
		want := map[string]string{
			"version": "v1.2.3-4-gabc1234", "tag": "v1.2.3", "commit": "abc1234", "buildDate": "2026-01-02T03:04:05Z",
		}
		if len(info) != len(want) {
			t.Errorf("version --json = %v, want %v", info, want)
		}
		for k, v := range want {
			if info[k] != v {
				t.Errorf("version --json: %s = %q, want %q", k, info[k], v)
			}
		}
	})
	t.Run("partial build metadata", func(t *testing.T) {
		setBuildInfo(t, "v9", "", "deadbee", "")
		wantOK(t, runCLI(t, "version"), "sunspecctl v9\ncommit:    deadbee\n")
		wantOK(t, runCLI(t, "version", "--json"), "{\n  \"commit\": \"deadbee\",\n  \"version\": \"v9\"\n}\n")
	})
	t.Run("needs no device", func(t *testing.T) {
		setBuildInfo(t, "dev", "", "", "")
		wantOK(t, runCLI(t, "version", "--url", "tcp://127.0.0.1:1"), "sunspecctl dev\n")
	})
}

func TestPrintDecodedModelRawWrapsAtSixteen(t *testing.T) {
	regs := make([]uint16, 33)
	for i := range regs {
		regs[i] = uint16(i)
	}
	flagRaw = true
	t.Cleanup(func() { flagRaw = false })

	var out bytes.Buffer
	printDecodedModel(&out, &sunspec.DecodedModel{ModelID: 5, Name: "five", RawRegisters: regs, Warnings: []string{"first", "second"}})
	want := lines(
		"=== Model 5: five ===",
		"  Raw registers (33):",
		"    0000: 0000 0001 0002 0003 0004 0005 0006 0007 0008 0009 000A 000B 000C 000D 000E 000F",
		"    0016: 0010 0011 0012 0013 0014 0015 0016 0017 0018 0019 001A 001B 001C 001D 001E 001F",
		"    0032: 0020",
		"  WARNING: first",
		"  WARNING: second",
		"",
	)
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}

	// Without registers there is no raw section, even with --raw.
	out.Reset()
	printDecodedModel(&out, &sunspec.DecodedModel{ModelID: 6, Name: "six"})
	if out.String() != "=== Model 6: six ===\n\n" {
		t.Errorf("empty model output = %q", out.String())
	}
}

func TestPrintJSONReportsEncodingErrors(t *testing.T) {
	if err := printJSON(io.Discard, map[string]interface{}{"f": func() {}}); err == nil {
		t.Error("printJSON of an unencodable value returned no error")
	}
	var out bytes.Buffer
	if err := printJSON(&out, []int{1}); err != nil || out.String() != "[\n  1\n]\n" {
		t.Errorf("printJSON = %q, %v", out.String(), err)
	}
}

// TestMain lets the test binary stand in for the sunspecctl executable: when
// SUNSPECCTL_TEST_MAIN is set it runs main with the remaining arguments
// instead of the tests.
func TestMain(m *testing.M) {
	if os.Getenv("SUNSPECCTL_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMain runs main in a child process, as the installed binary would.
func runMain(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "SUNSPECCTL_TEST_MAIN=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v", exe, err)
	}
	return out.String(), errb.String(), exitCode
}

func TestMainExitCodes(t *testing.T) {
	stdout, stderr, code := runMain(t, "version")
	if code != 0 || !strings.HasPrefix(stdout, "sunspecctl ") || stderr != "" {
		t.Errorf("version: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	stdout, stderr, code = runMain(t, "frobnicate")
	if code != 1 {
		t.Errorf("unknown command: exit %d, want 1", code)
	}
	if stdout != "" || !strings.HasPrefix(stderr, "Error: unknown command \"frobnicate\" for \"sunspecctl\"\n") {
		t.Errorf("unknown command: stdout %q, stderr %q", stdout, stderr)
	}

	url := startDevice(t, inverter(t))
	stdout, stderr, code = runMain(t, "read-point", "--model", "101", "--point", "Hz", "--url", url)
	want := lines("Point:   Hz", "Type:    uint16", "Raw:     5001", "Scaled:  50.01", "Units:   Hz")
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("read-point: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	// A failing command prints the error and the usage on stderr, nothing on
	// stdout.
	stdout, stderr, code = runMain(t, "read-model", "--id", "999", "--url", url)
	if code != 1 || stdout != "" {
		t.Errorf("unknown model: exit %d, stdout %q", code, stdout)
	}
	if !strings.HasPrefix(stderr, "Error: read model 999: sunspec: unknown model ID: model 999 not found in discovery\nUsage:\n  sunspecctl read-model [flags]\n") {
		t.Errorf("unknown model: stderr:\n%s", stderr)
	}

	// A partial read is a warning on stderr; the command still succeeds.
	stdout, stderr, code = runMain(t, "read", "--url", startDevice(t, brokenInverter(t)))
	if code != 0 || !strings.HasPrefix(stderr, "warning: partial read: ") || strings.Contains(stdout, "warning:") ||
		!strings.HasPrefix(stdout, "=== Model 1: Common ===\n") {
		t.Errorf("partial read: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

// TestMainInProcess runs main itself, with the process-wide arguments and
// stdout redirected, so that its success path is exercised in this process.
func TestMainInProcess(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	oldArgs, oldStdout := os.Args, os.Stdout
	os.Args, os.Stdout = []string{"sunspecctl", "version"}, out
	defer func() { os.Args, os.Stdout = oldArgs, oldStdout }()

	main()

	os.Stdout = oldStdout
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "sunspecctl "+version+"\n") {
		t.Errorf("main printed %q", got)
	}
}
