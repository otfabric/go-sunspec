// SPDX-License-Identifier: MIT

package sunspec

// Parallel stress tests: many goroutines discover and read a served device,
// through one client and through many, while others update its values and
// write to it, and while the Modbus server is stopped and started.
//
// They assert consistency, not only survival. The served values are built so
// that a torn or stale read is visible:
//
//   - every update of a model writes one generation number into many of its
//     points, top-level and in repeating groups, so a model read in a single
//     request must show one number in all of them;
//   - one goroutine per model produces the generations in ascending order, so
//     a reader must never see a model go back;
//   - a client writes pairs of equal registers in one request, so the pair
//     must stay equal under concurrent server-side updates of the same model,
//     and the writer must read its own write back.
//
// SUNSPEC_STRESS_DURATION (a Go duration) sets how long each test runs;
// `make soak` sets it.

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

// Positions of the stress device's models in its chain.
const (
	stressCommon = iota
	stressInverter
	stressMPPT
	stressControls
	stressVendor
	stressLong
	stressModels
)

const stressModules = 4

// stressInverterPoints are the points of model 103 that carry the generation.
var stressInverterPoints = []string{"A", "AphA", "AphB", "AphC", "PPVphAB", "PhVphA", "W", "Hz", "VA", "VAr", "DCA", "DCV", "DCW"}

// newStressServer builds the stress device at generation 1.
func newStressServer(t testing.TB) *Server {
	t.Helper()
	mppt := (&Values{}).Set("DCA_SF", 0).Set("DCV_SF", 0).Set("DCW_SF", 0).Set("N", stressModules)
	for i := 0; i < stressModules; i++ {
		mppt.Add("module", (&Values{}).Set("ID", i+1))
	}
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "Stress").Set("Md", "gen-1")},
		ServerModel{ID: 103, Values: (&Values{}).Set("A_SF", 0).Set("V_SF", 0).Set("W_SF", 0).Set("Hz_SF", 0).
			Set("VA_SF", 0).Set("VAr_SF", 0).Set("DCA_SF", 0).Set("DCV_SF", 0).Set("DCW_SF", 0)},
		ServerModel{ID: 160, Values: mppt},
		ServerModel{ID: 123, Values: (&Values{}).Set("Conn_WinTms", 1).Set("Conn_RvrtTms", 1)},
		ServerModel{ID: 64900, Registers: make([]uint16, 40)},
		ServerModel{ID: 64902, Registers: make([]uint16, 300)},
	)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for model := stressInverter; model <= stressLong; model++ {
		if err := stressSetGeneration(srv, model, 1); err != nil {
			t.Fatalf("generation 1 of model %d: %v", model, err)
		}
	}
	return srv
}

// stressSetGeneration writes generation gen into the model at index.
func stressSetGeneration(srv *Server, index int, gen int) error {
	switch index {
	case stressInverter:
		return srv.Update(index, func(v *Values) error {
			for _, name := range stressInverterPoints {
				v.Set(name, gen)
			}
			return nil
		})
	case stressMPPT:
		return srv.Update(index, func(v *Values) error {
			v.Set("TmsPer", gen)
			for _, m := range v.Groups["module"] {
				m.Set("DCA", gen).Set("DCV", gen).Set("DCW", gen)
			}
			return nil
		})
	case stressControls:
		// The points clients write (Conn_WinTms, Conn_RvrtTms) are left
		// alone: the update must keep them.
		return srv.Update(index, func(v *Values) error {
			v.Set("WMaxLimPct_WinTms", gen).Set("WMaxLimPct_RvrtTms", gen).Set("WMaxLimPct_RmpTms", gen)
			return nil
		})
	case stressVendor, stressLong:
		n := 40
		if index == stressLong {
			n = 300
		}
		regs := make([]uint16, n)
		for i := range regs {
			regs[i] = uint16(gen)
		}
		return srv.SetRegisters(index, regs)
	}
	return nil
}

