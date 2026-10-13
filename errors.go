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
	// still appear in discovery; they are not reported with this error. On the
	// serving side it indicates a model index a Server does not have, or a
	// ServerModel without registers whose ID has no schema in the registry.
	ErrUnknownModel = errors.New("sunspec: unknown model ID")

	// ErrPointNotFound indicates a named point was not present in a decoded model.
	ErrPointNotFound = errors.New("sunspec: point not found")

	// ErrDecode indicates a point or model decoding failure.
	ErrDecode = errors.New("sunspec: decode error")

	// ErrEncode indicates that values could not be encoded into a model's
	// registers. It is reported through *EncodeError.
	ErrEncode = errors.New("sunspec: encode error")

	// ErrValueOutOfRange indicates a value that does not fit its point: a
	// number outside the range of the point's type after scaling, a number
	// equal to the type's "not implemented" value, a string longer than the
	// point, or more group instances than the model can hold. Errors carrying
	// it also match ErrEncode.
	ErrValueOutOfRange = errors.New("sunspec: value out of range")

	// ErrModelLayout indicates models that cannot be laid out or kept in a
	// Server's register map: the chain does not fit the 16-bit address space,
	// a model has no data registers or uses the end marker's ID, or an update
	// would change the length of a model that is being served.
	ErrModelLayout = errors.New("sunspec: invalid model layout")

	// ErrPartialRead indicates some registers could not be read while splitting
	// a multi-chunk Modbus read. The underlying Modbus error is wrapped.
	ErrPartialRead = errors.New("sunspec: partial read")

	// ErrModelChanged indicates that the device no longer has the discovered
	// model at the discovered address: the ID and length registers read
	// together with the model's data differ from the discovery result. The
	// Device's layout is stale; discover again.
	ErrModelChanged = errors.New("sunspec: model changed since discovery")

	// ErrModelChainInvalid indicates a malformed model chain: a model with
	// length zero that is not the end marker, or a model that ends exactly
	// at the last register of the address space, leaving no room for the
	// next header. It is the same value as modbus.ErrSunSpecModelChainInvalid.
	//
	// A model whose announced length runs beyond address 65535 is not
	// reported with this error: go-modbus v1.4.0 returns
	// modbus.ErrProtocolError for it, or the error of the read that hits the
	// end of the address space. Discover fails in every one of these cases.
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

// EncodeError describes why values could not be encoded into a model, with
// the location of the failure. errors.Is matches it against ErrEncode and
// against its cause.
type EncodeError struct {
	// ModelID is the model that failed to encode.
	ModelID uint16
	// Group is the path of the group instance the failure concerns, for
	// example "DERCurve/Crv[2]/Pt[1]": group names from the model's top-level
	// group down, with the 1-based instance number of each nested group. It
	// is empty when the failure concerns the model as a whole.
	Group string
	// PointName is the point that failed, or empty when the failure concerns
	// a group or the model.
	PointName string
	// PointType is the SunSpec type of the failing point, or empty.
	PointType string
	// Message describes the failure. When empty, Err's message is used.
	Message string
	// Err is the cause, for example ErrValueOutOfRange or ErrPointNotFound,
	// or nil when there is none more specific than ErrEncode.
	Err error
}

// Error formats the failure as "sunspec: encode model <id> [group <path>]
// [point <name>] [type <t>]: <message>". A nil *EncodeError formats as a
// generic encode error.
func (e *EncodeError) Error() string {
	if e == nil {
		return ErrEncode.Error()
	}
	loc := fmt.Sprintf("model %d", e.ModelID)
	if e.Group != "" {
		loc = fmt.Sprintf("%s group %s", loc, e.Group)
	}
	if e.PointName != "" {
		loc = fmt.Sprintf("%s point %s", loc, e.PointName)
	}
	if e.PointType != "" {
		loc = fmt.Sprintf("%s type %s", loc, e.PointType)
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = "encode error"
	}
	return fmt.Sprintf("sunspec: encode %s: %s", loc, msg)
}

// Unwrap returns the cause, which may be nil. A nil *EncodeError unwraps to
// nil.
func (e *EncodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is reports whether target is ErrEncode, so that errors.Is(err, ErrEncode)
// holds for every EncodeError whatever its cause.
func (e *EncodeError) Is(target error) bool {
	return target == ErrEncode
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
