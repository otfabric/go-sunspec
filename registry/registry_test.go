// SPDX-License-Identifier: MIT

package registry_test

import (
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

func TestByIDCommonModel(t *testing.T) {
	m := registry.ByID(1)
	if m == nil {
		t.Fatal("ByID(1) returned nil, expected Common model")
	}
	if m.Name != "common" {
		t.Errorf("Name = %q, want %q", m.Name, "common")
	}
	if m.Label != "Common" {
		t.Errorf("Label = %q, want %q", m.Label, "Common")
	}
	if m.FixedBlock == nil {
		t.Fatal("FixedBlock is nil")
	}
	if m.FixedBlock.Length != 68 {
		t.Errorf("FixedBlock.Length = %d, want 68", m.FixedBlock.Length)
	}
	if m.RepeatingBlock != nil {
		t.Errorf("RepeatingBlock should be nil for model 1")
	}
	points := m.FixedBlock.Points
	names := make(map[string]bool)
	for _, p := range points {
		names[p.Name] = true
	}
	for _, want := range []string{"ID", "L", "Mn", "Md", "SN", "DA"} {
		if !names[want] {
			t.Errorf("missing expected point %q", want)
		}
	}
}

func TestByIDInverterModel(t *testing.T) {
	m := registry.ByID(101)
	if m == nil {
		t.Fatal("ByID(101) returned nil, expected inverter model")
	}
	if m.Name != "inverter_single_phase" {
		t.Errorf("Name = %q, want %q", m.Name, "inverter_single_phase")
	}
	if m.FixedBlock == nil {
		t.Fatal("FixedBlock is nil")
	}
	pointMap := make(map[string]registry.PointMeta)
	for _, p := range m.FixedBlock.Points {
		pointMap[p.Name] = p
	}
	a, ok := pointMap["A"]
	if !ok {
		t.Fatal("point A not found in model 101")
	}
	if a.SF != "A_SF" {
		t.Errorf("point A SF = %q, want %q", a.SF, "A_SF")
	}
	asf, ok := pointMap["A_SF"]
	if !ok {
		t.Fatal("point A_SF not found in model 101")
	}
	if asf.Type != "sunssf" {
		t.Errorf("A_SF type = %q, want %q", asf.Type, "sunssf")
	}
}

func TestKnownUnknown(t *testing.T) {
	if !registry.Known(1) {
		t.Error("Known(1) = false, want true")
	}
	if registry.Known(65535) {
		t.Error("Known(65535) = true, want false")
	}
}

func TestCountPositive(t *testing.T) {
	c := registry.Count()
	if c < 100 {
		t.Errorf("Count() = %d, want >= 100", c)
	}
}

func TestFixedAndRepeatingLength(t *testing.T) {
	common := registry.ByID(1)
	if common == nil {
		t.Fatal("ByID(1) returned nil")
	}
	if got := common.FixedLength(); got != 68 {
		t.Errorf("model 1 FixedLength() = %d, want 68", got)
	}
	if got := common.RepeatingLength(); got != 0 {
		t.Errorf("model 1 RepeatingLength() = %d, want 0", got)
	}

	// Model 160 (MPPT) has a repeating block in the compiled registry.
	mppt := registry.ByID(160)
	if mppt == nil {
		t.Fatal("ByID(160) returned nil")
	}
	if got := mppt.FixedLength(); got <= 0 {
		t.Errorf("model 160 FixedLength() = %d, want > 0", got)
	}
	if got := mppt.RepeatingLength(); got <= 0 {
		t.Errorf("model 160 RepeatingLength() = %d, want > 0", got)
	}

	empty := &registry.ModelMeta{}
	if empty.FixedLength() != 0 || empty.RepeatingLength() != 0 {
		t.Errorf("nil blocks: FixedLength=%d RepeatingLength=%d, want 0/0",
			empty.FixedLength(), empty.RepeatingLength())
	}
}

func TestPointOffsetsSumToGroupLength(t *testing.T) {
	all := registry.All()
	for id, m := range all {
		for _, block := range []*registry.GroupMeta{m.FixedBlock, m.RepeatingBlock} {
			if block == nil {
				continue
			}
			sum := 0
			for _, p := range block.Points {
				sum += p.Size
			}
			if sum != block.Length {
				t.Errorf("model %d block %q: point sizes sum to %d, group length is %d",
					id, block.Name, sum, block.Length)
			}
			if len(block.Points) > 0 {
				last := block.Points[len(block.Points)-1]
				end := last.Offset + last.Size
				if end != block.Length {
					t.Errorf("model %d block %q: last point ends at %d, group length is %d",
						id, block.Name, end, block.Length)
				}
			}
		}
	}
}
