// SPDX-License-Identifier: MIT

package sunspec

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// testSymbols is shared by the enum and bitfield cases: as enum values 1 and
// 2, as bit positions 0, 2 and 40. The negative and out-of-range entries must
// never be reported.
var testSymbols = []registry.SymbolMeta{
	{Name: "NEG", Value: -1},
	{Name: "B0", Value: 0},
	{Name: "ONE", Value: 1},
	{Name: "B2", Value: 2},
	{Name: "B40", Value: 40},
	{Name: "B64", Value: 64},
}

func TestDecodePointAllTypes(t *testing.T) {
	const ff = 0xFFFF
	tests := []struct {
		name     string
		typ      string
		regs     []uint16
		want     interface{}
		wantImpl bool
		wantSyms []string
		skipSyms bool
	}{
		{name: "int16 negative", typ: "int16", regs: []uint16{0xFFFE}, want: int16(-2), wantImpl: true},
		{name: "int16 max", typ: "int16", regs: []uint16{0x7FFF}, want: int16(32767), wantImpl: true},
		{name: "int16 unimplemented", typ: "int16", regs: []uint16{0x8000}, want: int16(-32768)},
		{name: "uint16", typ: "uint16", regs: []uint16{0xFFFE}, want: uint16(65534), wantImpl: true},
		{name: "uint16 zero", typ: "uint16", regs: []uint16{0}, want: uint16(0), wantImpl: true},
		{name: "uint16 unimplemented", typ: "uint16", regs: []uint16{ff}, want: uint16(ff)},
		{name: "count", typ: "count", regs: []uint16{3}, want: uint16(3), wantImpl: true},
		{name: "count unimplemented", typ: "count", regs: []uint16{ff}, want: uint16(ff)},
		{name: "sunssf", typ: "sunssf", regs: []uint16{0xFFFD}, want: int16(-3), wantImpl: true},
		{name: "sunssf unimplemented", typ: "sunssf", regs: []uint16{0x8000}, want: int16(-32768)},
		{name: "acc16", typ: "acc16", regs: []uint16{ff}, want: uint16(ff), wantImpl: true},
		{name: "acc16 unimplemented", typ: "acc16", regs: []uint16{0}, want: uint16(0)},
		{name: "pad", typ: "pad", regs: []uint16{0x8000}, want: uint16(0x8000), wantImpl: true},

		{name: "int32 negative", typ: "int32", regs: []uint16{ff, 0xFFFE}, want: int32(-2), wantImpl: true},
		{name: "int32 word order", typ: "int32", regs: []uint16{0x0001, 0x0002}, want: int32(0x00010002), wantImpl: true},
		{name: "int32 unimplemented", typ: "int32", regs: []uint16{0x8000, 0}, want: int32(math.MinInt32)},
		{name: "uint32", typ: "uint32", regs: []uint16{ff, 0xFFFE}, want: uint32(0xFFFFFFFE), wantImpl: true},
		{name: "uint32 unimplemented", typ: "uint32", regs: []uint16{ff, ff}, want: uint32(math.MaxUint32)},
		{name: "acc32", typ: "acc32", regs: []uint16{ff, ff}, want: uint32(math.MaxUint32), wantImpl: true},
		{name: "acc32 unimplemented", typ: "acc32", regs: []uint16{0, 0}, want: uint32(0)},

		{name: "int64 negative", typ: "int64", regs: []uint16{ff, ff, ff, 0xFFFE}, want: int64(-2), wantImpl: true},
		{name: "int64 word order", typ: "int64", regs: []uint16{1, 2, 3, 4}, want: int64(0x0001000200030004), wantImpl: true},
		{name: "int64 unimplemented", typ: "int64", regs: []uint16{0x8000, 0, 0, 0}, want: int64(math.MinInt64)},
		{name: "uint64", typ: "uint64", regs: []uint16{ff, ff, ff, 0xFFFE}, want: uint64(math.MaxUint64 - 1), wantImpl: true},
		{name: "uint64 unimplemented", typ: "uint64", regs: []uint16{ff, ff, ff, ff}, want: uint64(math.MaxUint64)},
		{name: "acc64", typ: "acc64", regs: []uint16{0, 0, 0, 9}, want: uint64(9), wantImpl: true},
		{name: "acc64 unimplemented", typ: "acc64", regs: []uint16{0, 0, 0, 0}, want: uint64(0)},

		{name: "enum16 known", typ: "enum16", regs: []uint16{1}, want: uint16(1), wantImpl: true, wantSyms: []string{"ONE"}},
		{name: "enum16 unlisted value", typ: "enum16", regs: []uint16{9}, want: uint16(9), wantImpl: true},
		{name: "enum16 unimplemented", typ: "enum16", regs: []uint16{ff}, want: uint16(ff)},
		{name: "enum32 known", typ: "enum32", regs: []uint16{0, 2}, want: uint32(2), wantImpl: true, wantSyms: []string{"B2"}},
		{name: "enum32 high word", typ: "enum32", regs: []uint16{1, 2}, want: uint32(0x00010002), wantImpl: true},
		{name: "enum32 unimplemented", typ: "enum32", regs: []uint16{ff, ff}, want: uint32(math.MaxUint32)},

		{name: "bitfield16", typ: "bitfield16", regs: []uint16{0b101}, want: uint16(5), wantImpl: true, wantSyms: []string{"B0", "B2"}},
		{name: "bitfield16 no bits", typ: "bitfield16", regs: []uint16{0}, want: uint16(0), wantImpl: true},
		{name: "bitfield16 unimplemented", typ: "bitfield16", regs: []uint16{ff}, want: uint16(ff), skipSyms: true},
		{name: "bitfield32", typ: "bitfield32", regs: []uint16{0, 0b110}, want: uint32(6), wantImpl: true, wantSyms: []string{"ONE", "B2"}},
		{name: "bitfield32 unimplemented", typ: "bitfield32", regs: []uint16{ff, ff}, want: uint32(math.MaxUint32), skipSyms: true},
		{name: "bitfield64 high bit", typ: "bitfield64", regs: []uint16{0, 0x0100, 0, 1}, want: uint64(1<<40 | 1), wantImpl: true, wantSyms: []string{"B0", "B40"}},
		{name: "bitfield64 unimplemented", typ: "bitfield64", regs: []uint16{ff, ff, ff, ff}, want: uint64(math.MaxUint64), skipSyms: true},

		{name: "float32", typ: "float32", regs: []uint16{0x3FC0, 0x0000}, want: float32(1.5), wantImpl: true},
		{name: "float32 negative", typ: "float32", regs: []uint16{0xC020, 0x0000}, want: float32(-2.5), wantImpl: true},
		{name: "float64", typ: "float64", regs: []uint16{0x4004, 0, 0, 0}, want: float64(2.5), wantImpl: true},

		{name: "string", typ: "string", regs: []uint16{0x4142, 0x4300}, want: "ABC", wantImpl: true},
		{name: "string padded with spaces and NULs", typ: "string", regs: []uint16{0x4120, 0x2000}, want: "A", wantImpl: true},
		{name: "string keeps inner space", typ: "string", regs: []uint16{0x4120, 0x4200}, want: "A B", wantImpl: true},
		{name: "string all NUL is not implemented", typ: "string", regs: []uint16{0, 0}, want: "", wantImpl: false},
		{name: "string of spaces is implemented", typ: "string", regs: []uint16{0x2020, 0x2020}, want: "", wantImpl: true},
		{name: "string no registers", typ: "string", regs: nil, want: "", wantImpl: false},
		{name: "ipaddr", typ: "ipaddr", regs: []uint16{0xC0A8, 0x010A}, want: "192.168.1.10", wantImpl: true},
		{name: "ipaddr short", typ: "ipaddr", regs: []uint16{0xC0A8}, want: "", wantImpl: true},
		{name: "ipv6addr", typ: "ipv6addr", regs: []uint16{0x2001, 0x0DB8, 0, 0, 0, 0, 0, 1}, want: "2001:db8::1", wantImpl: true},
		{name: "ipv6addr short", typ: "ipv6addr", regs: []uint16{0x2001, 0x0DB8, 0, 0, 0, 0, 0}, want: "", wantImpl: true},
		{name: "eui48", typ: "eui48", regs: []uint16{0, 0x0011, 0x2233, 0x4455}, want: "00:11:22:33:44:55", wantImpl: true},
		{name: "eui48 not implemented", typ: "eui48", regs: []uint16{0, 0xFFFF, 0xFFFF, 0xFFFF}, want: "ff:ff:ff:ff:ff:ff", wantImpl: false},
		{name: "ipaddr not implemented", typ: "ipaddr", regs: []uint16{0, 0}, want: "0.0.0.0", wantImpl: false},
		{name: "ipv6addr not implemented", typ: "ipv6addr", regs: make([]uint16, 8), want: "::", wantImpl: false},
		{name: "eui48 short", typ: "eui48", regs: []uint16{0x0011, 0x2233, 0x4455}, want: "", wantImpl: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pm := &registry.PointMeta{
				Name: "P", Type: tc.typ, Size: len(tc.regs), Offset: 7,
				Units: "U", SF: "P_SF", Symbols: testSymbols,
			}
			dp, warn := decodePoint(tc.regs, pm)
			if warn != "" {
				t.Fatalf("unexpected warning: %s", warn)
			}
			if !reflect.DeepEqual(dp.RawValue, tc.want) {
				t.Errorf("RawValue = %#v (%T), want %#v (%T)", dp.RawValue, dp.RawValue, tc.want, tc.want)
			}
			if dp.Implemented != tc.wantImpl {
				t.Errorf("Implemented = %v, want %v", dp.Implemented, tc.wantImpl)
			}
			if !tc.skipSyms && !reflect.DeepEqual(dp.Symbols, tc.wantSyms) {
				t.Errorf("Symbols = %v, want %v", dp.Symbols, tc.wantSyms)
			}
			// Schema metadata is copied verbatim.
			if dp.Name != "P" || dp.Type != tc.typ || dp.Units != "U" || dp.SFName != "P_SF" ||
				dp.RegisterOffset != 7 || dp.RegisterCount != len(tc.regs) {
				t.Errorf("metadata not copied from schema: %+v", dp)
			}
			if dp.ScaledValue != nil || dp.SFRawValue != nil {
				t.Errorf("decodePoint must not scale: %+v", dp)
			}
		})
	}
}

