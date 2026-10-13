// SPDX-License-Identifier: MIT

package sunspec

// Chaos tests: the library's client path against its own served device with
// faults in between, on the wire (chaos_proxy_test.go) and in the device (a
// handler shim that distorts what the device answers).
//
// They assert invariants, not timings:
//
//   - no panic;
//   - no hang: every call returns within its context deadline (plus a grace
//     period for the scheduler);
//   - no wrong value returned as valid: a call that returns no error returns
//     exactly what the device holds; a call that returns an error returns no
//     decoded model, and the registers it did read are the device's;
//   - no goroutine leak once client, proxy and server are closed;
//   - recovery: once the faults stop, a new connection works at once.
//
// The race detector covers "no data race" when the tests run with -race, as
// `make test` does.
//
// Randomness is seeded. The seed is logged, and SUNSPEC_CHAOS_SEED replays
// one. SUNSPEC_CHAOS_DURATION (a Go duration) sets how long the random
// scenario runs and makes the scripted scenarios sweep every frame instead of
// a sample; `make soak` sets it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec/registry"
)

const (
	// chaosTimeout is the client's response timeout in chaos tests. It
	// applies to calls whose context has no deadline: go-modbus lets a
	// context deadline override it.
	chaosTimeout = 60 * time.Millisecond
	// chaosDelay is how long a delayed frame is held: beyond the timeout
	// and the short deadline.
	chaosDelay = 250 * time.Millisecond
	// chaosShort is the context deadline of a call that may meet a fault:
	// long enough for a faultless call on loopback, short enough that a
	// lost answer does not stall the test.
	chaosShort = 150 * time.Millisecond
	// chaosLong is the context deadline of a call that must succeed.
	chaosLong = 10 * time.Second
	// chaosGrace is how much later than its deadline a call may return
	// before it counts as hung. A call without deadline is bounded by the
	// client timeout per request and gets the same grace in total.
	chaosGrace = 10 * time.Second
)

// chaosSeed returns the seed of a chaos or stress test and logs it.
func chaosSeed(t testing.TB) int64 {
	t.Helper()
	seed := time.Now().UnixNano()
	if s := os.Getenv("SUNSPEC_CHAOS_SEED"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("SUNSPEC_CHAOS_SEED=%q: %v", s, err)
		}
		seed = v
	}
	t.Logf("seed %d (replay with SUNSPEC_CHAOS_SEED=%d)", seed, seed)
	return seed
}

// envDuration returns the duration in the named environment variable and
// whether it was set.
func envDuration(t testing.TB, name string, def time.Duration) (time.Duration, bool) {
	t.Helper()
	s := os.Getenv(name)
	if s == "" {
		return def, false
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		t.Fatalf("%s=%q: not a positive duration", name, s)
	}
	return d, true
}

// leakCheck records the number of goroutines and returns a function that
// fails the test when, after everything was closed, more are left.
func leakCheck(t testing.TB) func() {
	t.Helper()
	before := runtime.NumGoroutine()
	return func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			n := runtime.NumGoroutine()
			if n <= before {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("goroutine leak: %d before, %d after\n%s", before, n, allStacks())
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// bounded runs one library call with a deadline and fails the test when the
// call does not return in time: the no-hang invariant.
func bounded[T any](t testing.TB, name string, fn func(ctx context.Context) (T, error)) (T, error) {
	t.Helper()
	return boundedBy(t, name, chaosLong, fn)
}

// boundedBy is bounded with a chosen deadline. A limit of zero gives the call
// a context without deadline.
func boundedBy[T any](t testing.TB, name string, limit time.Duration, fn func(ctx context.Context) (T, error)) (T, error) {
	t.Helper()
	type result struct {
		v   T
		err error
	}
	ctx, cancel := context.WithCancel(context.Background())
	if limit > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), limit)
	}
	defer cancel()
	ch := make(chan result, 1)
	go func() {
		v, err := fn(ctx)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(limit + chaosGrace):
		t.Fatalf("%s did not return %v after its deadline of %v\n%s", name, chaosGrace, limit, allStacks())
		panic("unreachable")
	}
}

// truth is what a device holds, as the tests' oracle.
type truth struct {
	*served
	// regs holds, per model, the registers including the header.
	regs [][]uint16
	// discovery is what a faultless discovery returns.
	discovery *DiscoveryResult
}

// newTruth builds a device of generated registry models plus one long vendor
// model, and its oracle.
func newTruth(t testing.TB, seed int64, base uint16, ids ...uint16) *truth {
	t.Helper()
	d := newServed(t, seed, &ServerOptions{BaseAddress: base}, ids...)
	return truthOf(t, d)
}

