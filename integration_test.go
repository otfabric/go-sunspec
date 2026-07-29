// SPDX-License-Identifier: MIT

package sunspec_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/testutil"
)

// buildCommonModel1 builds register data for SunSpec model 1 (Common).
func buildCommonModel1() testutil.FixtureModel {
	// Model 1 schema has 68 total registers (ID + L + 66 data).
	// The wire Length = 66 (excludes the 2-register header).
	regs := make([]uint16, 66)

	// Mn (Manufacturer) at schema offset 2 → data index 0, 16 registers (32 chars)
	copy(regs[0:16], testutil.StringToRegisters("TestManufacturer", 16))
	// Md (Model) at schema offset 18 → data index 16, 16 registers
	copy(regs[16:32], testutil.StringToRegisters("TestInverter", 16))
	// Opt (Options) at schema offset 34 → data index 32, 8 registers
	copy(regs[32:40], testutil.StringToRegisters("1.0", 8))
	// Vr (Version) at schema offset 42 → data index 40, 8 registers
	copy(regs[40:48], testutil.StringToRegisters("2.0.0", 8))
	// SN (Serial) at schema offset 50 → data index 48, 16 registers
	copy(regs[48:64], testutil.StringToRegisters("SN-12345678", 16))
	// DA (Device Address) at schema offset 66 → data index 64
	regs[64] = 1
	// Pad at schema offset 67 → data index 65
	regs[65] = 0x8000

	return testutil.FixtureModel{
		ID:        1,
		Length:    66,
		Registers: regs,
	}
}

// buildInverterModel101 builds register data for model 101 (Single Phase Inverter).
func buildInverterModel101() testutil.FixtureModel {
	// Model 101 schema has 52 total registers (ID + L + 50 data).
	// The wire Length = 50 (excludes the 2-register header).
	regs := make([]uint16, 50)

	// A (Current) at schema offset 2 → data index 0 = 1234
	regs[0] = 1234
	// AphA (Phase A current) at schema offset 3 → data index 1 = 1234
	regs[1] = 1234
	// AphB (Phase B) at data index 2 = not implemented
	regs[2] = 0xFFFF
	// AphC (Phase C) at data index 3 = not implemented
	regs[3] = 0xFFFF
	// A_SF at schema offset 6 → data index 4 = -2 (scale factor)
	regs[4] = 0xFFFE // int16(-2)
	// PPVphAB at data index 5 = 2400
	regs[5] = 2400
	// PPVphBC at data index 6 = not implemented
	regs[6] = 0xFFFF
	// PPVphCA at data index 7 = not implemented
	regs[7] = 0xFFFF
	// PhVphA at data index 8 = 2400
	regs[8] = 2400
	// PhVphB at data index 9 = not implemented
	regs[9] = 0xFFFF
	// PhVphC at data index 10 = not implemented
	regs[10] = 0xFFFF
	// V_SF at data index 11 = -1
	regs[11] = 0xFFFF // int16(-1)
	// W at data index 12 = 5000
	regs[12] = 5000
	// W_SF at data index 13 = -1
	regs[13] = 0xFFFF // int16(-1)

	// Fill remaining with not-implemented sentinels
	for i := 14; i < 50; i++ {
		regs[i] = 0xFFFF
	}

	return testutil.FixtureModel{
		ID:        101,
		Length:    50,
		Registers: regs,
	}
}

func TestIntegrationDiscoverAndReadAll(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(40000, 1,
		buildCommonModel1(),
		buildInverterModel101(),
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15020)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	}

	// Test Discover
	device, err := sunspec.Discover(ctx, client, opts)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(device.Discovery.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(device.Discovery.Models))
	}

	// Check model 1
	m1 := device.Discovery.Models[0]
	if m1.Header.ID != 1 {
		t.Errorf("model 0 ID = %d, want 1", m1.Header.ID)
	}
	if !m1.SchemaKnown {
		t.Error("model 1 should have schema")
	}

	// Check model 101
	m101 := device.Discovery.Models[1]
	if m101.Header.ID != 101 {
		t.Errorf("model 1 ID = %d, want 101", m101.Header.ID)
	}

	// Test ReadAll
	results, err := device.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 decoded models, got %d", len(results))
	}

	// Validate model 1 decoded data
	dm1 := results[0]
	if dm1.ModelID != 1 {
		t.Errorf("decoded model 0 ID = %d, want 1", dm1.ModelID)
	}
	if dm1.FixedBlock == nil {
		t.Fatal("model 1 fixed block is nil")
	}

	// Find Manufacturer point
	for _, p := range dm1.FixedBlock.Points {
		if p.Name == "Mn" {
			if p.RawValue != "TestManufacturer" {
				t.Errorf("Mn = %q, want %q", p.RawValue, "TestManufacturer")
			}
			break
		}
	}

	// Find Serial Number
	for _, p := range dm1.FixedBlock.Points {
		if p.Name == "SN" {
			if p.RawValue != "SN-12345678" {
				t.Errorf("SN = %q, want %q", p.RawValue, "SN-12345678")
			}
			break
		}
	}
}

