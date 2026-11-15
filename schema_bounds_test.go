// SPDX-License-Identifier: MIT

package sunspec

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// A schema given by the caller can hold any length, offset and size. The
// decoder reports one that does not fit the registers as a warning and the
// encoder as a *EncodeError, also when the numbers are so large that their
// sum does not fit an int. math.MaxInt32 is that boundary where an int has 32
// bits (the nightly GOARCH=386 run).

// boundsHeader is the ID and length registers of a test schema.
func boundsHeader() []registry.PointMeta {
	return []registry.PointMeta{
		{Name: "ID", Type: "uint16", Size: 1, Offset: 0},
		{Name: "L", Type: "uint16", Size: 1, Offset: 1},
	}
}

// TestSchemaPointBounds: a point of a group of four registers, two of them
// the header.
func TestSchemaPointBounds(t *testing.T) {
	for _, tc := range []struct {
		name         string
		offset, size int
		fits         bool
		warning      string // of DecodeModel, when the point does not fit
	}{
		{"one register", 2, 1, true, ""},
		{"up to the end", 2, 2, true, ""},
		{"the last register", 3, 1, true, ""},
		{"one past the end", 2, 3, false, "exceeds the group length 4"},
		{"starts at the end", 4, 1, false, "exceeds the group length 4"},
		{"starts past the end", 5, 1, false, "exceeds the group length 4"},
		{"negative offset", -1, 1, false, "invalid offset -1 or size 1"},
		{"negative size", 2, -1, false, "invalid offset 2 or size -1"},
		{"offset MaxInt32", math.MaxInt32, 1, false, "exceeds the group length 4"},
		{"size MaxInt32", 2, math.MaxInt32, false, "exceeds the group length 4"},
		{"offset and size MaxInt32", math.MaxInt32, math.MaxInt32, false, "exceeds the group length 4"},
		{"offset MaxInt", math.MaxInt, 1, false, "exceeds the group length 4"},
		{"offset MaxInt-1", math.MaxInt - 1, 2, false, "exceeds the group length 4"},
		{"size MaxInt", 2, math.MaxInt, false, "exceeds the group length 4"},
		{"size MaxInt-1", 2, math.MaxInt - 1, false, "exceeds the group length 4"},
		{"offset and size MaxInt", math.MaxInt, math.MaxInt, false, "exceeds the group length 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := &registry.ModelMeta{ID: 64040, Name: "bounds", Group: &registry.GroupMeta{
				Name: "bounds", Count: 1, Length: 4,
				Points: append(boundsHeader(), registry.PointMeta{Name: "P", Type: "string", Size: tc.size, Offset: tc.offset}),
			}}

			dm, err := DecodeModel([]uint16{64040, 2, 0x6162, 0x6364}, meta, 0)
			if err != nil {
				t.Fatalf("DecodeModel: %v", err)
			}
			p := dm.Point("P")
			if tc.fits {
				if p == nil || len(dm.Warnings) != 0 {
					t.Fatalf("DecodeModel: point %+v, warnings %q; want the point and no warning", p, dm.Warnings)
				}
			} else if p != nil || len(dm.Warnings) != 1 || !strings.Contains(dm.Warnings[0], tc.warning) {
				t.Fatalf("DecodeModel: point %+v, warnings %q; want no point and one warning with %q", p, dm.Warnings, tc.warning)
			}

			regs, err := EncodeModel(meta, nil)
			var ee *EncodeError
			if tc.fits {
				if err != nil || len(regs) != 4 {
					t.Fatalf("EncodeModel: %v, %v; want 4 registers", regs, err)
				}
			} else if !errors.As(err, &ee) || ee.PointName != "P" || !strings.Contains(ee.Message, "exceeds the group length 4") {
				t.Fatalf("EncodeModel: %v, %v; want a *EncodeError for point P", regs, err)
			}
		})
	}
}