func truthOf(t testing.TB, d *served) *truth {
	t.Helper()
	tr := &truth{served: d, discovery: &DiscoveryResult{BaseAddress: d.srv.BaseAddress(), RegType: d.srv.RegType()}}
	for i, h := range d.srv.Models() {
		regs, err := holdingRead(d.srv, h.StartAddress, h.Length+2)
		if err != nil {
			t.Fatalf("read model %d: %v", i, err)
		}
		tr.regs = append(tr.regs, regs)
		meta := d.metas[i]
		tr.discovery.Models = append(tr.discovery.Models, ModelInstance{
			Header: h, Name: meta.Label, Schema: meta, SchemaKnown: true, DecodingSupported: true,
		})
	}
	return tr
}

// device returns a Device for client with the true layout, so that reads can
// be tested without a successful discovery first.
func (tr *truth) device(client *modbus.Client) *Device {
	return NewDevice(client, tr.srv.UnitID(), tr.discovery)
}

// verifyModel applies the no-wrong-value invariant to the result of reading
// the model at index.
func (tr *truth) verifyModel(t testing.TB, what string, index int, dm *DecodedModel, err error) bool {
	t.Helper()
	failed := t.Failed()
	switch {
	case dm == nil:
		t.Errorf("%s: model %d: nil result (err %v)", what, index, err)
	case err == nil:
		if dm.Group == nil {
			t.Errorf("%s: model %d: no error and no decoded model", what, index)
			break
		}
		if !reflect.DeepEqual(dm.RawRegisters, tr.regs[index]) {
			t.Errorf("%s: model %d: registers differ from the device's without an error", what, index)
		}
		tr.checkModel(t, index, dm)
	default:
		if dm.Group != nil {
			t.Errorf("%s: model %d: decoded model returned next to error %v", what, index, err)
		}
		// What was read before the failure is the device's data.
		data := tr.regs[index][2:]
		if len(dm.RawRegisters) > len(data) || !reflect.DeepEqual(dm.RawRegisters, data[:len(dm.RawRegisters)]) {
			if len(dm.RawRegisters) != 0 {
				t.Errorf("%s: model %d: partial registers next to error %v are not the device's", what, index, err)
			}
		}
		if len(dm.Warnings) == 0 {
			t.Errorf("%s: model %d: error %v without a warning on the model", what, index, err)
		}
	}
	return t.Failed() == failed
}

// verifyAll applies the invariant to the result of ReadAll.
func (tr *truth) verifyAll(t testing.TB, what string, results []*DecodedModel, err error) {
	t.Helper()
	if len(results) != len(tr.regs) {
		t.Errorf("%s: %d results for %d models (err %v)", what, len(results), len(tr.regs), err)
		return
	}
	anyFailed := false
	for i, dm := range results {
		var modelErr error
		if dm != nil && dm.Group == nil {
			modelErr = errors.New("model not decoded")
			anyFailed = true
		}
		tr.verifyModel(t, what, i, dm, modelErr)
	}
	if anyFailed != (err != nil) {
		t.Errorf("%s: ReadAll error is %v but a model failed: %v", what, err, anyFailed)
	}
}

// verifyDiscovery applies the invariant to the result of Discover.
func (tr *truth) verifyDiscovery(t testing.TB, what string, dev *Device, err error) {
	t.Helper()
	if err != nil {
		if dev != nil {
			t.Errorf("%s: Discover returned a device next to error %v", what, err)
		}
		return
	}
	if dev == nil || dev.Discovery == nil {
		t.Errorf("%s: Discover returned neither device nor error", what)
		return
	}
	got := dev.Discovery
	if got.BaseAddress != tr.discovery.BaseAddress || len(got.Warnings) != 0 || !reflect.DeepEqual(got.Models, tr.discovery.Models) {
		t.Errorf("%s: Discover succeeded with a layout that is not the device's:\n%+v\nwarnings %q", what, got.Models, got.Warnings)
	}
}

// chaosEnv is a device behind a shim, a Modbus server and a chaos proxy.
type chaosEnv struct {
	t     testing.TB
	truth *truth
	shim  *shim
	ms    *modbus.Server
	proxy *chaosProxy
}

func newChaosEnv(t testing.TB, tr *truth, seed int64) *chaosEnv {
	t.Helper()
	sh := newShim(tr.srv)
	addr := listenAddr(t)
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, MaxClients: 64, Logger: modbus.NopLogger()}, sh)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	return &chaosEnv{t: t, truth: tr, shim: sh, ms: ms, proxy: startChaosProxy(t, addr, seed, chaosDelay)}
}

func (e *chaosEnv) stop() {
	e.proxy.stop()
	if err := e.ms.Stop(); err != nil {
		e.t.Errorf("server stop: %v", err)
	}
}

// connect opens a client through the proxy, or returns nil when the faults
// prevent it.
func (e *chaosEnv) connect() *modbus.Client {
	client, err := modbus.New(modbus.Config{
		URL: "tcp://" + e.proxy.addr, Timeout: chaosTimeout, DialTimeout: time.Second, Logger: modbus.NopLogger(),
	})
	if err != nil {
		e.t.Fatalf("modbus.New: %v", err)
	}
	if err := client.Open(); err != nil {
		return nil
	}
	return client
}