func TestIntegrationReadModelByID(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(40000, 1,
		buildCommonModel1(),
		buildInverterModel101(),
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15021)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	dm, err := device.ReadModelByID(ctx, 101)
	if err != nil {
		t.Fatalf("ReadModelByID(101) failed: %v", err)
	}
	if dm.ModelID != 101 {
		t.Errorf("ModelID = %d, want 101", dm.ModelID)
	}
	if dm.FixedBlock == nil {
		t.Fatal("fixed block is nil")
	}

	// Check current point A = 1234, A_SF = -2, scaled = 12.34
	for _, p := range dm.FixedBlock.Points {
		if p.Name == "A" {
			if p.RawValue != uint16(1234) {
				t.Errorf("A raw = %v, want 1234", p.RawValue)
			}
			if p.ScaledValue != nil {
				expected := 12.34
				if math.Abs(*p.ScaledValue-expected) > 0.01 {
					t.Errorf("A scaled = %v, want %v", *p.ScaledValue, expected)
				}
			}
			break
		}
	}
}

func TestIntegrationReadPoint(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(40000, 1,
		buildCommonModel1(),
		buildInverterModel101(),
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15022)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	inst := device.ModelByID(101)
	if inst == nil {
		t.Fatal("model 101 not found")
	}

	dp, err := device.ReadPoint(ctx, *inst, "A")
	if err != nil {
		t.Fatalf("ReadPoint failed: %v", err)
	}
	if dp.RawValue != uint16(1234) {
		t.Errorf("A raw = %v, want 1234", dp.RawValue)
	}

	_, err = device.ReadPoint(ctx, *inst, "DoesNotExist")
	if !errors.Is(err, sunspec.ErrPointNotFound) {
		t.Fatalf("missing point: got %v, want ErrPointNotFound", err)
	}
}

func buildMPPTModel160() testutil.FixtureModel {
	// Wire Length excludes the 2-register header. Fixed data = 8, one repeating instance = 20.
	regs := make([]uint16, 28)
	// Fixed data (schema offsets 2..9): SF, Evt, N, TmsPer
	regs[0] = 0 // DCA_SF
	regs[1] = 0 // DCV_SF
	regs[2] = 0 // DCW_SF
	regs[3] = 0 // DCWH_SF
	regs[4] = 0 // Evt hi
	regs[5] = 0 // Evt lo
	regs[6] = 1 // N
	regs[7] = 0 // TmsPer
	// Repeating instance starts at data index 8 (schema offsets 0..19 within block)
	regs[8] = 1                                               // ID
	copy(regs[9:17], testutil.StringToRegisters("MPPT-A", 8)) // IDStr
	regs[17] = 123                                            // DCA
	regs[18] = 456                                            // DCV
	regs[19] = 789                                            // DCW
	return testutil.FixtureModel{
		ID:        160,
		Length:    28,
		Registers: regs,
	}
}

func TestIntegrationReadPointRepeatingAndError(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(40000, 1,
		buildCommonModel1(),
		buildMPPTModel160(),
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15027)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	inst := device.ModelByID(160)
	if inst == nil {
		t.Fatal("model 160 not found")
	}

	dp, err := device.ReadPoint(ctx, *inst, "DCA")
	if err != nil {
		t.Fatalf("ReadPoint DCA: %v", err)
	}
	if dp.RawValue != uint16(123) {
		t.Errorf("DCA = %v, want 123", dp.RawValue)
	}

	_ = client.Close()
	_, err = device.ReadPoint(ctx, *inst, "DCA")
	if err == nil {
		t.Fatal("expected ReadPoint error after client close")
	}
}