// TestSchemaGroupLengthBounds: a nested group that occurs once, in a model of
// which two registers are left for it.
func TestSchemaGroupLengthBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length int
		fits   bool // the two registers that are left
		// encodes: within the 65537 registers a model can have.
		encodes bool
	}{
		{"one register", 1, true, true},
		{"what is left", 2, true, true},
		{"one more than is left", 3, false, true},
		{"the longest model", maxModelRegisters - 2, false, true},
		{"one more than the longest model", maxModelRegisters - 1, false, false},
		{"negative", -1, false, false},
		{"MaxInt32-1", math.MaxInt32 - 1, false, false},
		{"MaxInt32", math.MaxInt32, false, false},
		{"MaxInt-2", math.MaxInt - 2, false, false},
		{"MaxInt-1", math.MaxInt - 1, false, false},
		{"MaxInt", math.MaxInt, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := &registry.GroupMeta{Name: "Sub", Count: 1, Length: tc.length,
				Points: []registry.PointMeta{{Name: "B", Type: "uint16", Size: 1, Offset: 0}}}
			meta := &registry.ModelMeta{ID: 64041, Name: "bounds", Group: &registry.GroupMeta{
				Name: "bounds", Count: 1, Length: 2, Points: boundsHeader(), Groups: []*registry.GroupMeta{sub},
			}}

			dm, err := DecodeModel([]uint16{64041, 2, 7, 8}, meta, 0)
			if err != nil {
				t.Fatalf("DecodeModel: %v", err)
			}
			if got := len(dm.Group.Groups); tc.fits != (got == 1) {
				t.Fatalf("DecodeModel: %d instances of Sub, warnings %q", got, dm.Warnings)
			}
			if !tc.fits && (len(dm.Warnings) == 0 || !strings.Contains(dm.Warnings[0], "bounds/Sub: registers end before instance 1 of 1")) {
				t.Fatalf("DecodeModel: warnings %q, want the instance that does not fit", dm.Warnings)
			}

			regs, err := EncodeModel(meta, nil)
			var ee *EncodeError
			if tc.encodes {
				if err != nil || len(regs) != 2+tc.length {
					t.Fatalf("EncodeModel: %d registers, %v; want %d", len(regs), err, 2+tc.length)
				}
			} else if !errors.As(err, &ee) || ee.Group != "bounds/Sub[1]" || !strings.Contains(ee.Message, "model exceeds 65537 registers") {
				t.Fatalf("EncodeModel: %d registers, %v; want a *EncodeError for bounds/Sub[1]", len(regs), err)
			}
		})
	}
}

// TestCountValueBounds: the number of instances a count point gives. A 32-bit
// count above the registers a model can hold is that number, so that it is the
// same where an int has 32 bits, and as the encoder limits it.
func TestCountValueBounds(t *testing.T) {
	for _, tc := range []struct {
		raw  any
		want int
		ok   bool
	}{
		{uint16(0), 0, true},
		{uint16(1), 1, true},
		{uint16(0xFFFE), 0xFFFE, true},
		{int16(0), 0, true},
		{int16(math.MaxInt16), math.MaxInt16, true},
		{int16(-1), 0, false},
		{int16(math.MinInt16 + 1), 0, false},
		{uint32(0), 0, true},
		{uint32(1), 1, true},
		{uint32(maxModelRegisters - 1), maxModelRegisters - 1, true},
		{uint32(maxModelRegisters), maxModelRegisters, true},
		{uint32(maxModelRegisters + 1), maxModelRegisters, true},
		{uint32(math.MaxInt32), maxModelRegisters, true},
		{uint32(math.MaxInt32 + 1), maxModelRegisters, true},
		{uint32(math.MaxUint32 - 1), maxModelRegisters, true},
		{"text", 0, false},
	} {
		n, ok := countValue(&DecodedPoint{Implemented: true, RawValue: tc.raw})
		if n != tc.want || ok != tc.ok {
			t.Errorf("countValue(%T %v) = %d, %v; want %d, %v", tc.raw, tc.raw, n, ok, tc.want, tc.ok)
		}
	}
	if n, ok := countValue(nil); n != 0 || ok {
		t.Errorf("countValue(nil) = %d, %v", n, ok)
	}
	if n, ok := countValue(&DecodedPoint{RawValue: uint16(3)}); n != 0 || ok {
		t.Errorf("countValue of a point that is not implemented = %d, %v", n, ok)
	}
}

