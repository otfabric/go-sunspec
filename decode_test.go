// SPDX-License-Identifier: MIT

package sunspec

import (
	"math"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

func TestDecodeInt16(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "int16", Size: 1}
	dp, _ := decodePoint([]uint16{1234}, pm)
	if dp.RawValue != int16(1234) {
		t.Errorf("got %v, want 1234", dp.RawValue)
	}
	if !dp.Implemented {
		t.Error("should be implemented")
	}
}

func TestDecodeInt16Sentinel(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "int16", Size: 1}
	dp, _ := decodePoint([]uint16{0x8000}, pm)
	if dp.Implemented {
		t.Error("should not be implemented (sentinel 0x8000)")
	}
}

func TestDecodeUint16(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "uint16", Size: 1}
	dp, _ := decodePoint([]uint16{42}, pm)
	if dp.RawValue != uint16(42) {
		t.Errorf("got %v, want 42", dp.RawValue)
	}
}

func TestDecodeUint16Sentinel(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "uint16", Size: 1}
	dp, _ := decodePoint([]uint16{0xFFFF}, pm)
	if dp.Implemented {
		t.Error("should not be implemented (sentinel 0xFFFF)")
	}
}

func TestDecodeInt32(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "int32", Size: 2}
	dp, _ := decodePoint([]uint16{0x0000, 0x0064}, pm)
	if dp.RawValue != int32(100) {
		t.Errorf("got %v, want 100", dp.RawValue)
	}
}

func TestDecodeUint32(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "uint32", Size: 2}
	dp, _ := decodePoint([]uint16{0x0001, 0x0000}, pm)
	if dp.RawValue != uint32(65536) {
		t.Errorf("got %v, want 65536", dp.RawValue)
	}
}

func TestDecodeInt64(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "int64", Size: 4}
	dp, _ := decodePoint([]uint16{0, 0, 0, 1}, pm)
	if dp.RawValue != int64(1) {
		t.Errorf("got %v, want 1", dp.RawValue)
	}
}

func TestDecodeUint64(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "uint64", Size: 4}
	dp, _ := decodePoint([]uint16{0, 0, 0, 99}, pm)
	if dp.RawValue != uint64(99) {
		t.Errorf("got %v, want 99", dp.RawValue)
	}
}

func TestDecodeFloat32(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "float32", Size: 2}
	bits := math.Float32bits(3.14)
	dp, _ := decodePoint([]uint16{uint16(bits >> 16), uint16(bits)}, pm)
	v, ok := dp.RawValue.(float32)
	if !ok {
		t.Fatalf("expected float32, got %T", dp.RawValue)
	}
	if math.Abs(float64(v)-3.14) > 0.001 {
		t.Errorf("got %v, want ~3.14", v)
	}
}

func TestDecodeFloat64(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "float64", Size: 4}
	bits := math.Float64bits(2.718)
	dp, _ := decodePoint([]uint16{
		uint16(bits >> 48), uint16(bits >> 32),
		uint16(bits >> 16), uint16(bits),
	}, pm)
	v, ok := dp.RawValue.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", dp.RawValue)
	}
	if math.Abs(v-2.718) > 0.001 {
		t.Errorf("got %v, want ~2.718", v)
	}
}

func TestDecodeString(t *testing.T) {
	pm := &registry.PointMeta{Name: "Mn", Type: "string", Size: 4}
	regs := []uint16{0x5465, 0x7374, 0x4d66, 0x6700} // "TestMfg\0"
	dp, _ := decodePoint(regs, pm)
	if dp.RawValue != "TestMfg" {
		t.Errorf("got %q, want %q", dp.RawValue, "TestMfg")
	}
}

func TestDecodeStringTrailingSpaces(t *testing.T) {
	pm := &registry.PointMeta{Name: "V", Type: "string", Size: 2}
	regs := []uint16{0x4142, 0x2020} // "AB  "
	dp, _ := decodePoint(regs, pm)
	if dp.RawValue != "AB" {
		t.Errorf("got %q, want %q", dp.RawValue, "AB")
	}
}