// exercise runs Discover and every kind of read through client and applies
// the invariants. Each call gets limit as its context deadline (zero: none,
// the client timeout bounds each request). It returns how many of the four
// calls succeeded.
func (e *chaosEnv) exercise(what string, client *modbus.Client, rng *rand.Rand, limit time.Duration) (ok int) {
	t, tr := e.t, e.truth
	t.Helper()
	opts := &DiscoverOptions{BaseAddresses: []uint16{tr.srv.BaseAddress()}}

	dev, err := boundedBy(t, what+": Discover", limit, func(ctx context.Context) (*Device, error) {
		return Discover(ctx, client, opts)
	})
	tr.verifyDiscovery(t, what, dev, err)
	if err == nil {
		ok++
	}
	// Reads use the true layout, so that they are tested whether or not the
	// discovery above got through.
	dev = tr.device(client)
	results, err := boundedBy(t, what+": ReadAll", limit, func(ctx context.Context) ([]*DecodedModel, error) {
		return dev.ReadAll(ctx)
	})
	tr.verifyAll(t, what+": ReadAll", results, err)
	if err == nil {
		ok++
	}
	index := rng.Intn(len(tr.regs))
	dm, err := boundedBy(t, what+": ReadModel", limit, func(ctx context.Context) (*DecodedModel, error) {
		return dev.ReadModel(ctx, tr.discovery.Models[index])
	})
	tr.verifyModel(t, what+": ReadModel", index, dm, err)
	if err == nil {
		ok++
	}
	// The first point after the header, wherever it is implemented.
	pm := tr.metas[index].Group.Points[2]
	p, err := boundedBy(t, what+": ReadPoint", limit, func(ctx context.Context) (*DecodedPoint, error) {
		return dev.ReadPoint(ctx, tr.discovery.Models[index], pm.Name)
	})
	if err == nil {
		ok++
		if want, set := tr.gens[index].raw[pm.Name]; set && p.RawValue != want {
			t.Errorf("%s: ReadPoint(%s) of model %d = %v, the device holds %v", what, pm.Name, index, p.RawValue, want)
		} else if !set && p.Implemented && pm.Type != "pad" {
			t.Errorf("%s: ReadPoint(%s) of model %d = %v, the device does not implement it", what, pm.Name, index, p.RawValue)
		}
	}
	return ok
}

// recovered requires a new connection to work at once after the faults
// stopped.
func (e *chaosEnv) recovered(what string) {
	e.t.Helper()
	e.proxy.heal()
	client := e.connect()
	if client == nil {
		e.t.Errorf("%s: cannot connect after the faults stopped", what)
		return
	}
	defer func() { _ = client.Close() }()
	dev, err := bounded(e.t, what+": Discover after healing", func(ctx context.Context) (*Device, error) {
		return Discover(ctx, client, &DiscoverOptions{BaseAddresses: []uint16{e.truth.srv.BaseAddress()}})
	})
	if err != nil {
		e.t.Errorf("%s: Discover after the faults stopped: %v", what, err)
		return
	}
	e.truth.verifyDiscovery(e.t, what, dev, err)
	results, err := bounded(e.t, what+": ReadAll after healing", func(ctx context.Context) ([]*DecodedModel, error) {
		return dev.ReadAll(ctx)
	})
	if err != nil {
		e.t.Errorf("%s: ReadAll after the faults stopped: %v", what, err)
	}
	e.truth.verifyAll(e.t, what, results, err)
}

// chaosModels is the device of the wire chaos tests: single-request models,
// a model with repeating groups and one that needs several requests.
var chaosModels = []uint16{1, 103, 160, 502, 705}