// stressGeneration returns the generation a decoded model shows, or an error
// when its points do not agree: a torn read.
func stressGeneration(index int, dm *DecodedModel) (int, error) {
	gen := -1
	see := func(name string, p *DecodedPoint) error {
		if p == nil || !p.Implemented || p.ScaledValue == nil && p.RawValue == nil {
			return fmt.Errorf("point %s is missing or not implemented: %+v", name, p)
		}
		var v int
		switch raw := p.RawValue.(type) {
		case uint16:
			v = int(raw)
		case int16:
			v = int(raw)
		case uint32:
			v = int(raw)
		default:
			return fmt.Errorf("point %s has a %T value", name, p.RawValue)
		}
		if gen >= 0 && v != gen {
			return fmt.Errorf("torn read: point %s shows generation %d, earlier points %d", name, v, gen)
		}
		gen = v
		return nil
	}
	switch index {
	case stressInverter:
		for _, name := range stressInverterPoints {
			if err := see(name, dm.Group.Point(name)); err != nil {
				return 0, err
			}
		}
	case stressMPPT:
		if err := see("TmsPer", dm.Group.Point("TmsPer")); err != nil {
			return 0, err
		}
		mods := dm.Group.GroupsNamed("module")
		if len(mods) != stressModules {
			return 0, fmt.Errorf("%d modules decoded", len(mods))
		}
		for i, m := range mods {
			for _, name := range []string{"DCA", "DCV", "DCW"} {
				if err := see(fmt.Sprintf("module[%d].%s", i+1, name), m.Point(name)); err != nil {
					return 0, err
				}
			}
		}
	case stressControls:
		for _, name := range []string{"WMaxLimPct_WinTms", "WMaxLimPct_RvrtTms", "WMaxLimPct_RmpTms"} {
			if err := see(name, dm.Group.Point(name)); err != nil {
				return 0, err
			}
		}
		a, b := dm.Group.Point("Conn_WinTms"), dm.Group.Point("Conn_RvrtTms")
		if a.RawValue != b.RawValue {
			return 0, fmt.Errorf("torn write: Conn_WinTms %v, Conn_RvrtTms %v", a.RawValue, b.RawValue)
		}
	case stressVendor:
		if len(dm.RawRegisters) != 40 {
			return 0, fmt.Errorf("%d registers", len(dm.RawRegisters))
		}
		for i, r := range dm.RawRegisters {
			if r != dm.RawRegisters[0] {
				return 0, fmt.Errorf("torn read: register %d holds %d, register 0 holds %d", i, r, dm.RawRegisters[0])
			}
		}
		gen = int(dm.RawRegisters[0])
	case stressLong:
		// Three requests: the generations may differ between them, but
		// never go back, and never within one request.
		if len(dm.RawRegisters) != 300 {
			return 0, fmt.Errorf("%d registers", len(dm.RawRegisters))
		}
		for i := 1; i < 300; i++ {
			prev, cur := dm.RawRegisters[i-1], dm.RawRegisters[i]
			// The first request holds the header and 123 registers.
			boundary := i == 123 || i == 248
			if cur < prev || (cur != prev && !boundary) {
				return 0, fmt.Errorf("torn read: register %d holds %d after %d", i, cur, prev)
			}
		}
		gen = int(dm.RawRegisters[0])
	default:
		return 0, nil
	}
	return gen, nil
}

// stressRun is one stress scenario in progress.
type stressRun struct {
	t      testing.TB
	srv    *Server
	ctx    context.Context
	failed atomic.Bool
	wg     sync.WaitGroup
	// pace is slept between two updates or writes, so that the generations
	// last for the whole run: they are 16-bit values.
	pace time.Duration

	reads, readErrors, updates, writes atomic.Int64
}

func (r *stressRun) failf(format string, args ...any) {
	if r.failed.CompareAndSwap(false, true) {
		r.t.Errorf(format, args...)
	}
}

func (r *stressRun) running() bool { return r.ctx.Err() == nil && !r.failed.Load() }

