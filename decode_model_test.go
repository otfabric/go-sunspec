// SPDX-License-Identifier: MIT

package sunspec

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// pointByName returns the named own point of a group, failing the test when it is
// missing.
func pointByName(t *testing.T, b *DecodedGroup, name string) DecodedPoint {
	t.Helper()
	if b == nil {
		t.Fatalf("group is nil while looking for point %q", name)
	}
	for _, p := range b.Points {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("point %q not found", name)
	return DecodedPoint{}
}

// wantScaled asserts the resolved scale factor and scaled value of a point.
func wantScaled(t *testing.T, p DecodedPoint, sf int16, scaled float64) {
	t.Helper()
	if p.SFRawValue == nil {
		t.Fatalf("%s: SFRawValue is nil, want %d", p.Name, sf)
	}
	if *p.SFRawValue != sf {
		t.Errorf("%s: SFRawValue = %d, want %d", p.Name, *p.SFRawValue, sf)
	}
	if p.ScaledValue == nil {
		t.Fatalf("%s: ScaledValue is nil, want %g", p.Name, scaled)
	}
	if math.Abs(*p.ScaledValue-scaled) > 1e-9*math.Max(1, math.Abs(scaled)) {
		t.Errorf("%s: ScaledValue = %g, want %g", p.Name, *p.ScaledValue, scaled)
	}
}

// wantUnscaled asserts that a point carries neither a scale factor nor a
// scaled value.
func wantUnscaled(t *testing.T, p DecodedPoint) {
	t.Helper()
	if p.SFRawValue != nil {
		t.Errorf("%s: SFRawValue = %d, want nil", p.Name, *p.SFRawValue)
	}
	if p.ScaledValue != nil {
		t.Errorf("%s: ScaledValue = %g, want nil", p.Name, *p.ScaledValue)
	}
}

// root returns a top-level group (exactly once) with consecutive offsets
// assigned to its points and its length computed from them.
func root(name string, points []registry.PointMeta, groups ...*registry.GroupMeta) *registry.GroupMeta {
	g := nested(name, 1, "", points, groups...)
	return g
}

// nested returns a group with the given count or count point, with
// consecutive offsets assigned to its points and its length computed.
func nested(name string, count int, countPoint string, points []registry.PointMeta, groups ...*registry.GroupMeta) *registry.GroupMeta {
	g := &registry.GroupMeta{Name: name, Type: "group", Count: count, CountPoint: countPoint, Groups: groups}
	for _, p := range points {
		if p.Size == 0 {
			p.Size = 1
		}
		p.Offset = g.Length
		g.Length += p.Size
		g.Points = append(g.Points, p)
	}
	return g
}

// u16 is shorthand for a one-register uint16 point.
func u16(name string) registry.PointMeta {
	return registry.PointMeta{Name: name, Type: "uint16", Size: 1}
}

// flatten renders a decoded model as one line per point, in register order:
// "<group path>.<point>=<raw>[ x10^<sf>=<scaled>] @<offset>". Points that are
// not implemented are rendered with "=n/a". A group instance without points
// is rendered as its path alone.
func flatten(dm *DecodedModel) []string {
	var out []string
	var walk func(g *DecodedGroup, path string)
	walk = func(g *DecodedGroup, path string) {
		if len(g.Points) == 0 {
			out = append(out, path)
		}
		for _, p := range g.Points {
			line := fmt.Sprintf("%s.%s=%v", path, p.Name, p.RawValue)
			if !p.Implemented {
				line = fmt.Sprintf("%s.%s=n/a", path, p.Name)
			}
			if p.SFRawValue != nil && p.ScaledValue != nil {
				line += fmt.Sprintf(" x10^%d=%s", *p.SFRawValue, strconv.FormatFloat(*p.ScaledValue, 'f', 4, 64))
			}
			out = append(out, fmt.Sprintf("%s @%d", line, p.RegisterOffset))
		}
		for _, c := range g.Groups {
			walk(c, fmt.Sprintf("%s/%s[%d]", path, c.Name, c.Index))
		}
	}
	if dm != nil && dm.Group != nil {
		walk(dm.Group, dm.Group.Name)
	}
	return out
}

// wantLines compares two string lists and reports the first difference.
func wantLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			t.Errorf("%s: line %d = %q, want %q", what, i+1, got[i], want[i])
			return
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: %d lines, want %d\n got: %q\nwant: %q", what, len(got), len(want), got, want)
	}
}