func TestDecodePointFloatNaNIsUnimplemented(t *testing.T) {
	dp, warn := decodePoint([]uint16{0x7FC0, 0x0000}, &registry.PointMeta{Name: "F", Type: "float32", Size: 2})
	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	f32, ok := dp.RawValue.(float32)
	if !ok || !math.IsNaN(float64(f32)) {
		t.Errorf("float32 RawValue = %#v, want NaN", dp.RawValue)
	}
	if dp.Implemented {
		t.Error("float32 NaN must be reported as not implemented")
	}

	dp, warn = decodePoint([]uint16{0x7FF8, 0, 0, 0}, &registry.PointMeta{Name: "D", Type: "float64", Size: 4})
	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	f64, ok := dp.RawValue.(float64)
	if !ok || !math.IsNaN(f64) {
		t.Errorf("float64 RawValue = %#v, want NaN", dp.RawValue)
	}
	if dp.Implemented {
		t.Error("float64 NaN must be reported as not implemented")
	}

	// Infinity is a value, not the "not implemented" marker.
	dp, _ = decodePoint([]uint16{0x7F80, 0x0000}, &registry.PointMeta{Name: "F", Type: "float32", Size: 2})
	if v, _ := dp.RawValue.(float32); !math.IsInf(float64(v), 1) || !dp.Implemented {
		t.Errorf("float32 +Inf decoded as %#v implemented=%v", dp.RawValue, dp.Implemented)
	}
}

