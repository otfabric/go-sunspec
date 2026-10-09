// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strings"

	"github.com/otfabric/go-sunspec/registry"
)

// minRegisters returns how many registers a value of the given SunSpec type
// occupies at least. Types of variable or unknown size report 0.
func minRegisters(pointType string) int {
	switch pointType {
	case "int16", "uint16", "count", "sunssf", "acc16", "enum16", "bitfield16", "pad":
		return 1
	case "int32", "uint32", "acc32", "enum32", "bitfield32", "float32":
		return 2
	case "int64", "uint64", "acc64", "bitfield64", "float64":
		return 4
	default:
		return 0
	}
}

// decodePoint decodes one point from its registers. The second result is a
// warning, or empty. A point of an unsupported type, or one with fewer
// registers than its type needs, is returned with a copy of its raw registers
// as value.
func decodePoint(regs []uint16, pm *registry.PointMeta) (DecodedPoint, string) {
	dp := DecodedPoint{
		Name:           pm.Name,
		Type:           pm.Type,
		Units:          pm.Units,
		SFName:         pm.SF,
		RegisterOffset: pm.Offset,
		RegisterCount:  pm.Size,
		Implemented:    true,
	}

	if need := minRegisters(pm.Type); len(regs) < need {
		raw := make([]uint16, len(regs))
		copy(raw, regs)
		dp.RawValue = raw
		return dp, fmt.Sprintf("point %s: type %s needs %d registers, have %d, returning raw registers",
			pm.Name, pm.Type, need, len(regs))
	}

	switch pm.Type {
	case "int16":
		v := int16(regs[0])
		if v == -32768 { // 0x8000
			dp.Implemented = false
		}
		dp.RawValue = v

	case "uint16", "count":
		v := regs[0]
		if v == 0xFFFF {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "sunssf":
		v := int16(regs[0])
		if v == -32768 {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "acc16":
		v := regs[0]
		if v == 0 {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "int32":
		v := int32(uint32(regs[0])<<16 | uint32(regs[1]))
		if v == -2147483648 { // 0x80000000
			dp.Implemented = false
		}
		dp.RawValue = v

	case "uint32":
		v := uint32(regs[0])<<16 | uint32(regs[1])
		if v == 0xFFFFFFFF {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "acc32":
		v := uint32(regs[0])<<16 | uint32(regs[1])
		if v == 0 {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "int64":
		v := int64(uint64(regs[0])<<48 | uint64(regs[1])<<32 | uint64(regs[2])<<16 | uint64(regs[3]))
		if v == math.MinInt64 {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "uint64":
		v := uint64(regs[0])<<48 | uint64(regs[1])<<32 | uint64(regs[2])<<16 | uint64(regs[3])
		if v == math.MaxUint64 {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "acc64":
		v := uint64(regs[0])<<48 | uint64(regs[1])<<32 | uint64(regs[2])<<16 | uint64(regs[3])
		if v == 0 {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "enum16":
		v := regs[0]
		if v == 0xFFFF {
			dp.Implemented = false
		}
		dp.RawValue = v
		dp.Symbols = resolveEnumSymbols(uint32(v), pm.Symbols)

	case "enum32":
		v := uint32(regs[0])<<16 | uint32(regs[1])
		if v == 0xFFFFFFFF {
			dp.Implemented = false
		}
		dp.RawValue = v
		dp.Symbols = resolveEnumSymbols(v, pm.Symbols)

	case "bitfield16":
		v := regs[0]
		if v == 0xFFFF {
			dp.Implemented = false
		}
		dp.RawValue = v
		dp.Symbols = resolveBitfieldSymbols(uint64(v), pm.Symbols)

	case "bitfield32":
		v := uint32(regs[0])<<16 | uint32(regs[1])
		if v == 0xFFFFFFFF {
			dp.Implemented = false
		}
		dp.RawValue = v
		dp.Symbols = resolveBitfieldSymbols(uint64(v), pm.Symbols)

	case "bitfield64":
		v := uint64(regs[0])<<48 | uint64(regs[1])<<32 | uint64(regs[2])<<16 | uint64(regs[3])
		if v == math.MaxUint64 {
			dp.Implemented = false
		}
		dp.RawValue = v
		dp.Symbols = resolveBitfieldSymbols(v, pm.Symbols)

	case "float32":
		bits := uint32(regs[0])<<16 | uint32(regs[1])
		v := math.Float32frombits(bits)
		if math.IsNaN(float64(v)) {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "float64":
		bits := uint64(regs[0])<<48 | uint64(regs[1])<<32 | uint64(regs[2])<<16 | uint64(regs[3])
		v := math.Float64frombits(bits)
		if math.IsNaN(v) {
			dp.Implemented = false
		}
		dp.RawValue = v

	case "string":
		// Not implemented: all NUL.
		dp.Implemented = !allRegisters(regs, 0)
		dp.RawValue = decodeString(regs)

	case "ipaddr":
		// Not implemented: 0.0.0.0.
		dp.Implemented = !allRegisters(regs, 0)
		dp.RawValue = decodeIPAddr(regs)

	case "ipv6addr":
		// Not implemented: the all-zero address.
		dp.Implemented = !allRegisters(regs, 0)
		dp.RawValue = decodeIPv6Addr(regs)

	case "eui48":
		// Not implemented: FF:FF:FF:FF:FF:FF (0x0000FFFFFFFFFFFF).
		if len(regs) >= 4 && allRegisters(regs[1:4], 0xFFFF) {
			dp.Implemented = false
		}
		dp.RawValue = decodeEUI48(regs)

	case "pad":
		dp.RawValue = regs[0]

	default:
		// Unknown type: return raw registers
		raw := make([]uint16, len(regs))
		copy(raw, regs)
		dp.RawValue = raw
		return dp, fmt.Sprintf("point %s: unsupported type %q, returning raw registers", pm.Name, pm.Type)
	}

	return dp, ""
}

// decodeString decodes a big-endian byte string, dropping trailing NULs and
// spaces.
func decodeString(regs []uint16) string {
	buf := make([]byte, len(regs)*2)
	for i, r := range regs {
		buf[i*2] = byte(r >> 8)
		buf[i*2+1] = byte(r)
	}
	// Trim NULs and trailing spaces
	s := strings.TrimRight(string(buf), "\x00 ")
	return s
}

// decodeIPAddr formats two registers as a dotted IPv4 address. It returns an
// empty string for fewer than two registers.
func decodeIPAddr(regs []uint16) string {
	if len(regs) < 2 {
		return ""
	}
	ip := net.IP([]byte{
		byte(regs[0] >> 8), byte(regs[0]),
		byte(regs[1] >> 8), byte(regs[1]),
	})
	return ip.String()
}

// decodeIPv6Addr formats eight registers as an IPv6 address. It returns an
// empty string for fewer than eight registers.
func decodeIPv6Addr(regs []uint16) string {
	if len(regs) < 8 {
		return ""
	}
	buf := make([]byte, 16)
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint16(buf[i*2:], regs[i])
	}
	ip := net.IP(buf)
	return ip.String()
}

// decodeEUI48 formats an EUI-48 point as a colon-separated MAC address. The
// point is four registers: the address is the low 48 bits (registers 1 to 3)
// and register 0 is zero padding. It returns an empty string for fewer than
// four registers.
func decodeEUI48(regs []uint16) string {
	if len(regs) < 4 {
		return ""
	}
	buf := make([]byte, 6)
	for i := 0; i < 3; i++ {
		binary.BigEndian.PutUint16(buf[i*2:], regs[i+1])
	}
	return net.HardwareAddr(buf).String()
}

// allRegisters reports whether every register equals v.
func allRegisters(regs []uint16, v uint16) bool {
	for _, r := range regs {
		if r != v {
			return false
		}
	}
	return true
}

// resolveEnumSymbols returns the name of the symbol whose value equals val, or
// nil when the schema does not list it.
func resolveEnumSymbols(val uint32, symbols []registry.SymbolMeta) []string {
	for _, s := range symbols {
		if s.Value >= 0 && uint32(s.Value) == val {
			return []string{s.Name}
		}
	}
	return nil
}

// resolveBitfieldSymbols returns the names of the symbols whose bit (the symbol
// value is the bit position) is set in val.
func resolveBitfieldSymbols(val uint64, symbols []registry.SymbolMeta) []string {
	var result []string
	for _, s := range symbols {
		if s.Value >= 0 && val&(1<<uint(s.Value)) != 0 {
			result = append(result, s.Name)
		}
	}
	return result
}
