// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/testutil"
)

// r is shorthand for the registers of one point.
func r(regs ...uint16) []uint16 { return regs }

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

var (
	voltVarTable = lines(
		"=== Model 705: DER Volt-Var ===",
		"  [Fixed]",
		"    ID          uint16  705",
		"    L           uint16  41",
		"    Ena         enum16  1 [ENABLED]",
		"    NPt         uint16  2",
		"    NCrv        uint16  2",
		"    V_SF        sunssf  -1",
		"    DeptRef_SF  sunssf  -2",
		"    RspTms_SF   sunssf  0",
		"    [Crv 1]",
		"      ActPt     uint16  2",
		"      DeptRef   enum16  1 [VAR_MAX_PCT]",
		"      VRef      uint16  100 VNomPct",
		"      RspTms    uint32  5 Secs",
		"      ReadOnly  enum16  1 [R]",
		"      [Pt 1]",
		"        V    uint16  92 VNomPct",
		"        Var  int16   30 DeptRef",
		"      [Pt 2]",
		"        V    uint16  108 VNomPct",
		"        Var  int16   -30 DeptRef",
		"    [Crv 2]",
		"      ActPt     uint16  1",
		"      ReadOnly  enum16  0 [RW]",
		"      [Pt 1]",
		"        V    uint16  100 VNomPct",
		"        Var  int16   0 DeptRef",
		// An instance whose points are all not implemented still shows.
		"      [Pt 2]",
	)
	controlsTable = lines(
		"=== Model 704: DER AC Controls ===",
		"  [Fixed]",
		"    ID             uint16  704",
		"    L              uint16  65",
		"    WMaxLimPctEna  enum16  1 [ENABLED]",
		"    WMaxLimPct     uint16  80.5 Pct",
		"    PF_SF          sunssf  -2",
		"    WMaxLimPct_SF  sunssf  -2",
		"    [PFWInj 1]",
		"      PF   uint16  1",
		"      Ext  enum16  1 [UNDER_EXCITED]",
		"    [PFWInjRvrt 1]",
		"      PF   uint16  0.96",
		"      Ext  enum16  0 [OVER_EXCITED]",
		"    [PFWAbs 1]",
		"      PF  uint16  0.97",
		"    [PFWAbsRvrt 1]",
	)
	controlsRaw = lines(
		"  Raw registers (67):",
		"    0000: 02C0 0041 FFFF FFFF FFFF FFFF FFFF FFFF FFFF FFFF FFFF FFFF FFFF FFFF 0001 1F72",
		"    0016: FFFF FFFF FFFF FFFF FFFF FFFF FFFF FFFF 8000 0000 8000 0000 8000 8000 FFFF FFFF",
		"    0032: FFFF FFFF FFFF FFFF FFFF FFFF 8000 0000 8000 0000 8000 8000 FFFF FFFF FFFF FFFF",
		"    0048: FFFF FFFF FFFF FFFF FFFF FFFE FFFE 8000 8000 8000 8000 0064 0001 0060 0000 0061",
		"    0064: FFFF FFFF FFFF",
	)
)

func TestReadModelNestedTable(t *testing.T) {
	url := startDevice(t, derDevice(t))

	wantOK(t, runCLI(t, "models", "--url", url), lines(
		"ID   NAME             START  LENGTH  SCHEMA",
		"1    Common           40002  66      yes",
		"705  DER Volt-Var     40070  41      yes",
		"704  DER AC Controls  40113  65      yes",
	))
	wantOK(t, runCLI(t, "read-model", "--id", "705", "--url", url), voltVarTable+"\n")
	wantOK(t, runCLI(t, "read-model", "--id", "704", "--url", url), controlsTable+"\n")
	wantOK(t, runCLI(t, "read-model", "--id", "704", "--raw", "--url", url), controlsTable+controlsRaw+"\n")

	// read prints the same blocks for every model, poll-model per poll.
	all := runCLI(t, "read", "--url", url)
	if all.err != nil || !strings.HasSuffix(all.stdout, "\n"+voltVarTable+"\n"+controlsTable+"\n") {
		t.Errorf("read: err %v, stdout:\n%s", all.err, all.stdout)
	}
	polled := runCLI(t, "poll-model", "--id", "705", "--count", "2", "--interval", "1ms", "--url", url)
	if bodies := splitPolls(t, polled.stdout); polled.err != nil || len(bodies) != 2 || bodies[0] != voltVarTable+"\n" || bodies[1] != voltVarTable+"\n" {
		t.Errorf("poll-model: err %v, stdout:\n%s", polled.err, polled.stdout)
	}
}

