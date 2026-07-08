// SPDX-License-Identifier: MIT

package sunspec

import (
	"math"
	"strconv"
)

// resolveSF resolves scale factor references for all points in a DecodedModel.
func resolveSF(dm *DecodedModel) {
	if dm.Schema == nil {
		return
	}

	// Build map of SF point values from the fixed block
	sfValues := make(map[string]int16)
	if dm.FixedBlock != nil {
		for _, dp := range dm.FixedBlock.Points {
			if dp.Type == "sunssf" && dp.Implemented {
				if v, ok := dp.RawValue.(int16); ok {
					sfValues[dp.Name] = v
				}
			}
		}
	}

	// Build map of literal SF from schema
	literalSF := make(map[string]int)
	if dm.Schema.FixedBlock != nil {
		for _, pm := range dm.Schema.FixedBlock.Points {
			if pm.SFIsLiteral {
				literalSF[pm.Name] = pm.SFLiteral
			}
		}
	}
	if dm.Schema.RepeatingBlock != nil {
		for _, pm := range dm.Schema.RepeatingBlock.Points {
			if pm.SFIsLiteral {
				literalSF[pm.Name] = pm.SFLiteral
			}
		}
	}

	// Apply SF to fixed block
	if dm.FixedBlock != nil {
		applySF(dm.FixedBlock, sfValues, literalSF)
	}
	// Apply SF to repeating blocks
	for _, rb := range dm.RepeatingBlocks {
		// Repeating blocks can also have SF points
		localSF := make(map[string]int16)
		for k, v := range sfValues {
			localSF[k] = v
		}
		for _, dp := range rb.Points {
			if dp.Type == "sunssf" && dp.Implemented {
				if v, ok := dp.RawValue.(int16); ok {
					localSF[dp.Name] = v
				}
			}
		}
		applySF(rb, localSF, literalSF)
	}
}

func applySF(block *DecodedBlock, sfValues map[string]int16, literalSF map[string]int) {
	for i := range block.Points {
		dp := &block.Points[i]
		if dp.SFName == "" || !dp.Implemented {
			continue
		}

		// Check if this is a literal SF (as a numeric string)
		if litVal, err := strconv.Atoi(dp.SFName); err == nil {
			sfv := int16(litVal)
			dp.SFRawValue = &sfv
			applyScale(dp, sfv)
			continue
		}

		// Schema-defined literal SF for this point
		if litVal, ok := literalSF[dp.SFName]; ok {
			sfv := int16(litVal)
			dp.SFRawValue = &sfv
			applyScale(dp, sfv)
			continue
		}

		// Look up point name reference
		if sfv, ok := sfValues[dp.SFName]; ok {
			dp.SFRawValue = &sfv
			applyScale(dp, sfv)
		}
	}
}

func applyScale(dp *DecodedPoint, sfv int16) {
	var raw float64
	switch v := dp.RawValue.(type) {
	case int16:
		raw = float64(v)
	case uint16:
		raw = float64(v)
	case int32:
		raw = float64(v)
	case uint32:
		raw = float64(v)
	case int64:
		raw = float64(v)
	case uint64:
		raw = float64(v)
	case float32:
		raw = float64(v)
	case float64:
		raw = v
	default:
		return
	}
	scaled := raw * math.Pow(10, float64(sfv))
	dp.ScaledValue = &scaled
}
