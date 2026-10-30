// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// FuzzDecodeModel decodes arbitrary registers, as a device may report them,
// against every schema of the registry. model selects the schema by position,
// so that the fuzzer reaches all of them; data holds the registers, big-endian.
//
// DecodeModel must not panic and must keep every point inside the registers it
// was given. It fails only on registers shorter than the schema's fixed points,
// and then with ErrDecode. Values must be the inverse DecodeModel documents.
func FuzzDecodeModel(f *testing.F) {
	ids := registryIDs()
	for i, id := range ids {
		regs, err := EncodeModel(registry.ByID(id), nil)
		if err != nil {
			f.Fatalf("model %d: EncodeModel: %v", id, err)
		}
		// Whole, cut in the middle, and empty.
		f.Add(uint16(i), registerBytes(regs))
		f.Add(uint16(i), registerBytes(regs[:len(regs)/2]))
		f.Add(uint16(i), []byte{})
	}

	f.Fuzz(func(t *testing.T, model uint16, data []byte) {
		meta := registry.ByID(ids[int(model)%len(ids)])
		regs := make([]uint16, len(data)/2)
		for i := range regs {
			regs[i] = binary.BigEndian.Uint16(data[2*i:])
		}

		dm, err := DecodeModel(regs, meta, 40000)
		if err != nil {
			if !errors.Is(err, ErrDecode) {
				t.Fatalf("error does not match ErrDecode: %v", err)
			}
			if len(regs) >= meta.FixedLength() {
				t.Fatalf("%d registers hold the %d fixed ones, but: %v", len(regs), meta.FixedLength(), err)
			}
			return
		}
		if len(regs) < meta.FixedLength() {
			t.Fatalf("no error for %d registers, %d fixed ones", len(regs), meta.FixedLength())
		}
		if dm.Group == nil {
			t.Fatal("no error and no group")
		}
		walkDecoded(dm.Group, func(p *DecodedPoint) {
			if p.RegisterOffset < 0 || p.RegisterOffset+p.RegisterCount > len(regs) {
				t.Fatalf("point %s at %d+%d is outside the %d registers", p.Name, p.RegisterOffset, p.RegisterCount, len(regs))
			}
		})

		// The documented inverse: a model that decoded without warnings
		// comes back with the same points. Registers cannot be compared
		// directly: the ID and length registers are the encoder's.
		values := dm.Values()
		again, err := EncodeModel(meta, values)
		if err != nil {
			if len(dm.Warnings) == 0 {
				t.Fatalf("EncodeModel(Values()): %v", err)
			}
			return
		}
		if len(dm.Warnings) != 0 {
			return
		}
		redecoded, err := DecodeModel(again, meta, 40000)
		if err != nil {
			t.Fatalf("DecodeModel of the encoded values: %v", err)
		}
		want, got := pointList(dm.Group), pointList(redecoded.Group)
		if len(want) != len(got) {
			t.Fatalf("%d points decoded, %d after encoding them again", len(want), len(got))
		}
		for i := range want {
			if want[i] != got[i] {
				t.Errorf("point %d: decoded %s, after encoding it again %s", i, want[i], got[i])
			}
		}
	})
}

// registerBytes returns regs big-endian, the form FuzzDecodeModel takes them in.
func registerBytes(regs []uint16) []byte {
	data := make([]byte, 0, 2*len(regs))
	for _, r := range regs {
		data = binary.BigEndian.AppendUint16(data, r)
	}
	return data
}

// walkDecoded calls fn for every point of g and of the groups nested in it.
func walkDecoded(g *DecodedGroup, fn func(*DecodedPoint)) {
	for i := range g.Points {
		fn(&g.Points[i])
	}
	for _, child := range g.Groups {
		walkDecoded(child, fn)
	}
}

// pointList describes every point of g, without the header registers the
// encoder sets, in a form that compares by value: a formatted value also
// makes NaN equal to itself. The value of a point that is not implemented is
// not part of it: every such value means the same.
func pointList(g *DecodedGroup) []string {
	var list []string
	walkDecoded(g, func(p *DecodedPoint) {
		if p.RegisterOffset < 2 {
			return
		}
		if !p.Implemented {
			list = append(list, fmt.Sprintf("%s@%d not implemented", p.Name, p.RegisterOffset))
			return
		}
		list = append(list, fmt.Sprintf("%s@%d = %v", p.Name, p.RegisterOffset, p.RawValue))
	})
	return list
}