func TestDecodeModelNilSchema(t *testing.T) {
	dm, err := DecodeModel([]uint16{1, 2, 3}, nil, 40002)
	if dm != nil {
		t.Errorf("model = %+v, want nil", dm)
	}
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("err = %v, want ErrDecode", err)
	}
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("err = %T, want *DecodeError", err)
	}

	// A schema without a group cannot be decoded either.
	dm, err = DecodeModel([]uint16{1, 2, 3}, &registry.ModelMeta{ID: 9, Name: "empty"}, 40002)
	if dm != nil {
		t.Errorf("model = %+v, want nil", dm)
	}
	if !errors.As(err, &de) || !errors.Is(err, ErrDecode) || de.ModelID != 9 {
		t.Fatalf("err = %v, want a *DecodeError for model 9", err)
	}
}

func TestDecodeModelTooShortForFixedBlock(t *testing.T) {
	meta := &registry.ModelMeta{
		ID:    77,
		Name:  "short",
		Group: root("short", []registry.PointMeta{u16("ID"), u16("L"), u16("V")}, nested("rep", 0, "", []registry.PointMeta{u16("A")})),
	}
	regs := []uint16{77, 1}
	dm, err := DecodeModel(regs, meta, 123)
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrDecode) {
		t.Fatalf("err = %v, want *DecodeError matching ErrDecode", err)
	}
	if de.ModelID != 77 || !strings.Contains(de.Message, "have 2, need 3") {
		t.Errorf("DecodeError = %+v", de)
	}
	if dm == nil {
		t.Fatal("model is nil; the raw registers must still be returned")
	}
	if dm.ModelID != 77 || dm.Name != "short" || dm.InstanceAddress != 123 || dm.Schema != meta {
		t.Errorf("model identity = %+v", dm)
	}
	if dm.Group != nil {
		t.Errorf("a too short model must not be partially decoded: %+v", dm.Group)
	}
	if dm.Point("ID") != nil {
		t.Error("Point on an undecoded model must be nil")
	}
	if !reflect.DeepEqual(dm.RawRegisters, regs) {
		t.Errorf("RawRegisters = %v, want %v", dm.RawRegisters, regs)
	}
	if len(dm.Warnings) != 1 || dm.Warnings[0] != de.Message {
		t.Errorf("Warnings = %q, want the decode error message", dm.Warnings)
	}
}