func TestReadModelNestedJSON(t *testing.T) {
	url := startDevice(t, derDevice(t))

	var dm sunspec.DecodedModel
	res := runCLI(t, "read-model", "--id", "705", "--json", "--url", url)
	decodeJSON(t, res, &dm)
	if dm.ModelID != 705 || dm.Name != "DER Volt-Var" || dm.InstanceAddress != 40070 || len(dm.RawRegisters) != 43 || len(dm.Warnings) != 0 {
		t.Errorf("model = ID %d %q at %d, %d registers, warnings %q", dm.ModelID, dm.Name, dm.InstanceAddress, len(dm.RawRegisters), dm.Warnings)
	}
	top := dm.Group
	if top == nil || top.Name != "DERVoltVar" || top.Index != 0 || len(top.Points) != 13 {
		t.Fatalf("top-level group = %+v", top)
	}
	crvs := top.GroupsNamed("Crv")
	if len(crvs) != 2 || len(top.Groups) != 2 {
		t.Fatalf("%d Crv instances in JSON, want 2", len(crvs))
	}
	type pt struct {
		v, vars float64
		off     int
		ok      bool
	}
	want := [][]pt{
		{{92, 30, 25, true}, {108, -30, 27, true}},
		{{100, 0, 39, true}, {0, 0, 41, false}},
	}
	for i, c := range crvs {
		pts := c.GroupsNamed("Pt")
		if c.Index != i+1 || len(c.Points) != 9 || len(pts) != 2 || len(c.Groups) != 2 {
			t.Fatalf("Crv %d = index %d, %d points, %d nested", i+1, c.Index, len(c.Points), len(c.Groups))
		}
		for k, p := range pts {
			w := want[i][k]
			vp, varp := point(t, p, "V"), point(t, p, "Var")
			if p.Index != k+1 || vp.RegisterOffset != w.off || varp.RegisterOffset != w.off+1 || vp.Implemented != w.ok || varp.Implemented != w.ok {
				t.Errorf("Crv %d Pt %d = index %d, V %+v, Var %+v", i+1, k+1, p.Index, vp, varp)
			}
			if !w.ok {
				if vp.ScaledValue != nil || varp.ScaledValue != nil {
					t.Errorf("Crv %d Pt %d: a not implemented point is scaled", i+1, k+1)
				}
				continue
			}
			if vp.ScaledValue == nil || *vp.ScaledValue != w.v || vp.SFName != "V_SF" || vp.SFRawValue == nil || *vp.SFRawValue != -1 {
				t.Errorf("Crv %d Pt %d V = %+v, want scaled %g", i+1, k+1, vp, w.v)
			}
			if varp.ScaledValue == nil || *varp.ScaledValue != w.vars || varp.SFName != "DeptRef_SF" || varp.SFRawValue == nil || *varp.SFRawValue != -2 {
				t.Errorf("Crv %d Pt %d Var = %+v, want scaled %g", i+1, k+1, varp, w.vars)
			}
		}
	}
	if vref := point(t, crvs[0], "VRef"); vref.ScaledValue == nil || *vref.ScaledValue != 100 || vref.RegisterOffset != 18 {
		t.Errorf("Crv 1 VRef = %+v", vref)
	}
	// The JSON field names are those of the Go structs.
	for _, key := range []string{`"Group": {`, `"Groups": [`, `"Name": "Crv"`, `"Index": 2`, `"Name": "Pt"`, `"RegisterOffset": 41`} {
		if !strings.Contains(res.stdout, key) {
			t.Errorf("JSON output lacks %s", key)
		}
	}
	// The schema travels with the model, nested groups included.
	if dm.Schema == nil || dm.Schema.Group == nil || dm.Schema.Group.Group("Crv") == nil || dm.Schema.Group.Group("Crv").Group("Pt") == nil {
		t.Errorf("Schema in JSON lacks the nested groups")
	}

	var ctl sunspec.DecodedModel
	decodeJSON(t, runCLI(t, "read-model", "--id", "704", "--json", "--url", url), &ctl)
	var names []string
	for _, g := range ctl.Group.Groups {
		names = append(names, g.Name)
		if g.Index != 1 || len(g.Points) != 2 || g.Groups != nil {
			t.Errorf("704 group %s = %+v", g.Name, g)
		}
	}
	if want := []string{"PFWInj", "PFWInjRvrt", "PFWAbs", "PFWAbsRvrt"}; !reflect.DeepEqual(names, want) {
		t.Errorf("704 groups = %v, want %v", names, want)
	}
	if pf := point(t, ctl.Group.Groups[2], "PF"); pf.ScaledValue == nil || *pf.ScaledValue != 0.97 || pf.RegisterOffset != 63 {
		t.Errorf("PFWAbs PF = %+v", pf)
	}
}