// updaters starts one goroutine per model that writes ascending generations.
func (r *stressRun) updaters() {
	for model := stressInverter; model <= stressLong; model++ {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for gen := 2; r.running(); gen++ {
				if gen > 30000 {
					return
				}
				if err := stressSetGeneration(r.srv, model, gen); err != nil {
					r.failf("update of model %d to generation %d: %v", model, gen, err)
					return
				}
				r.updates.Add(1)
				// Also read it back locally: the server's own view is
				// consistent too.
				dm, err := r.srv.Model(model)
				if err != nil {
					r.failf("Server.Model(%d): %v", model, err)
					return
				}
				if got, err := stressGeneration(model, dm); err != nil || got != gen {
					r.failf("Server.Model(%d) after update %d: generation %d, %v", model, gen, got, err)
					return
				}
				time.Sleep(r.pace)
			}
		}()
	}
}

// reader discovers and reads through client until the run ends. tolerate
// says whether read errors are expected (the server is being restarted).
func (r *stressRun) reader(client *modbus.Client, seed int64, tolerate bool) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		rng := rand.New(rand.NewSource(seed))
		last := make([]int, stressModels)
		layout := r.srv.Models()
		var dev *Device

		check := func(index int, dm *DecodedModel) {
			gen, err := stressGeneration(index, dm)
			if err != nil {
				r.failf("model %d: %v", index, err)
				return
			}
			if gen < last[index] {
				r.failf("model %d went back from generation %d to %d", index, last[index], gen)
			}
			last[index] = gen
		}
		fail := func(what string, err error) {
			r.readErrors.Add(1)
			if !tolerate && r.ctx.Err() == nil {
				r.failf("%s: %v", what, err)
			}
		}

		for r.running() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			op := rng.Intn(10)
			switch {
			case dev == nil || op == 0:
				d, err := Discover(ctx, client, nil)
				if err != nil {
					fail("Discover", err)
					break
				}
				if len(d.Discovery.Models) != stressModels {
					r.failf("Discover found %d models", len(d.Discovery.Models))
				}
				for i, inst := range d.Discovery.Models {
					if inst.Header != layout[i] {
						r.failf("Discover: model %d header %+v, want %+v", i, inst.Header, layout[i])
					}
				}
				dev = d
			case op < 4:
				results, err := dev.ReadAll(ctx)
				if err != nil {
					fail("ReadAll", err)
					break
				}
				if len(results) != stressModels {
					r.failf("ReadAll returned %d models", len(results))
					break
				}
				for i, dm := range results {
					check(i, dm)
				}
			case op < 8:
				index := 1 + rng.Intn(stressModels-1)
				dm, err := dev.ReadModel(ctx, dev.Discovery.Models[index])
				if err != nil {
					fail("ReadModel", err)
					break
				}
				check(index, dm)
			default:
				p, err := dev.ReadPoint(ctx, dev.Discovery.Models[stressInverter], "W")
				if err != nil {
					fail("ReadPoint", err)
					break
				}
				if gen := int(p.RawValue.(int16)); gen < last[stressInverter] {
					r.failf("ReadPoint(W) went back from generation %d to %d", last[stressInverter], gen)
				} else {
					last[stressInverter] = gen
				}
			}
			cancel()
			r.reads.Add(1)
		}
	}()
}

// writer writes ascending pairs to the two writable points it owns and reads
// each back. id tells the writers apart: each owns the value range id*1000.
func (r *stressRun) writer(client *modbus.Client, tolerate bool) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		addr := r.srv.Models()[stressControls].StartAddress + 2
		for v := uint16(2); r.running(); v++ {
			if v > 60000 {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := client.WriteRegisters(ctx, 1, addr, []uint16{v, v})
			if err == nil {
				var got []uint16
				got, err = client.ReadRegisters(ctx, 1, addr, 2, modbus.HoldingRegister)
				if err == nil && (got[0] != v || got[1] != v) {
					r.failf("wrote %d to both registers, read back %v", v, got)
				}
			}
			// A read-only register next to them stays refused under load.
			if err == nil && v%16 == 0 {
				if werr := client.WriteRegister(ctx, 1, addr+21, v); !errors.Is(werr, modbus.ErrIllegalDataAddress) {
					if !tolerate {
						r.failf("write to a read-only point: %v", werr)
					}
				}
			}
			cancel()
			if err != nil {
				if !tolerate && r.ctx.Err() == nil {
					r.failf("write of %d: %v", v, err)
				}
				continue
			}
			r.writes.Add(1)
			time.Sleep(r.pace)
		}
	}()
}