func TestDecodeModelNameAndRawRegisters(t *testing.T) {
	fixed := root("machine", []registry.PointMeta{u16("V")})

	regs := []uint16{9}
	dm, err := DecodeModel(regs, &registry.ModelMeta{ID: 5, Name: "machine", Label: "Human", Group: fixed}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dm.Name != "Human" {
		t.Errorf("Name = %q, want the label", dm.Name)
	}
	if dm.Group == nil || dm.Group.Name != "machine" || dm.Group.Index != 0 || dm.Group.Groups != nil {
		t.Errorf("top-level group = %+v", dm.Group)
	}
	// Documented: RawRegisters aliases the input slice.
	regs[0] = 10
	if dm.RawRegisters[0] != 10 {
		t.Error("RawRegisters is documented to alias regs")
	}
	if got := pointByName(t, dm.Group, "V").RawValue; got != uint16(9) {
		t.Errorf("decoded value changed with the input slice: %v", got)
	}

	dm, err = DecodeModel([]uint16{9}, &registry.ModelMeta{ID: 5, Name: "machine", Group: fixed}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dm.Name != "machine" {
		t.Errorf("Name = %q, want the schema name when the label is empty", dm.Name)
	}
}

func TestDecodeModelGroupWithoutPoints(t *testing.T) {
	meta := &registry.ModelMeta{ID: 9, Name: "empty", Group: root("empty", nil)}
	dm, err := DecodeModel(nil, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dm.Group == nil || len(dm.Group.Points) != 0 || len(dm.Group.Groups) != 0 || len(dm.Warnings) != 0 {
		t.Errorf("model without points decoded to %+v, warnings %q", dm.Group, dm.Warnings)
	}

	// Registers nothing in the schema accounts for are reported.
	dm, err = DecodeModel([]uint16{1, 2}, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "warnings", dm.Warnings, []string{"2 registers left over after the last group"})
}

func TestDecodeModelRepeatingOnlyAndLeftover(t *testing.T) {
	meta := &registry.ModelMeta{
		ID:    10,
		Name:  "rep",
		Group: root("rep", nil, nested("item", 0, "", []registry.PointMeta{u16("A"), u16("B")})),
	}
	dm, err := DecodeModel([]uint16{1, 2, 3, 4, 5}, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.Group.Points) != 0 {
		t.Errorf("top-level points = %+v, want none", dm.Group.Points)
	}
	wantLines(t, "decoded", flatten(dm), []string{
		"rep",
		"rep/item[1].A=1 @0", "rep/item[1].B=2 @1",
		"rep/item[2].A=3 @2", "rep/item[2].B=4 @3",
	})
	wantLines(t, "warnings", dm.Warnings, []string{"1 registers left over after the last group"})

	// No registers past the top-level points: no instances, no warning.
	dm, err = DecodeModel(nil, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.Group.Groups) != 0 || len(dm.Warnings) != 0 {
		t.Errorf("empty model decoded to %+v, warnings %q", dm.Group, dm.Warnings)
	}
}

func TestDecodeSkipsPointsOutsideTheirGroup(t *testing.T) {
	meta := &registry.ModelMeta{ID: 12, Group: &registry.GroupMeta{Name: "g", Count: 1, Length: 3, Points: []registry.PointMeta{
		{Name: "OK", Type: "uint16", Size: 1, Offset: 0},
		{Name: "PAST_END", Type: "uint32", Size: 2, Offset: 2},
		{Name: "NEG_OFFSET", Type: "uint16", Size: 1, Offset: -1},
		{Name: "NEG_SIZE", Type: "uint16", Size: -1, Offset: 2},
		{Name: "ODD", Type: "mystery", Size: 1, Offset: 1},
		{Name: "TOO_SMALL", Type: "uint32", Size: 1, Offset: 2},
		{Name: "LAST", Type: "int16", Size: 1, Offset: 2},
	}}}
	// The fourth register is beyond the group's length and must not make
	// PAST_END decodable.
	dm, err := DecodeModel([]uint16{11, 22, 0xFFFF, 44}, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	g := dm.Group

	var names []string
	for _, p := range g.Points {
		names = append(names, p.Name)
	}
	if want := []string{"OK", "ODD", "TOO_SMALL", "LAST"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("decoded points = %v, want %v", names, want)
	}
	if got := pointByName(t, g, "LAST").RawValue; got != int16(-1) {
		t.Errorf("LAST = %v, want -1", got)
	}
	if got := pointByName(t, g, "TOO_SMALL").RawValue; !reflect.DeepEqual(got, []uint16{0xFFFF}) {
		t.Errorf("TOO_SMALL = %#v, want its raw register", got)
	}

	wantLines(t, "warnings", dm.Warnings, []string{
		"g: point PAST_END: offset 2+size 2 exceeds the group length 3",
		"g: point NEG_OFFSET: invalid offset -1 or size 1 in schema",
		"g: point NEG_SIZE: invalid offset 2 or size -1 in schema",
		`g: point ODD: unsupported type "mystery", returning raw registers`,
		"g: point TOO_SMALL: type uint32 needs 2 registers, have 1, returning raw registers",
		"1 registers left over after the last group",
	})
}

func TestDecodeModelWarningsNameTheGroupInstance(t *testing.T) {
	meta := &registry.ModelMeta{
		ID: 11,
		Group: root("top", []registry.PointMeta{u16("V"), {Name: "W", Type: "weird"}},
			nested("outer", 2, "", []registry.PointMeta{u16("A")},
				nested("inner", 2, "", []registry.PointMeta{{Name: "R", Type: "weird"}}))),
	}
	dm, err := DecodeModel([]uint16{1, 2, 10, 11, 12, 20, 21, 22}, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, "warnings", dm.Warnings, []string{
		`top: point W: unsupported type "weird", returning raw registers`,
		`top/outer[1]/inner[1]: point R: unsupported type "weird", returning raw registers`,
		`top/outer[1]/inner[2]: point R: unsupported type "weird", returning raw registers`,
		`top/outer[2]/inner[1]: point R: unsupported type "weird", returning raw registers`,
		`top/outer[2]/inner[2]: point R: unsupported type "weird", returning raw registers`,
	})
	if n := len(dm.Group.GroupsNamed("outer")); n != 2 {
		t.Errorf("%d outer instances, want 2", n)
	}
}

func TestScaleFactorSources(t *testing.T) {
	meta := &registry.ModelMeta{
		ID:   200,
		Name: "sf",
		Group: &registry.GroupMeta{Name: "sf", Count: 1, Length: 13, Points: []registry.PointMeta{
			// Reference to a sunssf point that comes later in the block.
			{Name: "W", Type: "int16", Size: 1, Offset: 0, SF: "W_SF"},
			{Name: "W_SF", Type: "sunssf", Size: 1, Offset: 1},
			// Literal exponent.
			{Name: "LIT", Type: "uint16", Size: 1, Offset: 2, SF: "2", SFLiteral: 2, SFIsLiteral: true},
			// Reference to a sunssf point the device does not implement.
			{Name: "NOSF", Type: "uint16", Size: 1, Offset: 3, SF: "DEAD_SF"},
			{Name: "DEAD_SF", Type: "sunssf", Size: 1, Offset: 4},
			// Reference to a point that does not exist.
			{Name: "ORPHAN", Type: "uint16", Size: 1, Offset: 5, SF: "MISSING_SF"},
			// The point itself is not implemented.
			{Name: "UNIMPL", Type: "int16", Size: 1, Offset: 6, SF: "W_SF"},
			// No scale factor at all.
			{Name: "PLAIN", Type: "uint16", Size: 1, Offset: 7},
			// A non-numeric value cannot be scaled.
			{Name: "TEXT", Type: "string", Size: 1, Offset: 8, SF: "W_SF"},
			// Wider types.
			{Name: "WIDE", Type: "acc32", Size: 2, Offset: 9, SF: "W_SF"},
			{Name: "ZERO_SF", Type: "uint16", Size: 1, Offset: 11, SF: "0", SFIsLiteral: true},
			// Reference to a point that is not a sunssf.
			{Name: "BADREF", Type: "uint16", Size: 1, Offset: 12, SF: "PLAIN"},
		}},
	}
	regs := []uint16{
		0xFF9C,         // W = -100
		0xFFFE,         // W_SF = -2
		7,              // LIT
		50,             // NOSF
		0x8000,         // DEAD_SF not implemented
		60,             // ORPHAN
		0x8000,         // UNIMPL
		70,             // PLAIN
		0x4142,         // TEXT "AB"
		0x0001, 0x0000, // WIDE = 65536
		9,  // ZERO_SF
		80, // BADREF
	}
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	fb := dm.Group

	wantScaled(t, pointByName(t, fb, "W"), -2, -1)
	wantScaled(t, pointByName(t, fb, "LIT"), 2, 700)
	wantScaled(t, pointByName(t, fb, "WIDE"), -2, 655.36)
	wantScaled(t, pointByName(t, fb, "ZERO_SF"), 0, 9)

	for _, name := range []string{"NOSF", "ORPHAN", "UNIMPL", "PLAIN", "BADREF", "W_SF", "DEAD_SF"} {
		wantUnscaled(t, pointByName(t, fb, name))
	}
	if pointByName(t, fb, "DEAD_SF").Implemented {
		t.Error("DEAD_SF must be reported as not implemented")
	}

	// The scale factor resolves, but there is no number to apply it to.
	text := pointByName(t, fb, "TEXT")
	if text.SFRawValue == nil || *text.SFRawValue != -2 {
		t.Errorf("TEXT: SFRawValue = %v, want -2", text.SFRawValue)
	}
	if text.ScaledValue != nil {
		t.Errorf("TEXT: ScaledValue = %g, want nil", *text.ScaledValue)
	}
}

// Scale factors are looked up in the point's own group instance first, then in
// the enclosing ones.
func TestScaleFactorScoping(t *testing.T) {
	sf := func(name string) registry.PointMeta { return registry.PointMeta{Name: name, Type: "sunssf"} }
	scaled := func(name, sfName string) registry.PointMeta {
		return registry.PointMeta{Name: name, Type: "uint16", SF: sfName}
	}
	meta := &registry.ModelMeta{
		ID: 202,
		Group: root("m",
			[]registry.PointMeta{sf("G_SF"), sf("L_SF"), sf("DEAD_SF"), scaled("TOP", "L_SF"), scaled("FWD", "IN_SF")},
			nested("mid", 3, "", []registry.PointMeta{
				scaled("G", "G_SF"),       // only defined at the top
				scaled("L", "L_SF"),       // defined here and at the top
				sf("L_SF"),                // shadows the top-level L_SF
				scaled("D", "DEAD_SF"),    // top-level scale factor not implemented
				scaled("N", "NOWHERE_SF"), // defined nowhere
				{Name: "LIT", Type: "int16", SF: "-1", SFLiteral: -1, SFIsLiteral: true},
			},
				nested("leaf", 2, "", []registry.PointMeta{
					scaled("X", "L_SF"),  // nearest is mid's L_SF, not the top-level one
					scaled("Y", "G_SF"),  // two levels up
					scaled("Z", "IN_SF"), // own group
					sf("IN_SF"),
				}))),
	}
	regs := []uint16{
		1, 3, 0x8000, 5, 6, // G_SF=1, L_SF=3, DEAD_SF n/a, TOP=5, FWD=6
		// mid 1: L_SF = -1
		10, 20, 0xFFFF, 30, 40, 50,
		100, 101, 102, 2, // leaf 1: IN_SF = 2
		110, 111, 112, 0x8000, // leaf 2: IN_SF not implemented
		// mid 2: L_SF = 2
		11, 21, 2, 31, 41, 51,
		200, 201, 202, 0,
		210, 211, 212, 0xFFFE,
		// mid 3: L_SF not implemented
		12, 22, 0x8000, 32, 42, 52,
		300, 301, 302, 1,
		310, 311, 312, 1,
	}
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dm.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", dm.Warnings)
	}
	wantLines(t, "decoded", flatten(dm), []string{
		"m.G_SF=1 @0", "m.L_SF=3 @1", "m.DEAD_SF=n/a @2",
		"m.TOP=5 x10^3=5000.0000 @3",
		// A scale factor in a nested group is out of scope for its parent.
		"m.FWD=6 @4",

		"m/mid[1].G=10 x10^1=100.0000 @5",
		"m/mid[1].L=20 x10^-1=2.0000 @6",
		"m/mid[1].L_SF=-1 @7",
		"m/mid[1].D=30 @8",
		"m/mid[1].N=40 @9",
		"m/mid[1].LIT=50 x10^-1=5.0000 @10",
		"m/mid[1]/leaf[1].X=100 x10^-1=10.0000 @11",
		"m/mid[1]/leaf[1].Y=101 x10^1=1010.0000 @12",
		"m/mid[1]/leaf[1].Z=102 x10^2=10200.0000 @13",
		"m/mid[1]/leaf[1].IN_SF=2 @14",
		"m/mid[1]/leaf[2].X=110 x10^-1=11.0000 @15",
		"m/mid[1]/leaf[2].Y=111 x10^1=1110.0000 @16",
		// The instance's own scale factor is not implemented.
		"m/mid[1]/leaf[2].Z=112 @17",
		"m/mid[1]/leaf[2].IN_SF=n/a @18",

		"m/mid[2].G=11 x10^1=110.0000 @19",
		"m/mid[2].L=21 x10^2=2100.0000 @20",
		"m/mid[2].L_SF=2 @21",
		"m/mid[2].D=31 @22",
		"m/mid[2].N=41 @23",
		"m/mid[2].LIT=51 x10^-1=5.1000 @24",
		"m/mid[2]/leaf[1].X=200 x10^2=20000.0000 @25",
		"m/mid[2]/leaf[1].Y=201 x10^1=2010.0000 @26",
		"m/mid[2]/leaf[1].Z=202 x10^0=202.0000 @27",
		"m/mid[2]/leaf[1].IN_SF=0 @28",
		"m/mid[2]/leaf[2].X=210 x10^2=21000.0000 @29",
		"m/mid[2]/leaf[2].Y=211 x10^1=2110.0000 @30",
		"m/mid[2]/leaf[2].Z=212 x10^-2=2.1200 @31",
		"m/mid[2]/leaf[2].IN_SF=-2 @32",

		// mid 3 reports its own L_SF as not implemented. That does not hide
		// the usable top-level L_SF, so L and the leaf X points are scaled
		// with it.
		"m/mid[3].G=12 x10^1=120.0000 @33",
		"m/mid[3].L=22 x10^3=22000.0000 @34",
		"m/mid[3].L_SF=n/a @35",
		"m/mid[3].D=32 @36",
		"m/mid[3].N=42 @37",
		"m/mid[3].LIT=52 x10^-1=5.2000 @38",
		"m/mid[3]/leaf[1].X=300 x10^3=300000.0000 @39",
		"m/mid[3]/leaf[1].Y=301 x10^1=3010.0000 @40",
		"m/mid[3]/leaf[1].Z=302 x10^1=3020.0000 @41",
		"m/mid[3]/leaf[1].IN_SF=1 @42",
		"m/mid[3]/leaf[2].X=310 x10^3=310000.0000 @43",
		"m/mid[3]/leaf[2].Y=311 x10^1=3110.0000 @44",
		"m/mid[3]/leaf[2].Z=312 x10^1=3120.0000 @45",
		"m/mid[3]/leaf[2].IN_SF=1 @46",
	})

	// Unresolved scale factors leave both fields nil; instances do not share
	// the pointer of a resolved one.
	mids := dm.Group.GroupsNamed("mid")
	wantUnscaled(t, pointByName(t, mids[0], "D"))
	wantUnscaled(t, pointByName(t, mids[0], "N"))
	if p1, p2 := pointByName(t, mids[0], "G").SFRawValue, pointByName(t, mids[1], "G").SFRawValue; p1 == p2 {
		t.Error("instances share one SFRawValue pointer")
	}
}

func TestApplySFWithoutScope(t *testing.T) {
	g := &DecodedGroup{Points: []DecodedPoint{
		{Name: "LIT", Type: "int16", RawValue: int16(5), SFName: "-1", Implemented: true},
		{Name: "REF", Type: "int16", RawValue: int16(5), SFName: "W_SF", Implemented: true},
		{Name: "W_SF", Type: "sunssf", RawValue: int16(2), Implemented: true},
		{Name: "OFF", Type: "int16", RawValue: int16(-32768), SFName: "-1"},
		// A scale factor point holding something other than an int16.
		{Name: "ODD", Type: "int16", RawValue: int16(5), SFName: "ODD_SF", Implemented: true},
		{Name: "ODD_SF", Type: "sunssf", RawValue: "text", Implemented: true},
	}}
	// Without a scope only literal exponents can be resolved.
	applySF(g, nil)
	wantScaled(t, g.Points[0], -1, 0.5)
	wantUnscaled(t, g.Points[1])
	wantUnscaled(t, g.Points[3])

	applySF(g, &scope{group: g})
	wantScaled(t, g.Points[1], 2, 500)
	wantUnscaled(t, g.Points[3])
	wantUnscaled(t, g.Points[4])
}

func TestApplyScaleAllNumericTypes(t *testing.T) {
	tests := []struct {
		raw  interface{}
		want float64
	}{
		{int16(-4), -400}, {uint16(4), 400},
		{int32(-4), -400}, {uint32(4), 400},
		{int64(-4), -400}, {uint64(4), 400},
		{float32(1.5), 150}, {float64(-2.5), -250},
	}
	for _, tc := range tests {
		dp := DecodedPoint{RawValue: tc.raw}
		applyScale(&dp, 2)
		if dp.ScaledValue == nil || *dp.ScaledValue != tc.want {
			t.Errorf("%T: ScaledValue = %v, want %g", tc.raw, dp.ScaledValue, tc.want)
		}
	}
	for _, raw := range []interface{}{"text", []uint16{1}, nil} {
		dp := DecodedPoint{RawValue: raw}
		applyScale(&dp, 2)
		if dp.ScaledValue != nil {
			t.Errorf("%T: ScaledValue = %g, want nil", raw, *dp.ScaledValue)
		}
	}
}

func TestDecodeErrorMessageAndUnwrap(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		name string
		err  *DecodeError
		want string
		is   error
	}{
		{"nil receiver", nil, "sunspec: decode error", nil},
		{"model only", &DecodeError{ModelID: 103}, "sunspec: model 103: decode error", ErrDecode},
		{"message", &DecodeError{ModelID: 103, Message: "too short", Err: ErrDecode}, "sunspec: model 103: too short", ErrDecode},
		{"cause message", &DecodeError{ModelID: 1, Err: cause}, "sunspec: model 1: boom", cause},
		{"message wins over cause", &DecodeError{ModelID: 1, Message: "bad", Err: cause}, "sunspec: model 1: bad", cause},
		{"offset without point", &DecodeError{ModelID: 1, Offset: 4}, "sunspec: model 1 offset 4: decode error", ErrDecode},
		{"point at offset zero", &DecodeError{ModelID: 1, PointName: "W"}, "sunspec: model 1 point W offset 0: decode error", ErrDecode},
		{
			"full location",
			&DecodeError{ModelID: 160, PointName: "DCW", Offset: 12, PointType: "uint16", Message: "bad value"},
			"sunspec: model 160 point DCW type uint16 offset 12: bad value", ErrDecode,
		},
		{"type without point", &DecodeError{ModelID: 2, PointType: "int16"}, "sunspec: model 2 type int16: decode error", ErrDecode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
			if got := tc.err.Unwrap(); got != tc.is {
				t.Errorf("Unwrap() = %v, want %v", got, tc.is)
			}
			if tc.err != nil && !errors.Is(tc.err, tc.is) {
				t.Errorf("errors.Is(%v) = false", tc.is)
			}
		})
	}

	// A custom cause replaces ErrDecode in the chain.
	if errors.Is(&DecodeError{Err: cause}, ErrDecode) {
		t.Error("DecodeError with a custom cause must not also match ErrDecode")
	}
}