// TestChaosScriptedFaults injects exactly one fault, of each kind, in each
// direction, at a chosen frame of a discovery-and-read sequence: a connection
// dropped mid-discovery or mid-read, a response delayed beyond the timeout,
// truncated, split, duplicated or corrupted frames.
func TestChaosScriptedFaults(t *testing.T) {
	seed := chaosSeed(t)
	_, sweep := envDuration(t, "SUNSPEC_CHAOS_DURATION", 0)
	leaks := leakCheck(t)
	rng := rand.New(rand.NewSource(seed))
	env := newChaosEnv(t, newTruth(t, seed, 40000, chaosModels...), seed)

	// A faultless run tells how many frames the sequence has.
	client := env.connect()
	if n := env.exercise("faultless", client, rng, chaosLong); n != 4 {
		t.Fatalf("faultless run: %d of 4 calls succeeded", n)
	}
	_ = client.Close()
	frames := int(env.proxy.frames[chaosS2C].Load())
	if frames < 10 {
		t.Fatalf("the sequence has only %d frames", frames)
	}

	runs, failedCalls := 0, 0
	for _, fault := range chaosDetectable {
		for _, dir := range []chaosDir{chaosC2S, chaosS2C} {
			// Every frame in a soak run; a seeded sample of two otherwise.
			targets := rng.Perm(frames)
			if !sweep {
				targets = targets[:2]
			}
			for _, target := range targets {
				what := fmt.Sprintf("%s on %s frame %d", fault, map[chaosDir]string{chaosC2S: "request", chaosS2C: "response"}[dir], target)
				env.proxy.setDecide(func(f chaosFrame, _ *rand.Rand) chaosFault {
					if f.dir == dir && f.seq == target {
						return fault
					}
					return faultNone
				})
				client := env.connect()
				if client == nil {
					t.Fatalf("%s: cannot connect", what)
				}
				// A context deadline or, every other run, the client timeout.
				// A split frame is intact and gets the time to arrive.
				limit := []time.Duration{chaosShort, 0}[runs%2]
				if fault == faultSplit {
					limit = chaosLong
				}
				ok := env.exercise(what, client, rng, limit)
				_ = client.Close()
				runs++
				failedCalls += 4 - ok
				// A split frame is intact: nothing may fail.
				if fault == faultSplit && ok != 4 {
					t.Errorf("%s: %d of 4 calls succeeded, a fragmented frame must be reassembled", what, ok)
				}
				env.recovered(what)
				if t.Failed() {
					t.Fatalf("stopping after %s", what)
				}
			}
		}
	}
	if env.proxy.injectedTotal() < int64(runs) || failedCalls == 0 {
		t.Errorf("%d runs injected %d faults and made %d calls fail", runs, env.proxy.injectedTotal(), failedCalls)
	}
	t.Logf("%d runs over %d frames, %d faults injected, %d calls failed and none returned a wrong value",
		runs, frames, env.proxy.injectedTotal(), failedCalls)
	env.stop()
	leaks()
}

// TestChaosRandomFaults lets the proxy fault frames at random, with a random
// mix and rate per round, while the library discovers and reads.
func TestChaosRandomFaults(t *testing.T) {
	seed := chaosSeed(t)
	duration, _ := envDuration(t, "SUNSPEC_CHAOS_DURATION", 2*time.Second)
	leaks := leakCheck(t)
	rng := rand.New(rand.NewSource(seed))
	env := newChaosEnv(t, newTruth(t, seed, 0, chaosModels...), seed)

	rounds, succeeded := 0, 0
	for start := time.Now(); time.Since(start) < duration; rounds++ {
		// A random subset of the faults, at a random rate per frame.
		var enabled []chaosFault
		for _, f := range chaosDetectable {
			if rng.Intn(3) == 0 {
				enabled = append(enabled, f)
			}
		}
		if len(enabled) == 0 {
			enabled = []chaosFault{chaosDetectable[rng.Intn(len(chaosDetectable))]}
		}
		rate := []float64{0.02, 0.1, 0.3}[rng.Intn(3)]
		env.proxy.setDecide(func(_ chaosFrame, r *rand.Rand) chaosFault {
			if r.Float64() < rate {
				return enabled[r.Intn(len(enabled))]
			}
			return faultNone
		})
		what := fmt.Sprintf("round %d (rate %.2f, faults %v)", rounds, rate, enabled)
		client := env.connect()
		if client == nil {
			continue
		}
		// Several passes over one connection: what a fault leaves behind
		// (a late or duplicated answer) meets the next request.
		for pass := 0; pass < 2; pass++ {
			succeeded += env.exercise(what, client, rng, []time.Duration{chaosShort, 0}[rng.Intn(2)])
		}
		_ = client.Close()
		if rounds%10 == 9 {
			env.recovered(what)
		}
		if t.Failed() {
			t.Fatalf("stopping after %s", what)
		}
	}
	env.recovered("end")
	if env.proxy.injectedTotal() == 0 || succeeded == 0 {
		t.Errorf("%d rounds: %d faults injected, %d calls succeeded", rounds, env.proxy.injectedTotal(), succeeded)
	}
	t.Logf("%d rounds, %d faults injected, %d of %d calls succeeded", rounds, env.proxy.injectedTotal(), succeeded, rounds*8)
	env.stop()
	leaks()
}