func stressDuration(t testing.TB) time.Duration {
	d, _ := envDuration(t, "SUNSPEC_STRESS_DURATION", 1200*time.Millisecond)
	return d
}

// newStressRun starts the clock of a stress scenario.
func newStressRun(t testing.TB, srv *Server) (*stressRun, context.CancelFunc) {
	d := stressDuration(t)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	pace := d / 25000
	if pace < 50*time.Microsecond {
		pace = 50 * time.Microsecond
	}
	return &stressRun{t: t, srv: srv, ctx: ctx, pace: pace}, cancel
}

// stressClient opens a client with a pool of maxConns connections.
func stressClient(t testing.TB, addr string, maxConns int) *modbus.Client {
	t.Helper()
	client, err := modbus.New(modbus.Config{
		URL: "tcp://" + addr, Timeout: 5 * time.Second, DialTimeout: 2 * time.Second, MaxConns: maxConns, Logger: modbus.NopLogger(),
	})
	if err != nil {
		t.Fatalf("modbus.New: %v", err)
	}
	if err := client.Open(); err != nil {
		t.Fatalf("client.Open: %v", err)
	}
	return client
}

// runStress serves the stress device and reads it with the given number of
// clients, each shared by readersPerClient goroutines.
func runStress(t *testing.T, clients, readersPerClient, maxConns int) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	srv := newStressServer(t)
	addr := listenAddr(t)
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, MaxClients: 256, Logger: modbus.NopLogger()}, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	run, cancel := newStressRun(t, srv)
	defer cancel()

	var all []*modbus.Client
	for c := 0; c < clients; c++ {
		client := stressClient(t, addr, maxConns)
		all = append(all, client)
		for g := 0; g < readersPerClient; g++ {
			run.reader(client, seed+int64(c*100+g), false)
		}
	}
	// One writer: it owns the two registers it writes.
	writerClient := stressClient(t, addr, 1)
	all = append(all, writerClient)
	run.writer(writerClient, false)
	run.updaters()

	run.wg.Wait()
	for _, client := range all {
		_ = client.Close()
	}
	if err := ms.Stop(); err != nil {
		t.Errorf("server stop: %v", err)
	}
	if run.reads.Load() < 20 || run.updates.Load() < 20 || run.writes.Load() < 5 {
		t.Errorf("too little happened: %d reads, %d updates, %d writes", run.reads.Load(), run.updates.Load(), run.writes.Load())
	}
	t.Logf("%d reads, %d updates, %d client writes", run.reads.Load(), run.updates.Load(), run.writes.Load())
	leaks()
}

// TestStressOneClient shares a single connection between many readers.
func TestStressOneClient(t *testing.T) { runStress(t, 1, 8, 1) }

// TestStressPooledClient shares a client with a connection pool.
func TestStressPooledClient(t *testing.T) { runStress(t, 1, 12, 4) }

// TestStressManyClients gives every reader its own client.
func TestStressManyClients(t *testing.T) { runStress(t, 12, 1, 1) }