func TestDecodeEnum16(t *testing.T) {
	pm := &registry.PointMeta{
		Name: "St", Type: "enum16", Size: 1,
		Symbols: []registry.SymbolMeta{
			{Name: "OFF", Value: 1},
			{Name: "ON", Value: 4},
		},
	}
	dp, _ := decodePoint([]uint16{4}, pm)
	if dp.RawValue != uint16(4) {
		t.Errorf("got %v, want 4", dp.RawValue)
	}
	if len(dp.Symbols) != 1 || dp.Symbols[0] != "ON" {
		t.Errorf("symbols = %v, want [ON]", dp.Symbols)
	}
}

func TestDecodeBitfield32(t *testing.T) {
	pm := &registry.PointMeta{
		Name: "Evt", Type: "bitfield32", Size: 2,
		Symbols: []registry.SymbolMeta{
			{Name: "GROUND_FAULT", Value: 0},
			{Name: "DC_OVER_VOLT", Value: 1},
			{Name: "AC_DISCONNECT", Value: 2},
		},
	}
	// bits 0 and 2 set = 0x0005
	dp, _ := decodePoint([]uint16{0, 5}, pm)
	if len(dp.Symbols) != 2 || dp.Symbols[0] != "GROUND_FAULT" || dp.Symbols[1] != "AC_DISCONNECT" {
		t.Errorf("symbols = %v, want [GROUND_FAULT, AC_DISCONNECT]", dp.Symbols)
	}
}

func TestDecodeIPAddr(t *testing.T) {
	pm := &registry.PointMeta{Name: "IP", Type: "ipaddr", Size: 2}
	// 192.168.1.100
	dp, _ := decodePoint([]uint16{0xC0A8, 0x0164}, pm)
	if dp.RawValue != "192.168.1.100" {
		t.Errorf("got %q, want %q", dp.RawValue, "192.168.1.100")
	}
}

func TestDecodeEUI48(t *testing.T) {
	pm := &registry.PointMeta{Name: "MAC", Type: "eui48", Size: 4}
	// aa:bb:cc:dd:ee:ff
	dp, _ := decodePoint([]uint16{0xAABB, 0xCCDD, 0xEEFF, 0x0000}, pm)
	if dp.RawValue != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("got %q, want %q", dp.RawValue, "aa:bb:cc:dd:ee:ff")
	}
}

func TestDecodeUnknownType(t *testing.T) {
	pm := &registry.PointMeta{Name: "X", Type: "custom_thing", Size: 2}
	_, warn := decodePoint([]uint16{0x1234, 0x5678}, pm)
	if warn == "" {
		t.Error("expected warning for unknown type")
	}
}

func TestScaleFactorResolution(t *testing.T) {
	meta := &registry.ModelMeta{
		ID:    101,
		Name:  "test_inverter",
		Label: "Test Inverter",
		FixedBlock: &registry.GroupMeta{
			Name:   "test",
			Length: 3,
			Points: []registry.PointMeta{
				{Name: "W", Type: "int16", Size: 1, Offset: 0, SF: "W_SF"},
				{Name: "W_SF", Type: "sunssf", Size: 1, Offset: 1},
				{Name: "V", Type: "int16", Size: 1, Offset: 2, SF: "-1", SFIsLiteral: true, SFLiteral: -1},
			},
		},
	}

	// W=1000, W_SF=-2, V=2345
	regs := []uint16{1000, 0xFFFE, 2345}
	dm, err := DecodeModel(regs, meta, 40002)
	if err != nil {
		t.Fatal(err)
	}

	// Check W scaled value: 1000 * 10^(-2) = 10.0
	wPoint := dm.FixedBlock.Points[0]
	if wPoint.ScaledValue == nil {
		t.Fatal("W ScaledValue is nil")
	}
	if math.Abs(*wPoint.ScaledValue-10.0) > 0.001 {
		t.Errorf("W scaled = %v, want 10.0", *wPoint.ScaledValue)
	}
	if wPoint.SFRawValue == nil || *wPoint.SFRawValue != -2 {
		t.Errorf("W SFRawValue = %v, want -2", wPoint.SFRawValue)
	}

	// Check V with literal SF -1: 2345 * 10^(-1) = 234.5
	vPoint := dm.FixedBlock.Points[2]
	if vPoint.ScaledValue == nil {
		t.Fatal("V ScaledValue is nil")
	}
	if math.Abs(*vPoint.ScaledValue-234.5) > 0.01 {
		t.Errorf("V scaled = %v, want 234.5", *vPoint.ScaledValue)
	}
}

