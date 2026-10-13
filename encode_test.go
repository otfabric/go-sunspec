// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// pointSchema builds a model with the ID and L registers followed by the
// given points, laid out back to back.
func pointSchema(id uint16, points ...registry.PointMeta) *registry.ModelMeta {
	g := &registry.GroupMeta{Name: "m", Count: 1, Points: []registry.PointMeta{
		{Name: "ID", Type: "uint16", Size: 1, Offset: 0},
		{Name: "L", Type: "uint16", Size: 1, Offset: 1},
	}}
	off := 2
	for _, p := range points {
		p.Offset = off
		off += p.Size
		g.Points = append(g.Points, p)
	}
	g.Length = off
	return &registry.ModelMeta{ID: id, Name: "m", Group: g}
}

func TestEncodeValueTable(t *testing.T) {
	sf := func(v int16) *int16 { return &v }
	syms := []registry.SymbolMeta{{Name: "A", Value: 0}, {Name: "B", Value: 3}, {Name: "HI", Value: 40}}
	tests := []struct {
		name string
		typ  string
		size int
		val  any
		sf   *int16
		want []uint16
		// wantErr is the sentinel the error must match; errAny accepts any
		// error.
		wantErr error
		errAny  bool
	}{
		{name: "int16 positive", typ: "int16", size: 1, val: 1234, want: []uint16{1234}},
		{name: "int16 negative", typ: "int16", size: 1, val: -2, want: []uint16{0xFFFE}},
		{name: "int16 max", typ: "int16", size: 1, val: int16(32767), want: []uint16{0x7FFF}},
		{name: "int16 min", typ: "int16", size: 1, val: -32767, want: []uint16{0x8001}},
		{name: "int16 sentinel", typ: "int16", size: 1, val: -32768, wantErr: ErrValueOutOfRange},
		{name: "int16 too large", typ: "int16", size: 1, val: 32768, wantErr: ErrValueOutOfRange},
		{name: "int16 too small", typ: "int16", size: 1, val: -32769, wantErr: ErrValueOutOfRange},
		{name: "int16 scaled", typ: "int16", size: 1, val: 123.45, sf: sf(-2), want: []uint16{12345}},
		{name: "int16 scaled rounds", typ: "int16", size: 1, val: 1.26, sf: sf(-1), want: []uint16{13}},
		{name: "int16 scaled half away from zero", typ: "int16", size: 1, val: -0.25, sf: sf(-1), want: []uint16{0xFFFD}},
		{name: "int16 positive sf", typ: "int16", size: 1, val: 12300, sf: sf(2), want: []uint16{123}},
		{name: "int16 positive sf rounds", typ: "int16", size: 1, val: 12350, sf: sf(2), want: []uint16{124}},
		{name: "int16 scaled out of range", typ: "int16", size: 1, val: 400.0, sf: sf(-2), wantErr: ErrValueOutOfRange},
		{name: "int16 sf zero keeps integers exact", typ: "int16", size: 1, val: 7, sf: sf(0), want: []uint16{7}},
		{name: "int16 float rounds unscaled", typ: "int16", size: 1, val: 2.5, want: []uint16{3}},
		{name: "int16 NaN", typ: "int16", size: 1, val: math.NaN(), wantErr: ErrValueOutOfRange},
		{name: "int16 infinity", typ: "int16", size: 1, val: math.Inf(1), wantErr: ErrValueOutOfRange},
		{name: "int16 wrong type", typ: "int16", size: 1, val: "x", errAny: true},
		{name: "int16 bool", typ: "int16", size: 1, val: true, errAny: true},
		{name: "sunssf", typ: "sunssf", size: 1, val: int8(-3), want: []uint16{0xFFFD}},
		{name: "uint16", typ: "uint16", size: 1, val: uint(65534), want: []uint16{0xFFFE}},
		{name: "uint16 sentinel", typ: "uint16", size: 1, val: 65535, wantErr: ErrValueOutOfRange},
		{name: "uint16 negative", typ: "uint16", size: 1, val: -1, wantErr: ErrValueOutOfRange},
		{name: "uint16 minus zero", typ: "uint16", size: 1, val: -0.2, want: []uint16{0}},
		{name: "uint16 too large", typ: "uint16", size: 1, val: uint64(65536), wantErr: ErrValueOutOfRange},
		{name: "count", typ: "count", size: 1, val: uint8(3), want: []uint16{3}},
		{name: "acc16 zero allowed", typ: "acc16", size: 1, val: 0, want: []uint16{0}},
		{name: "acc16 max", typ: "acc16", size: 1, val: 65535, want: []uint16{0xFFFF}},
		{name: "pad any value", typ: "pad", size: 1, val: 0x8000, want: []uint16{0x8000}},
		{name: "enum16 number", typ: "enum16", size: 1, val: uint16(4), want: []uint16{4}},
		{name: "enum16 symbol", typ: "enum16", size: 1, val: "B", want: []uint16{3}},
		{name: "enum16 symbol ignores sf", typ: "enum16", size: 1, val: "B", sf: sf(-1), want: []uint16{3}},
		{name: "enum16 unknown symbol", typ: "enum16", size: 1, val: "Z", errAny: true},
		{name: "enum16 names list", typ: "enum16", size: 1, val: []string{"A"}, errAny: true},
		{name: "enum32 symbol", typ: "enum32", size: 2, val: "A", want: []uint16{0, 0}},
		{name: "bitfield16 names", typ: "bitfield16", size: 1, val: []string{"A", "B"}, want: []uint16{0x0009}},
		{name: "bitfield16 no names", typ: "bitfield16", size: 1, val: []string{}, want: []uint16{0}},
		{name: "bitfield16 bit beyond the type", typ: "bitfield16", size: 1, val: []string{"HI"}, wantErr: ErrValueOutOfRange},
		{name: "bitfield16 unknown name", typ: "bitfield16", size: 1, val: []string{"A", "Z"}, errAny: true},
		{name: "bitfield16 single string", typ: "bitfield16", size: 1, val: "A", errAny: true},
		{name: "bitfield32 number", typ: "bitfield32", size: 2, val: uint32(0x80000001), want: []uint16{0x8000, 0x0001}},
		{name: "bitfield64 names", typ: "bitfield64", size: 4, val: []string{"HI", "A"}, want: []uint16{0, 0x0100, 0, 1}},
		{name: "bitfield64 sentinel", typ: "bitfield64", size: 4, val: uint64(math.MaxUint64), wantErr: ErrValueOutOfRange},
		{name: "int32", typ: "int32", size: 2, val: int32(-2), want: []uint16{0xFFFF, 0xFFFE}},
		{name: "int32 sentinel", typ: "int32", size: 2, val: math.MinInt32, wantErr: ErrValueOutOfRange},
		{name: "uint32", typ: "uint32", size: 2, val: uint32(0x12345678), want: []uint16{0x1234, 0x5678}},
		{name: "uint32 sentinel", typ: "uint32", size: 2, val: uint32(math.MaxUint32), wantErr: ErrValueOutOfRange},
		{name: "uint32 scaled", typ: "uint32", size: 2, val: 7000.5, sf: sf(-1), want: []uint16{0x0001, 0x1175}},
		{name: "acc32", typ: "acc32", size: 2, val: uint32(math.MaxUint32), want: []uint16{0xFFFF, 0xFFFF}},
		{name: "int64 min valid", typ: "int64", size: 4, val: int64(math.MinInt64 + 1), want: []uint16{0x8000, 0, 0, 1}},
		{name: "int64 sentinel", typ: "int64", size: 4, val: int64(math.MinInt64), wantErr: ErrValueOutOfRange},
		{name: "int64 from uint64 too large", typ: "int64", size: 4, val: uint64(1 << 63), wantErr: ErrValueOutOfRange},
		{name: "int64 float too large", typ: "int64", size: 4, val: 1e19, wantErr: ErrValueOutOfRange},
		{name: "uint64 exact", typ: "uint64", size: 4, val: uint64(math.MaxUint64 - 1), want: []uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFE}},
		{name: "uint64 float too large", typ: "uint64", size: 4, val: 1e20, wantErr: ErrValueOutOfRange},
		{name: "uint64 float at 2^64", typ: "uint64", size: 4, val: 18446744073709551616.0, wantErr: ErrValueOutOfRange},
		{name: "acc64 scaled", typ: "acc64", size: 4, val: 1.5, sf: sf(-3), want: []uint16{0, 0, 0, 1500}},
		{name: "json integer", typ: "int32", size: 2, val: json.Number("-70000"), want: []uint16{0xFFFE, 0xEE90}},
		{name: "json big unsigned", typ: "uint64", size: 4, val: json.Number("18446744073709551614"), want: []uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFE}},
		{name: "json float", typ: "int16", size: 1, val: json.Number("1.5"), sf: sf(-1), want: []uint16{15}},
		{name: "json garbage", typ: "int16", size: 1, val: json.Number("abc"), errAny: true},
		{name: "float32", typ: "float32", size: 2, val: float32(1.5), want: []uint16{0x3FC0, 0x0000}},
		{name: "float32 from int", typ: "float32", size: 2, val: -2, want: []uint16{0xC000, 0x0000}},
		{name: "float32 scaled", typ: "float32", size: 2, val: 15.0, sf: sf(1), want: []uint16{0x3FC0, 0x0000}},
		{name: "float32 infinity", typ: "float32", size: 2, val: math.Inf(-1), want: []uint16{0xFF80, 0x0000}},
		{name: "float32 too large", typ: "float32", size: 2, val: 1e39, wantErr: ErrValueOutOfRange},
		{name: "float32 NaN", typ: "float32", size: 2, val: math.NaN(), wantErr: ErrValueOutOfRange},
		{name: "float32 string", typ: "float32", size: 2, val: "1.5", errAny: true},
		{name: "float64", typ: "float64", size: 4, val: 1.5, want: []uint16{0x3FF8, 0, 0, 0}},
		{name: "float64 NaN", typ: "float64", size: 4, val: float32(math.NaN()), wantErr: ErrValueOutOfRange},
		{name: "string", typ: "string", size: 3, val: "ABC", want: []uint16{0x4142, 0x4300, 0}},
		{name: "string full", typ: "string", size: 2, val: "ABCD", want: []uint16{0x4142, 0x4344}},
		{name: "string empty", typ: "string", size: 2, val: "", want: []uint16{0, 0}},
		{name: "string too long", typ: "string", size: 2, val: "ABCDE", wantErr: ErrValueOutOfRange},
		{name: "string number", typ: "string", size: 2, val: 5, errAny: true},
		{name: "ipaddr", typ: "ipaddr", size: 2, val: "192.168.1.10", want: []uint16{0xC0A8, 0x010A}},
		{name: "ipaddr v6 given", typ: "ipaddr", size: 2, val: "fe80::1", errAny: true},
		{name: "ipaddr garbage", typ: "ipaddr", size: 2, val: "1.2.3", errAny: true},
		{name: "ipaddr number", typ: "ipaddr", size: 2, val: 1, errAny: true},
		{name: "ipv6addr", typ: "ipv6addr", size: 8, val: "fe80::1", want: []uint16{0xFE80, 0, 0, 0, 0, 0, 0, 1}},
		{name: "ipv6addr mapped v4", typ: "ipv6addr", size: 8, val: "1.2.3.4", want: []uint16{0, 0, 0, 0, 0, 0xFFFF, 0x0102, 0x0304}},
		{name: "ipv6addr garbage", typ: "ipv6addr", size: 8, val: "zz", errAny: true},
		{name: "eui48", typ: "eui48", size: 4, val: "00:11:22:33:44:55", want: []uint16{0, 0x0011, 0x2233, 0x4455}},
		{name: "eui48 eui64 given", typ: "eui48", size: 4, val: "00:11:22:33:44:55:66:77", errAny: true},
		{name: "eui48 garbage", typ: "eui48", size: 4, val: "nope", errAny: true},
		{name: "eui48 number", typ: "eui48", size: 4, val: 1, errAny: true},
		{name: "raw registers", typ: "int32", size: 2, val: []uint16{0x8000, 0}, want: []uint16{0x8000, 0}},
		{name: "raw registers wrong length", typ: "int32", size: 2, val: []uint16{1}, wantErr: ErrValueOutOfRange},
		{name: "raw registers for unknown type", typ: "mystery", size: 3, val: []uint16{1, 2, 3}, want: []uint16{1, 2, 3}},
		{name: "unknown type", typ: "mystery", size: 3, val: 1, errAny: true},
		{name: "schema size too small", typ: "int32", size: 1, val: 1, errAny: true},
		{name: "schema ip size too small", typ: "ipv6addr", size: 2, val: "::1", errAny: true},
		{name: "schema eui size too small", typ: "eui48", size: 3, val: "00:11:22:33:44:55", errAny: true},
		{name: "oversized point keeps the tail zero", typ: "uint16", size: 2, val: 7, want: []uint16{7, 0}},
	}
	for _, tc := range tests {
		pm := &registry.PointMeta{Name: "P", Type: tc.typ, Size: tc.size, Symbols: syms}
		got, err := encodeValue(pm, tc.val, tc.sf)
		switch {
		case tc.wantErr != nil:
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: error = %v, want %v", tc.name, err, tc.wantErr)
			}
		case tc.errAny:
			if err == nil {
				t.Errorf("%s: no error, got %04X", tc.name, got)
			} else if errors.Is(err, ErrValueOutOfRange) {
				t.Errorf("%s: error %v must not be a range error", tc.name, err)
			}
		case err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case !reflect.DeepEqual(got, tc.want):
			t.Errorf("%s: got %04X, want %04X", tc.name, got, tc.want)
		}
	}
}

