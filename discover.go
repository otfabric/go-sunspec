// SPDX-License-Identifier: MIT

package sunspec

import (
	"context"
	"fmt"

	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// Discover detects SunSpec and enumerates models, returning a Device ready for reading.
//
// It walks the model chain from the detected base address up to the end
// marker and attaches the registry schema to every model it knows. Models
// without a schema are kept, named "unknown_<ID>", and noted in
// Device.Discovery.Warnings; they can still be read as raw registers. A chain
// that is longer than opts.MaxModels is cut there without error; the missing
// end marker is noted in Device.Discovery.Warnings too.
//
// Discover returns ErrNotSunSpec when no base address holds the marker, and
// otherwise the Modbus or chain error (ErrModelChainInvalid,
// ErrModelChainLimitExceeded) that stopped the walk; no partial Device is
// returned. A nil client is reported as an error wrapping
// modbus.ErrUnexpectedParameters. The returned Device uses opts.UnitID
// (1 when opts is nil or the unit ID is zero) and does not own client.
func Discover(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*Device, error) {
	if client == nil {
		return nil, errNilClient
	}
	reader := &readerAdapter{client: client}
	gmOpts := opts.toGMSunspecOptions()
	raw, err := gmsunspec.Discover(ctx, reader, gmOpts)
	if err != nil {
		return nil, err
	}
	if !raw.Detection.Detected {
		return nil, ErrNotSunSpec
	}

	var unitID uint8 = 1
	if opts != nil && opts.UnitID != 0 {
		unitID = opts.UnitID
	}

	result := &DiscoveryResult{
		BaseAddress: raw.Detection.BaseAddress,
		RegType:     raw.Detection.RegType,
		Raw:         raw,
	}

	for _, hdr := range raw.Models {
		if hdr.IsEndModel {
			continue
		}
		inst := ModelInstance{
			Header: ModelHeader{
				ID:           hdr.ID,
				Length:       hdr.Length,
				StartAddress: hdr.StartAddress,
				EndAddress:   hdr.EndAddress,
				NextAddress:  hdr.NextAddress,
				IsEndModel:   hdr.IsEndModel,
			},
		}
		meta := registry.ByID(hdr.ID)
		if meta != nil {
			inst.Schema = meta
			inst.SchemaKnown = true
			inst.DecodingSupported = true
			inst.Name = meta.Label
			if inst.Name == "" {
				inst.Name = meta.Name
			}
		} else {
			inst.Name = fmt.Sprintf("unknown_%d", hdr.ID)
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("model %d: no local schema definition", hdr.ID))
		}
		result.Models = append(result.Models, inst)
	}

	// The walk stops without error when it has read MaxModels headers. A
	// chain that did not reach its end marker by then is incomplete, or is
	// not a model chain at all.
	if n := len(raw.Models); n > 0 && !raw.Models[n-1].IsEndModel {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("model chain not terminated: no end marker within the first %d models (MaxModels)", n))
	}

	return NewDevice(client, unitID, result), nil
}

// Open is a convenience that discovers and returns a Device in one step.
// It is identical to Discover.
func Open(ctx context.Context, client *modbus.Client, opts *DiscoverOptions) (*Device, error) {
	return Discover(ctx, client, opts)
}
