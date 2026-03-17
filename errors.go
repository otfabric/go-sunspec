package sunspec

import (
	"errors"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
)

var (
	// ErrNotSunSpec indicates the device does not expose a SunSpec marker.
	ErrNotSunSpec = errors.New("sunspec: device is not SunSpec-enabled")

	// ErrUnknownModel indicates a model ID has no local schema definition.
	ErrUnknownModel = errors.New("sunspec: unknown model ID")

	// ErrDecode indicates a point or model decoding failure.
	ErrDecode = errors.New("sunspec: decode error")

	// ErrPartialRead indicates some registers could not be read.
	ErrPartialRead = errors.New("sunspec: partial read")

	// ErrUnsupportedPointType indicates a point type is not handled by the decoder.
	ErrUnsupportedPointType = errors.New("sunspec: unsupported point type")

	// Re-export modbus chain errors for convenience.
	ErrModelChainInvalid       = modbus.ErrSunSpecModelChainInvalid
	ErrModelChainLimitExceeded = modbus.ErrSunSpecModelChainLimitExceeded
)

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
