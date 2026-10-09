// SPDX-License-Identifier: MIT

package sunspec

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// notImplemented returns the SunSpec "not implemented" registers for a point.
func notImplemented(p *registry.PointMeta) []uint16 {
	out := make([]uint16, p.Size)
	if len(out) == 0 {
		return out
	}
	switch p.Type {
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

// instance returns the registers of one instance of g's own points: every
// point not implemented, except those given in vals (point name -> registers).
func instance(t *testing.T, g *registry.GroupMeta, vals map[string][]uint16) []uint16 {
	t.Helper()
	if g == nil {
		t.Fatal("instance: nil group")
	}
	regs := make([]uint16, g.Length)
	for i := range g.Points {
		p := &g.Points[i]
		copy(regs[p.Offset:], notImplemented(p))
	}
	for name, v := range vals {
		p := g.Point(name)
		if p == nil {
			t.Fatalf("group %s has no point %s", g.Name, name)
		}
		if len(v) != p.Size {
			t.Fatalf("group %s point %s: %d registers given, size is %d", g.Name, name, len(v), p.Size)
		}
		copy(regs[p.Offset:], v)
	}
	return regs
}

// concat concatenates register slices.
func concat(parts ...[]uint16) []uint16 {
	var out []uint16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// join concatenates the register slices of a whole model and patches its
// length register.
func join(parts ...[]uint16) []uint16 {
	out := concat(parts...)
	if len(out) >= 2 {
		out[1] = uint16(len(out) - 2)
	}
	return out
}

// v is shorthand for the registers of a point.
func v(regs ...uint16) []uint16 { return regs }

// implemented returns the lines of flatten that describe implemented points,
// which keeps expectations for large real models readable.
func implemented(dm *DecodedModel) []string {
	var out []string
	for _, l := range flatten(dm) {
		if !strings.Contains(l, "=n/a @") {
			out = append(out, l)
		}
	}
	return out
}

// groupPaths lists every group instance of a decoded model in Walk order.
func groupPaths(dm *DecodedModel) []string {
	var out []string
	dm.Group.Walk(func(g *DecodedGroup) {
		out = append(out, fmt.Sprintf("%s[%d] %d points %d groups", g.Name, g.Index, len(g.Points), len(g.Groups)))
	})
	return out
}

// Model 704 (DER AC Controls) has four sibling "sync" groups after its fixed
// points, each occurring exactly once.
func TestDecodeModel704SiblingGroups(t *testing.T) {
	meta := registry.ByID(704)
	if meta == nil {
		t.Fatal("model 704 missing")
	}
	g := meta.Group
	regs := join(
		instance(t, g, map[string][]uint16{
			"ID": v(704), "L": v(0),
			"PFWInjEna": v(1), "WMaxLimPct": v(8050), "WSet": v(0xFFFF, 0xFC18), // -1000
			"PF_SF": v(0xFFFE), "WMaxLimPct_SF": v(0xFFFE), "WSet_SF": v(1),
		}),
		instance(t, g.Group("PFWInj"), map[string][]uint16{"PF": v(95), "Ext": v(1)}),
		instance(t, g.Group("PFWInjRvrt"), map[string][]uint16{"PF": v(96), "Ext": v(0)}),
		instance(t, g.Group("PFWAbs"), map[string][]uint16{"PF": v(97)}),
		instance(t, g.Group("PFWAbsRvrt"), map[string][]uint16{"PF": v(98), "Ext": v(1)}),
	)
	if len(regs) != 67 {
		t.Fatalf("test setup: %d registers, want 67", len(regs))
	}

	dm, err := DecodeModel(regs, meta, 40100)
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", dm.Warnings)
	}
	wantLines(t, "implemented points", implemented(dm), []string{
		"DERCtlAC.ID=704 @0",
		"DERCtlAC.L=65 @1",
		"DERCtlAC.PFWInjEna=1 @2",
		"DERCtlAC.WMaxLimPct=8050 x10^-2=80.5000 @15",
		"DERCtlAC.WSet=-1000 x10^1=-10000.0000 @24",
		"DERCtlAC.PF_SF=-2 @53",
		"DERCtlAC.WMaxLimPct_SF=-2 @54",
		"DERCtlAC.WSet_SF=1 @55",
		"DERCtlAC/PFWInj[1].PF=95 x10^-2=0.9500 @59",
		"DERCtlAC/PFWInj[1].Ext=1 @60",
		"DERCtlAC/PFWInjRvrt[1].PF=96 x10^-2=0.9600 @61",
		"DERCtlAC/PFWInjRvrt[1].Ext=0 @62",
		"DERCtlAC/PFWAbs[1].PF=97 x10^-2=0.9700 @63",
		"DERCtlAC/PFWAbsRvrt[1].PF=98 x10^-2=0.9800 @65",
		"DERCtlAC/PFWAbsRvrt[1].Ext=1 @66",
	})
	wantLines(t, "groups", groupPaths(dm), []string{
		"DERCtlAC[0] 45 points 4 groups",
		"PFWInj[1] 2 points 0 groups",
		"PFWInjRvrt[1] 2 points 0 groups",
		"PFWAbs[1] 2 points 0 groups",
		"PFWAbsRvrt[1] 2 points 0 groups",
	})

	// The not implemented Ext of PFWAbs is still present, as a point.
	abs := dm.Group.GroupsNamed("PFWAbs")
	if len(abs) != 1 {
		t.Fatalf("%d PFWAbs instances, want 1", len(abs))
	}
	if ext := abs[0].Point("Ext"); ext == nil || ext.Implemented || ext.RegisterOffset != 64 || ext.RegisterCount != 1 {
		t.Errorf("PFWAbs.Ext = %+v", ext)
	}
	// Model-wide lookup finds the first PF in register order.
	if pf := dm.Point("PF"); pf == nil || pf.RawValue != uint16(95) {
		t.Errorf("Point(PF) = %+v, want the PFWInj one", pf)
	}
	// RegisterOffset indexes RawRegisters for every point of the model.
	dm.Group.Walk(func(g *DecodedGroup) {
		for _, p := range g.Points {
			if p.RegisterCount == 1 && p.Type == "uint16" && p.Implemented && dm.RawRegisters[p.RegisterOffset] != p.RawValue {
				t.Errorf("%s.%s: RawRegisters[%d] = %d, RawValue %v", g.Name, p.Name, p.RegisterOffset, dm.RawRegisters[p.RegisterOffset], p.RawValue)
			}
		}
	})
}

// A device that stops after the second sibling group: the groups that are
// there decode, the missing ones are reported.
func TestDecodeModel704Truncated(t *testing.T) {
	meta := registry.ByID(704)
	g := meta.Group
	regs := join(
		instance(t, g, map[string][]uint16{"ID": v(704), "L": v(0), "PF_SF": v(0)}),
		instance(t, g.Group("PFWInj"), map[string][]uint16{"PF": v(1)}),
		instance(t, g.Group("PFWInjRvrt"), map[string][]uint16{"PF": v(2)}),
		v(3), // half of PFWAbs
	)
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "groups", groupPaths(dm), []string{
		"DERCtlAC[0] 45 points 2 groups",
		"PFWInj[1] 2 points 0 groups",
		"PFWInjRvrt[1] 2 points 0 groups",
	})
	wantLines(t, "warnings", dm.Warnings, []string{
		"DERCtlAC/PFWAbs: registers end before instance 1 of 1",
		"DERCtlAC/PFWAbsRvrt: registers end before instance 1 of 1",
		"1 registers left over after the last group",
	})
}

// Model 705 (DER Volt-Var): NCrv curves, each followed by NPt points. The
// curve points are scaled by the scale factors of the top-level group.
func TestDecodeModel705Curves(t *testing.T) {
	meta := registry.ByID(705)
	if meta == nil {
		t.Fatal("model 705 missing")
	}
	g := meta.Group
	crv := g.Group("Crv")
	pt := crv.Group("Pt")
	point := func(volt, vars uint16) []uint16 {
		return instance(t, pt, map[string][]uint16{"V": v(volt), "Var": v(vars)})
	}
	regs := join(
		instance(t, g, map[string][]uint16{
			"ID": v(705), "L": v(0), "Ena": v(1), "NPt": v(3), "NCrv": v(2),
			"V_SF": v(0xFFFF), "DeptRef_SF": v(0xFFFE), "RspTms_SF": v(0),
		}),
		instance(t, crv, map[string][]uint16{"ActPt": v(3), "DeptRef": v(1), "VRef": v(1000), "RspTms": v(0, 6), "ReadOnly": v(1)}),
		point(920, 3000), point(1000, 0), point(1080, 0xF448), // -3000
		instance(t, crv, map[string][]uint16{"ActPt": v(2), "DeptRef": v(1), "VRef": v(1005), "ReadOnly": v(0)}),
		point(900, 4400), point(1100, 0xEED0), // -4400
		// The third point of the second curve is not in use.
		instance(t, pt, nil),
	)
	if len(regs) != 15+2*(10+3*2) {
		t.Fatalf("test setup: %d registers", len(regs))
	}

	dm, err := DecodeModel(regs, meta, 40200)
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", dm.Warnings)
	}
	wantLines(t, "implemented points", implemented(dm), []string{
		"DERVoltVar.ID=705 @0",
		"DERVoltVar.L=45 @1",
		"DERVoltVar.Ena=1 @2",
		"DERVoltVar.NPt=3 @5",
		"DERVoltVar.NCrv=2 @6",
		"DERVoltVar.V_SF=-1 @12",
		"DERVoltVar.DeptRef_SF=-2 @13",
		"DERVoltVar.RspTms_SF=0 @14",

		"DERVoltVar/Crv[1].ActPt=3 @15",
		"DERVoltVar/Crv[1].DeptRef=1 @16",
		"DERVoltVar/Crv[1].VRef=1000 x10^-1=100.0000 @18",
		"DERVoltVar/Crv[1].RspTms=6 x10^0=6.0000 @22",
		"DERVoltVar/Crv[1].ReadOnly=1 @24",
		"DERVoltVar/Crv[1]/Pt[1].V=920 x10^-1=92.0000 @25",
		"DERVoltVar/Crv[1]/Pt[1].Var=3000 x10^-2=30.0000 @26",
		"DERVoltVar/Crv[1]/Pt[2].V=1000 x10^-1=100.0000 @27",
		"DERVoltVar/Crv[1]/Pt[2].Var=0 x10^-2=0.0000 @28",
		"DERVoltVar/Crv[1]/Pt[3].V=1080 x10^-1=108.0000 @29",
		"DERVoltVar/Crv[1]/Pt[3].Var=-3000 x10^-2=-30.0000 @30",

		"DERVoltVar/Crv[2].ActPt=2 @31",
		"DERVoltVar/Crv[2].DeptRef=1 @32",
		"DERVoltVar/Crv[2].VRef=1005 x10^-1=100.5000 @34",
		"DERVoltVar/Crv[2].ReadOnly=0 @40",
		"DERVoltVar/Crv[2]/Pt[1].V=900 x10^-1=90.0000 @41",
		"DERVoltVar/Crv[2]/Pt[1].Var=4400 x10^-2=44.0000 @42",
		"DERVoltVar/Crv[2]/Pt[2].V=1100 x10^-1=110.0000 @43",
		"DERVoltVar/Crv[2]/Pt[2].Var=-4400 x10^-2=-44.0000 @44",
	})
	wantLines(t, "groups", groupPaths(dm), []string{
		"DERVoltVar[0] 13 points 2 groups",
		"Crv[1] 9 points 3 groups",
		"Pt[1] 2 points 0 groups", "Pt[2] 2 points 0 groups", "Pt[3] 2 points 0 groups",
		"Crv[2] 9 points 3 groups",
		"Pt[1] 2 points 0 groups", "Pt[2] 2 points 0 groups", "Pt[3] 2 points 0 groups",
	})

	// The unused point is decoded, not implemented, at the last two registers.
	last := dm.Group.GroupsNamed("Crv")[1].GroupsNamed("Pt")[2]
	if last.Index != 3 || last.Points[0].Implemented || last.Points[1].Implemented ||
		last.Points[0].RegisterOffset != 45 || last.Points[1].RegisterOffset != 46 {
		t.Errorf("Crv[2]/Pt[3] = %+v", last)
	}
	// Pt instances are nested in their curve, not in the top-level group.
	if n := len(dm.Group.GroupsNamed("Pt")); n != 0 {
		t.Errorf("%d Pt instances directly in the top-level group, want 0", n)
	}
	// FindPoint is depth first: the first V is that of the first curve's first point.
	if p := dm.Group.FindPoint("V"); p == nil || p.RegisterOffset != 25 {
		t.Errorf("FindPoint(V) = %+v, want the one at offset 25", p)
	}
	if p := dm.Group.Point("V"); p != nil {
		t.Errorf("Point(V) on the top-level group = %+v, want nil", p)
	}
}