func TestReadPointNested(t *testing.T) {
	url := startDevice(t, derDevice(t))

	// A point that exists only in nested groups: the first instance in
	// register order, with its offset from the model's ID register.
	wantOK(t, runCLI(t, "read-point", "--model", "705", "--point", "V", "--raw", "--url", url),
		lines("Point:   V", "Type:    uint16", "Raw:     920", "Scaled:  92", "Units:   VNomPct", "Offset:  25", "Count:   1"))
	wantOK(t, runCLI(t, "read-point", "--model", "705", "--point", "ActPt", "--url", url),
		lines("Point:   ActPt", "Type:    uint16", "Raw:     2"))
	wantOK(t, runCLI(t, "read-point", "--model", "704", "--point", "Ext", "--raw", "--url", url),
		lines("Point:   Ext", "Type:    enum16", "Raw:     1", "Symbols: UNDER_EXCITED", "Offset:  60", "Count:   1"))
	wantOK(t, runCLI(t, "poll-point", "--model", "705", "--point", "Var", "--count", "1", "--json", "--url", url),
		lines("{", `  "Name": "Var",`, `  "Type": "int16",`, `  "RawValue": 3000,`, `  "ScaledValue": 30,`, `  "Units": "DeptRef",`,
			`  "SFName": "DeptRef_SF",`, `  "SFRawValue": -2,`, `  "RegisterOffset": 26,`, `  "RegisterCount": 1,`, `  "Implemented": true,`, `  "Symbols": null`, "}"))

	// Group names are not point names.
	res := runCLI(t, "read-point", "--model", "705", "--point", "Crv", "--url", url)
	wantFail(t, res, `read point: sunspec: point not found: point "Crv" in model 705`)
	if !errors.Is(res.err, sunspec.ErrPointNotFound) {
		t.Errorf("err = %v", res.err)
	}
}

// A device whose registers end before the announced curves do: what is there
// is printed, the rest is a warning.
func TestReadModelNestedTruncated(t *testing.T) {
	short := newModel(t, 705, 0).set("NPt", 2).set("NCrv", 2).set("V_SF", 0).set("DeptRef_SF", 0).
		add("Crv", map[string][]uint16{"ActPt": r(1)}).
		add("Crv/Pt", map[string][]uint16{"V": r(7), "Var": r(8)})
	url := startDevice(t, testutil.NewSunSpecFixture(40000, 1, short.fixture()))

	wantOK(t, runCLI(t, "read-model", "--id", "705", "--url", url), lines(
		"=== Model 705: DER Volt-Var ===",
		"  [Fixed]",
		"    ID          uint16  705",
		"    L           uint16  25",
		"    NPt         uint16  2",
		"    NCrv        uint16  2",
		"    V_SF        sunssf  0",
		"    DeptRef_SF  sunssf  0",
		"    [Crv 1]",
		"      ActPt  uint16  1",
		"      [Pt 1]",
		"        V    uint16  7 VNomPct",
		"        Var  int16   8 DeptRef",
		"  WARNING: DERVoltVar/Crv[1]/Pt: registers end before instance 2 of 2",
		"  WARNING: DERVoltVar/Crv: registers end before instance 2 of 2",
		"",
	))
}
