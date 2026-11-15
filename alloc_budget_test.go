// SPDX-License-Identifier: MIT

package sunspec

import (
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// allocated returns the bytes fn allocates. The tests of this package do not
// run in parallel, so nothing else allocates meanwhile.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// The memory DecodeModel and Values may take for each register they are
// given (LIMITATIONS.md, "Memory"). Measured worst cases at the maximum
// length: 923 bytes per register to decode (model 64410) and 410 for Values
// (model 303); under the race detector 1228 and 442.
const (
	decodeBudgetPerRegister = 2048
	valuesBudgetPerRegister = 768
	// budgetFloor covers what does not grow with the registers: the model,
	// its top-level group and a warning.
	budgetFloor = 8 << 10
	// budgetLength is the length every model is decoded at: long enough to
	// show what a register costs, in a fraction of the time the maximum
	// length takes.
	budgetLength = 4096
)

// budgetAtMaximum lists the models that are also decoded at the longest
// length a device can hold, 65535 data registers: the ones that take the most
// memory per register to decode (64410, then the 13x and 14x curve models, of
// which 144 is one) and for Values (303, 3), and the nested curves of 705.
var budgetAtMaximum = []uint16{3, 144, 303, 705, 64410}

// TestDecodeModelAllocationBudget: decoding allocates for the registers that
// are there, not for what they announce. Every model of the registry is
// decoded from registers that all hold 0x7FFF, so that every count point
// announces 32767 instances and every scale factor is out of range, which
// adds a warning for each point it scales: once with only the model's own
// points, where the announced instances have no registers behind them, and
// once with budgetLength registers; the models of budgetAtMaximum also with
// the longest model a device can hold.
func TestDecodeModelAllocationBudget(t *testing.T) {
	var worstDecode, worstValues float64
	var worstDecodeID, worstValuesID uint16
	for _, meta := range registry.All() {
		lengths := []int{meta.FixedLength(), max(meta.FixedLength(), budgetLength)}
		if slices.Contains(budgetAtMaximum, meta.ID) {
			lengths = append(lengths, maxModelRegisters)
		}
		for _, length := range lengths {
			regs := make([]uint16, length)
			for i := range regs {
				regs[i] = 0x7FFF
			}
			regs[0], regs[1] = meta.ID, mustUint16(length-2)

			var dm *DecodedModel
			var err error
			decode := allocated(func() { dm, err = DecodeModel(regs, meta, 0) })
			if err != nil {
				t.Fatalf("model %d, %d registers: %v", meta.ID, length, err)
			}
			values := allocated(func() { _ = dm.Values() })

			if limit := uint64(budgetFloor + decodeBudgetPerRegister*length); decode > limit {
				t.Errorf("model %d: DecodeModel of %d registers allocated %d bytes, budget %d (%d per register)",
					meta.ID, length, decode, limit, decodeBudgetPerRegister)
			}
			if limit := uint64(budgetFloor + valuesBudgetPerRegister*length); values > limit {
				t.Errorf("model %d: Values of %d registers allocated %d bytes, budget %d (%d per register)",
					meta.ID, length, values, limit, valuesBudgetPerRegister)
			}
			if length == maxModelRegisters {
				if per := float64(decode) / float64(length); per > worstDecode {
					worstDecode, worstDecodeID = per, meta.ID
				}
				if per := float64(values) / float64(length); per > worstValues {
					worstValues, worstValuesID = per, meta.ID
				}
			}
		}
	}
	t.Logf("at %d registers: DecodeModel at most %.0f bytes per register (model %d), Values %.0f (model %d)",
		maxModelRegisters, worstDecode, worstDecodeID, worstValues, worstValuesID)
}

// countPointNames adds the names of the count points of g's nested groups,
// at any depth, to out.
func countPointNames(g *registry.GroupMeta, out map[string]bool) {
	for _, c := range g.Groups {
		if c.CountPoint != "" {
			out[c.CountPoint] = true
		}
		countPointNames(c, out)
	}
}

// fillByType returns regs, as decoded with meta, with every point other than
// the header and the count points filled with a pattern the device chooses
// for its type: sf in every scale factor, every bit of a bitfield set (but
// the not-implemented value), strings without a NUL, the largest finite
// floats. The count points keep their value, so the layout is the one regs
// gives.
func fillByType(t *testing.T, meta *registry.ModelMeta, regs []uint16, sf uint16) []uint16 {
	t.Helper()
	dm, err := DecodeModel(regs, meta, 0)
	if err != nil {
		t.Fatalf("model %d: %v", meta.ID, err)
	}
	counts := map[string]bool{}
	countPointNames(meta.Group, counts)
	out := slices.Clone(regs)
	dm.Group.Walk(func(g *DecodedGroup) {
		for _, p := range g.Points {
			o, n := p.RegisterOffset, p.RegisterCount
			if counts[p.Name] || o < 2 || n <= 0 || o+n > len(out) {
				continue
			}
			fill := uint16(0x1234)
			switch p.Type {
			case "sunssf":
				fill = sf
			case "bitfield16", "bitfield32", "bitfield64":
				fill = 0xFFFF
			case "string":
				fill = 0x4141
			case "float32", "float64":
				fill = 0x7F7F
			}
			for i := o; i < o+n; i++ {
				out[i] = fill
			}
			if strings.HasPrefix(p.Type, "bitfield") {
				out[o+n-1] = 0xFFFE
			}
		}
	})
	return out
}

// TestDecodeModelAllocationBudgetByType: the budget of
// TestDecodeModelAllocationBudget holds for register patterns chosen per
// point type, with scale factors that are usable, at the edge of the range
// and not implemented, at budgetLength registers.
func TestDecodeModelAllocationBudgetByType(t *testing.T) {
	for _, meta := range registry.All() {
		length := max(meta.FixedLength(), budgetLength)
		base := make([]uint16, length)
		for i := range base {
			base[i] = 0x7FFF
		}
		base[0], base[1] = meta.ID, mustUint16(length-2)
		for _, sf := range []uint16{0x0002, 0xFFF6, 0x8000} {
			regs := fillByType(t, meta, base, sf)
			var dm *DecodedModel
			decode := allocated(func() { dm, _ = DecodeModel(regs, meta, 0) })
			values := allocated(func() { _ = dm.Values() })
			if limit := uint64(budgetFloor + decodeBudgetPerRegister*length); decode > limit {
				t.Errorf("model %d, scale factors %#04x: DecodeModel of %d registers allocated %d bytes, budget %d",
					meta.ID, sf, length, decode, limit)
			}
			if limit := uint64(budgetFloor + valuesBudgetPerRegister*length); values > limit {
				t.Errorf("model %d, scale factors %#04x: Values of %d registers allocated %d bytes, budget %d",
					meta.ID, sf, length, values, limit)
			}
		}
	}
}

// TestDecodeCountAllocationBudget: a 32-bit count point that announces more
// instances than any model holds costs no more than the instances that are
// there.
func TestDecodeCountAllocationBudget(t *testing.T) {
	sub := &registry.GroupMeta{Name: "Sub", CountPoint: "N", Length: 1,
		Points: []registry.PointMeta{{Name: "B", Type: "uint16", Size: 1, Offset: 0}}}
	meta := &registry.ModelMeta{ID: 64042, Name: "counted", Group: &registry.GroupMeta{
		Name: "counted", Count: 1, Length: 4, Groups: []*registry.GroupMeta{sub},
		Points: append(boundsHeader(), registry.PointMeta{Name: "N", Type: "uint32", Size: 2, Offset: 2}),
	}}
	for _, count := range []uint32{3, maxModelRegisters, maxModelRegisters + 1, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFE} {
		regs := []uint16{64042, 5, uint16(count >> 16), uint16(count), 10, 11, 12}
		var dm *DecodedModel
		got := allocated(func() { dm, _ = DecodeModel(regs, meta, 0) })
		if n := len(dm.Group.Groups); n != 3 {
			t.Errorf("count %d: %d instances decoded, want 3", count, n)
		}
		if limit := uint64(budgetFloor + decodeBudgetPerRegister*len(regs)); got > limit {
			t.Errorf("count %d: DecodeModel of %d registers allocated %d bytes, budget %d", count, len(regs), got, limit)
		}
	}
}
