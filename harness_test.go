// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

// This file holds what the end-to-end, chaos and stress tests share: a
// handler shim that can swap or distort the device behind a running Modbus
// server, and helpers that start servers and devices on loopback.

// literalSchema has a point with a literal scale factor and one whose scale
// factor is zero.
func literalSchema() *registry.ModelMeta {
	return pointSchema(64020,
		registry.PointMeta{Name: "Lit", Type: "int32", Size: 2, SF: "-3", Access: "R"},
		registry.PointMeta{Name: "Zero", Type: "uint16", Size: 1, SF: "Zero_SF", Access: "R"},
		registry.PointMeta{Name: "Zero_SF", Type: "sunssf", Size: 1, Access: "R"},
	)
}

// The schemas the tests add to the registry, so that a client decodes them:
// like the generated ones, they are registered during package initialization.
func init() {
	registry.Register(curveSchema())
	registry.Register(literalSchema())
	registry.Register(ctlSchema())
}

// readFilter post-processes the answer to a register read. regs and err are
// what the device answered; the filter returns what the client gets.
type readFilter func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error)

// shim is a modbus.RequestHandler in front of another one. The handler behind
// it can be swapped and its read answers filtered while a server is running.
type shim struct {
	mu     sync.RWMutex
	target modbus.RequestHandler
	filter readFilter

	reads  atomic.Int64
	writes atomic.Int64
}

func newShim(target modbus.RequestHandler) *shim { return &shim{target: target} }

func (s *shim) setTarget(h modbus.RequestHandler) {
	s.mu.Lock()
	s.target = h
	s.mu.Unlock()
}

func (s *shim) setFilter(f readFilter) {
	s.mu.Lock()
	s.filter = f
	s.mu.Unlock()
}

func (s *shim) current() (modbus.RequestHandler, readFilter) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.target, s.filter
}

func (s *shim) HandleCoils(ctx context.Context, req *modbus.CoilsRequest) ([]bool, error) {
	h, _ := s.current()
	return h.HandleCoils(ctx, req)
}

func (s *shim) HandleDiscreteInputs(ctx context.Context, req *modbus.DiscreteInputsRequest) ([]bool, error) {
	h, _ := s.current()
	return h.HandleDiscreteInputs(ctx, req)
}

func (s *shim) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	h, f := s.current()
	if req.IsWrite {
		s.writes.Add(1)
		return h.HandleHoldingRegisters(ctx, req)
	}
	s.reads.Add(1)
	regs, err := h.HandleHoldingRegisters(ctx, req)
	if f != nil {
		return f(req.Addr, req.Quantity, regs, err)
	}
	return regs, err
}

func (s *shim) HandleInputRegisters(ctx context.Context, req *modbus.InputRegistersRequest) ([]uint16, error) {
	h, f := s.current()
	s.reads.Add(1)
	regs, err := h.HandleInputRegisters(ctx, req)
	if f != nil {
		return f(req.Addr, req.Quantity, regs, err)
	}
	return regs, err
}

// startModbus serves handler on a loopback port until the test ends and
// returns the address and the server.
func startModbus(t testing.TB, handler modbus.RequestHandler) (string, *modbus.Server) {
	t.Helper()
	var (
		ms   *modbus.Server
		addr string
		err  error
	)
	// The port is picked and released before the server binds it: retry
	// when something else took it in between.
	for attempt := 0; attempt < 10; attempt++ {
		addr = listenAddr(t)
		ms, err = modbus.NewServer(&modbus.ServerConfig{
			URL: "tcp://" + addr, MaxClients: 256, Logger: modbus.NopLogger(), Timeout: 30 * time.Second,
		}, handler)
		if err != nil {
			t.Fatalf("modbus.NewServer: %v", err)
		}
		if err = ms.Start(); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() { _ = ms.Stop() })
	return addr, ms
}

// served is a Server built from generated values, with what a client must
// read back from it.
type served struct {
	srv *Server
	// gens holds, per model of the chain, the generated values; nil for a
	// model served from raw registers.
	gens []*genGroup
	// raws holds the data registers of the models served from raw registers.
	raws  [][]uint16
	metas []*registry.ModelMeta
}

// newServed builds a device holding generated values for the given registry
// model IDs, in order.
func newServed(t testing.TB, seed int64, opts *ServerOptions, ids ...uint16) *served {
	t.Helper()
	gen := newGenerator(seed)
	d := &served{}
	var models []ServerModel
	for _, id := range ids {
		meta := registry.ByID(id)
		if meta == nil {
			t.Fatalf("no schema for model %d", id)
		}
		g := gen.model(meta)
		// A model whose only content is a repeating group can come out
		// without data registers, which no device may serve.
		for regs, _ := EncodeModel(meta, g.vals); len(regs) <= 2; regs, _ = EncodeModel(meta, g.vals) {
			g = gen.model(meta)
		}
		d.gens = append(d.gens, g)
		d.raws = append(d.raws, nil)
		d.metas = append(d.metas, meta)
		models = append(models, ServerModel{ID: id, Values: g.vals})
	}
	srv, err := NewServer(opts, models...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	d.srv = srv
	return d
}

// checkAll compares the result of ReadAll with what the device holds.
func (d *served) checkAll(t testing.TB, results []*DecodedModel) {
	t.Helper()
	if len(results) != len(d.gens) {
		t.Fatalf("%d models read, the device has %d", len(results), len(d.gens))
	}
	for i, dm := range results {
		d.checkModel(t, i, dm)
	}
}

// checkModel compares one decoded model with what the device holds at index.
func (d *served) checkModel(t testing.TB, index int, dm *DecodedModel) {
	t.Helper()
	meta := d.metas[index]
	if dm == nil || dm.ModelID != meta.ID {
		t.Errorf("model %d of the chain: got %+v, want model %d", index, dm, meta.ID)
		return
	}
	if len(dm.Warnings) != 0 {
		t.Errorf("model %d (ID %d): warnings %v", index, meta.ID, dm.Warnings)
	}
	d.gens[index].check(t, dm.Group, meta.Group.Name)
}