func TestIntegrationDetectSuccess(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(40000, 1, buildCommonModel1())
	client, cleanup := testutil.StartServerClient(t, fixture, 15028)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := sunspec.Detect(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !result.Detected {
		t.Fatal("expected Detected=true")
	}
	if result.BaseAddress != 40000 {
		t.Errorf("BaseAddress = %d, want 40000", result.BaseAddress)
	}
	if result.UnitID != 1 {
		t.Errorf("UnitID = %d, want 1", result.UnitID)
	}
	if len(result.Attempts) == 0 {
		t.Fatal("expected at least one probe attempt")
	}
	matched := false
	for _, a := range result.Attempts {
		if a.Matched {
			matched = true
			break
		}
	}
	if !matched {
		t.Error("expected a matched probe attempt")
	}
}

func TestIntegrationDetectNotSunSpec(t *testing.T) {
	// Readable registers that are not a SunSpec marker (avoids illegal-address probe errors).
	handler := &testutil.SunSpecHandler{
		Registers: map[uint16]uint16{
			40000: 0x0000,
			40001: 0x0000,
		},
		UnitID: 1,
	}
	client, cleanup := testutil.StartServerClient(t, handler, 15023)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := sunspec.Detect(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if !errors.Is(err, sunspec.ErrNotSunSpec) {
		t.Fatalf("got %v, want ErrNotSunSpec", err)
	}
	if result == nil || result.Detected {
		t.Fatalf("expected Detected=false result, got %+v", result)
	}
}

func TestIntegrationDetectTransportError(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(40000, 1, buildCommonModel1())
	client, cleanup := testutil.StartServerClient(t, fixture, 15029)
	cleanup() // stop server immediately

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := sunspec.Detect(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err == nil {
		t.Fatal("expected Detect transport error against stopped server")
	}
	if errors.Is(err, sunspec.ErrNotSunSpec) {
		t.Fatalf("got ErrNotSunSpec, want transport/IO error: %v", err)
	}
}

func TestIntegrationUnknownModelInChain(t *testing.T) {
	unknownModel := testutil.FixtureModel{
		ID:        65000,
		Length:    4,
		Registers: []uint16{100, 200, 300, 400},
	}
	fixture := testutil.NewSunSpecFixture(40000, 1,
		buildCommonModel1(),
		unknownModel,
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15024)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	// Should have 2 models (1 + unknown)
	if len(device.Discovery.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(device.Discovery.Models))
	}

	// Unknown model should not have schema
	unknown := device.Discovery.Models[1]
	if unknown.SchemaKnown {
		t.Error("unknown model should not have schema")
	}

	// Should have warnings about unknown model
	if len(device.Discovery.Warnings) == 0 {
		t.Error("expected warnings for unknown model")
	}

	// ReadAll should still work
	results, err := device.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Unknown model result should have raw registers
	unknownResult := results[1]
	if len(unknownResult.RawRegisters) != 4 {
		t.Errorf("unknown model raw regs = %d, want 4", len(unknownResult.RawRegisters))
	}
}

func TestIntegrationDifferentBaseAddress(t *testing.T) {
	fixture := testutil.NewSunSpecFixture(0, 1,
		buildCommonModel1(),
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15025)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{0},
	})
	if err != nil {
		t.Fatalf("Discover at base 0 failed: %v", err)
	}

	if device.Discovery.BaseAddress != 0 {
		t.Errorf("BaseAddress = %d, want 0", device.Discovery.BaseAddress)
	}
	if len(device.Discovery.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(device.Discovery.Models))
	}
}

func TestIntegrationBatchRead(t *testing.T) {
	// Create a model with >125 registers to test batch splitting
	bigRegs := make([]uint16, 200)
	for i := range bigRegs {
		bigRegs[i] = uint16(i)
	}
	bigModel := testutil.FixtureModel{
		ID:        64999,
		Length:    200,
		Registers: bigRegs,
	}
	fixture := testutil.NewSunSpecFixture(40000, 1,
		buildCommonModel1(),
		bigModel,
	)
	client, cleanup := testutil.StartServerClient(t, fixture, 15026)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	device, err := sunspec.Discover(ctx, client, &sunspec.DiscoverOptions{
		UnitID:        1,
		BaseAddresses: []uint16{40000},
	})
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	// ReadAll should handle the >125 register model
	results, err := device.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	// Should have 2 models
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Big model should have 200 raw registers
	bigResult := results[1]
	if len(bigResult.RawRegisters) != 200 {
		t.Errorf("big model raw regs = %d, want 200", len(bigResult.RawRegisters))
	}
}
