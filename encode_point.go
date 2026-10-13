// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strconv"

	"github.com/otfabric/go-sunspec/registry"
)

// intSpec describes how an integer point type is stored.
type intSpec struct {
	bits   int
	signed bool
	// notImpl is the register pattern of the type's "not implemented" value.
	notImpl uint64
	// reserved is true when notImpl is not a value the point can take, so
	// that giving it is an error.
	reserved bool
}

// intSpecOf returns the storage of an integer point type; ok is false for
// every other type.
func intSpecOf(pointType string) (spec intSpec, ok bool) {
	switch pointType {
	case "int16", "sunssf":
		return intSpec{16, true, 0x8000, true}, true
	case "uint16", "count", "enum16", "bitfield16":
		return intSpec{16, false, 0xFFFF, true}, true
	case "acc16":
		return intSpec{16, false, 0, false}, true
	case "pad":
		return intSpec{16, false, 0x8000, false}, true
	case "int32":
		return intSpec{32, true, 0x80000000, true}, true
	case "uint32", "enum32", "bitfield32":
		return intSpec{32, false, 0xFFFFFFFF, true}, true
	case "acc32":
		return intSpec{32, false, 0, false}, true
	case "int64":
		return intSpec{64, true, 0x8000000000000000, true}, true
	case "uint64", "bitfield64":
		return intSpec{64, false, math.MaxUint64, true}, true
	case "acc64":
		return intSpec{64, false, 0, false}, true
	default:
		return intSpec{}, false
	}
}

// Register patterns of the floating point "not implemented" values (NaN).
const (
	float32NotImpl = 0x7FC00000
	float64NotImpl = 0x7FF8000000000000
)

// number is a numeric value given for a point: an exact integer (sign and
// magnitude) or a float.
type number struct {
	isInt bool
	neg   bool
	mag   uint64
	f     float64
}

func intNumber(v int64) number {
	if v < 0 {
		// -v overflows for the minimum; the unsigned negation does not.
		return number{isInt: true, neg: true, mag: -uint64(v)}
	}
	return number{isInt: true, mag: uint64(v)}
}

func (n number) float() float64 {
	if !n.isInt {
		return n.f
	}
	if n.neg {
		return -float64(n.mag)
	}
	return float64(n.mag)
}

// toNumber converts a Go numeric value or json.Number; ok is false for
// anything else.
func toNumber(val any) (n number, ok bool) {
	switch v := val.(type) {
	case int:
		return intNumber(int64(v)), true
	case int8:
		return intNumber(int64(v)), true
	case int16:
		return intNumber(int64(v)), true
	case int32:
		return intNumber(int64(v)), true
	case int64:
		return intNumber(v), true
	case uint:
		return number{isInt: true, mag: uint64(v)}, true
	case uint8:
		return number{isInt: true, mag: uint64(v)}, true
	case uint16:
		return number{isInt: true, mag: uint64(v)}, true
	case uint32:
		return number{isInt: true, mag: uint64(v)}, true
	case uint64:
		return number{isInt: true, mag: v}, true
	case float32:
		return number{f: float64(v)}, true
	case float64:
		return number{f: v}, true
	case json.Number:
		if i, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			return intNumber(i), true
		}
		if u, err := strconv.ParseUint(string(v), 10, 64); err == nil {
			return number{isInt: true, mag: u}, true
		}
		if f, err := strconv.ParseFloat(string(v), 64); err == nil {
			return number{f: f}, true
		}
	}
	return number{}, false
}

// literalSF parses a scale factor reference that is a literal exponent, the
// way the decoder recognises one.
func literalSF(ref string) (int16, bool) {
	lit, err := strconv.Atoi(ref)
	if err != nil {
		return 0, false
	}
	return int16(lit), true
}

// unscale converts an engineering value to the raw value for the scale factor
// exponent sf: the inverse of the decoder's applyScale.
func unscale(f float64, sf int16) float64 {
	if sf < 0 {
		return f * math.Pow(10, float64(-int(sf)))
	}
	return f / math.Pow(10, float64(sf))
}

