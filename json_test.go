// SPDX-License-Identifier: MIT

package sunspec_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// A float point the device reports as not implemented holds NaN, which JSON
// cannot represent: it must be encoded as null instead of failing the encoding.
func TestDecodedPoint_MarshalJSON_NonFiniteAsNull(t *testing.T) {
	inf := math.Inf(1)
	ok := 12.5
	cases := []struct {
		name  string
		point sunspec.DecodedPoint
		want  []string
	}{
		{"float32 NaN", sunspec.DecodedPoint{Name: "A", Type: "float32", RawValue: float32(math.NaN())}, []string{`"RawValue":null`, `"Implemented":false`}},
		{"float64 NaN", sunspec.DecodedPoint{Name: "A", Type: "float64", RawValue: math.NaN()}, []string{`"RawValue":null`}},
		{"float32 infinity", sunspec.DecodedPoint{Name: "A", Type: "float32", RawValue: float32(math.Inf(-1))}, []string{`"RawValue":null`}},
		{"infinite scaled value", sunspec.DecodedPoint{Name: "A", Type: "int16", RawValue: int16(1), ScaledValue: &inf, Implemented: true}, []string{`"RawValue":1`, `"ScaledValue":null`}},
		{"finite values are kept", sunspec.DecodedPoint{Name: "W", Type: "float32", RawValue: float32(1.5), ScaledValue: &ok, Units: "W", Implemented: true, Symbols: []string{"ON"}},
			[]string{`"Name":"W"`, `"Type":"float32"`, `"RawValue":1.5`, `"ScaledValue":12.5`, `"Units":"W"`, `"Implemented":true`, `"Symbols":["ON"]`, `"SFName":""`, `"SFRawValue":null`, `"RegisterOffset":0`, `"RegisterCount":0`}},
		{"other types pass through", sunspec.DecodedPoint{Name: "Mn", Type: "string", RawValue: "ACME", Implemented: true}, []string{`"RawValue":"ACME"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both the value and a pointer must use the custom encoding.
			for _, v := range []interface{}{tc.point, &tc.point} {
				b, err := json.Marshal(v)
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				for _, want := range tc.want {
					if !strings.Contains(string(b), want) {
						t.Errorf("JSON %s does not contain %s", b, want)
					}
				}
			}
		})
	}
}

// A whole decoded float model with unimplemented points must encode to JSON.
func TestDecodedModel_JSONWithUnimplementedFloats(t *testing.T) {
	meta := registry.ByID(111)
	if meta == nil {
		t.Fatal("model 111 schema missing")
	}
	// Every register 0x7FC0/0x0000 pattern: float32 points decode to NaN.
	regs := make([]uint16, meta.FixedLength())
	regs[0], regs[1] = 111, uint16(meta.FixedLength()-2)
	for i := 2; i+1 < len(regs); i += 2 {
		regs[i], regs[i+1] = 0x7FC0, 0x0000
	}
	dm, err := sunspec.DecodeModel(regs, meta, 40000)
	if err != nil {
		t.Fatalf("DecodeModel: %v", err)
	}
	nan := 0
	for _, p := range dm.Group.Points {
		if f, isFloat := p.RawValue.(float32); isFloat && math.IsNaN(float64(f)) {
			nan++
			if p.Implemented {
				t.Errorf("point %s is NaN but marked implemented", p.Name)
			}
		}
	}
	if nan == 0 {
		t.Fatal("test setup: no NaN points decoded")
	}
	b, err := json.Marshal(dm)
	if err != nil {
		t.Fatalf("a model with %d unimplemented float points does not encode: %v", nan, err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

// Scaling by a negative exponent must not introduce float artefacts: 95 with
// scale factor -2 is 0.95, not 0.9500000000000001.
func TestScaledValue_NegativeExponentIsExact(t *testing.T) {
	meta := &registry.ModelMeta{ID: 9, Name: "m", Group: &registry.GroupMeta{Name: "m", Count: 1, Length: 4, Points: []registry.PointMeta{
		{Name: "PF", Type: "uint16", Size: 1, Offset: 0, SF: "PF_SF"},
		{Name: "PF_SF", Type: "sunssf", Size: 1, Offset: 1},
		{Name: "E", Type: "uint16", Size: 1, Offset: 2, SF: "3"},
		{Name: "T", Type: "int16", Size: 1, Offset: 3, SF: "-1"},
	}}}
	dm, err := sunspec.DecodeModel([]uint16{95, 0xFFFE, 7, 0xFE9D}, meta, 0)
	if err != nil {
		t.Fatalf("DecodeModel: %v", err)
	}
	for name, want := range map[string]float64{"PF": 0.95, "E": 7000, "T": -35.5} {
		p := dm.Point(name)
		if p == nil || p.ScaledValue == nil || *p.ScaledValue != want {
			t.Errorf("%s: ScaledValue = %v, want exactly %v", name, p.ScaledValue, want)
		}
	}
}