func TestEncodeNotImplementedDecodesAsNotImplemented(t *testing.T) {
	want := map[string][]uint16{
		"int16": {0x8000}, "sunssf": {0x8000}, "uint16": {0xFFFF}, "count": {0xFFFF}, "enum16": {0xFFFF},
		"bitfield16": {0xFFFF}, "acc16": {0}, "pad": {0x8000},
		"int32": {0x8000, 0}, "uint32": {0xFFFF, 0xFFFF}, "enum32": {0xFFFF, 0xFFFF}, "bitfield32": {0xFFFF, 0xFFFF}, "acc32": {0, 0},
		"int64": {0x8000, 0, 0, 0}, "uint64": {0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF}, "bitfield64": {0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF}, "acc64": {0, 0, 0, 0},
		"float32": {0x7FC0, 0}, "float64": {0x7FF8, 0, 0, 0},
		"string": {0, 0, 0}, "ipaddr": {0, 0}, "ipv6addr": {0, 0, 0, 0, 0, 0, 0, 0}, "eui48": {0, 0xFFFF, 0xFFFF, 0xFFFF},
		"mystery": {0, 0},
	}
	for typ, regs := range want {
		pm := &registry.PointMeta{Name: "P", Type: typ, Size: len(regs)}
		dst := make([]uint16, len(regs))
		if err := encodeNotImplemented(pm, dst); err != nil {
			t.Errorf("%s: %v", typ, err)
			continue
		}
		if !reflect.DeepEqual(dst, regs) {
			t.Errorf("%s: not implemented = %04X, want %04X", typ, dst, regs)
		}
		dp, _ := decodePoint(dst, pm)
		if dp.Implemented != (typ == "pad" || typ == "mystery") {
			t.Errorf("%s: decoder reads %04X as Implemented=%v", typ, dst, dp.Implemented)
		}
	}
	if err := encodeNotImplemented(&registry.PointMeta{Type: "int64", Size: 2}, make([]uint16, 2)); err == nil {
		t.Error("a point smaller than its type must be an error")
	}
}

