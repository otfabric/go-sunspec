// SPDX-License-Identifier: MIT

package sunspec

import (
	"fmt"
	"math"
	"strconv"
)

// The range of a scale factor exponent: the SunSpec information model
// specification gives sunssf the range -10 to 10. A device that reports a
// value outside it has no usable scale factor; applying one such as 10^400
// would turn every measurement into an infinity, zero or NaN.
const (
	minScaleFactor = -10
	maxScaleFactor = 10
)

// validScaleFactor reports whether sf is a scale factor exponent SunSpec
// allows.
func validScaleFactor(sf int16) bool {
	return sf >= minScaleFactor && sf <= maxScaleFactor
}

// literalSF parses a scale factor reference that is a literal exponent, such
// as "-2". ok is false for a name, and for a number outside the range of a
// scale factor.
func literalSF(ref string) (int16, bool) {
	lit, err := strconv.ParseInt(ref, 10, 16)
	if err != nil || lit < minScaleFactor || lit > maxScaleFactor {
		return 0, false
	}
	return int16(lit), true
}

// applySF resolves the scale factor of every point of g and fills ScaledValue.
//
// A scale factor is either a literal exponent or the name of a sunssf point.
// A named scale factor is looked up in the point's own group instance first
// and then in the enclosing ones, so a point in a nested group (a curve point,
// say) can use a scale factor defined at the top of the model. Points whose
// scale factor is missing or not implemented at every level are left unscaled,
// as are points whose nearest implemented scale factor is outside the range
// SunSpec allows (-10 to 10); those add a warning.
func applySF(g *DecodedGroup, sc *scope, warn func(string)) {
	for i := range g.Points {
		dp := &g.Points[i]
		if dp.SFName == "" || !dp.Implemented {
			continue
		}

		// A literal exponent is carried as a decimal string.
		if lit, ok := literalSF(dp.SFName); ok {
			dp.SFRawValue = &lit
			applyScale(dp, lit)
			continue
		}

		sfv, ok := sc.scaleFactor(dp.SFName)
		if !ok {
			continue
		}
		if !validScaleFactor(sfv) {
			warn(fmt.Sprintf("point %s: scale factor %s is %d, outside %d..%d, value left unscaled",
				dp.Name, dp.SFName, sfv, minScaleFactor, maxScaleFactor))
			continue
		}
		dp.SFRawValue = &sfv
		applyScale(dp, sfv)
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
