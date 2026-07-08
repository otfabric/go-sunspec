// SPDX-License-Identifier: MIT

package sunspec

import "github.com/otfabric/go-sunspec/registry"

// DecodedModel holds the decoded output of a single model instance.
type DecodedModel struct {
	ModelID         uint16
	Name            string
	InstanceAddress uint16
	Schema          *registry.ModelMeta
	FixedBlock      *DecodedBlock
	RepeatingBlocks []*DecodedBlock
	RawRegisters    []uint16
	Warnings        []string
}

// DecodedBlock holds decoded points for one block (fixed or one repeating instance).
type DecodedBlock struct {
	GroupIndex int // 0 for fixed, 1..N for repeating instances
	Points     []DecodedPoint
}

// DecodedPoint holds a single decoded point value.
type DecodedPoint struct {
	Name           string
	Type           string
	RawValue       interface{}
	ScaledValue    *float64
	Units          string
	SFName         string
	SFRawValue     *int16
	RegisterOffset int
	RegisterCount  int
	Implemented    bool
	Symbols        []string // active enum/bitfield symbol names
}
