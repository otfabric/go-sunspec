// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
)

// FuzzWritePoint writes arbitrary values to arbitrary point names of the ctl
// test device over loopback TCP; with the high bit of model set, the name
// is a point of the schema instead. WritePoint must not panic; it fails only
// with a documented error, and then the device is unchanged; when it
// succeeds, only the point's registers changed and it reports what the
// device now holds.
func FuzzWritePoint(f *testing.F) {
	f.Add(uint8(0), "Rw", uint8(0), int64(33), 0.0, "", []byte{})
	f.Add(uint8(0), "Lim", uint8(1), int64(0), 2.5, "", []byte{})
	f.Add(uint8(0), "Name", uint8(2), int64(0), 0.0, "zz", []byte{})
	f.Add(uint8(0), "Wide", uint8(3), int64(0), 0.0, "", []byte{0, 1, 0, 2})
	f.Add(uint8(0), "Ro", uint8(0), int64(1), 0.0, "", []byte{})
	f.Add(uint8(2), "NCrv", uint8(0), int64(1), 0.0, "", []byte{})
	f.Add(uint8(2), "En", uint8(4), int64(0), 0.0, "ON", []byte{})
	f.Add(uint8(1), "X", uint8(0), int64(1), 0.0, "", []byte{})
	f.Add(uint8(0), "Lim_SF", uint8(0), int64(-40), 0.0, "", []byte{})

	dev, srv := writeTarget(f, ctlServer(f, nil), modbus.Config{Timeout: 2 * time.Second})
	curves := &Values{}
	curves.Add("Crv", (&Values{}).Set("X_SF", -1)).Add("Pt", (&Values{}).Set("X", 1.5).Set("Y", 3))
	snapshot := func() []uint16 {
		srv.mu.RLock()
		defer srv.mu.RUnlock()
		return slices.Clone(srv.image)
	}

	f.Fuzz(func(t *testing.T, model uint8, name string, kind uint8, i int64, x float64, s string, data []byte) {
		if err := srv.SetValues(0, ctlValues()); err != nil {
			t.Fatal(err)
		}
		if err := srv.SetValues(2, curves); err != nil {
			t.Fatal(err)
		}
		before := snapshot()
		inst := dev.Discovery.Models[int(model)%len(dev.Discovery.Models)]
		// Half the inputs name a point of the schema, so that most of them
		// reach the device; the others are any name.
		if model&0x80 != 0 && inst.Schema != nil && inst.Schema.Group != nil {
			points := inst.Schema.Group.Points
			name = points[len(name)%len(points)].Name
		}
		var value any
		switch kind % 5 {
		case 0:
			value = i
		case 1:
			value = x
		case 2:
			value = s
		case 3:
			regs := make([]uint16, len(data)/2)
			for k := range regs {
				regs[k] = binary.BigEndian.Uint16(data[2*k:])
			}
			value = regs
		case 4:
			value = []string{s}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p, err := dev.WritePoint(ctx, inst, name, value)
		after := snapshot()
		if err != nil {
			if !errors.Is(err, ErrPointNotWritable) && !errors.Is(err, ErrPointNotFound) && !errors.Is(err, ErrEncode) && !isException(err) {
				t.Fatalf("WritePoint(%q, %#v): undocumented error %v", name, value, err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("WritePoint(%q, %#v) failed with %v and changed the device", name, value, err)
			}
			return
		}
		var lo, hi int
		for _, pm := range inst.Schema.Group.Points {
			if pm.Name == name {
				lo = int(inst.Header.StartAddress) - int(srv.BaseAddress()) + pm.Offset
				hi = lo + pm.Size
			}
		}
		if pm := inst.Schema.Group.Points; hi == 0 {
			t.Fatalf("WritePoint(%q) succeeded on a point not in %v", name, pm)
		}
		for k := range after {
			if (k < lo || k >= hi) && after[k] != before[k] {
				t.Fatalf("WritePoint(%q, %#v) changed register %d", name, value, int(srv.BaseAddress())+k)
			}
		}
		index := slices.IndexFunc(dev.Discovery.Models, func(m ModelInstance) bool { return m.Header.StartAddress == inst.Header.StartAddress })
		dm, err := srv.Model(index)
		if err != nil {
			t.Fatal(err)
		}
		if held := dm.Point(name); held == nil || !reflect.DeepEqual(held.RawValue, p.RawValue) {
			t.Fatalf("WritePoint(%q, %#v) reports %#v, the device holds %+v", name, value, p.RawValue, held)
		}
	})
}