func TestRepeatingBlockDecode(t *testing.T) {
	meta := &registry.ModelMeta{
		ID:   160,
		Name: "test_mppt",
		FixedBlock: &registry.GroupMeta{
			Name:   "mppt",
			Length: 2,
			Points: []registry.PointMeta{
				{Name: "ID", Type: "uint16", Size: 1, Offset: 0},
				{Name: "L", Type: "uint16", Size: 1, Offset: 1},
			},
		},
		RepeatingBlock: &registry.GroupMeta{
			Name:      "module",
			Length:    3,
			Repeating: true,
			Points: []registry.PointMeta{
				{Name: "DCA", Type: "uint16", Size: 1, Offset: 0},
				{Name: "DCV", Type: "uint16", Size: 1, Offset: 1},
				{Name: "DCW", Type: "uint16", Size: 1, Offset: 2},
			},
		},
	}

	// Fixed: ID=160, L=11. Repeating: 3 instances of 3 registers each.
	regs := []uint16{
		160, 11,
		10, 300, 3000,
		20, 310, 6200,
		30, 320, 9600,
	}

	dm, err := DecodeModel(regs, meta, 40070)
	if err != nil {
		t.Fatal(err)
	}

	if len(dm.RepeatingBlocks) != 3 {
		t.Fatalf("got %d repeating blocks, want 3", len(dm.RepeatingBlocks))
	}

	// Check first repeating block
	rb0 := dm.RepeatingBlocks[0]
	if rb0.GroupIndex != 1 {
		t.Errorf("block 0 GroupIndex = %d, want 1", rb0.GroupIndex)
	}
	if rb0.Points[0].RawValue != uint16(10) {
		t.Errorf("block 0 DCA = %v, want 10", rb0.Points[0].RawValue)
	}

	// Check third repeating block
	rb2 := dm.RepeatingBlocks[2]
	if rb2.GroupIndex != 3 {
		t.Errorf("block 2 GroupIndex = %d, want 3", rb2.GroupIndex)
	}
	if rb2.Points[2].RawValue != uint16(9600) {
		t.Errorf("block 2 DCW = %v, want 9600", rb2.Points[2].RawValue)
	}
}

func TestDecodeSunssfPoint(t *testing.T) {
	pm := &registry.PointMeta{Name: "W_SF", Type: "sunssf", Size: 1}
	dp, _ := decodePoint([]uint16{0xFFFE}, pm)
	if dp.RawValue != int16(-2) {
		t.Errorf("got %v, want -2", dp.RawValue)
	}
	if !dp.Implemented {
		t.Error("should be implemented")
	}
}

func TestDecodeAccumulators(t *testing.T) {
	// acc16 with value 0 = not implemented
	pm := &registry.PointMeta{Name: "WH", Type: "acc16", Size: 1}
	dp, _ := decodePoint([]uint16{0}, pm)
	if dp.Implemented {
		t.Error("acc16 with 0 should not be implemented")
	}

	// acc32 with non-zero
	pm32 := &registry.PointMeta{Name: "WH", Type: "acc32", Size: 2}
	dp32, _ := decodePoint([]uint16{0, 100}, pm32)
	if !dp32.Implemented {
		t.Error("acc32 with 100 should be implemented")
	}
	if dp32.RawValue != uint32(100) {
		t.Errorf("acc32 raw = %v, want 100", dp32.RawValue)
	}
}