// Model 705 with a count the registers do not bear out.
func TestDecodeModel705CountsVersusRegisters(t *testing.T) {
	meta := registry.ByID(705)
	g := meta.Group
	crv := g.Group("Crv")
	pt := crv.Group("Pt")
	top := func(nPt, nCrv uint16) []uint16 {
		return instance(t, g, map[string][]uint16{"ID": v(705), "L": v(0), "NPt": v(nPt), "NCrv": v(nCrv), "V_SF": v(0), "DeptRef_SF": v(0)})
	}
	curve := instance(t, crv, map[string][]uint16{"ActPt": v(1)})
	p := instance(t, pt, map[string][]uint16{"V": v(7), "Var": v(8)})

	tests := []struct {
		name     string
		regs     []uint16
		groups   []string
		warnings []string
	}{
		{
			name:   "no curves announced, none present",
			regs:   join(top(4, 0)),
			groups: []string{"DERVoltVar[0] 13 points 0 groups"},
		},
		{
			name:     "no curves announced, registers present",
			regs:     join(top(4, 0), curve, p),
			groups:   []string{"DERVoltVar[0] 13 points 0 groups"},
			warnings: []string{"12 registers left over after the last group"},
		},
		{
			name:   "curves without points",
			regs:   join(top(0, 2), curve, curve),
			groups: []string{"DERVoltVar[0] 13 points 2 groups", "Crv[1] 9 points 0 groups", "Crv[2] 9 points 0 groups"},
		},
		{
			name:     "curve count not implemented",
			regs:     join(top(1, 0xFFFF), curve, p),
			groups:   []string{"DERVoltVar[0] 13 points 0 groups"},
			warnings: []string{"DERVoltVar/Crv: count point NCrv is missing or not implemented, no instances decoded", "12 registers left over after the last group"},
		},
		{
			name:   "point count not implemented",
			regs:   join(top(0xFFFF, 2), curve, curve),
			groups: []string{"DERVoltVar[0] 13 points 2 groups", "Crv[1] 9 points 0 groups", "Crv[2] 9 points 0 groups"},
			warnings: []string{
				"DERVoltVar/Crv[1]/Pt: count point NPt is missing or not implemented, no instances decoded",
				"DERVoltVar/Crv[2]/Pt: count point NPt is missing or not implemented, no instances decoded",
			},
		},
		{
			name:     "more curves announced than present",
			regs:     join(top(1, 3), curve, p, curve, p),
			groups:   []string{"DERVoltVar[0] 13 points 2 groups", "Crv[1] 9 points 1 groups", "Pt[1] 2 points 0 groups", "Crv[2] 9 points 1 groups", "Pt[1] 2 points 0 groups"},
			warnings: []string{"DERVoltVar/Crv: registers end before instance 3 of 3"},
		},
		{
			name:   "registers end inside a curve's points",
			regs:   join(top(3, 2), curve, p, p, p, curve, p, v(1)),
			groups: []string{"DERVoltVar[0] 13 points 2 groups", "Crv[1] 9 points 3 groups", "Pt[1] 2 points 0 groups", "Pt[2] 2 points 0 groups", "Pt[3] 2 points 0 groups", "Crv[2] 9 points 1 groups", "Pt[1] 2 points 0 groups"},
			warnings: []string{
				"DERVoltVar/Crv[2]/Pt: registers end before instance 2 of 3",
				"1 registers left over after the last group",
			},
		},
		{
			name:     "registers end inside a curve header",
			regs:     join(top(1, 2), curve, p, curve[:4]),
			groups:   []string{"DERVoltVar[0] 13 points 1 groups", "Crv[1] 9 points 1 groups", "Pt[1] 2 points 0 groups"},
			warnings: []string{"DERVoltVar/Crv: registers end before instance 2 of 2", "4 registers left over after the last group"},
		},
		{
			name:     "fewer curves announced than present",
			regs:     join(top(1, 1), curve, p, curve, p),
			groups:   []string{"DERVoltVar[0] 13 points 1 groups", "Crv[1] 9 points 1 groups", "Pt[1] 2 points 0 groups"},
			warnings: []string{"12 registers left over after the last group"},
		},
		{
			name:     "absurd count",
			regs:     join(top(1, 65534), curve, p),
			groups:   []string{"DERVoltVar[0] 13 points 1 groups", "Crv[1] 9 points 1 groups", "Pt[1] 2 points 0 groups"},
			warnings: []string{"DERVoltVar/Crv: registers end before instance 2 of 65534"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dm, err := DecodeModel(tc.regs, meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantLines(t, "groups", groupPaths(dm), tc.groups)
			wantLines(t, "warnings", dm.Warnings, tc.warnings)
		})
	}
}

// Model 707 (DER Trip LV) nests three levels: Crv -> MustTrip / MayTrip /
// MomCess -> Pt.
func TestDecodeModel707ThreeLevels(t *testing.T) {
	meta := registry.ByID(707)
	if meta == nil {
		t.Fatal("model 707 missing")
	}
	g := meta.Group
	crv := g.Group("Crv")
	// trip returns one MustTrip/MayTrip/MomCess instance with two points.
	trip := func(name string, base uint16) []uint16 {
		tg := crv.Group(name)
		pt := tg.Group("Pt")
		return concat(
			instance(t, tg, map[string][]uint16{"ActPt": v(2)}),
			instance(t, pt, map[string][]uint16{"V": v(base), "Tms": v(0, base+1)}),
			instance(t, pt, map[string][]uint16{"V": v(base + 10), "Tms": v(1, base+11)}),
		)
	}
	regs := join(
		instance(t, g, map[string][]uint16{"ID": v(707), "L": v(0), "Ena": v(1), "NPt": v(2), "NCrvSet": v(2), "V_SF": v(0xFFFE), "Tms_SF": v(0xFFFE)}),
		instance(t, crv, map[string][]uint16{"ReadOnly": v(1)}),
		trip("MustTrip", 100), trip("MayTrip", 200), trip("MomCess", 300),
		instance(t, crv, map[string][]uint16{"ReadOnly": v(0)}),
		trip("MustTrip", 400), trip("MayTrip", 500), trip("MomCess", 600),
	)
	if len(regs) != 53 {
		t.Fatalf("test setup: %d registers, want 53", len(regs))
	}

	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", dm.Warnings)
	}

	var want []string
	want = append(want,
		"DERTripLV.ID=707 @0", "DERTripLV.L=51 @1", "DERTripLV.Ena=1 @2",
		"DERTripLV.NPt=2 @5", "DERTripLV.NCrvSet=2 @6", "DERTripLV.V_SF=-2 @7", "DERTripLV.Tms_SF=-2 @8",
	)
	off := 9
	for c, readOnly := range []int{1, 0} {
		cp := fmt.Sprintf("DERTripLV/Crv[%d]", c+1)
		want = append(want, fmt.Sprintf("%s.ReadOnly=%d @%d", cp, readOnly, off))
		off++
		for k, name := range []string{"MustTrip", "MayTrip", "MomCess"} {
			base := (c*3 + k + 1) * 100
			tp := fmt.Sprintf("%s/%s[1]", cp, name)
			want = append(want,
				fmt.Sprintf("%s.ActPt=2 @%d", tp, off),
				fmt.Sprintf("%s/Pt[1].V=%d x10^-2=%d.0000 @%d", tp, base, base/100, off+1),
				fmt.Sprintf("%s/Pt[1].Tms=%d x10^-2=%d.%02d00 @%d", tp, base+1, base/100, 1, off+2),
				fmt.Sprintf("%s/Pt[2].V=%d x10^-2=%d.1000 @%d", tp, base+10, base/100, off+4),
				// 0x0001_0000 + base + 11
				fmt.Sprintf("%s/Pt[2].Tms=%d x10^-2=%d.%02d00 @%d", tp, 65536+base+11, (65536+base)/100, (65536+base+11)%100, off+5),
			)
			off += 7
		}
	}
	wantLines(t, "implemented points", implemented(dm), want)
	if off != len(regs) {
		t.Fatalf("test setup: expectations cover %d registers of %d", off, len(regs))
	}

	// Structure: each curve holds exactly the three trip groups, in schema
	// order, each with its own two points.
	curves := dm.Group.GroupsNamed("Crv")
	if len(curves) != 2 || len(dm.Group.Groups) != 2 {
		t.Fatalf("%d curves, want 2", len(curves))
	}
	for i, c := range curves {
		if c.Index != i+1 || len(c.Groups) != 3 {
			t.Fatalf("curve %d: index %d, %d nested groups", i+1, c.Index, len(c.Groups))
		}
		for k, name := range []string{"MustTrip", "MayTrip", "MomCess"} {
			tg := c.Groups[k]
			if tg.Name != name || tg.Index != 1 || len(tg.GroupsNamed("Pt")) != 2 || len(tg.Groups) != 2 {
				t.Errorf("curve %d group %d = %s[%d] with %d nested, want %s[1] with 2 Pt", i+1, k, tg.Name, tg.Index, len(tg.Groups), name)
			}
		}
	}
	// The last register belongs to the last point.
	lastPt := curves[1].Groups[2].Groups[1]
	if tms := lastPt.Point("Tms"); tms == nil || tms.RegisterOffset != 51 || tms.RegisterCount != 2 || tms.RawValue != uint32(65536+611) {
		t.Errorf("Crv[2]/MomCess/Pt[2].Tms = %+v", tms)
	}
}

