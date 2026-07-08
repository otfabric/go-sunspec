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

func (a *readerAdapter) ReadRawBytes(ctx context.Context, unitID uint8, addr uint16, byteCount uint16, regType gmsunspec.RegType) ([]byte, error) {
	return a.client.ReadRegisterBytes(ctx, unitID, addr, byteCount, regType)
}

// Detect checks whether a device is SunSpec-enabled.
// Returns ErrNotSunSpec if the device does not have a SunSpec marker.
func Detect(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*DetectionResult, error) {
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
