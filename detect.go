// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
)

// readerAdapter satisfies gmsunspec.Reader using *modbus.Client.
type readerAdapter struct {
	client *modbus.Client
}

// ReadRawBytes reads byteCount bytes of register data starting at addr.
func (a *readerAdapter) ReadRawBytes(ctx context.Context, unitID uint8, addr uint16, byteCount uint16, regType gmsunspec.RegType) ([]byte, error) {
	return a.client.ReadRegisterBytes(ctx, unitID, addr, byteCount, regType)
}

// Detect checks whether a device is SunSpec-enabled by probing each base
// address in opts (nil selects the defaults) for the "SunS" marker.
//
// When at least one probe could be read but none held the marker, Detect
// returns a non-nil result with Detected false together with ErrNotSunSpec;
// the result's Attempts show what was read. When every probe failed, or opts
// is invalid, or ctx ended, it returns a nil result and the underlying error
// (for all-failed probes: the probe errors joined, so errors.Is matches each).
// A nil client is reported as an error wrapping modbus.ErrUnexpectedParameters.
func Detect(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*DetectionResult, error) {
	if client == nil {
		return nil, errNilClient
	}
	reader := &readerAdapter{client: client}
	gmOpts := opts.toGMSunspecOptions()
	result, err := gmsunspec.Detect(ctx, reader, gmOpts)
	if err != nil {
		return nil, err
	}
	out := &DetectionResult{
		Detected:    result.Detected,
		UnitID:      result.UnitID,
		RegType:     result.RegType,
		BaseAddress: result.BaseAddress,
		Marker:      result.Marker,
		Attempts:    make([]ProbeAttempt, len(result.Attempts)),
	}
	for i := range result.Attempts {
		a := &result.Attempts[i]
		out.Attempts[i] = ProbeAttempt{
			BaseAddress: a.BaseAddress,
			RegType:     a.RegType,
			Registers:   a.Registers,
			Matched:     a.Matched,
			Error:       a.Error,
			ErrorString: a.ErrorString,
		}
	}
	if !out.Detected {
		return out, ErrNotSunSpec
	}
	return out, nil
}