// Model 160 (MPPT) is a legacy model: its "module" group has count 0 and
// repeats for as long as registers remain. The N point is informational.
func TestDecodeModel160FillAndLeftover(t *testing.T) {
	meta := registry.ByID(160)
	g := meta.Group
	module := g.Group("module")
	mod := func(id, dca uint16) []uint16 {
		return instance(t, module, map[string][]uint16{"ID": v(id), "DCA": v(dca), "DCWH": v(0, 500+id)})
	}
	top := instance(t, g, map[string][]uint16{"ID": v(160), "L": v(0), "DCA_SF": v(0xFFFF), "DCWH_SF": v(3), "N": v(7)})

	tests := []struct {
		name     string
		regs     []uint16
		modules  int
		warnings []string
	}{
		{"no modules", join(top), 0, nil},
		{"one module", join(top, mod(1, 10)), 1, nil},
		{"three modules, whatever N says", join(top, mod(1, 10), mod(2, 20), mod(3, 30)), 3, nil},
		{"partial trailing module", join(top, mod(1, 10), mod(2, 20), mod(3, 30)[:5]), 2, []string{"5 registers left over after the last group"}},
		{"less than one module", join(top, mod(1, 10)[:19]), 0, []string{"19 registers left over after the last group"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dm, err := DecodeModel(tc.regs, meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantLines(t, "warnings", dm.Warnings, tc.warnings)
			mods := dm.Group.GroupsNamed("module")
			if len(mods) != tc.modules || len(dm.Group.Groups) != tc.modules {
				t.Fatalf("%d module instances, want %d", len(mods), tc.modules)
			}
			for i, m := range mods {
				n := uint16(i + 1)
				base := 10 + 20*i
				if m.Index != i+1 || len(m.Points) != len(module.Points) || len(m.Groups) != 0 {
					t.Errorf("module %d = index %d, %d points", i+1, m.Index, len(m.Points))
				}
				if id := m.Point("ID"); id == nil || id.RawValue != n || id.RegisterOffset != base {
					t.Errorf("module %d ID = %+v", i+1, id)
				}
				wantScaled(t, *m.Point("DCA"), -1, float64(n))
				wantScaled(t, *m.Point("DCWH"), 3, float64(500+n)*1000)
				if dcwh := m.Point("DCWH"); dcwh.RegisterOffset != base+12 || dcwh.RegisterCount != 2 {
					t.Errorf("module %d DCWH at %d+%d", i+1, dcwh.RegisterOffset, dcwh.RegisterCount)
				}
				// DCV's scale factor is not implemented at the top.
				wantUnscaled(t, *m.Point("DCV"))
			}
			if n := dm.Point("N"); n == nil || n.RawValue != uint16(7) {
				t.Errorf("N = %+v", n)
			}
			// The top-level ID comes before any module ID.
			if id := dm.Point("ID"); id == nil || id.RawValue != uint16(160) {
				t.Errorf("Point(ID) = %+v, want the model's own", id)
			}
		})
	}
}

// Model 803 (lithium-ion bank): strings counted by NStr.
func TestDecodeModel803CountPoint(t *testing.T) {
	meta := registry.ByID(803)
	g := meta.Group
	str := g.Group("string")
	one := func(soc uint16) []uint16 {
		return instance(t, str, map[string][]uint16{"StrNMod": v(4), "StrSoC": v(soc), "Pad1": v(0x8000), "Pad2": v(0x8000)})
	}
	regs := join(
		instance(t, g, map[string][]uint16{"ID": v(803), "L": v(0), "NStr": v(2), "SoC_SF": v(0xFFFF)}),
		one(805), one(790), one(999), // a third string NStr does not announce
	)
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	strs := dm.Group.GroupsNamed("string")
	if len(strs) != 2 {
		t.Fatalf("%d string instances, want the 2 that NStr announces", len(strs))
	}
	wantScaled(t, *strs[0].Point("StrSoC"), -1, 80.5)
	wantScaled(t, *strs[1].Point("StrSoC"), -1, 79)
	if off := strs[1].Point("StrSoC").RegisterOffset; off != 28+32+4 {
		t.Errorf("string 2 StrSoC at %d, want %d", off, 28+32+4)
	}
	wantLines(t, "warnings", dm.Warnings, []string{"32 registers left over after the last group"})
}

// countModel returns a hand-made model whose "item" group (two registers) is
// counted by the point N of the given type.
func countModel(countType string, countSize int) *registry.ModelMeta {
	return &registry.ModelMeta{ID: 300, Group: root("m",
		[]registry.PointMeta{u16("ID"), u16("L"), {Name: "N", Type: countType, Size: countSize}},
		nested("item", 0, "N", []registry.PointMeta{u16("A"), u16("B")}))}
}

func TestCountPointValues(t *testing.T) {
	items := []uint16{1, 2, 3, 4, 5, 6}
	tests := []struct {
		name      string
		typ       string
		count     []uint16
		want      int // item instances decoded
		warnCount bool
		leftover  int
	}{
		{"uint16", "uint16", v(3), 3, false, 0},
		{"count", "count", v(2), 2, false, 2},
		{"uint16 zero", "uint16", v(0), 0, false, 6},
		{"uint16 not implemented", "uint16", v(0xFFFF), 0, true, 6},
		{"int16", "int16", v(3), 3, false, 0},
		{"int16 negative", "int16", v(0xFFFD), 0, true, 6},
		{"int16 not implemented", "int16", v(0x8000), 0, true, 6},
		{"uint32", "uint32", v(0, 1), 1, false, 4},
		{"uint32 not implemented", "uint32", v(0xFFFF, 0xFFFF), 0, true, 6},
		// An accumulator of 0 is "not implemented", not "none".
		{"acc16 zero", "acc16", v(0), 0, true, 6},
		{"acc16", "acc16", v(1), 1, false, 4},
		{"enum16", "enum16", v(2), 2, false, 2},
		{"string is not a count", "string", v(0x3300), 0, true, 6},
		{"int32 is not a count", "int32", v(0, 2), 0, true, 6},
		{"float32 is not a count", "float32", v(0x4000, 0), 0, true, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := countModel(tc.typ, len(tc.count))
			regs := join(v(300, 0), tc.count, items)
			dm, err := DecodeModel(regs, meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := dm.Group.GroupsNamed("item")
			if len(got) != tc.want {
				t.Errorf("%d item instances, want %d", len(got), tc.want)
			}
			for i, it := range got {
				if it.Index != i+1 || it.Point("A").RawValue != items[2*i] || it.Point("B").RawValue != items[2*i+1] ||
					it.Point("B").RegisterOffset != 2+len(tc.count)+2*i+1 {
					t.Errorf("item %d = %+v", i+1, it)
				}
			}
			var want []string
			if tc.warnCount {
				want = append(want, "m/item: count point N is missing or not implemented, no instances decoded")
			}
			if tc.leftover > 0 {
				want = append(want, fmt.Sprintf("%d registers left over after the last group", tc.leftover))
			}
			wantLines(t, "warnings", dm.Warnings, want)
		})
	}
}

func TestCountPointLookup(t *testing.T) {
	item := func(countPoint string) *registry.GroupMeta {
		return nested("item", 0, countPoint, []registry.PointMeta{u16("A")})
	}

	t.Run("count point does not exist", func(t *testing.T) {
		meta := &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("N")}, item("Nope"))}
		dm, err := DecodeModel(v(2, 7, 8), meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines(t, "groups", groupPaths(dm), []string{"m[0] 1 points 0 groups"})
		wantLines(t, "warnings", dm.Warnings, []string{
			"m/item: count point Nope is missing or not implemented, no instances decoded",
			"2 registers left over after the last group",
		})
	})

	t.Run("count point two levels up", func(t *testing.T) {
		meta := &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("N")},
			nested("mid", 2, "", []registry.PointMeta{u16("M")}, item("N")))}
		dm, err := DecodeModel(v(2, 10, 11, 12, 20, 21, 22), meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines(t, "decoded", flatten(dm), []string{
			"m.N=2 @0",
			"m/mid[1].M=10 @1", "m/mid[1]/item[1].A=11 @2", "m/mid[1]/item[2].A=12 @3",
			"m/mid[2].M=20 @4", "m/mid[2]/item[1].A=21 @5", "m/mid[2]/item[2].A=22 @6",
		})
		wantLines(t, "warnings", dm.Warnings, nil)
	})

	t.Run("nearest count point wins, per instance", func(t *testing.T) {
		// Both the top-level group and each "mid" instance have a point N.
		meta := &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("N")},
			nested("mid", 0, "N", []registry.PointMeta{u16("N")}, item("N")))}
		dm, err := DecodeModel(v(3, 1, 11, 0, 2, 31, 32), meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines(t, "decoded", flatten(dm), []string{
			"m.N=3 @0",
			"m/mid[1].N=1 @1", "m/mid[1]/item[1].A=11 @2",
			"m/mid[2].N=0 @3",
			"m/mid[3].N=2 @4", "m/mid[3]/item[1].A=31 @5", "m/mid[3]/item[2].A=32 @6",
		})
		wantLines(t, "warnings", dm.Warnings, nil)
	})

	t.Run("a count point of a sibling group is out of scope", func(t *testing.T) {
		meta := &registry.ModelMeta{ID: 1, Group: root("m", nil,
			nested("head", 1, "", []registry.PointMeta{u16("N")}), item("N"))}
		dm, err := DecodeModel(v(1, 9), meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines(t, "decoded", flatten(dm), []string{"m", "m/head[1].N=1 @0"})
		wantLines(t, "warnings", dm.Warnings, []string{
			"m/item: count point N is missing or not implemented, no instances decoded",
			"1 registers left over after the last group",
		})
	})

	t.Run("count point wins over a fixed count", func(t *testing.T) {
		g := item("N")
		g.Count = 5
		meta := &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("N")}, g)}
		dm, err := DecodeModel(v(1, 7), meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines(t, "decoded", flatten(dm), []string{"m.N=1 @0", "m/item[1].A=7 @1"})
		wantLines(t, "warnings", dm.Warnings, nil)
	})
}

