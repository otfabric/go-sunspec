// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-modbus/batch"
	"github.com/otfabric/go-sunspec/testutil"
)

// hangHandler forwards requests to target and counts them; a read starting
// at hangAt gets no response until the request's context ends.
type hangHandler struct {
	delayedHandler
	hangAt int // -1 for none

	mu    sync.Mutex
	addrs []uint16
}

func (h *hangHandler) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if !req.IsWrite {
		h.mu.Lock()
		h.addrs = append(h.addrs, req.Addr)
		h.mu.Unlock()
		if int(req.Addr) == h.hangAt {
			<-ctx.Done()
			return nil, modbus.ErrServerDeviceFailure
		}
	}
	return h.delayedHandler.HandleHoldingRegisters(ctx, req)
}

func (h *hangHandler) requestAddrs() []uint16 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]uint16(nil), h.addrs...)
}

func (h *hangHandler) resetAddrs() {
	h.mu.Lock()
	h.addrs = nil
	h.mu.Unlock()
	h.reset()
}

// modelsDevice serves the benchmark chain (1, 103, 120-123) behind a
// hangHandler and returns the discovered device.
func modelsDevice(t *testing.T, timeout time.Duration) (*Device, *served, *hangHandler) {
	t.Helper()
	d := newServed(t, 1, &ServerOptions{BaseAddress: 40000}, benchModels...)
	h := &hangHandler{delayedHandler: delayedHandler{target: d.srv}, hangAt: -1}
	addr, _ := startModbus(t, h)
	dev, err := Discover(testCtx(t), dial(t, addr, timeout), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	h.resetAddrs()
	return dev, d, h
}

// checkLikeReadModel checks that every result is what ReadModel returns for
// its instance.
func checkLikeReadModel(t *testing.T, dev *Device, results []ModelResult) {
	t.Helper()
	for i, r := range results {
		dm, err := dev.ReadModel(testCtx(t), r.Instance)
		if !reflect.DeepEqual(r.Model, dm) {
			t.Errorf("result %d (model %d): Model differs from ReadModel:\n%+v\n%+v", i, r.Instance.Header.ID, r.Model, dm)
		}
		if (err == nil) != (r.Err == nil) || (err != nil && err.Error() != r.Err.Error()) {
			t.Errorf("result %d (model %d): Err %v, ReadModel %v", i, r.Instance.Header.ID, r.Err, err)
		}
	}
}

func TestReadModelsFewerRequests(t *testing.T) {
	dev, d, h := modelsDevice(t, 5*time.Second)
	ctx := testCtx(t)

	results, err := dev.ReadModels(ctx, dev.Discovery.Models)
	if err != nil {
		t.Fatal(err)
	}
	// 1+103 (120 registers), 120-122 (106), 123 (26): three requests for six
	// models, none of them split.
	if got := h.requestAddrs(); len(got) != 3 {
		t.Errorf("%d requests at %v, want 3", len(got), got)
	}
	if len(results) != len(benchModels) {
		t.Fatalf("%d results", len(results))
	}
	models := make([]*DecodedModel, len(results))
	for i, r := range results {
		if r.Err != nil || r.Instance != dev.Discovery.Models[i] {
			t.Errorf("result %d = %+v", i, r)
		}
		models[i] = r.Model
	}
	d.checkAll(t, models)

	all, err := dev.ReadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, models) {
		t.Error("ReadModels and ReadAll decode the chain differently")
	}
}

func TestReadModelsOrderAndDuplicates(t *testing.T) {
	dev, _, h := modelsDevice(t, 5*time.Second)
	m := dev.Discovery.Models
	insts := []ModelInstance{m[5], m[1], m[1], m[0]}

	results, err := dev.ReadModels(testCtx(t), insts)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if r.Instance != insts[i] || r.Model == nil || r.Model.ModelID != insts[i].Header.ID {
			t.Errorf("result %d = %+v, want model %d", i, r, insts[i].Header.ID)
		}
	}
	if results[1].Model == results[2].Model {
		t.Error("a duplicate instance shares its DecodedModel")
	}
	// 1+103 in one request, 123 in another; 103 is read once.
	if got := h.requestAddrs(); len(got) != 2 {
		t.Errorf("%d requests at %v, want 2", len(got), got)
	}
	checkLikeReadModel(t, dev, results)

	if r, err := dev.ReadModels(testCtx(t), nil); r != nil || err != nil {
		t.Errorf("ReadModels(nil) = %v, %v", r, err)
	}
}