func TestEncodeModelScaleFactors(t *testing.T) {
	meta := pointSchema(64001,
		registry.PointMeta{Name: "W", Type: "int16", Size: 1, SF: "W_SF"},
		registry.PointMeta{Name: "W_SF", Type: "sunssf", Size: 1},
		registry.PointMeta{Name: "Lit", Type: "uint16", Size: 1, SF: "-2"},
		registry.PointMeta{Name: "V", Type: "uint16", Size: 1, SF: "V_SF"},
		registry.PointMeta{Name: "V_SF", Type: "sunssf", Size: 1},
		registry.PointMeta{Name: "X", Type: "uint16", Size: 1, SF: "Nope"},
		registry.PointMeta{Name: "F", Type: "float64", Size: 4},
	)
	v := (&Values{}).Set("W", 123.4).Set("W_SF", -1).Set("Lit", 1.25).Set("V", 230).Set("X", 9).Set("F", 2.5)
	regs, err := EncodeModel(meta, v)
	if err != nil {
		t.Fatal(err)
	}
	// V_SF is not set: V is stored unscaled, as the decoder reads it. X
	// refers to a scale factor the schema does not have.
	want := []uint16{64001, 10, 1234, 0xFFFF, 125, 230, 0x8000, 9, 0x4004, 0, 0, 0}
	if !reflect.DeepEqual(regs, want) {
		t.Fatalf("registers = %v, want %v", regs, want)
	}
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil || len(dm.Warnings) != 0 {
		t.Fatalf("DecodeModel: %v %v", err, dm.Warnings)
	}
	if p := dm.Point("W"); p.ScaledValue == nil || *p.ScaledValue != 123.4 {
		t.Errorf("W = %+v", p)
	}
	if p := dm.Point("Lit"); p.ScaledValue == nil || *p.ScaledValue != 1.25 {
		t.Errorf("Lit = %+v", p)
	}
	if p := dm.Point("V"); p.ScaledValue != nil || p.RawValue != uint16(230) {
		t.Errorf("V = %+v", p)
	}
	if p := dm.Point("F"); p.RawValue != 2.5 {
		t.Errorf("F = %+v", p)
	}

	// A value that only overflows because of its scale factor.
	_, err = EncodeModel(meta, (&Values{}).Set("W", 4000).Set("W_SF", -1))
	var ee *EncodeError
	if !errors.As(err, &ee) || !errors.Is(err, ErrValueOutOfRange) || !errors.Is(err, ErrEncode) {
		t.Fatalf("error = %v, want an EncodeError matching ErrValueOutOfRange and ErrEncode", err)
	}
	if ee.ModelID != 64001 || ee.PointName != "W" || ee.PointType != "int16" || ee.Group != "m" {
		t.Errorf("EncodeError = %+v", ee)
	}
	// An invalid scale factor value is reported for the point that uses it
	// and for the scale factor itself.
	for _, vals := range []*Values{
		(&Values{}).Set("W", 1).Set("W_SF", "x"),
		(&Values{}).Set("W_SF", 99999),
	} {
		if _, err := EncodeModel(meta, vals); !errors.Is(err, ErrEncode) {
			t.Errorf("invalid scale factor: error = %v", err)
		}
	}
	// A scale factor given as its raw not-implemented register is not set.
	regs, err = EncodeModel(meta, (&Values{}).Set("W", 7).Set("W_SF", []uint16{0x8000}))
	if err != nil || regs[2] != 7 {
		t.Errorf("raw not-implemented scale factor: %v %v", regs, err)
	}
}