func TestFixedCounts(t *testing.T) {
	model := func(count int) *registry.ModelMeta {
		return &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("H")},
			nested("pair", count, "", []registry.PointMeta{u16("A"), u16("B")}),
			nested("tail", 1, "", []registry.PointMeta{u16("T")}))}
	}
	regs := v(0, 1, 2, 3, 4, 9)

	dm, err := DecodeModel(regs, model(2), 0)
	if err != nil {
		t.Fatal(err)
	}
	// All instances of the first nested group come before the second group.
	wantLines(t, "decoded", flatten(dm), []string{
		"m.H=0 @0",
		"m/pair[1].A=1 @1", "m/pair[1].B=2 @2",
		"m/pair[2].A=3 @3", "m/pair[2].B=4 @4",
		"m/tail[1].T=9 @5",
	})
	wantLines(t, "warnings", dm.Warnings, nil)
	if got := []string{dm.Group.Groups[0].Name, dm.Group.Groups[1].Name, dm.Group.Groups[2].Name}; !reflect.DeepEqual(got, []string{"pair", "pair", "tail"}) {
		t.Errorf("nested order = %v", got)
	}

	// Fewer instances than the schema fixes: the following group takes what
	// is there, and the shortfall is reported.
	dm, err = DecodeModel(regs, model(3), 0)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "groups", groupPaths(dm), []string{
		"m[0] 1 points 3 groups", "pair[1] 2 points 0 groups", "pair[2] 2 points 0 groups", "tail[1] 1 points 0 groups",
	})
	wantLines(t, "warnings", dm.Warnings, []string{"m/pair: registers end before instance 3 of 3"})

	// A negative count decodes nothing and must not loop.
	dm, err = DecodeModel(regs, model(-1), 0)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "groups", groupPaths(dm), []string{"m[0] 1 points 1 groups", "tail[1] 1 points 0 groups"})
	wantLines(t, "warnings", dm.Warnings, []string{"4 registers left over after the last group"})
}