// TestChaosCorruptedData flips bits in the register data. Nothing in Modbus
// TCP detects that, so the values may be wrong; the library must still not
// panic or hang on what it decodes.
func TestChaosCorruptedData(t *testing.T) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	tr := newTruth(t, seed, 0, chaosModels...)
	env := newChaosEnv(t, tr, seed)
	env.proxy.setDecide(func(f chaosFrame, r *rand.Rand) chaosFault {
		if r.Intn(2) == 0 {
			return faultCorruptData
		}
		return faultNone
	})
	client := env.connect()
	if client == nil {
		t.Fatal("cannot connect")
	}
	for i := 0; i < 30; i++ {
		dev, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) {
			return Discover(ctx, client, nil)
		})
		if err != nil {
			continue
		}
		results, _ := bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) {
			return dev.ReadAll(ctx)
		})
		for _, dm := range results {
			// Whatever was decoded can be turned into values and JSON.
			_ = dm.Values()
			if _, err := json.Marshal(dm); err != nil {
				t.Errorf("model %d does not marshal: %v", dm.ModelID, err)
			}
		}
	}
	_ = client.Close()
	env.recovered("corrupted data")
	env.stop()
	leaks()
}

// TestChaosDeviceExceptions has the device answer some requests with an
// exception: a model is then reported as failed, and the others are right.
func TestChaosDeviceExceptions(t *testing.T) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	rng := rand.New(rand.NewSource(seed))
	tr := newTruth(t, seed, 40000, chaosModels...)
	env := newChaosEnv(t, tr, seed)
	client := env.connect()
	if client == nil {
		t.Fatal("cannot connect")
	}
	exceptions := []error{modbus.ErrIllegalDataAddress, modbus.ErrServerDeviceFailure, modbus.ErrServerDeviceBusy,
		modbus.ErrIllegalFunction, modbus.ErrGWTargetFailedToRespond, errors.New("not an exception")}

	// One model fails for good.
	for index, h := range tr.srv.Models() {
		exc := exceptions[index%len(exceptions)]
		env.shim.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
			if addr >= h.StartAddress && addr <= h.EndAddress && quantity > 2 {
				return nil, exc
			}
			return regs, err
		})
		dev := tr.device(client)
		results, err := bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) { return dev.ReadAll(ctx) })
		if err == nil {
			t.Errorf("model %d answers %v: ReadAll returned no error", index, exc)
		}
		tr.verifyAll(t, fmt.Sprintf("model %d answers %v", index, exc), results, err)
		for i, dm := range results {
			if (dm.Group == nil) != (i == index) {
				t.Errorf("model %d answers %v: model %d decoded = %v", index, exc, i, dm.Group != nil)
			}
		}
	}
	// Any request fails at random.
	for round := 0; round < 40; round++ {
		rate := rng.Float64() * 0.6
		var mu sync.Mutex
		frng := rand.New(rand.NewSource(seed + int64(round)))
		env.shim.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
			mu.Lock()
			defer mu.Unlock()
			if frng.Float64() < rate {
				return nil, exceptions[frng.Intn(len(exceptions))]
			}
			return regs, err
		})
		env.exercise(fmt.Sprintf("random exceptions, round %d", round), client, rng, chaosLong)
	}
	env.shim.setFilter(nil)
	_ = client.Close()
	env.recovered("exceptions")
	env.stop()
	leaks()
}

// TestChaosMarkerDisappears takes the "SunS" marker away after a discovery.
func TestChaosMarkerDisappears(t *testing.T) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	tr := newTruth(t, seed, 40000, chaosModels...)
	env := newChaosEnv(t, tr, seed)
	client := env.connect()
	if client == nil {
		t.Fatal("cannot connect")
	}
	dev, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
	if err != nil {
		t.Fatal(err)
	}

	// Gone as data: the address reads zero. Gone as registers: exception.
	filters := map[string]readFilter{
		"zeroed": func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
			for i := range regs {
				if a := int(addr) + i; a == 40000 || a == 40001 {
					regs[i] = 0
				}
			}
			return regs, err
		},
		"unmapped": func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
			if addr <= 40001 && int(addr)+int(quantity) > 40000 {
				return nil, modbus.ErrIllegalDataAddress
			}
			return regs, err
		},
	}
	for name, filter := range filters {
		env.shim.setFilter(filter)
		res, err := bounded(t, "Detect", func(ctx context.Context) (*DetectionResult, error) { return Detect(ctx, client, nil) })
		if err == nil || (res != nil && res.Detected) {
			t.Errorf("marker %s: Detect = %+v, %v", name, res, err)
		}
		if name == "zeroed" && !errors.Is(err, ErrNotSunSpec) {
			t.Errorf("marker zeroed: Detect err = %v, want ErrNotSunSpec", err)
		}
		d2, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
		if err == nil || d2 != nil {
			t.Errorf("marker %s: Discover = %+v, %v", name, d2, err)
		}
		// The models are still where they were: a device discovered before
		// keeps reading them, correctly.
		results, err := bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) { return dev.ReadAll(ctx) })
		if err != nil {
			t.Errorf("marker %s: ReadAll: %v", name, err)
		}
		tr.verifyAll(t, "marker "+name, results, err)
	}

	// The marker disappears between the probe and the walk of the chain:
	// the discovery that is under way describes the device as it was.
	probes := 0
	env.shim.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if addr == 40000 {
			probes++
			if probes > 1 {
				return []uint16{0, 0}, nil
			}
		}
		return regs, err
	})
	d3, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
	tr.verifyDiscovery(t, "marker gone mid-discovery", d3, err)
	env.shim.setFilter(nil)
	_ = client.Close()
	env.stop()
	leaks()
}