// curveSchema is a model with a counted group that nests a second counted
// group, a fixed group and, last, a group that fills the model.
func curveSchema() *registry.ModelMeta {
	pt := &registry.GroupMeta{Name: "Pt", CountPoint: "NPt", Length: 2, Points: []registry.PointMeta{
		{Name: "X", Type: "int16", Size: 1, Offset: 0, SF: "X_SF", Access: "RW"},
		{Name: "Y", Type: "uint16", Size: 1, Offset: 1, Access: "R"},
	}}
	crv := &registry.GroupMeta{Name: "Crv", CountPoint: "NCrv", Length: 2, Groups: []*registry.GroupMeta{pt}, Points: []registry.PointMeta{
		{Name: "En", Type: "enum16", Size: 1, Offset: 0, Access: "RW", Symbols: []registry.SymbolMeta{{Name: "OFF", Value: 0}, {Name: "ON", Value: 1}}},
		{Name: "X_SF", Type: "sunssf", Size: 1, Offset: 1, Access: "RW"},
	}}
	fixed := &registry.GroupMeta{Name: "Fix", Count: 2, Length: 1, Points: []registry.PointMeta{
		{Name: "A", Type: "uint16", Size: 1, Offset: 0, Access: "RW"},
	}}
	fill := &registry.GroupMeta{Name: "Rep", Count: 0, Length: 2, Points: []registry.PointMeta{
		{Name: "B", Type: "uint32", Size: 2, Offset: 0, Access: "RW"},
	}}
	return &registry.ModelMeta{ID: 64002, Name: "curves", Group: &registry.GroupMeta{
		Name: "curves", Count: 1, Length: 5, Groups: []*registry.GroupMeta{crv, fixed, fill},
		Points: []registry.PointMeta{
			{Name: "ID", Type: "uint16", Size: 1, Offset: 0},
			{Name: "L", Type: "uint16", Size: 1, Offset: 1},
			{Name: "NCrv", Type: "uint16", Size: 1, Offset: 2, Access: "RW"},
			{Name: "NPt", Type: "count", Size: 1, Offset: 3, Access: "R"},
			{Name: "X_SF", Type: "sunssf", Size: 1, Offset: 4, Access: "R"},
		},
	}}
}