// TestStressServerRestart stops and starts the Modbus server while clients
// read and write and the device is updated. Calls may fail; those that
// succeed must be consistent, and after the last start everything works.
func TestStressServerRestart(t *testing.T) {
	seed := chaosSeed(t)
	leaks := leakCheck(t)
	srv := newStressServer(t)
	addr := listenAddr(t)
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, MaxClients: 256, Logger: modbus.NopLogger()}, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	run, cancel := newStressRun(t, srv)
	defer cancel()

	var all []*modbus.Client
	for c := 0; c < 6; c++ {
		client, err := modbus.New(modbus.Config{
			URL: "tcp://" + addr, Timeout: 500 * time.Millisecond, DialTimeout: 500 * time.Millisecond, MaxConns: 1 + c%2, Logger: modbus.NopLogger(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Open(); err != nil {
			t.Fatal(err)
		}
		all = append(all, client)
		run.reader(client, seed+int64(c), true)
		run.reader(client, seed+int64(c)+50, true)
	}
	run.writer(all[0], true)
	run.updaters()

	cycles := 0
	rng := rand.New(rand.NewSource(seed))
	for run.running() {
		time.Sleep(time.Duration(5+rng.Intn(40)) * time.Millisecond)
		if err := ms.Stop(); err != nil {
			t.Errorf("cycle %d: Stop: %v", cycles, err)
		}
		time.Sleep(time.Duration(rng.Intn(10)) * time.Millisecond)
		// The port was just released by this server; another process may
		// take it, but not for long.
		var startErr error
		for attempt := 0; attempt < 100; attempt++ {
			if startErr = ms.Start(); startErr == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if startErr != nil {
			t.Fatalf("cycle %d: Start: %v", cycles, startErr)
		}
		cycles++
	}
	run.wg.Wait()
	for _, client := range all {
		_ = client.Close()
	}

	// After the last start a new client reads everything, consistently.
	client := stressClient(t, addr, 1)
	dev, err := Discover(testCtx(t), client, nil)
	if err != nil {
		t.Fatalf("Discover after the restarts: %v", err)
	}
	results, err := dev.ReadAll(testCtx(t))
	if err != nil {
		t.Fatalf("ReadAll after the restarts: %v", err)
	}
	for i, dm := range results {
		if _, err := stressGeneration(i, dm); err != nil {
			t.Errorf("model %d after the restarts: %v", i, err)
		}
	}
	_ = client.Close()
	if err := ms.Stop(); err != nil {
		t.Errorf("final stop: %v", err)
	}
	if cycles < 3 || run.reads.Load() < 20 {
		t.Errorf("%d restarts, %d reads", cycles, run.reads.Load())
	}
	t.Logf("%d restarts, %d reads (%d failed), %d updates, %d client writes",
		cycles, run.reads.Load(), run.readErrors.Load(), run.updates.Load(), run.writes.Load())
	leaks()
}

// TestStressServeCycles runs Server.Serve again and again on one address
// while a client keeps reading.
func TestStressServeCycles(t *testing.T) {
	leaks := leakCheck(t)
	srv := newStressServer(t)
	addr := listenAddr(t)
	deadline := time.Now().Add(stressDuration(t))
	conf := &modbus.ServerConfig{URL: "tcp://" + addr, Logger: modbus.NopLogger()}

	stop := make(chan struct{})
	var readerDone sync.WaitGroup
	var ok atomic.Int64
	readerDone.Add(1)
	go func() {
		defer readerDone.Done()
		client, err := modbus.New(modbus.Config{URL: "tcp://" + addr, Timeout: 300 * time.Millisecond, DialTimeout: 300 * time.Millisecond, Logger: modbus.NopLogger()})
		if err != nil {
			t.Errorf("modbus.New: %v", err)
			return
		}
		defer func() { _ = client.Close() }()
		opened := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !opened {
				if client.Open() != nil {
					time.Sleep(2 * time.Millisecond)
					continue
				}
				opened = true
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			dev, err := Discover(ctx, client, nil)
			if err == nil {
				var results []*DecodedModel
				if results, err = dev.ReadAll(ctx); err == nil {
					for i, dm := range results {
						if _, cerr := stressGeneration(i, dm); cerr != nil {
							t.Errorf("model %d: %v", i, cerr)
						}
					}
					ok.Add(1)
				}
			}
			cancel()
		}
	}()

	cycles := 0
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- srv.Serve(ctx, conf) }()
		time.Sleep(30 * time.Millisecond)
		if err := stressSetGeneration(srv, stressInverter, cycles+2); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case err := <-done:
			// The address may be taken for a moment between two cycles.
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Logf("cycle %d: Serve: %v", cycles, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("cycle %d: Serve did not return\n%s", cycles, allStacks())
		}
		cycles++
	}
	close(stop)
	readerDone.Wait()
	if cycles < 3 || ok.Load() == 0 {
		t.Errorf("%d Serve cycles, %d complete reads", cycles, ok.Load())
	}
	t.Logf("%d Serve cycles, %d complete reads", cycles, ok.Load())
	leaks()
}