// Groups that occupy no registers must not make the decoder loop.
func TestZeroLengthGroupsTerminate(t *testing.T) {
	empty := func(count int, countPoint string) *registry.ModelMeta {
		return &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("N")}, nested("void", count, countPoint, nil))}
	}
	tests := []struct {
		name     string
		meta     *registry.ModelMeta
		regs     []uint16
		groups   []string
		warnings []string
	}{
		{
			name:   "exactly once",
			meta:   empty(1, ""),
			regs:   v(5),
			groups: []string{"m[0] 1 points 1 groups", "void[1] 0 points 0 groups"},
		},
		{
			name:     "fixed count",
			meta:     empty(1000000, ""),
			regs:     v(5),
			groups:   []string{"m[0] 1 points 1 groups", "void[1] 0 points 0 groups"},
			warnings: []string{"m/void: group occupies no registers, decoded once"},
		},
		{
			name:     "count point",
			meta:     empty(0, "N"),
			regs:     v(65000),
			groups:   []string{"m[0] 1 points 1 groups", "void[1] 0 points 0 groups"},
			warnings: []string{"m/void: group occupies no registers, decoded once"},
		},
		{
			name:   "count point of one",
			meta:   empty(0, "N"),
			regs:   v(1),
			groups: []string{"m[0] 1 points 1 groups", "void[1] 0 points 0 groups"},
		},
		{
			name:     "fill with registers remaining",
			meta:     empty(0, ""),
			regs:     v(5, 6, 7),
			groups:   []string{"m[0] 1 points 1 groups", "void[1] 0 points 0 groups"},
			warnings: []string{"m/void: group occupies no registers, decoded once", "2 registers left over after the last group"},
		},
		{
			name:   "fill with no registers remaining",
			meta:   empty(0, ""),
			regs:   v(5),
			groups: []string{"m[0] 1 points 0 groups"},
		},
		{
			// The wrapper has no points of its own but its nested group
			// consumes registers, so filling makes progress and ends.
			name: "empty wrapper around a consuming group",
			meta: &registry.ModelMeta{ID: 1, Group: root("m", nil,
				nested("wrap", 0, "", nil, nested("leaf", 2, "", []registry.PointMeta{u16("A")})))},
			regs: v(1, 2, 3, 4, 5),
			groups: []string{
				"m[0] 0 points 3 groups",
				"wrap[1] 0 points 2 groups", "leaf[1] 1 points 0 groups", "leaf[2] 1 points 0 groups",
				"wrap[2] 0 points 2 groups", "leaf[1] 1 points 0 groups", "leaf[2] 1 points 0 groups",
				"wrap[3] 0 points 1 groups", "leaf[1] 1 points 0 groups",
			},
			warnings: []string{"m/wrap[3]/leaf: registers end before instance 2 of 2"},
		},
		{
			name: "negative length",
			meta: &registry.ModelMeta{ID: 1, Group: root("m", []registry.PointMeta{u16("N")},
				&registry.GroupMeta{Name: "bad", Count: 2, Length: -1},
				&registry.GroupMeta{Name: "worse", Count: 0, Length: -5})},
			regs:   v(5, 6),
			groups: []string{"m[0] 1 points 0 groups"},
			warnings: []string{
				"m/bad: registers end before instance 1 of 2",
				"1 registers left over after the last group",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dm, err := DecodeModel(tc.regs, tc.meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			wantLines(t, "groups", groupPaths(dm), tc.groups)
			wantLines(t, "warnings", dm.Warnings, tc.warnings)
		})
	}
}