// rawInt converts n, an engineering value when sf is not nil, to the register
// pattern of an integer point.
func rawInt(n number, sf *int16, spec intSpec) (uint64, error) {
	if !n.isInt || (sf != nil && *sf != 0) {
		f := n.float()
		if sf != nil {
			f = unscale(f, *sf)
		}
		f = math.Round(f)
		// 2^64: beyond it the conversion to uint64 is undefined.
		if math.IsNaN(f) || math.Abs(f) >= 18446744073709551616.0 {
			return 0, fmt.Errorf("%v does not fit: %w", f, ErrValueOutOfRange)
		}
		n = number{isInt: true, neg: f < 0, mag: uint64(math.Abs(f))}
	}
	if n.mag == 0 {
		n.neg = false
	}

	var pattern uint64
	mask := uint64(math.MaxUint64)
	if spec.bits < 64 {
		mask = 1<<uint(spec.bits) - 1
	}
	switch {
	case spec.signed:
		limit := uint64(1) << uint(spec.bits-1)
		if (n.neg && n.mag > limit) || (!n.neg && n.mag > limit-1) {
			return 0, fmt.Errorf("%s does not fit a signed %d-bit point: %w", n, spec.bits, ErrValueOutOfRange)
		}
		pattern = n.mag
		if n.neg {
			pattern = -n.mag & mask
		}
	case n.neg:
		return 0, fmt.Errorf("%s is negative for an unsigned point: %w", n, ErrValueOutOfRange)
	default:
		if n.mag > mask {
			return 0, fmt.Errorf("%s does not fit an unsigned %d-bit point: %w", n, spec.bits, ErrValueOutOfRange)
		}
		pattern = n.mag
	}
	if spec.reserved && pattern == spec.notImpl {
		return 0, fmt.Errorf("%s is the type's not-implemented value, leave the point unset instead: %w", n, ErrValueOutOfRange)
	}
	return pattern, nil
}

// String formats an integer number for error messages.
func (n number) String() string {
	s := strconv.FormatUint(n.mag, 10)
	if n.neg {
		s = "-" + s
	}
	return s
}

// putUint writes the low bits of v into dst, most significant register first.
func putUint(dst []uint16, v uint64, bits int) {
	for i := 0; i < bits/16; i++ {
		dst[i] = uint16(v >> uint(bits-16*(i+1)))
	}
}

// encodeValue encodes one point value into pm.Size registers. sf is the
// resolved scale factor exponent, or nil when the point is stored unscaled.
// Errors are plain; the caller adds the location.
func encodeValue(pm *registry.PointMeta, val any, sf *int16) ([]uint16, error) {
	dst := make([]uint16, pm.Size)
	if raw, ok := val.([]uint16); ok {
		if len(raw) != pm.Size {
			return nil, fmt.Errorf("%d raw registers given for a point of %d registers: %w", len(raw), pm.Size, ErrValueOutOfRange)
		}
		copy(dst, raw)
		return dst, nil
	}
	if err := checkSize(pm); err != nil {
		return nil, err
	}

	if spec, ok := intSpecOf(pm.Type); ok {
		n, isNum := toNumber(val)
		if !isNum {
			sym, err := symbolValue(pm, val)
			if err != nil {
				return nil, err
			}
			// Symbols are raw values: they are never scaled.
			n, sf = number{isInt: true, mag: sym}, nil
		}
		pattern, err := rawInt(n, sf, spec)
		if err != nil {
			return nil, err
		}
		putUint(dst, pattern, spec.bits)
		return dst, nil
	}

	switch pm.Type {
	case "float32", "float64":
		n, ok := toNumber(val)
		if !ok {
			return nil, typeError(pm, val)
		}
		f := n.float()
		if sf != nil {
			f = unscale(f, *sf)
		}
		if math.IsNaN(f) {
			return nil, fmt.Errorf("NaN is the type's not-implemented value, leave the point unset instead: %w", ErrValueOutOfRange)
		}
		if pm.Type == "float64" {
			putUint(dst, math.Float64bits(f), 64)
			break
		}
		if !math.IsInf(f, 0) && math.Abs(f) > math.MaxFloat32 {
			return nil, fmt.Errorf("%v does not fit a float32 point: %w", f, ErrValueOutOfRange)
		}
		putUint(dst, uint64(math.Float32bits(float32(f))), 32)

	case "string":
		s, ok := val.(string)
		if !ok {
			return nil, typeError(pm, val)
		}
		if len(s) > pm.Size*2 {
			return nil, fmt.Errorf("string of %d bytes does not fit %d registers: %w", len(s), pm.Size, ErrValueOutOfRange)
		}
		putBytes(dst, []byte(s))

	case "ipaddr", "ipv6addr":
		s, ok := val.(string)
		if !ok {
			return nil, typeError(pm, val)
		}
		ip := net.ParseIP(s)
		if pm.Type == "ipaddr" {
			ip = ip.To4()
		}
		if ip == nil {
			return nil, fmt.Errorf("%q is not a valid %s address", s, pm.Type)
		}
		putBytes(dst, ip)

	case "eui48":
		s, ok := val.(string)
		if !ok {
			return nil, typeError(pm, val)
		}
		mac, err := net.ParseMAC(s)
		if err != nil || len(mac) != 6 {
			return nil, fmt.Errorf("%q is not a valid EUI-48 address", s)
		}
		// The address is the low 48 bits; register 0 is zero padding.
		putBytes(dst[1:], mac)

	default:
		return nil, fmt.Errorf("unsupported point type %q: give the raw registers as []uint16", pm.Type)
	}
	return dst, nil
}