// TestChaosChainChanges swaps the device for one with another model chain,
// between calls and in the middle of them. A read then returns either an
// error or the values of the device that answered, never one device's
// registers decoded with the other's layout.
func TestChaosChainChanges(t *testing.T) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	rng := rand.New(rand.NewSource(seed))
	// The same first model, then different models at different addresses.
	a := newTruth(t, seed, 40000, 1, 103, 160, 502, 705)
	b := newTruth(t, seed+1, 40000, 1, 101, 705, 160, 120, 103)
	// Like a, with other values: only the data differs.
	a2 := newTruth(t, seed+2, 40000, 1, 103, 160, 502, 705)
	for len(a2.regs[2]) != len(a.regs[2]) || len(a2.regs[3]) != len(a.regs[3]) || len(a2.regs[4]) != len(a.regs[4]) {
		seed += 7
		a2 = newTruth(t, seed, 40000, 1, 103, 160, 502, 705)
	}
	env := newChaosEnv(t, a, seed)
	client := env.connect()
	if client == nil {
		t.Fatal("cannot connect")
	}
	truths := []*truth{a, b, a2}

	// matches reports which device's model a decoded model is, by its
	// registers.
	matches := func(dm *DecodedModel) *truth {
		for _, tr := range truths {
			for i, regs := range tr.regs {
				if tr.discovery.Models[i].Header.StartAddress == dm.InstanceAddress && reflect.DeepEqual(dm.RawRegisters, regs) {
					return tr
				}
			}
		}
		return nil
	}

	// Swapped between calls: a device discovered on one chain reads the
	// other.
	for round := 0; round < 30; round++ {
		from, to := truths[rng.Intn(3)], truths[rng.Intn(3)]
		env.shim.setTarget(from.srv)
		dev, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
		if err != nil {
			t.Fatalf("round %d: Discover: %v", round, err)
		}
		from.verifyDiscovery(t, "before the swap", dev, err)
		env.shim.setTarget(to.srv)
		results, err := bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) { return dev.ReadAll(ctx) })
		sameLayout := reflect.DeepEqual(from.srv.Models(), to.srv.Models())
		if sameLayout != (err == nil) {
			t.Errorf("round %d: same layout %v, ReadAll err = %v", round, sameLayout, err)
		}
		for i, dm := range results {
			if dm.Group == nil {
				continue
			}
			// Decoded: then it is a model the answering device has, at
			// that address, with exactly these registers.
			if got := matches(dm); got == nil || !reflect.DeepEqual(got.srv.Models(), to.srv.Models()) {
				t.Errorf("round %d: model %d (ID %d) was decoded from registers the device does not hold there", round, i, dm.ModelID)
				continue
			}
			if to.discovery.Models[i].Header == from.discovery.Models[i].Header {
				to.verifyModel(t, "after the swap", i, dm, nil)
			}
		}
		if !sameLayout && !errors.Is(err, ErrModelChanged) && !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("round %d: ReadAll err = %v, want ErrModelChanged", round, err)
		}
	}

	// Swapped in the middle of a call, after a random number of requests.
	for round := 0; round < 60; round++ {
		from, to := truths[rng.Intn(3)], truths[rng.Intn(3)]
		env.shim.setTarget(from.srv)
		after := int64(rng.Intn(14))
		start := env.shim.reads.Load()
		env.shim.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
			if env.shim.reads.Load()-start > after {
				return to.srv.read(addr, quantity)
			}
			return regs, err
		})
		dev, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
		if err != nil {
			// A chain that changes under the walk may be invalid.
			env.shim.setFilter(nil)
			continue
		}
		results, _ := bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) { return dev.ReadAll(ctx) })
		env.shim.setFilter(nil)
		for i, dm := range results {
			if dm.Group == nil {
				continue
			}
			inst := dev.Discovery.Models[i]
			// A model read in one request is one device's model. A longer
			// one may mix two devices that have the same model at the same
			// address, as it may mix two updates of one device.
			if int(inst.Header.Length)+2 <= maxRegistersPerRead && matches(dm) == nil {
				t.Errorf("round %d (swap after %d requests): model %d (ID %d at %d) holds registers of no device",
					round, after, i, dm.ModelID, dm.InstanceAddress)
			}
			if dm.RawRegisters[0] != inst.Header.ID || dm.RawRegisters[1] != inst.Header.Length {
				t.Errorf("round %d: model %d decoded with header %v, discovered as %+v", round, i, dm.RawRegisters[:2], inst.Header)
			}
		}
	}
	_ = client.Close()
	env.stop()
	leaks()
}