func TestDecodedGroupHelpers(t *testing.T) {
	leafA := &DecodedGroup{Name: "Pt", Index: 1, Points: []DecodedPoint{{Name: "V", RawValue: uint16(1)}, {Name: "Deep", RawValue: uint16(9)}}}
	leafB := &DecodedGroup{Name: "Pt", Index: 2, Points: []DecodedPoint{{Name: "V", RawValue: uint16(2)}}}
	crv1 := &DecodedGroup{Name: "Crv", Index: 1, Points: []DecodedPoint{{Name: "ActPt", RawValue: uint16(2)}}, Groups: []*DecodedGroup{leafA, leafB}}
	other := &DecodedGroup{Name: "Other", Index: 1, Points: []DecodedPoint{{Name: "V", RawValue: uint16(3)}, {Name: "Only", RawValue: uint16(4)}}}
	crv2 := &DecodedGroup{Name: "Crv", Index: 2, Points: []DecodedPoint{{Name: "ActPt", RawValue: uint16(0)}, {Name: "Late", RawValue: uint16(5)}}}
	top := &DecodedGroup{Name: "top", Points: []DecodedPoint{{Name: "ID", RawValue: uint16(7)}, {Name: "ID", RawValue: uint16(8)}}, Groups: []*DecodedGroup{crv1, other, crv2}}

	// Point: own points only, first match, pointer into the slice.
	if p := top.Point("ID"); p != &top.Points[0] {
		t.Errorf("Point(ID) = %+v, want a pointer to the first own point", p)
	}
	if p := top.Point("V"); p != nil {
		t.Errorf("Point(V) = %+v, want nil: V is only in nested groups", p)
	}
	if p := top.Point("id"); p != nil {
		t.Errorf("Point(id) = %+v, want nil: names are case-sensitive", p)
	}
	if p := top.Point("Crv"); p != nil {
		t.Errorf("Point(Crv) = %+v, want nil: Crv is a group", p)
	}

	// FindPoint: own points first, then depth first in order.
	finds := map[string]*DecodedPoint{
		"ID":    &top.Points[0],
		"ActPt": &crv1.Points[0],
		"V":     &leafA.Points[0], // deep in the first group, before Other.V
		"Deep":  &leafA.Points[1], // two levels down
		"Only":  &other.Points[1], // second nested group
		"Late":  &crv2.Points[1],  // last nested group
		"Nope":  nil,
	}
	for name, want := range finds {
		if got := top.FindPoint(name); got != want {
			t.Errorf("FindPoint(%s) = %+v, want %+v", name, got, want)
		}
	}
	if got := crv2.FindPoint("V"); got != nil {
		t.Errorf("FindPoint searches only below the receiver, got %+v", got)
	}

	// GroupsNamed: direct children only, in order.
	if got := top.GroupsNamed("Crv"); !reflect.DeepEqual(got, []*DecodedGroup{crv1, crv2}) {
		t.Errorf("GroupsNamed(Crv) = %v", got)
	}
	if got := top.GroupsNamed("Pt"); got != nil {
		t.Errorf("GroupsNamed(Pt) = %v, want nil: Pt is one level deeper", got)
	}
	if got := crv1.GroupsNamed("Pt"); !reflect.DeepEqual(got, []*DecodedGroup{leafA, leafB}) {
		t.Errorf("Crv[1].GroupsNamed(Pt) = %v", got)
	}
	if got := top.GroupsNamed("top"); got != nil {
		t.Errorf("GroupsNamed(top) = %v, want nil", got)
	}

	// Walk: the group itself, then depth first.
	var order []*DecodedGroup
	top.Walk(func(g *DecodedGroup) { order = append(order, g) })
	if want := []*DecodedGroup{top, crv1, leafA, leafB, other, crv2}; !reflect.DeepEqual(order, want) {
		t.Errorf("Walk visited %d groups in the wrong order", len(order))
	}
	order = nil
	leafB.Walk(func(g *DecodedGroup) { order = append(order, g) })
	if len(order) != 1 || order[0] != leafB {
		t.Errorf("Walk on a leaf visited %v", order)
	}

	// DecodedModel.Point is FindPoint on the top-level group.
	dm := &DecodedModel{Group: top}
	if got := dm.Point("Deep"); got != &leafA.Points[1] {
		t.Errorf("DecodedModel.Point(Deep) = %+v", got)
	}
	if got := dm.Point("Nope"); got != nil {
		t.Errorf("DecodedModel.Point(Nope) = %+v", got)
	}
}

