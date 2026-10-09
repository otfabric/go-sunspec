// SPDX-License-Identifier: MIT

package sunspec_test

import (
	"fmt"

	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

func ExampleDecodeModel() {
	meta := registry.ByID(1)
	if meta == nil {
		panic("model 1 schema missing")
	}

	// Minimal Common model registers: ID, L, then data sized to the schema.
	regs := make([]uint16, meta.FixedLength())
	regs[0] = 1
	regs[1] = uint16(meta.FixedLength() - 2)

	dm, err := sunspec.DecodeModel(regs, meta, 40000)
	if err != nil {
		panic(err)
	}

	fmt.Println(dm.ModelID, dm.Name != "", dm.Group != nil)
	// Output:
	// 1 true true
}

func Example_registry() {
	meta := registry.ByID(101)
	fmt.Println(registry.Known(101), meta != nil, registry.Count() > 0)
	all := registry.All()
	_, ok := all[101]
	fmt.Println(ok)
	// Output:
	// true true true
	// true
}
