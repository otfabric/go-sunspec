// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// The tests in this file decode registers a hostile or broken device may
// report, without a network. The device-level counterparts are in
// hostile_device_test.go.

// sfSchema has an int16 point W scaled by the sunssf point W_SF.
func sfSchema() *registry.ModelMeta {
	return pointSchema(64030,
		registry.PointMeta{Name: "W", Type: "int16", Size: 1, SF: "W_SF", Access: "RW"},
		registry.PointMeta{Name: "W_SF", Type: "sunssf", Size: 1, Access: "RW"},
	)
}

// Regression: a scale factor outside the -10..10 SunSpec allows was applied
// as it was. 10^400 is +Inf in a float64, so a measurement of 5 read as +Inf,
// a measurement of 0 as NaN (0 x Inf) while it claimed to be implemented, and
// 10^-400 turned every measurement into 0.
func TestHostileScaleFactorOutOfRange(t *testing.T) {
	meta := sfSchema()
	for _, tc := range []struct {
		sf  int16
		raw int16
	}{
		{11, 5}, {-11, 5}, {400, 5}, {400, 0}, {-400, 5}, {math.MaxInt16, 1}, {math.MinInt16 + 1, 1},
	} {
		name := strconv.Itoa(int(tc.sf)) + "/" + strconv.Itoa(int(tc.raw))
		t.Run(name, func(t *testing.T) {
			regs := []uint16{64030, 2, uint16(tc.raw), uint16(tc.sf)}
			dm, err := DecodeModel(regs, meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			w := dm.Point("W")
			if w.ScaledValue != nil || w.SFRawValue != nil {
				t.Errorf("W scaled by %d: ScaledValue %v, SFRawValue %v", tc.sf, deref(w.ScaledValue), deref(w.SFRawValue))
			}
			if w.RawValue != tc.raw || !w.Implemented {
				t.Errorf("W = %+v", w)
			}
			if len(dm.Warnings) != 1 || !strings.Contains(dm.Warnings[0], "outside -10..10") {
				t.Errorf("warnings = %q", dm.Warnings)
			}
			if _, err := json.Marshal(dm); err != nil {
				t.Errorf("json.Marshal: %v", err)
			}
			// The model rebuilds to the registers it was read from.
			if again, err := EncodeModel(meta, dm.Values()); err != nil || !reflect.DeepEqual(again, regs) {
				t.Errorf("EncodeModel(Values()) = %v, %v; want %v", again, err, regs)
			}
		})
	}
	// The limits themselves are applied.
	for _, sf := range []int16{-10, 10} {
		dm, err := DecodeModel([]uint16{64030, 2, 3, uint16(sf)}, meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		w := dm.Point("W")
		if w.SFRawValue == nil || *w.SFRawValue != sf || w.ScaledValue == nil || *w.ScaledValue != 3*math.Pow(10, float64(sf)) {
			t.Errorf("sf %d: W = %+v", sf, w)
		}
		if len(dm.Warnings) != 0 {
			t.Errorf("sf %d: warnings %q", sf, dm.Warnings)
		}
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// Regression: encoding against a scale factor outside -10..10 stored
// round(5 / 10^400) = 0 for a W of 5. The scale factor is not applied, as when
// decoding, and a number outside the range is refused for a sunssf point.
func TestHostileScaleFactorOutOfRangeEncode(t *testing.T) {
	meta := sfSchema()
	// Given as raw registers, the scale factor is stored, and not applied.
	regs, err := EncodeModel(meta, (&Values{}).Set("W", 5).Set("W_SF", []uint16{400}))
	if err != nil || !reflect.DeepEqual(regs, []uint16{64030, 2, 5, 400}) {
		t.Errorf("raw scale factor 400: %v, %v", regs, err)
	}
	// Given as a number, it is refused: by the first point it scales, or
	// by the sunssf point itself.
	for _, sf := range []int{11, -11, 400, math.MaxInt16} {
		_, err := EncodeModel(meta, (&Values{}).Set("W", 5).Set("W_SF", sf))
		var enc *EncodeError
		if !errors.As(err, &enc) || !errors.Is(err, ErrValueOutOfRange) || !strings.Contains(enc.Message, "outside -10..10") {
			t.Errorf("W_SF %d: err = %v", sf, err)
		}
	}
	for _, sf := range []int{-10, 10, 0} {
		if _, err := EncodeModel(meta, (&Values{}).Set("W", 0).Set("W_SF", sf)); err != nil {
			t.Errorf("W_SF %d: %v", sf, err)
		}
	}
	// A count point of type sunssf (no registry model has one) counts as
	// before within the range, and is refused beyond it.
	if _, _, err := countFromValue(&registry.PointMeta{Name: "N", Type: "sunssf", Size: 1}, 11); !errors.Is(err, ErrValueOutOfRange) {
		t.Errorf("sunssf count 11: err = %v", err)
	}
}

// Regression: a literal scale factor in a schema was parsed with Atoi and
// truncated to int16, so "70000" became 4464 and scaled every value to +Inf.
// A literal outside -10..10 is no literal: the point stays unscaled, on both
// sides.
func TestHostileLiteralScaleFactor(t *testing.T) {
	for _, lit := range []string{"70000", "-70000", "11", "-11", "99999999999999999999"} {
		meta := pointSchema(64031, registry.PointMeta{Name: "V", Type: "uint16", Size: 1, SF: lit})
		dm, err := DecodeModel([]uint16{64031, 1, 7}, meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		if v := dm.Point("V"); v.ScaledValue != nil || v.SFRawValue != nil {
			t.Errorf("literal %q: V = %+v", lit, v)
		}
		regs, err := EncodeModel(meta, (&Values{}).Set("V", 7))
		if err != nil || regs[2] != 7 {
			t.Errorf("literal %q: EncodeModel = %v, %v", lit, regs, err)
		}
	}
	if sf, ok := literalSF("-10"); !ok || sf != -10 {
		t.Errorf(`literalSF("-10") = %d, %v`, sf, ok)
	}
}

// Regression (64-bit platforms): an enum symbol whose schema value does not
// fit 32 bits was compared after truncation, so 1<<32 | 1 named the value 1.
func TestHostileEnumSymbolBeyond32Bits(t *testing.T) {
	if strconv.IntSize < 64 {
		// An int cannot hold such a value: there is nothing to truncate.
		if got := resolveEnumSymbols(1, []registry.SymbolMeta{{Name: "ONE", Value: 1}}); !reflect.DeepEqual(got, []string{"ONE"}) {
			t.Errorf("symbols = %v", got)
		}
		return
	}
	big := int64(1)<<32 | 1
	syms := []registry.SymbolMeta{{Name: "BIG", Value: int(big)}, {Name: "NEG", Value: -1}}
	if got := resolveEnumSymbols(1, syms); got != nil {
		t.Errorf("value 1 resolved to %v", got)
	}
	if got := resolveEnumSymbols(math.MaxUint32, syms); got != nil {
		t.Errorf("value 0xFFFFFFFF resolved to %v", got)
	}
}

// A string point holds whatever bytes the device sends: control and escape
// characters, invalid UTF-8, NULs inside. They are returned as they are (a
// consumer printing them must escape them), and the model still encodes to
// JSON and back to the same registers.
func TestHostileStrings(t *testing.T) {
	meta := pointSchema(64032, registry.PointMeta{Name: "S", Type: "string", Size: 6})
	raw := "\x1b[2J\x00\xff\xfe\r\n\a"
	regs := append([]uint16{64032, 6}, bytesToRegs([]byte(raw), 6)...)
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := dm.Point("S")
	if s.RawValue != raw || !s.Implemented {
		t.Errorf("S = %q", s.RawValue)
	}
	out, err := json.Marshal(dm)
	if err != nil || !json.Valid(out) {
		t.Fatalf("json.Marshal: %v", err)
	}
	if again, err := EncodeModel(meta, dm.Values()); err != nil || !reflect.DeepEqual(again, regs) {
		t.Errorf("EncodeModel(Values()) = %v, %v", again, err)
	}
}

func bytesToRegs(b []byte, n int) []uint16 {
	buf := make([]byte, 2*n)
	copy(buf, b)
	regs := make([]uint16, n)
	for i := range regs {
		regs[i] = uint16(buf[2*i])<<8 | uint16(buf[2*i+1])
	}
	return regs
}

// Floats a device may report: every NaN pattern reads as not implemented,
// infinities as implemented values, and the model marshals to JSON.
func TestHostileFloats(t *testing.T) {
	meta := pointSchema(64033,
		registry.PointMeta{Name: "F", Type: "float32", Size: 2, SF: "F_SF"},
		registry.PointMeta{Name: "F_SF", Type: "sunssf", Size: 1},
		registry.PointMeta{Name: "D", Type: "float64", Size: 4},
	)
	for _, tc := range []struct {
		name   string
		f32    uint32
		f64    uint64
		impl32 bool
		impl64 bool
	}{
		{"quiet NaN", 0x7FC00000, 0x7FF8000000000000, false, false},
		{"signalling NaN", 0x7F800001, 0x7FF0000000000001, false, false},
		{"negative NaN", 0xFFC00000, 0xFFF8000000000000, false, false},
		{"+Inf", 0x7F800000, 0x7FF0000000000000, true, true},
		{"-Inf", 0xFF800000, 0xFFF0000000000000, true, true},
		{"max", 0x7F7FFFFF, 0x7FEFFFFFFFFFFFFF, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regs := []uint16{64033, 7, uint16(tc.f32 >> 16), uint16(tc.f32 & 0xFFFF), 10,
				uint16(tc.f64 >> 48), uint16(tc.f64 >> 32 & 0xFFFF), uint16(tc.f64 >> 16 & 0xFFFF), uint16(tc.f64 & 0xFFFF)}
			dm, err := DecodeModel(regs, meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			if f := dm.Point("F"); f.Implemented != tc.impl32 {
				t.Errorf("F implemented = %v", f.Implemented)
			}
			if d := dm.Point("D"); d.Implemented != tc.impl64 {
				t.Errorf("D implemented = %v", d.Implemented)
			}
			if _, err := json.Marshal(dm); err != nil {
				t.Errorf("json.Marshal: %v", err)
			}
		})
	}
}

// Every point type decodes the extreme register patterns the same way as a
// two's-complement reinterpretation does.
func TestHostileSignedExtremes(t *testing.T) {
	meta := pointSchema(64034,
		registry.PointMeta{Name: "A", Type: "int16", Size: 1},
		registry.PointMeta{Name: "B", Type: "int32", Size: 2},
		registry.PointMeta{Name: "C", Type: "int64", Size: 4},
		registry.PointMeta{Name: "S", Type: "sunssf", Size: 1},
	)
	for _, tc := range []struct {
		fill uint16
		a    int16
		b    int32
		c    int64
	}{
		{0xFFFF, -1, -1, -1},
		{0x7FFF, math.MaxInt16, 0x7FFF7FFF, 0x7FFF7FFF7FFF7FFF},
		{0x8000, math.MinInt16, -0x7FFF8000, -0x7FFF7FFF7FFF8000},
		{0x8001, -0x7FFF, -0x7FFE7FFF, -0x7FFE7FFE7FFE7FFF},
	} {
		f := tc.fill
		dm, err := DecodeModel([]uint16{64034, 8, f, f, f, f, f, f, f, f}, meta, 0)
		if err != nil {
			t.Fatal(err)
		}
		if a := dm.Point("A").RawValue; a != tc.a {
			t.Errorf("%#x: int16 %v", f, a)
		}
		if b := dm.Point("B").RawValue; b != tc.b {
			t.Errorf("%#x: int32 %v", f, b)
		}
		if c := dm.Point("C").RawValue; c != tc.c {
			t.Errorf("%#x: int64 %v", f, c)
		}
		if s := dm.Point("S").RawValue; s != tc.a {
			t.Errorf("%#x: sunssf %v", f, s)
		}
	}
	// And they encode back.
	for _, v := range []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, math.MaxInt64} {
		if got := intNumber(v); got.String() != strconv.FormatInt(v, 10) {
			t.Errorf("intNumber(%d) = %s", v, got)
		}
	}
}