func TestDecodedGroupHelpersNilReceivers(t *testing.T) {
	var g *DecodedGroup
	if g.Point("x") != nil || g.FindPoint("x") != nil || g.GroupsNamed("x") != nil {
		t.Error("nil DecodedGroup: lookups must return nil")
	}
	calls := 0
	g.Walk(func(*DecodedGroup) { calls++ })
	if calls != 0 {
		t.Errorf("Walk on a nil group called fn %d times", calls)
	}

	var dm *DecodedModel
	if dm.Point("x") != nil {
		t.Error("nil DecodedModel: Point must return nil")
	}
	if (&DecodedModel{}).Point("x") != nil {
		t.Error("undecoded DecodedModel: Point must return nil")
	}

	var sc *scope
	if sc.lookup("x") != nil {
		t.Error("nil scope: lookup must return nil")
	}
	if n, ok := countValue(nil); ok || n != 0 {
		t.Errorf("countValue(nil) = %d, %v", n, ok)
	}
}

// oneOfEach returns the number of registers of a model that holds k instances
// of every group that repeats (and the fixed number of every other).
func oneOfEach(g *registry.GroupMeta, k int) int {
	n := g.Length
	for _, c := range g.Groups {
		count := c.Count
		if c.CountPoint != "" || c.Count == 0 {
			count = k
		}
		n += count * oneOfEach(c, k)
	}
	return n
}