// encodeNotImplemented fills dst, the registers of pm, with the "not
// implemented" value of the point's type. A point of an unsupported type is
// left zero.
func encodeNotImplemented(pm *registry.PointMeta, dst []uint16) error {
	if err := checkSize(pm); err != nil {
		return err
	}
	if spec, ok := intSpecOf(pm.Type); ok {
		putUint(dst, spec.notImpl, spec.bits)
		return nil
	}
	switch pm.Type {
	case "float32":
		putUint(dst, float32NotImpl, 32)
	case "float64":
		putUint(dst, float64NotImpl, 64)
	case "eui48":
		// 0x0000FFFFFFFFFFFF.
		dst[1], dst[2], dst[3] = 0xFFFF, 0xFFFF, 0xFFFF
	}
	return nil
}

// checkSize reports a schema point with fewer registers than its type needs.
func checkSize(pm *registry.PointMeta) error {
	need := minRegisters(pm.Type)
	switch pm.Type {
	case "ipaddr":
		need = 2
	case "ipv6addr":
		need = 8
	case "eui48":
		need = 4
	}
	if pm.Size < need {
		return fmt.Errorf("type %s needs %d registers, the schema gives it %d", pm.Type, need, pm.Size)
	}
	return nil
}

// putBytes packs b into dst, two bytes per register, high byte first. Bytes
// beyond dst are dropped; the rest of dst is left as it is.
func putBytes(dst []uint16, b []byte) {
	buf := make([]byte, len(dst)*2)
	n := copy(buf, b)
	for i := 0; i*2 < n; i++ {
		dst[i] = binary.BigEndian.Uint16(buf[i*2:])
	}
}

func typeError(pm *registry.PointMeta, val any) error {
	return fmt.Errorf("cannot use a %T as %s value", val, pm.Type)
}

// symbolValue resolves a symbolic value: the name of an enum symbol, or the
// names of the bits of a bitfield.
func symbolValue(pm *registry.PointMeta, val any) (uint64, error) {
	switch pm.Type {
	case "enum16", "enum32":
		name, ok := val.(string)
		if !ok {
			break
		}
		for _, s := range pm.Symbols {
			if s.Name == name && s.Value >= 0 {
				return uint64(s.Value), nil
			}
		}
		return 0, fmt.Errorf("the point has no symbol %q", name)
	case "bitfield16", "bitfield32", "bitfield64":
		names, ok := val.([]string)
		if !ok {
			break
		}
		var bits uint64
		for _, name := range names {
			found := false
			for _, s := range pm.Symbols {
				if s.Name == name && s.Value >= 0 && s.Value < 64 {
					bits |= 1 << uint(s.Value)
					found = true
					break
				}
			}
			if !found {
				return 0, fmt.Errorf("the point has no symbol %q", name)
			}
		}
		return bits, nil
	}
	return 0, typeError(pm, val)
}