// TestReadModelsTimeoutStopsBatch makes the second of three requests time
// out: the models of the first are decoded, those of the second fail with
// the timeout, and the last is not read.
func TestReadModelsTimeoutStopsBatch(t *testing.T) {
	dev, d, h := modelsDevice(t, 200*time.Millisecond)
	m := dev.Discovery.Models
	h.hangAt = int(m[2].Header.StartAddress) // the request for 120-122

	// The client's 200 ms timeout ends the hung request well within the
	// test context's deadline (go-modbus v1.9.0 and later).
	results, err := dev.ReadModels(testCtx(t), m)
	if !errors.Is(err, modbus.ErrRequestTimedOut) {
		t.Fatalf("err = %v, want it to wrap ErrRequestTimedOut", err)
	}
	if got := h.requestAddrs(); len(got) != 2 {
		t.Errorf("%d requests at %v, want 2", len(got), got)
	}
	for i := 0; i < 2; i++ {
		if results[i].Err != nil {
			t.Errorf("model %d: %v", m[i].Header.ID, results[i].Err)
		}
		d.checkModel(t, i, results[i].Model)
	}
	for i := 2; i < 5; i++ {
		r := results[i]
		if !errors.Is(r.Err, modbus.ErrRequestTimedOut) || r.Model == nil || r.Model.Group != nil || len(r.Model.Warnings) != 1 {
			t.Errorf("model %d = %+v, %v; want the timeout and an undecoded model", m[i].Header.ID, r.Model, r.Err)
		}
	}
	last := results[5]
	if last.Model != nil || !errors.Is(last.Err, batch.ErrNotAttempted) || !errors.Is(last.Err, modbus.ErrRequestTimedOut) {
		t.Errorf("model 123 = %+v, want not attempted because of the timeout", last)
	}
}

