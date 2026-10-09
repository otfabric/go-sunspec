// SPDX-License-Identifier: MIT

package sunspec

import (
	"math"
	"strconv"
)

// applySF resolves the scale factor of every point of g and fills ScaledValue.
//
// A scale factor is either a literal exponent or the name of a sunssf point.
// A named scale factor is looked up in the point's own group instance first
// and then in the enclosing ones, so a point in a nested group (a curve point,
// say) can use a scale factor defined at the top of the model. Points whose
// scale factor is missing or not implemented at every level are left unscaled.
func applySF(g *DecodedGroup, sc *scope) {
	for i := range g.Points {
		dp := &g.Points[i]
		if dp.SFName == "" || !dp.Implemented {
			continue
		}

		// A literal exponent is carried as a decimal string.
		if lit, err := strconv.Atoi(dp.SFName); err == nil {
			sfv := int16(lit)
			dp.SFRawValue = &sfv
			applyScale(dp, sfv)
			continue
		}

		if sfv, ok := sc.scaleFactor(dp.SFName); ok {
			dp.SFRawValue = &sfv
			applyScale(dp, sfv)
		}
	}
}

// applyScale sets dp.ScaledValue to the raw value times 10^sfv. Non-numeric
// points are left unscaled.
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
	// Dividing by 10^n for a negative exponent, rather than multiplying by
	// the inexact 10^-n, keeps values such as 95 x 10^-2 at exactly 0.95.
	var scaled float64
	if sfv < 0 {
		scaled = raw / math.Pow(10, float64(-int(sfv)))
	} else {
		scaled = raw * math.Pow(10, float64(sfv))
	}
	dp.ScaledValue = &scaled
}

// scaleFactor returns the value of the nearest enclosing implemented sunssf
// point with the given name. A scale factor a nested group instance reports as
// not implemented does not hide a usable one further out.
func (s *scope) scaleFactor(name string) (int16, bool) {
	for ; s != nil; s = s.parent {
		p := s.group.Point(name)
		if p == nil || p.Type != "sunssf" || !p.Implemented {
			continue
		}
		if v, ok := p.RawValue.(int16); ok {
			return v, true
		}
	}
	return 0, false
}