// endlessDevice is a device whose model chain never ends: after the marker
// at address 0, every tenth register starts a model of eight registers.
type endlessDevice struct{}

func (endlessDevice) HandleCoils(context.Context, *modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (endlessDevice) HandleDiscreteInputs(context.Context, *modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (endlessDevice) HandleInputRegisters(context.Context, *modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (endlessDevice) HandleHoldingRegisters(_ context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	out := make([]uint16, req.Quantity)
	for i := range out {
		addr := int(req.Addr) + i
		switch {
		case addr == 0:
			out[i] = 0x5375
		case addr == 1:
			out[i] = 0x6E53
		case (addr-2)%10 == 0:
			out[i] = 64000 + uint16((addr-2)/10%100)
		case (addr-2)%10 == 1:
			out[i] = 8
		default:
			out[i] = uint16(addr)
		}
	}
	return out, nil
}

// TestChaosEndlessChain discovers a chain without end marker: the walk is
// bounded by MaxModels and by the address space, and says so.
func TestChaosEndlessChain(t *testing.T) {
	leaks := leakCheck(t)
	addr := listenAddr(t)
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()}, &endlessDevice{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	client := dial(t, addr, 2*time.Second)

	dev, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	warnings := dev.Discovery.Warnings
	if len(dev.Discovery.Models) != 256 || len(warnings) == 0 || !strings.Contains(warnings[len(warnings)-1], "not terminated") {
		t.Errorf("default limits: %d models, last warning %q", len(dev.Discovery.Models), warnings[len(warnings)-1:])
	}
	// What was found is what the device holds there.
	dm, err := bounded(t, "ReadModel", func(ctx context.Context) (*DecodedModel, error) {
		return dev.ReadModel(ctx, dev.Discovery.Models[255])
	})
	if err != nil || len(dm.RawRegisters) != 8 || dm.RawRegisters[0] != 2554 {
		t.Errorf("model 255 = %+v, %v", dm, err)
	}
	// With room for more models than the address space holds, the walk
	// ends at address 65535 with an error.
	dev, err = bounded(t, "Discover", func(ctx context.Context) (*Device, error) {
		return Discover(ctx, client, &DiscoverOptions{MaxModels: 100000})
	})
	if err == nil || dev != nil {
		t.Errorf("unbounded walk: %d models, err %v", len(dev.Discovery.Models), err)
	}
	if _, err := bounded(t, "Discover", func(ctx context.Context) (*Device, error) {
		return Discover(ctx, client, &DiscoverOptions{MaxAddressSpan: 1000})
	}); !errors.Is(err, ErrModelChainLimitExceeded) {
		t.Errorf("MaxAddressSpan: err = %v", err)
	}
	// A deadline ends the walk too.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := Discover(ctx, client, &DiscoverOptions{MaxModels: 100000}); err == nil {
		t.Error("Discover outlived its context")
	}
	_ = client.Close()
	if err := ms.Stop(); err != nil {
		t.Error(err)
	}
	leaks()
}

// TestChaosLyingLengths has model headers announce a length the model does
// not have.
func TestChaosLyingLengths(t *testing.T) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	tr := newTruth(t, seed, 40000, 1, 103, 101)
	env := newChaosEnv(t, tr, seed)
	client := env.connect()
	if client == nil {
		t.Fatal("cannot connect")
	}
	layout := tr.srv.Models()
	// lie makes the length register of the model at index read as length.
	lie := func(index int, length uint16) {
		at := layout[index].StartAddress + 1
		env.shim.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
			if err == nil && addr <= at && int(addr)+len(regs) > int(at) {
				regs[at-addr] = length
			}
			return regs, err
		})
	}
	discover := func() (*Device, error) {
		return bounded(t, "Discover", func(ctx context.Context) (*Device, error) { return Discover(ctx, client, nil) })
	}
	readAll := func(dev *Device) ([]*DecodedModel, error) {
		return bounded(t, "ReadAll", func(ctx context.Context) ([]*DecodedModel, error) { return dev.ReadAll(ctx) })
	}

	// The last model claims more registers than exist: the read runs into
	// unmapped addresses and fails; the end marker is never found.
	lie(2, 60)
	if dev, err := discover(); err == nil {
		t.Errorf("last model too long: Discover found %d models without error", len(dev.Discovery.Models))
	}

	// The first model claims to be shorter than its schema: the chain walk
	// lands inside it, on registers that are no header.
	lie(0, 20)
	dev, err := discover()
	if err == nil {
		// Whatever the walk made of it, nothing wrong may be decoded as
		// model 1.
		results, _ := readAll(dev)
		for _, dm := range results {
			if dm.ModelID == 1 && dm.Group != nil {
				t.Errorf("a model 1 of 20 registers was decoded: %+v", dm.Warnings)
			}
		}
	}

	// A model claims length zero.
	lie(1, 0)
	if _, err := discover(); !errors.Is(err, ErrModelChainInvalid) {
		t.Errorf("zero length: err = %v, want ErrModelChainInvalid", err)
	}
	// A model claims to reach up to, or past, the end of the address
	// space. go-modbus reports these with different errors; all that is
	// required here is that none of them yields a device.
	end := 0x10000 - int(layout[1].StartAddress) - 2
	for _, length := range []int{end - 1, end, end + 1, 0xFFFF} {
		lie(1, uint16(length))
		if dev, err := discover(); err == nil || dev != nil {
			t.Errorf("length %d, past the address space: Discover returned a device (err %v)", length, err)
		}
	}

	// The length changes after discovery, in both directions.
	env.shim.setFilter(nil)
	dev, err = discover()
	if err != nil {
		t.Fatal(err)
	}
	for _, length := range []uint16{49, 51, 0, 0xFFFF} {
		lie(1, length)
		results, err := readAll(dev)
		if !errors.Is(err, ErrModelChanged) {
			t.Errorf("length now %d: ReadAll err = %v, want ErrModelChanged", length, err)
		}
		if len(results) != 3 || results[1].Group != nil || results[0].Group == nil || results[2].Group == nil {
			t.Errorf("length now %d: only the lying model may fail", length)
		}
	}
	// The ID changes after discovery.
	env.shim.setFilter(func(addr, quantity uint16, regs []uint16, err error) ([]uint16, error) {
		if err == nil && addr == layout[1].StartAddress {
			regs[0] = 113
		}
		return regs, err
	})
	if _, err := readAll(dev); !errors.Is(err, ErrModelChanged) {
		t.Errorf("ID changed: err = %v, want ErrModelChanged", err)
	}
	env.shim.setFilter(nil)
	_ = client.Close()
	env.recovered("lying lengths")
	env.stop()
	leaks()
}

