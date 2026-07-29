// SPDX-License-Identifier: MIT

package sunspec

import (
	"testing"

	"github.com/otfabric/go-modbus"
)

func TestToGMSunspecOptionsNil(t *testing.T) {
	var opts *DiscoverOptions
	if opts.toGMSunspecOptions() != nil {
		t.Fatal("nil DiscoverOptions should convert to nil")
	}
}

func TestToGMSunspecOptionsFields(t *testing.T) {
	opts := &DiscoverOptions{
		UnitID:         7,
		RegType:        modbus.HoldingRegister,
		BaseAddresses:  []uint16{40000, 50000},
		MaxModels:      32,
		MaxAddressSpan: 1000,
	}
	gm := opts.toGMSunspecOptions()
	if gm == nil {
		t.Fatal("expected non-nil options")
	}
	if gm.UnitID != 7 {
		t.Errorf("UnitID = %d, want 7", gm.UnitID)
	}
	if gm.RegType != modbus.HoldingRegister {
		t.Errorf("RegType = %v, want HoldingRegister", gm.RegType)
	}
	if len(gm.BaseAddresses) != 2 || gm.BaseAddresses[0] != 40000 || gm.BaseAddresses[1] != 50000 {
		t.Errorf("BaseAddresses = %v, want [40000 50000]", gm.BaseAddresses)
	}
	if gm.MaxModels != 32 {
		t.Errorf("MaxModels = %d, want 32", gm.MaxModels)
	}
	if gm.MaxAddressSpan != 1000 {
		t.Errorf("MaxAddressSpan = %d, want 1000", gm.MaxAddressSpan)
	}
}
