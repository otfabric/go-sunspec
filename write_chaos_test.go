// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

// chaosWriteSafe lists the faults a request may meet in TestChaosWritePoint:
// those that lose, delay, cut, split or repeat it. Faults that change the
// bytes of a request (garbage, a corrupted function code or address) can turn
// it into another valid write, which Modbus TCP, without a checksum, cannot
// detect; responses meet every detectable fault.
var chaosWriteSafe = []chaosFault{
	faultDrop, faultDelay, faultCloseBefore, faultResetBefore, faultCloseAfter, faultTruncate, faultStall,
	faultSplit, faultDuplicate,
}

// TestChaosWritePoint writes a scaled control point through the chaos proxy.
// Invariants: no panic, no hang, no goroutine leak; no register but the
// point's ever changes; the device only ever holds a value that some call
// asked for; a call that succeeds reports such a value, never a made-up one;
// and once the faults stop, a write succeeds and reads back exactly.
func TestChaosWritePoint(t *testing.T) {
	seed := chaosSeed(t)
	duration, _ := envDuration(t, "SUNSPEC_CHAOS_DURATION", 2*time.Second)
	leaks := leakCheck(t)
	rng := rand.New(rand.NewSource(seed))

	vals := (&Values{}).Set("WMaxLimPct", 50.0).Set("WMaxLimPct_SF", -1).Set("WMaxLim_Ena", "ENABLED").Set("Conn", "CONNECT")
	srv, err := NewServer(&ServerOptions{BaseAddress: 40000},
		ServerModel{ID: 1, Values: (&Values{}).Set("Mn", "OT Fabric").Set("Md", "chaos")},
		ServerModel{ID: 123, Values: vals})
	if err != nil {
		t.Fatal(err)
	}
	addr := listenAddr(t)
	ms, err := modbus.NewServer(&modbus.ServerConfig{URL: "tcp://" + addr, MaxClients: 64, Logger: modbus.NopLogger()}, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Start(); err != nil {
		t.Fatal(err)
	}
	proxy := startChaosProxy(t, addr, seed, chaosDelay)

	direct := dial(t, addr, time.Second)
	truthDev, err := Discover(testCtx(t), direct, &DiscoverOptions{BaseAddresses: []uint16{40000}})
	if err != nil {
		t.Fatal(err)
	}
	_ = direct.Close()
	inst := *truthDev.ModelByID(123)
	var pointIndex int // index of WMaxLimPct in the server's image
	for _, pm := range inst.Schema.Group.Points {
		if pm.Name == "WMaxLimPct" {
			pointIndex = int(inst.Header.StartAddress) - int(srv.BaseAddress()) + pm.Offset
		}
	}
	snapshot := func() []uint16 {
		srv.mu.RLock()
		defer srv.mu.RUnlock()
		return slices.Clone(srv.image)
	}
	initial := snapshot()
	asked := map[uint16]bool{initial[pointIndex]: true}
	check := func(what string) {
		t.Helper()
		now := snapshot()
		for i := range now {
			if i != pointIndex && now[i] != initial[i] {
				t.Fatalf("%s: register %d changed from %d to %d", what, int(srv.BaseAddress())+i, initial[i], now[i])
			}
		}
		if !asked[now[pointIndex]] {
			t.Fatalf("%s: the device holds %d, which no call asked for", what, now[pointIndex])
		}
	}
	connect := func() *modbus.Client {
		client, err := modbus.New(modbus.Config{URL: "tcp://" + proxy.addr, Timeout: chaosTimeout, DialTimeout: time.Second, Logger: modbus.NopLogger()})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Open(); err != nil {
			return nil
		}
		return client
	}

	rounds, succeeded := 0, 0
	for start := time.Now(); time.Since(start) < duration; rounds++ {
		var enabled []chaosFault
		for _, f := range chaosDetectable {
			if rng.Intn(3) == 0 {
				enabled = append(enabled, f)
			}
		}
		if len(enabled) == 0 {
			enabled = []chaosFault{faultDrop}
		}
		rate := []float64{0.02, 0.1, 0.3}[rng.Intn(3)]
		proxy.setDecide(func(f chaosFrame, r *rand.Rand) chaosFault {
			if r.Float64() >= rate {
				return faultNone
			}
			fault := enabled[r.Intn(len(enabled))]
			if f.dir == chaosC2S && !slices.Contains(chaosWriteSafe, fault) {
				return faultNone
			}
			return fault
		})
		client := connect()
		if client == nil {
			continue
		}
		dev := NewDevice(client, 1, truthDev.Discovery)
		for k := range 3 {
			raw := uint16(rng.Intn(1001)) // 0..100.0 %
			asked[raw] = true
			what := fmt.Sprintf("round %d write %d (rate %.2f, faults %v)", rounds, k, rate, enabled)
			p, err := boundedBy(t, what, []time.Duration{chaosShort, 0}[rng.Intn(2)], func(ctx context.Context) (*DecodedPoint, error) {
				return dev.WritePoint(ctx, inst, "WMaxLimPct", float64(raw)/10)
			})
			if err == nil {
				succeeded++
				if got, ok := p.RawValue.(uint16); !ok || !asked[got] {
					t.Fatalf("%s: reports %v, which no call asked for", what, p.RawValue)
				}
			}
			check(what)
		}
		_ = client.Close()
	}

	// Late frames of the faults land before the healed write is checked.
	proxy.heal()
	time.Sleep(2 * chaosDelay)
	client := connect()
	if client == nil {
		t.Fatal("cannot connect after the faults stopped")
	}
	asked[777] = true
	p, err := bounded(t, "write after healing", func(ctx context.Context) (*DecodedPoint, error) {
		return NewDevice(client, 1, truthDev.Discovery).WritePoint(ctx, inst, "WMaxLimPct", 77.7)
	})
	if err != nil || p.RawValue != uint16(777) {
		t.Errorf("write after the faults stopped: %v, %v", p, err)
	}
	check("after healing")
	if now := snapshot(); now[pointIndex] != 777 {
		t.Errorf("after healing the device holds %d", now[pointIndex])
	}
	_ = client.Close()
	if proxy.injectedTotal() == 0 || succeeded == 0 {
		t.Errorf("%d rounds: %d faults injected, %d writes succeeded", rounds, proxy.injectedTotal(), succeeded)
	}
	t.Logf("%d rounds, %d faults injected, %d of %d writes succeeded", rounds, proxy.injectedTotal(), succeeded, rounds*3)
	proxy.stop()
	if err := ms.Stop(); err != nil {
		t.Errorf("server stop: %v", err)
	}
	leaks()
}