// TestDecodeCountBeyondAnInt32: a 32-bit count of 0x80000000, which is
// negative as a 32-bit int, decodes as many instances as the registers hold,
// like every other count that is larger than the model.
func TestDecodeCountBeyondAnInt32(t *testing.T) {
	sub := &registry.GroupMeta{Name: "Sub", CountPoint: "N", Length: 1,
		Points: []registry.PointMeta{{Name: "B", Type: "uint16", Size: 1, Offset: 0}}}
	meta := &registry.ModelMeta{ID: 64042, Name: "counted", Group: &registry.GroupMeta{
		Name: "counted", Count: 1, Length: 4, Groups: []*registry.GroupMeta{sub},
		Points: append(boundsHeader(), registry.PointMeta{Name: "N", Type: "uint32", Size: 2, Offset: 2}),
	}}
	for _, count := range []uint32{3, 4, maxModelRegisters + 1, math.MaxInt32, math.MaxInt32 + 1, math.MaxUint32 - 1} {
		regs := []uint16{64042, 5, uint16(count >> 16), uint16(count), 10, 11, 12}
		dm, err := DecodeModel(regs, meta, 0)
		if err != nil {
			t.Fatalf("count %d: %v", count, err)
		}
		if got := len(dm.Group.Groups); got != 3 {
			t.Errorf("count %d: %d instances decoded, want the 3 the registers hold (warnings %q)", count, got, dm.Warnings)
		}
		if count > 3 && (len(dm.Warnings) != 1 || !strings.Contains(dm.Warnings[0], "registers end before instance 4")) {
			t.Errorf("count %d: warnings %q, want the instance that is not there", count, dm.Warnings)
		}
	}
}

// undersizedCountSchema is a caller's schema whose count point NPt has fewer
// registers than its type needs, followed by a writable point W.
func undersizedCountSchema(countType string, countSize int) *registry.ModelMeta {
	return &registry.ModelMeta{ID: 64060, Name: "m", Group: &registry.GroupMeta{
		Name: "m", Count: 1, Length: 3 + countSize,
		Points: append(boundsHeader(),
			registry.PointMeta{Name: "NPt", Type: countType, Size: countSize, Offset: 2, Access: "R"},
			registry.PointMeta{Name: "W", Type: "uint16", Size: 1, Offset: 2 + countSize, Access: "RW"},
		),
		Groups: []*registry.GroupMeta{{Name: "Pt", CountPoint: "NPt", Length: 1,
			Points: []registry.PointMeta{{Name: "V", Type: "uint16", Size: 1, Offset: 0}}}},
	}}
}

// TestEncodeUndersizedCountPoint: a count point with fewer registers than its
// type needs is refused with a *EncodeError naming it, whether its value is
// given as a number, as its registers or not at all.
func TestEncodeUndersizedCountPoint(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		size int
		raw  []uint16
	}{
		{"uint32", 1, []uint16{5}},
		{"acc32", 1, []uint16{0}},
		{"uint16", 0, []uint16{}},
	} {
		meta := undersizedCountSchema(tc.typ, tc.size)
		for _, vals := range []*Values{
			(&Values{}).Set("NPt", 1),
			(&Values{}).Set("NPt", tc.raw),
			{},
		} {
			_, err := EncodeModel(meta, vals)
			ee, ok := errors.AsType[*EncodeError](err)
			if !ok || ee.PointName != "NPt" || !strings.Contains(err.Error(), "needs") {
				t.Errorf("%s of %d registers, NPt %v: error %v, want a *EncodeError for NPt", tc.typ, tc.size, vals.Points, err)
			}
		}
	}
}
