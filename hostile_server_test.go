// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/otfabric/go-modbus"
)

// Requests a hostile client may send that the Modbus framing does not allow,
// but a RequestHandler can be handed by any caller: they are refused with an
// exception, never truncated into a valid one.
func TestHostileServerRequests(t *testing.T) {
	srv := ctlServer(t, nil)
	ctx := context.Background()
	// 65536 registers: the count does not fit 16 bits; truncated it would be
	// a write of zero registers, or with one more, of one register at 40005.
	for _, n := range []int{0x10000, 0x10001} {
		args := make([]uint16, n)
		_, err := srv.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, Addr: 40005, Quantity: 1, IsWrite: true, Args: args})
		if !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("write of %d registers: err = %v", n, err)
		}
	}
	if got, _ := holdingRead(srv, 40005, 1); got[0] != 22 {
		t.Errorf("Rw = %d after refused writes", got[0])
	}
	// Ranges that end past address 65535.
	for _, r := range [][2]uint16{{0xFFFF, 0xFFFF}, {40000, 0xFFFF}, {0xFFFF, 1}} {
		if _, err := holdingRead(srv, r[0], r[1]); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("read %d+%d: err = %v", r[0], r[1], err)
		}
	}
	if err := holdingWrite(srv, 40005); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("write of no registers: err = %v", err)
	}
}

func TestConversionHelpers(t *testing.T) {
	for _, tc := range []struct {
		n  int
		ok bool
	}{{-1, false}, {0, true}, {math.MaxUint16, true}, {math.MaxUint16 + 1, false}} {
		u, ok := toUint16(tc.n)
		if ok != tc.ok || ok && int(u) != tc.n {
			t.Errorf("toUint16(%d) = %d, %v", tc.n, u, ok)
		}
		func() {
			defer func() {
				if r := recover(); (r == nil) != tc.ok {
					t.Errorf("mustUint16(%d): panic %v", tc.n, r)
				}
			}()
			if u := mustUint16(tc.n); int(u) != tc.n {
				t.Errorf("mustUint16(%d) = %d", tc.n, u)
			}
		}()
	}
	for _, tc := range []struct {
		v    int64
		want uint64
	}{{0, 0}, {1, 1}, {-1, 1}, {math.MaxInt64, math.MaxInt64}, {math.MinInt64, 1 << 63}, {math.MinInt64 + 1, math.MaxInt64}} {
		if got := magnitude(tc.v); got != tc.want {
			t.Errorf("magnitude(%d) = %d", tc.v, got)
		}
	}
	if uint16ToInt16(0x8000) != math.MinInt16 || uint32ToInt32(0x80000000) != math.MinInt32 || uint64ToInt64(1<<63) != math.MinInt64 {
		t.Error("two's complement minimum")
	}
}