func TestEncodeModelGroupsAndCounts(t *testing.T) {
	meta := curveSchema()
	v := &Values{}
	v.Set("X_SF", -1)
	c1 := v.Add("Crv", nil)
	c1.Set("En", "ON")
	c1.Add("Pt", (&Values{}).Set("X", 1.5).Set("Y", 10))
	c2 := v.Add("Crv", (&Values{}).Set("X_SF", -2))
	c2.Add("Pt", (&Values{}).Set("X", 1.5))
	c2.Add("Pt", nil)
	v.Add("Fix", (&Values{}).Set("A", 7))
	v.Add("Rep", (&Values{}).Set("B", 70000))

	regs, err := EncodeModel(meta, v)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{64002, 19,
		2, 2, 0xFFFF, // NCrv and NPt derived from the instances given, X_SF -1
		1, 0x8000, 15, 10, 0x8000, 0xFFFF, // curve 1: outer scale factor, second point completed
		0xFFFF, 0xFFFE, 150, 0xFFFF, 0x8000, 0xFFFF, // curve 2: its own scale factor
		7, 0xFFFF, // fixed group completed to two instances
		1, 0x1170, // one filling instance
	}
	if !reflect.DeepEqual(regs, want) {
		t.Fatalf("registers =\n%v, want\n%v", regs, want)
	}
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil || len(dm.Warnings) != 0 {
		t.Fatalf("DecodeModel: %v %v", err, dm.Warnings)
	}
	if n := len(dm.Group.GroupsNamed("Crv")); n != 2 {
		t.Fatalf("%d curves decoded", n)
	}
	if p := dm.Group.GroupsNamed("Crv")[1].GroupsNamed("Pt")[0].Point("X"); *p.ScaledValue != 1.5 {
		t.Errorf("curve 2 point 1 X = %v", *p.ScaledValue)
	}

	// An explicit count completes the groups it counts with empty instances.
	regs, err = EncodeModel(meta, (&Values{}).Set("NCrv", 1).Set("NPt", uint16(3)))
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 5+2+3*2+2 || regs[2] != 1 || regs[3] != 3 {
		t.Errorf("explicit counts: %d registers, counts %d %d", len(regs), regs[2], regs[3])
	}
	// No instances at all: the counts are zero, not "not implemented".
	regs, err = EncodeModel(meta, nil)
	if err != nil || len(regs) != 7 || regs[2] != 0 || regs[3] != 0 {
		t.Errorf("empty model: %v %v", regs, err)
	}

	errorTests := []struct {
		name    string
		vals    func() *Values
		wantErr error
		group   string
		point   string
	}{
		{"more instances than the count", func() *Values {
			v := (&Values{}).Set("NCrv", 1)
			v.Add("Crv", nil)
			v.Add("Crv", nil)
			return v
		}, ErrValueOutOfRange, "curves", "NCrv"},
		{"more nested instances than the count", func() *Values {
			v := (&Values{}).Set("NPt", 1)
			c := v.Add("Crv", nil)
			c.Add("Pt", nil)
			c.Add("Pt", nil)
			return v
		}, ErrValueOutOfRange, "curves", "NPt"},
		{"too many fixed instances", func() *Values {
			v := &Values{}
			v.Add("Fix", nil)
			v.Add("Fix", nil)
			v.Add("Fix", nil)
			return v
		}, ErrValueOutOfRange, "curves/Fix", ""},
		{"negative count", func() *Values { return (&Values{}).Set("NCrv", -1) }, ErrValueOutOfRange, "curves", "NCrv"},
		{"count of the wrong type", func() *Values { return (&Values{}).Set("NCrv", "two") }, ErrEncode, "curves", "NCrv"},
		{"unknown point", func() *Values { return (&Values{}).Set("Nope", 1) }, ErrPointNotFound, "curves", "Nope"},
		{"unknown nested point", func() *Values {
			v := &Values{}
			v.Add("Crv", nil).Add("Pt", (&Values{}).Set("Z", 1))
			return v
		}, ErrPointNotFound, "curves/Crv[1]/Pt[1]", "Z"},
		{"unknown group", func() *Values {
			v := &Values{}
			v.Add("Nope", nil)
			return v
		}, ErrEncode, "curves", ""},
		{"header given", func() *Values { return (&Values{}).Set("ID", 1) }, ErrEncode, "curves", "ID"},
		{"length given", func() *Values { return (&Values{}).Set("L", 1) }, ErrEncode, "curves", "L"},
		{"bad value in a nested instance", func() *Values {
			v := &Values{}
			v.Add("Fix", nil)
			v.Add("Fix", (&Values{}).Set("A", 70000))
			return v
		}, ErrValueOutOfRange, "curves/Fix[2]", "A"},
		{"model too long", func() *Values {
			v := &Values{}
			for i := 0; i < 33000; i++ {
				v.Add("Rep", nil)
			}
			return v
		}, ErrEncode, "curves/Rep[32766]", ""},
	}
	for _, tc := range errorTests {
		_, err := EncodeModel(meta, tc.vals())
		var ee *EncodeError
		if !errors.As(err, &ee) || !errors.Is(err, tc.wantErr) || !errors.Is(err, ErrEncode) {
			t.Errorf("%s: error = %v, want an EncodeError matching %v", tc.name, err, tc.wantErr)
			continue
		}
		if ee.Group != tc.group || ee.PointName != tc.point || ee.ModelID != 64002 {
			t.Errorf("%s: location = group %q point %q model %d (%v)", tc.name, ee.Group, ee.PointName, ee.ModelID, err)
		}
	}
}

