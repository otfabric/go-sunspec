// SPDX-License-Identifier: MIT

package sunspec

import (
	"errors"
	"fmt"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
)

var (
	// ErrNotSunSpec indicates the device does not expose a SunSpec marker.
	ErrNotSunSpec = errors.New("sunspec: device is not SunSpec-enabled")

	// ErrUnknownModel indicates the requested model ID was not present in the
	// device's discovery result (ReadModelByID). Models without a local schema
	// still appear in discovery; they are not reported with this error.
	ErrUnknownModel = errors.New("sunspec: unknown model ID")

	// ErrPointNotFound indicates a named point was not present in a decoded model.
	ErrPointNotFound = errors.New("sunspec: point not found")

	// ErrDecode indicates a point or model decoding failure.
	ErrDecode = errors.New("sunspec: decode error")

	// ErrPartialRead indicates some registers could not be read while splitting
	// a multi-chunk Modbus read. The underlying Modbus error is wrapped.
	ErrPartialRead = errors.New("sunspec: partial read")

	// ErrModelChainInvalid indicates a malformed model chain: a model with
	// length zero that is not the end marker, or a chain that runs past the
	// end of the register address space. It is the same value as
	// modbus.ErrSunSpecModelChainInvalid.
	ErrModelChainInvalid = modbus.ErrSunSpecModelChainInvalid

	// ErrModelChainLimitExceeded indicates the model chain extends further
	// from the base address than DiscoverOptions.MaxAddressSpan allows. It is
	// the same value as modbus.ErrSunSpecModelChainLimitExceeded.
	ErrModelChainLimitExceeded = modbus.ErrSunSpecModelChainLimitExceeded
)

// errNilClient is returned instead of dereferencing a nil *modbus.Client.
var errNilClient = fmt.Errorf("sunspec: nil modbus client: %w", modbus.ErrUnexpectedParameters)

// DecodeError describes a decode failure with optional location detail.
// It unwraps to ErrDecode (or another cause) for errors.Is matching.
type DecodeError struct {
	// ModelID is the model that failed to decode.
	ModelID uint16
	// PointName is the point that failed, or empty when the failure concerns
	// the model as a whole.
	PointName string
	// Offset is the register offset of the failure within its block. It is
	// only reported in the message when it is non-zero or PointName is set.
	Offset int
	// PointType is the SunSpec type of the failing point, or empty.
	PointType string
	// Message describes the failure. When empty, Err's message is used.
	Message string
	// Err is the cause. A nil Err makes the error unwrap to ErrDecode.
	Err error
}

// Error formats the failure as "sunspec: model <id> [point <name>] [type <t>]
// [offset <n>]: <message>". A nil *DecodeError formats as a generic decode
// error.
func (e *DecodeError) Error() string {
	if e == nil {
		return "sunspec: decode error"
	}
	loc := fmt.Sprintf("model %d", e.ModelID)
	if e.PointName != "" {
		loc = fmt.Sprintf("%s point %s", loc, e.PointName)
	}
	if e.PointType != "" {
		loc = fmt.Sprintf("%s type %s", loc, e.PointType)
	}
	if e.Offset != 0 || e.PointName != "" {
		loc = fmt.Sprintf("%s offset %d", loc, e.Offset)
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = "decode error"
	}
	return fmt.Sprintf("sunspec: %s: %s", loc, msg)
}

// Unwrap returns Err, or ErrDecode when Err is nil, so that
// errors.Is(err, ErrDecode) holds for every DecodeError this package creates.
// A nil *DecodeError unwraps to nil.
func (e *DecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Err != nil {
		return e.Err
	}
	return ErrDecode
}

// SunSpec protocol constants re-exported from github.com/otfabric/go-modbus/sunspec.
var (
	// SunSpecMarkerReg0 is the first register of the "SunS" marker (0x5375).
	SunSpecMarkerReg0 = gmsunspec.MarkerReg0

	// SunSpecMarkerReg1 is the second register of the "SunS" marker (0x6E53).
	SunSpecMarkerReg1 = gmsunspec.MarkerReg1

	// SunSpecEndModelID is the end-of-model-chain sentinel ID (0xFFFF).
	SunSpecEndModelID = gmsunspec.EndModelID

	// SunSpecEndModelLength is the end-of-model-chain sentinel length (0).
	SunSpecEndModelLength = gmsunspec.EndModelLength

	// SunSpecDefaultBaseAddresses are the default base addresses probed during detection.
	SunSpecDefaultBaseAddresses = gmsunspec.DefaultBaseAddresses
)