func TestReadModelsCancelled(t *testing.T) {
	dev, _, h := modelsDevice(t, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := dev.ReadModels(ctx, dev.Discovery.Models)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	for _, r := range results {
		if r.Model != nil || !errors.Is(r.Err, batch.ErrNotAttempted) || !errors.Is(r.Err, context.Canceled) {
			t.Errorf("result %+v", r)
		}
	}
	if got := h.requestAddrs(); len(got) != 0 {
		t.Errorf("requests at %v, want none", got)
	}
}

// TestReadModelsFailuresLikeReadModel serves models that fail in each way
// ReadModel reports, and checks that ReadModels reports them alike.
func TestReadModelsFailuresLikeReadModel(t *testing.T) {
	ctx := testCtx(t)

	t.Run("partial read of a long model", func(t *testing.T) {
		// Model 64950 announces 200 registers; only 150 exist.
		broken := testutil.FixtureModel{ID: 64950, Length: 200, Registers: seq(1, 150)}
		tail := testutil.FixtureModel{ID: labelLessModelID, Length: 1, Registers: []uint16{42}}
		dev, err := Discover(ctx, serve(t, testutil.NewSunSpecFixture(40000, 1, commonModel(), broken, tail)), at(40000))
		if err != nil {
			t.Fatal(err)
		}
		results, err := dev.ReadModels(ctx, dev.Discovery.Models)
		if !errors.Is(err, ErrPartialRead) || !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Fatalf("err = %v", err)
		}
		if r := results[1]; !reflect.DeepEqual(r.Model.RawRegisters, seq(1, 123)) {
			t.Errorf("broken model has %d registers, want 123", len(r.Model.RawRegisters))
		}
		if results[0].Err != nil || results[2].Err != nil {
			t.Errorf("neighbours failed: %v, %v", results[0].Err, results[2].Err)
		}
		checkLikeReadModel(t, dev, results)
	})

	t.Run("exception in a shared request", func(t *testing.T) {
		// Model 1 announces 10 registers, too few for its schema; the next
		// model's data registers do not exist, so a request covering both
		// fails, and each is read again on its own.
		short := testutil.FixtureModel{ID: 1, Length: 10, Registers: seq(1, 10)}
		missing := testutil.FixtureModel{ID: 1, Length: 66}
		dev, err := Discover(ctx, serve(t, testutil.NewSunSpecFixture(100, 1, short, missing)), at(100))
		if err != nil {
			t.Fatal(err)
		}
		results, err := dev.ReadModels(ctx, dev.Discovery.Models)
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Fatalf("err = %v, want the first failure, a *DecodeError", err)
		}
		if !errors.Is(results[1].Err, modbus.ErrIllegalDataAddress) || errors.Is(results[1].Err, ErrPartialRead) {
			t.Errorf("missing model: %v", results[1].Err)
		}
		checkLikeReadModel(t, dev, results)
	})

	t.Run("changed model", func(t *testing.T) {
		dev, _, _ := modelsDevice(t, 5*time.Second)
		insts := append([]ModelInstance(nil), dev.Discovery.Models...)
		insts[1].Header.ID = 999 // same address and length, another ID
		results, err := dev.ReadModels(ctx, insts)
		if !errors.Is(err, ErrModelChanged) {
			t.Fatalf("err = %v", err)
		}
		for i, r := range results {
			if (i == 1) != (r.Err != nil) {
				t.Errorf("result %d: %v", i, r.Err)
			}
		}
		checkLikeReadModel(t, dev, results)
	})

	t.Run("outside the address space", func(t *testing.T) {
		dev, _, h := modelsDevice(t, 5*time.Second)
		insts := []ModelInstance{dev.Discovery.Models[0], {Header: ModelHeader{ID: 7, Length: 1, StartAddress: 0xFFFF}}}
		results, err := dev.ReadModels(ctx, insts)
		if !errors.Is(err, modbus.ErrUnexpectedParameters) || results[0].Err != nil {
			t.Fatalf("err = %v, first %v", err, results[0].Err)
		}
		if got := h.requestAddrs(); len(got) != 1 {
			t.Errorf("requests at %v, want one", got)
		}
		checkLikeReadModel(t, dev, results)
		only := insts[1:]
		if results, err = dev.ReadModels(ctx, only); len(results) != 1 || !errors.Is(err, modbus.ErrUnexpectedParameters) {
			t.Errorf("only an invalid instance: %v, %v", results, err)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		dev := NewDevice(nil, 1, &DiscoveryResult{Models: []ModelInstance{{Header: ModelHeader{ID: 1, Length: 66, StartAddress: 40002}}}})
		results, err := dev.ReadModels(ctx, dev.Discovery.Models)
		if !errors.Is(err, modbus.ErrUnexpectedParameters) || len(results) != 1 || results[0].Err == nil {
			t.Errorf("nil client: %v, %v", results, err)
		}
	})
}

func TestReadModelsInputRegisters(t *testing.T) {
	ctx := testCtx(t)
	d := newServed(t, 4, &ServerOptions{BaseAddress: 0, RegType: modbus.InputRegister}, 1, 103, 160)
	client := e2eStart(t, d.srv)
	dev, err := Discover(ctx, client, &DiscoverOptions{RegType: modbus.InputRegister})
	if err != nil {
		t.Fatal(err)
	}
	results, err := dev.ReadModels(ctx, dev.Discovery.Models)
	if err != nil {
		t.Fatal(err)
	}
	models := make([]*DecodedModel, len(results))
	for i, r := range results {
		models[i] = r.Model
	}
	d.checkAll(t, models)
}