func TestEncodeModelSchemaErrors(t *testing.T) {
	if _, err := EncodeModel(nil, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("nil schema: %v", err)
	}
	if _, err := EncodeModel(&registry.ModelMeta{ID: 5}, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("schema without group: %v", err)
	}
	if _, err := EncodeModel(&registry.ModelMeta{ID: 5, Group: &registry.GroupMeta{Length: 1}}, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("schema without header: %v", err)
	}
	bad := pointSchema(64003, registry.PointMeta{Name: "P", Type: "int32", Size: 2})
	bad.Group.Length = 3
	if _, err := EncodeModel(bad, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("point beyond the group: %v", err)
	}
	small := pointSchema(64003, registry.PointMeta{Name: "P", Type: "int32", Size: 1})
	if _, err := EncodeModel(small, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("unset point smaller than its type: %v", err)
	}
	neg := pointSchema(64003)
	neg.Group.Groups = []*registry.GroupMeta{{Name: "G", Count: 1, Length: -1}}
	if _, err := EncodeModel(neg, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("negative group length: %v", err)
	}

	// A group counted by a point the schema does not have, and by a point
	// whose type cannot hold a count.
	orphan := pointSchema(64003)
	orphan.Group.Groups = []*registry.GroupMeta{{Name: "G", CountPoint: "N", Length: 1,
		Points: []registry.PointMeta{{Name: "A", Type: "uint16", Size: 1}}}}
	if _, err := EncodeModel(orphan, nil); !errors.Is(err, ErrEncode) {
		t.Errorf("missing count point: %v", err)
	}
	for _, typ := range []string{"float32", "int32", "uint64", "string"} {
		size := map[string]int{"float32": 2, "int32": 2, "uint64": 4, "string": 2}[typ]
		m := pointSchema(64003, registry.PointMeta{Name: "N", Type: typ, Size: size})
		m.Group.Groups = orphan.Group.Groups
		if _, err := EncodeModel(m, nil); !errors.Is(err, ErrEncode) {
			t.Errorf("count point of type %s: %v", typ, err)
		}
	}
	// The countable types the decoder accepts.
	for _, typ := range []string{"int16", "uint32", "acc32"} {
		size := minRegisters(typ)
		m := pointSchema(64003, registry.PointMeta{Name: "N", Type: typ, Size: size})
		m.Group.Groups = orphan.Group.Groups
		v := (&Values{}).Set("N", 2)
		regs, err := EncodeModel(m, v)
		if err != nil {
			t.Errorf("count point of type %s: %v", typ, err)
			continue
		}
		dm, err := DecodeModel(regs, m, 0)
		if err != nil || len(dm.Group.Groups) != 2 || len(dm.Warnings) != 0 {
			t.Errorf("count point of type %s: decoded %d instances, %v %v", typ, len(dm.Group.Groups), err, dm.Warnings)
		}
	}
	m := pointSchema(64003, registry.PointMeta{Name: "N", Type: "uint32", Size: 2})
	m.Group.Groups = orphan.Group.Groups
	if _, err := EncodeModel(m, (&Values{}).Set("N", 70000)); !errors.Is(err, ErrValueOutOfRange) {
		t.Errorf("count beyond a model: %v", err)
	}
	m = pointSchema(64003, registry.PointMeta{Name: "N", Type: "int16", Size: 1})
	m.Group.Groups = orphan.Group.Groups
	if _, err := EncodeModel(m, (&Values{}).Set("N", -2)); !errors.Is(err, ErrValueOutOfRange) {
		t.Errorf("negative signed count: %v", err)
	}

	// A count point of the same name in a nested group hides the outer one
	// for the groups below it.
	inner := &registry.GroupMeta{Name: "In", CountPoint: "N", Length: 1, Points: []registry.PointMeta{{Name: "A", Type: "uint16", Size: 1}}}
	mid := &registry.GroupMeta{Name: "Mid", CountPoint: "N", Length: 1, Groups: []*registry.GroupMeta{inner},
		Points: []registry.PointMeta{{Name: "N", Type: "uint16", Size: 1}}}
	shadow := pointSchema(64004, registry.PointMeta{Name: "N", Type: "uint16", Size: 1})
	shadow.Group.Groups = []*registry.GroupMeta{mid}
	v := &Values{}
	v.Add("Mid", nil).Add("In", nil)
	mid2 := v.Add("Mid", nil)
	mid2.Add("In", nil)
	mid2.Add("In", nil)
	mid2.Add("In", nil)
	regs, err := EncodeModel(shadow, v)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{64004, 7, 2, 1, 0xFFFF, 3, 0xFFFF, 0xFFFF, 0xFFFF}; !reflect.DeepEqual(regs, want) {
		t.Errorf("shadowed count: %v, want %v", regs, want)
	}

	// A group without registers is encoded once, however often it is given.
	empty := pointSchema(64005, registry.PointMeta{Name: "A", Type: "uint16", Size: 1})
	empty.Group.Groups = []*registry.GroupMeta{{Name: "E", Count: 0}}
	v = &Values{}
	v.Add("E", nil)
	v.Add("E", nil)
	if regs, err := EncodeModel(empty, v); err != nil || len(regs) != 3 {
		t.Errorf("empty group: %v %v", regs, err)
	}
}

