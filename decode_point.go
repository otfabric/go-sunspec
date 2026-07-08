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
		dp.RawValue = decodeString(regs)

	case "ipaddr":
		dp.RawValue = decodeIPAddr(regs)

	case "ipv6addr":
		dp.RawValue = decodeIPv6Addr(regs)

	case "eui48":
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

func decodeEUI48(regs []uint16) string {
	if len(regs) < 4 {
		return ""
	}
	// EUI-48 = 6 bytes in registers 0..2, register 3 is padding
	buf := make([]byte, 6)
	for i := 0; i < 3; i++ {
		buf[i*2] = byte(regs[i] >> 8)
		buf[i*2+1] = byte(regs[i])
	}
	return net.HardwareAddr(buf).String()
}

func resolveEnumSymbols(val uint32, symbols []registry.SymbolMeta) []string {
	for _, s := range symbols {
		if uint32(s.Value) == val {
			return []string{s.Name}
		}
	}
	return nil
}

func resolveBitfieldSymbols(val uint64, symbols []registry.SymbolMeta) []string {
	var result []string
	for _, s := range symbols {
		if s.Value >= 0 && val&(1<<uint(s.Value)) != 0 {
			result = append(result, s.Name)
		}
	}
	return result
}