func TestDecodePointUnknownTypeReturnsRawCopy(t *testing.T) {
	regs := []uint16{0x1234, 0x5678}
	dp, warn := decodePoint(regs, &registry.PointMeta{Name: "X", Type: "custom_thing", Size: 2})
	if !strings.Contains(warn, "X") || !strings.Contains(warn, `"custom_thing"`) {
		t.Errorf("warning %q does not name the point and its type", warn)
	}
	raw, ok := dp.RawValue.([]uint16)
	if !ok || !reflect.DeepEqual(raw, []uint16{0x1234, 0x5678}) {
		t.Fatalf("RawValue = %#v, want the raw registers", dp.RawValue)
	}
	regs[0] = 0
	if raw[0] != 0x1234 {
		t.Error("RawValue aliases the input registers; it must be a copy")
	}
	if !dp.Implemented {
		t.Error("an unsupported type is not the same as a not-implemented value")
	}
}

// A schema that gives a point fewer registers than its type needs must not
// panic: the point is returned raw, with a warning.
func TestDecodePointTooFewRegisters(t *testing.T) {
	sizes := map[string]int{
		"int16": 1, "uint16": 1, "count": 1, "sunssf": 1, "acc16": 1, "enum16": 1, "bitfield16": 1, "pad": 1,
		"int32": 2, "uint32": 2, "acc32": 2, "enum32": 2, "bitfield32": 2, "float32": 2,
		"int64": 4, "uint64": 4, "acc64": 4, "bitfield64": 4, "float64": 4,
	}
	for typ, need := range sizes {
		if got := minRegisters(typ); got != need {
			t.Errorf("minRegisters(%q) = %d, want %d", typ, got, need)
		}
		for have := 0; have < need; have++ {
			regs := make([]uint16, have)
			for i := range regs {
				regs[i] = uint16(0xA000 + i)
			}
			dp, warn := decodePoint(regs, &registry.PointMeta{Name: "S", Type: typ, Size: have})
			if warn == "" {
				t.Errorf("%s with %d registers: no warning", typ, have)
			}
			raw, ok := dp.RawValue.([]uint16)
			if !ok || len(raw) != have {
				t.Errorf("%s with %d registers: RawValue = %#v, want %d raw registers", typ, have, dp.RawValue, have)
				continue
			}
			for i := range raw {
				if raw[i] != regs[i] {
					t.Errorf("%s with %d registers: raw[%d] = %#x", typ, have, i, raw[i])
				}
			}
		}
		// Exactly enough registers decodes without a warning.
		if _, warn := decodePoint(make([]uint16, need), &registry.PointMeta{Name: "S", Type: typ, Size: need}); warn != "" {
			t.Errorf("%s with %d registers: unexpected warning %q", typ, need, warn)
		}
	}
	for _, typ := range []string{"string", "ipaddr", "ipv6addr", "eui48", "no_such_type"} {
		if got := minRegisters(typ); got != 0 {
			t.Errorf("minRegisters(%q) = %d, want 0", typ, got)
		}
	}
}