// TestChaosGarbageRegisters decodes random registers with every schema of the
// registry: a device may answer anything. Nothing panics, and whatever
// decodes can be turned into values, encoded and decoded again unchanged.
func TestChaosGarbageRegisters(t *testing.T) {
	seed := chaosSeed(t)
	duration, soak := envDuration(t, "SUNSPEC_CHAOS_DURATION", 0)
	rng := rand.New(rand.NewSource(seed))
	decoded, stable := 0, 0
	for start := time.Now(); ; {
		decoded, stable = chaosGarbagePass(t, rng, decoded, stable)
		if !soak || time.Since(start) > duration || t.Failed() {
			break
		}
	}
	if decoded < 300 || stable < 50 {
		t.Errorf("%d models decoded, %d compared", decoded, stable)
	}
}

// chaosGarbagePass decodes a few sets of random registers per registry model
// and returns the updated counts of models decoded and compared.
func chaosGarbagePass(t *testing.T, rng *rand.Rand, decoded, stable int) (int, int) {
	for _, id := range registryIDs() {
		meta := registry.ByID(id)
		for round := 0; round < 6; round++ {
			// Around the fixed length, and sometimes much longer.
			n := meta.FixedLength() + rng.Intn(40) - 5
			if round%4 == 0 {
				n = rng.Intn(600)
			}
			if n < 0 {
				n = 0
			}
			regs := make([]uint16, n)
			for i := range regs {
				switch rng.Intn(6) {
				case 0:
					regs[i] = 0xFFFF
				case 1:
					regs[i] = 0x8000
				case 2:
					regs[i] = uint16(rng.Intn(4))
				default:
					regs[i] = uint16(rng.Intn(0x10000))
				}
			}
			if n >= 2 {
				regs[0], regs[1] = id, uint16(n-2)
			}
			dm, err := DecodeModel(regs, meta, 0)
			if err != nil {
				continue
			}
			decoded++
			if _, err := json.Marshal(dm); err != nil {
				t.Fatalf("model %d: decoded garbage does not marshal: %v", id, err)
			}
			// A model with warnings was cut short or has registers the
			// schema does not account for: its values still convert, but
			// need not describe a model that can be built.
			vals := dm.Values()
			if len(dm.Warnings) != 0 {
				continue
			}
			again, err := EncodeModel(meta, vals)
			if err != nil {
				t.Fatalf("model %d round %d (%d registers): Values() of a decoded model do not encode: %v", id, round, n, err)
			}
			dm2, err := DecodeModel(again, meta, 0)
			if err != nil {
				t.Fatalf("model %d: re-encoded model does not decode: %v", id, err)
			}
			stable++
			j1, _ := json.Marshal(dm.Group)
			j2, _ := json.Marshal(dm2.Group)
			if string(j1) != string(j2) {
				t.Fatalf("model %d round %d: decode -> Values -> encode -> decode changed the model\n%s\n%s", id, round, j1, j2)
			}
		}
	}
	return decoded, stable
}