func TestValuesHelpers(t *testing.T) {
	var nilValues *Values
	if nilValues.Clone() != nil {
		t.Error("Clone of nil must be nil")
	}
	v := (&Values{}).Set("A", []uint16{1, 2}).Set("B", []string{"x"}).Set("C", 5)
	v.Add("G", nil).Set("D", 1)
	c := v.Clone()
	if !reflect.DeepEqual(c, v) {
		t.Fatalf("Clone = %+v, want %+v", c, v)
	}
	c.Points["A"].([]uint16)[0] = 99
	c.Points["B"].([]string)[0] = "y"
	c.Groups["G"][0].Set("D", 2)
	c.Add("G", nil)
	if v.Points["A"].([]uint16)[0] != 1 || v.Points["B"].([]string)[0] != "x" || v.Groups["G"][0].Points["D"] != 1 || len(v.Groups["G"]) != 1 {
		t.Errorf("modifying the clone changed the original: %+v", v)
	}
	if got := (&Values{}).Clone(); got == nil || got.Points != nil || got.Groups != nil {
		t.Errorf("Clone of empty = %+v", got)
	}
	// A nil value is an unset point.
	if _, set := (&Values{Points: map[string]any{"A": nil}}).point("A"); set {
		t.Error("nil value counts as set")
	}
}