func TestDecodeAddressHelpersIgnoreExtraRegisters(t *testing.T) {
	if got := decodeIPAddr([]uint16{0x0A00, 0x0001, 0xDEAD}); got != "10.0.0.1" {
		t.Errorf("decodeIPAddr = %q", got)
	}
	if got := decodeIPAddr(nil); got != "" {
		t.Errorf("decodeIPAddr(nil) = %q, want empty", got)
	}
	if got := decodeEUI48([]uint16{0x0000, 0xAABB, 0xCCDD, 0xEEFF, 0x5678}); got != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("decodeEUI48 = %q", got)
	}
	if got := decodeEUI48(nil); got != "" {
		t.Errorf("decodeEUI48(nil) = %q, want empty", got)
	}
	if got := decodeIPv6Addr(make([]uint16, 9)); got != "::" {
		t.Errorf("decodeIPv6Addr = %q", got)
	}
	if got := decodeIPv6Addr(nil); got != "" {
		t.Errorf("decodeIPv6Addr(nil) = %q, want empty", got)
	}
}

func TestResolveSymbolsEdgeCases(t *testing.T) {
	if got := resolveEnumSymbols(5, nil); got != nil {
		t.Errorf("enum without symbols = %v, want nil", got)
	}
	dup := []registry.SymbolMeta{{Name: "FIRST", Value: 3}, {Name: "SECOND", Value: 3}}
	if got := resolveEnumSymbols(3, dup); !reflect.DeepEqual(got, []string{"FIRST"}) {
		t.Errorf("duplicate enum values = %v, want the first match only", got)
	}
	if got := resolveBitfieldSymbols(0xFF, nil); got != nil {
		t.Errorf("bitfield without symbols = %v, want nil", got)
	}
	syms := []registry.SymbolMeta{{Name: "TOP", Value: 63}, {Name: "OVER", Value: 64}, {Name: "NEG", Value: -3}}
	if got := resolveBitfieldSymbols(math.MaxUint64, syms); !reflect.DeepEqual(got, []string{"TOP"}) {
		t.Errorf("bitfield symbols = %v, want [TOP]", got)
	}
}
