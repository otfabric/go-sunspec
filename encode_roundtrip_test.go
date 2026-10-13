// SPDX-License-Identifier: MIT

package sunspec

import (
	"reflect"
	"testing"

	"github.com/otfabric/go-sunspec/registry"
)

// TestEncodeDecodeRoundTripAllModels encodes generated values for every model
// of the registry, several times each, and requires DecodeModel to return
// exactly those values.
func TestEncodeDecodeRoundTripAllModels(t *testing.T) {
	types := map[string]int{}
	points, repeated := 0, 0
	for _, id := range registryIDs() {
		meta := registry.ByID(id)
		for round := int64(0); round < 8; round++ {
			gen := newGenerator(int64(id)*100 + round)
			g := gen.model(meta)
			regs, err := EncodeModel(meta, g.vals)
			if err != nil {
				t.Fatalf("model %d round %d: EncodeModel: %v", id, round, err)
			}
			if regs[0] != id || int(regs[1]) != len(regs)-2 {
				t.Fatalf("model %d: header = %d, %d for %d registers", id, regs[0], regs[1], len(regs))
			}
			dm, err := DecodeModel(regs, meta, 0)
			if err != nil {
				t.Fatalf("model %d round %d: DecodeModel: %v", id, round, err)
			}
			if len(dm.Warnings) != 0 {
				t.Errorf("model %d round %d: warnings: %v", id, round, dm.Warnings)
			}
			points += g.check(t, dm.Group, meta.Group.Name)
			repeated += len(dm.Group.Groups)

			// Values is the inverse of the decode: encoding it again gives
			// the same registers.
			again, err := EncodeModel(meta, dm.Values())
			if err != nil {
				t.Fatalf("model %d round %d: EncodeModel(Values()): %v", id, round, err)
			}
			if !reflect.DeepEqual(again, regs) {
				t.Errorf("model %d round %d: EncodeModel(Values()) differs from the original registers", id, round)
			}
		}
		walkMeta(meta.Group, func(gm *registry.GroupMeta) {
			for _, p := range gm.Points {
				types[p.Type]++
			}
		})
		if t.Failed() {
			t.Fatalf("stopping at model %d", id)
		}
	}
	// Every type the decoder handles occurs in the registry, so all were
	// exercised.
	for _, typ := range []string{"int16", "uint16", "count", "acc16", "enum16", "bitfield16", "pad", "sunssf",
		"int32", "uint32", "acc32", "enum32", "bitfield32", "float32", "int64", "uint64", "acc64",
		"string", "ipaddr", "ipv6addr", "eui48"} {
		if types[typ] == 0 {
			t.Errorf("no registry model has a %s point", typ)
		}
	}
	if points < 10000 || repeated < 500 {
		t.Errorf("only %d points and %d group instances were compared", points, repeated)
	}
	t.Logf("%d models, %d points, %d nested group instances compared", registry.Count(), points, repeated)
}