func TestDecodedModelValues(t *testing.T) {
	var nilModel *DecodedModel
	if nilModel.Values() != nil || (&DecodedModel{}).Values() != nil {
		t.Error("Values of an undecoded model must be nil")
	}

	meta := pointSchema(64006,
		registry.PointMeta{Name: "Big", Type: "uint64", Size: 4, SF: "SF"},
		registry.PointMeta{Name: "SF", Type: "sunssf", Size: 1},
		registry.PointMeta{Name: "Pad", Type: "pad", Size: 1},
		registry.PointMeta{Name: "Pad2", Type: "pad", Size: 1},
		registry.PointMeta{Name: "S", Type: "string", Size: 2},
		registry.PointMeta{Name: "Odd", Type: "mystery", Size: 2},
		registry.PointMeta{Name: "U", Type: "uint16", Size: 1},
		registry.PointMeta{Name: "W", Type: "int16", Size: 1, SF: "SF"},
		registry.PointMeta{Name: "Z", Type: "int16", Size: 1, SF: "0"},
	)
	regs := []uint16{64006, 14,
		0x7FFF, 0xFFFF, 0xFFFF, 0xFFF1, // beyond the precision of a float64
		0xFFFD,         // 10^-3
		0x8000, 0x1234, // a pad holding the pad value, and one that does not
		0x2020, 0x2020, // only spaces: decodes as an implemented empty string
		7, 8,
		0xFFFF,
		1500,
		42,
	}
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := dm.Values()
	want := map[string]any{
		"Big":  []uint16{0x7FFF, 0xFFFF, 0xFFFF, 0xFFF1},
		"SF":   int16(-3),
		"Pad2": uint16(0x1234),
		"S":    []uint16{0x2020, 0x2020},
		"Odd":  []uint16{7, 8},
		"W":    1.5,
		"Z":    int16(42),
	}
	if !reflect.DeepEqual(v.Points, want) {
		t.Errorf("Values().Points =\n%#v, want\n%#v", v.Points, want)
	}
	again, err := EncodeModel(meta, v)
	if err != nil || !reflect.DeepEqual(again, regs) {
		t.Errorf("EncodeModel(Values()) = %v, %v", again, err)
	}
	// The values own their registers.
	v.Points["Odd"].([]uint16)[0] = 99
	v.Points["Big"].([]uint16)[0] = 99
	if regs[11] != 7 || regs[2] != 0x7FFF {
		t.Error("Values aliases the decoded registers")
	}

	// A model whose raw registers are not available is still converted.
	dm.RawRegisters = nil
	if got := dm.Values().Points["W"]; got != 1.5 {
		t.Errorf("without raw registers W = %v", got)
	}
	// A scale factor so large that the engineering value cannot be turned
	// back: the registers are given.
	absurd := []uint16{64006, 14, 0, 0, 0, 5, 0x7000, 0x8000, 0x8000, 0, 0, 0, 0, 0xFFFF, 3, 0x8000}
	dm, err = DecodeModel(absurd, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := dm.Values().Points["W"]; !reflect.DeepEqual(got, []uint16{3}) {
		t.Errorf("absurd scale factor: W = %#v", got)
	}
}

// Regression, found by TestChaosGarbageRegisters: a count point the device
// reports as not implemented came back from Values() as a count of zero,
// because an unset count point is encoded as the number of instances.
func TestValuesKeepsNotImplementedCountPoint(t *testing.T) {
	meta := curveSchema()
	// NCrv 0, NPt not implemented, nothing else.
	regs := []uint16{64002, 5, 0, 0xFFFF, 0x8000, 0xFFFF, 0xFFFF}
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil || len(dm.Warnings) != 0 {
		t.Fatalf("DecodeModel: %v %v", err, dm.Warnings)
	}
	v := dm.Values()
	if got := v.Points["NPt"]; !reflect.DeepEqual(got, []uint16{0xFFFF}) {
		t.Errorf("NPt in Values() = %#v, want its not-implemented registers", got)
	}
	again, err := EncodeModel(meta, v)
	if err != nil || !reflect.DeepEqual(again, regs) {
		t.Errorf("EncodeModel(Values()) = %v, %v, want %v", again, err, regs)
	}
	// A count that is not implemented counts no instances.
	bad := (&Values{}).Set("NCrv", []uint16{0xFFFF})
	bad.Add("Crv", nil)
	if _, err := EncodeModel(meta, bad); !errors.Is(err, ErrValueOutOfRange) {
		t.Errorf("instances under a not-implemented count: %v", err)
	}
	// Without the schema Values cannot tell a count point: it is left out.
	dm.Schema = nil
	if _, ok := dm.Values().Points["NPt"]; ok {
		t.Error("NPt given without a schema")
	}
}

func TestEncodeErrorFormatting(t *testing.T) {
	var nilErr *EncodeError
	if nilErr.Error() != "sunspec: encode error" || nilErr.Unwrap() != nil {
		t.Errorf("nil EncodeError: %q", nilErr.Error())
	}
	e := &EncodeError{ModelID: 103, Group: "inverter", PointName: "W", PointType: "int16", Message: "boom"}
	if got, want := e.Error(), "sunspec: encode model 103 group inverter point W type int16: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(e, ErrEncode) || errors.Is(e, ErrDecode) || e.Unwrap() != nil {
		t.Error("EncodeError must match ErrEncode only")
	}
	e = &EncodeError{ModelID: 1, Err: ErrValueOutOfRange}
	if !strings.Contains(e.Error(), "model 1: sunspec: value out of range") || !errors.Is(e, ErrValueOutOfRange) || !errors.Is(e, ErrEncode) {
		t.Errorf("Error() = %q", e.Error())
	}
	if got := (&EncodeError{ModelID: 1}).Error(); got != "sunspec: encode model 1: encode error" {
		t.Errorf("Error() = %q", got)
	}
}