// expectInstances mirrors oneOfEach for the number of group instances.
func expectInstances(g *registry.GroupMeta, k int) int {
	n := 1
	for _, c := range g.Groups {
		count := c.Count
		if c.CountPoint != "" || c.Count == 0 {
			count = k
		}
		n += count * expectInstances(c, k)
	}
	return n
}

// TestEveryRegistryModelDecodesCompletely decodes, for every model in the
// registry, a register image that holds k instances of every repeating group.
// Every register holds the value k, so every count point announces exactly k
// instances. The decoder must consume all registers, raise no warning, lay
// the points out back to back, and resolve every scale factor.
func TestEveryRegistryModelDecodesCompletely(t *testing.T) {
	ids := make([]int, 0, registry.Count())
	for id, m := range registry.All() {
		if m.Group != nil && id != labelLessModelID {
			ids = append(ids, int(id))
		}
	}
	sort.Ints(ids)
	if len(ids) < 100 {
		t.Fatalf("only %d models in the registry", len(ids))
	}

	for _, k := range []int{1, 3} {
		for _, id := range ids {
			meta := registry.ByID(uint16(id))
			regs := make([]uint16, oneOfEach(meta.Group, k))
			for i := range regs {
				regs[i] = uint16(k)
			}
			dm, err := DecodeModel(regs, meta, 0)
			if err != nil {
				t.Errorf("model %d, k=%d: %v", id, k, err)
				continue
			}
			if len(dm.Warnings) != 0 {
				t.Errorf("model %d, k=%d: warnings %q", id, k, dm.Warnings)
			}

			next, instances := 0, 0
			dm.Group.Walk(func(g *DecodedGroup) {
				instances++
				for _, p := range g.Points {
					if p.RegisterOffset != next {
						t.Errorf("model %d, k=%d: %s[%d].%s at offset %d, want %d", id, k, g.Name, g.Index, p.Name, p.RegisterOffset, next)
					}
					next = p.RegisterOffset + p.RegisterCount
					if !p.Implemented {
						t.Errorf("model %d, k=%d: %s[%d].%s not implemented for register value %d", id, k, g.Name, g.Index, p.Name, k)
					}
					if p.SFName == "" {
						continue
					}
					// Every scale factor point holds k, every literal is
					// what the schema says.
					if p.SFRawValue == nil || p.ScaledValue == nil {
						t.Errorf("model %d, k=%d: %s[%d].%s: scale factor %s not resolved", id, k, g.Name, g.Index, p.Name, p.SFName)
					}
				}
			})
			if next != len(regs) {
				t.Errorf("model %d, k=%d: points cover %d registers of %d", id, k, next, len(regs))
			}
			if want := expectInstances(meta.Group, k); instances != want {
				t.Errorf("model %d, k=%d: %d group instances, want %d", id, k, instances, want)
			}
		}
	}
}
