// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// Baseline benchmarks for reading a SunSpec device (run with make bench).
// End-to-end benchmarks report requests/op and pdu-bytes/op next to time and
// allocations, at each of benchDelays, on an inverter chain: common (1),
// three-phase inverter (103) and its nameplate, settings, status and
// controls (120-123). Allocations include the in-process server's.

// benchDelays are the per-request delays the end-to-end benchmarks run at:
// a loopback link, and two delays in the range of serial RTU links.
var benchDelays = []time.Duration{0, 5 * time.Millisecond, 20 * time.Millisecond}

// benchModels is the model chain of the benchmark device.
var benchModels = []uint16{1, 103, 120, 121, 122, 123}

// delayedHandler forwards requests to a handler after a delay per request,
// and counts requests and PDU bytes.
type delayedHandler struct {
	target   modbus.RequestHandler
	delay    time.Duration
	requests atomic.Int64
	pduBytes atomic.Int64
}

func (h *delayedHandler) reset() {
	h.requests.Store(0)
	h.pduBytes.Store(0)
}

func (h *delayedHandler) report(b *testing.B) {
	b.ReportMetric(float64(h.requests.Load())/float64(b.N), "requests/op")
	b.ReportMetric(float64(h.pduBytes.Load())/float64(b.N), "pdu-bytes/op")
}

// wait counts one register read of quantity registers and waits the delay.
func (h *delayedHandler) wait(quantity uint16) {
	h.requests.Add(1)
	// Request: function code, address, quantity. Response: function code,
	// byte count, data.
	h.pduBytes.Add(5 + 2 + 2*int64(quantity))
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
}

func (h *delayedHandler) HandleCoils(ctx context.Context, req *modbus.CoilsRequest) ([]bool, error) {
	return h.target.HandleCoils(ctx, req)
}

func (h *delayedHandler) HandleDiscreteInputs(ctx context.Context, req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return h.target.HandleDiscreteInputs(ctx, req)
}

func (h *delayedHandler) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if !req.IsWrite {
		h.wait(req.Quantity)
	}
	return h.target.HandleHoldingRegisters(ctx, req)
}

func (h *delayedHandler) HandleInputRegisters(ctx context.Context, req *modbus.InputRegistersRequest) ([]uint16, error) {
	h.wait(req.Quantity)
	return h.target.HandleInputRegisters(ctx, req)
}

// benchDevice serves the benchmark chain behind a delayedHandler and returns
// the discovered device and the handler, its counters reset.
func benchDevice(b *testing.B, delay time.Duration) (*Device, *delayedHandler) {
	b.Helper()
	d := newServed(b, 1, &ServerOptions{BaseAddress: 40000}, benchModels...)
	h := &delayedHandler{target: d.srv, delay: delay}
	addr, _ := startModbus(b, h)
	client := dial(b, addr, 10*time.Second)
	dev, err := Discover(context.Background(), client, nil)
	if err != nil {
		b.Fatalf("Discover: %v", err)
	}
	if len(dev.Discovery.Models) != len(benchModels) {
		b.Fatalf("discovered %d models, want %d", len(dev.Discovery.Models), len(benchModels))
	}
	h.reset()
	return dev, h
}

// runE2E runs read on a fresh benchmark device at every delay.
func runE2E(b *testing.B, read func(ctx context.Context, dev *Device) error) {
	for _, delay := range benchDelays {
		b.Run(fmt.Sprintf("delay=%s", delay), func(b *testing.B) {
			dev, h := benchDevice(b, delay)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := read(ctx, dev); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			h.report(b)
		})
	}
}

// BenchmarkReadAll reads and decodes the whole chain, one request per model.
func BenchmarkReadAll(b *testing.B) {
	runE2E(b, func(ctx context.Context, dev *Device) error {
		_, err := dev.ReadAll(ctx)
		return err
	})
}

// BenchmarkReadModels reads and decodes the whole chain with ReadModels,
// adjacent models sharing requests. Compare with BenchmarkReadAll.
func BenchmarkReadModels(b *testing.B) {
	runE2E(b, func(ctx context.Context, dev *Device) error {
		_, err := dev.ReadModels(ctx, dev.Discovery.Models)
		return err
	})
}

// BenchmarkReadModel reads and decodes the inverter model (103).
func BenchmarkReadModel(b *testing.B) {
	runE2E(b, func(ctx context.Context, dev *Device) error {
		_, err := dev.ReadModelByID(ctx, 103)
		return err
	})
}

// BenchmarkReadPoint reads the inverter's AC power (103, W), which reads and
// decodes the whole model.
func BenchmarkReadPoint(b *testing.B) {
	runE2E(b, func(ctx context.Context, dev *Device) error {
		_, err := dev.ReadPoint(ctx, *dev.ModelByID(103), "W")
		return err
	})
}

// BenchmarkDecodeModel measures decoding alone (no I/O), per model of the
// benchmark chain.
func BenchmarkDecodeModel(b *testing.B) {
	d := newServed(b, 1, &ServerOptions{BaseAddress: 40000}, benchModels...)
	for i, id := range benchModels {
		meta := registry.ByID(id)
		regs, err := EncodeModel(meta, d.gens[i].vals)
		if err != nil {
			b.Fatalf("EncodeModel(%d): %v", id, err)
		}
		b.Run(fmt.Sprintf("model=%d", id), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				if _, err := DecodeModel(regs, meta, 40002); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
