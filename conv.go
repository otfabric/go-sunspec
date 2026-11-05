// SPDX-License-Identifier: MIT

package sunspec

import (
	"fmt"
	"math"
)

// Two's-complement reinterpretations of register patterns, and checked
// narrowing of counts and addresses. Written as range checks the compiler and
// static analysis can follow, rather than as bare conversions.

// uint16ToInt16 reinterprets a 16-bit register pattern as a signed value.
func uint16ToInt16(u uint16) int16 {
	if u > math.MaxInt16 {
		return int16(u&math.MaxInt16) + math.MinInt16
	}
	return int16(u)
}

// uint32ToInt32 reinterprets a 32-bit register pattern as a signed value.
func uint32ToInt32(u uint32) int32 {
	if u > math.MaxInt32 {
		return int32(u&math.MaxInt32) + math.MinInt32
	}
	return int32(u)
}

// uint64ToInt64 reinterprets a 64-bit register pattern as a signed value.
func uint64ToInt64(u uint64) int64 {
	if u > math.MaxInt64 {
		return int64(u&math.MaxInt64) + math.MinInt64
	}
	return int64(u)
}

// magnitude returns |v|, also for math.MinInt64, whose negation does not fit
// an int64: the unsigned negation of v's two's-complement pattern.
func magnitude(v int64) uint64 {
	if v >= 0 {
		return uint64(v)
	}
	return -(uint64(v&math.MaxInt64) | 1<<63)
}

// mustUint16 returns n for a register address, count or length field. Callers
// bound n before (the device map is checked to fit the 16-bit address space
// when it is laid out, a model is checked against maxModelRegisters), so a
// value outside 0..65535 is a bug in this package: it panics rather than
// going out truncated.
func mustUint16(n int) uint16 {
	if n < 0 || n > math.MaxUint16 {
		panic(fmt.Sprintf("sunspec: %d does not fit in a 16-bit field", n))
	}
	return uint16(n)
}

// toUint16 returns n as a 16-bit field, or ok false when n is outside
// 0..65535.
func toUint16(n int) (u uint16, ok bool) {
	if n < 0 || n > math.MaxUint16 {
		return 0, false
	}
	return uint16(n), true
}
