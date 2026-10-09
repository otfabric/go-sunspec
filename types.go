// SPDX-License-Identifier: MIT

package sunspec

import (
	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// ModelHeader describes where one SunSpec model sits in the device's register
// map. All addresses are zero-based Modbus register addresses.
type ModelHeader struct {
	// ID is the SunSpec model ID read from the device (for example 1 for
	// Common, 101 for a single phase inverter).
	ID uint16
	// Length is the number of data registers that follow the two-register
	// model header (ID and L), as reported by the device.
	Length uint16
	// StartAddress is the address of the model header, i.e. of the ID
	// register. The model data starts at StartAddress+2.
	StartAddress uint16
	// EndAddress is the address of the last register of the model
	// (StartAddress+1+Length).
	EndAddress uint16
	// NextAddress is the address of the header of the model that follows
	// (EndAddress+1).
	NextAddress uint16
	// IsEndModel reports whether this is the end-of-chain marker (ID 0xFFFF,
	// length 0). Discover never returns such a header in DiscoveryResult.Models.
	IsEndModel bool
}

// ModelInstance represents one discovered model instance enriched with schema metadata.
type ModelInstance struct {
	// Header locates the model in the device's register map.
	Header ModelHeader
	// Name is the schema label, or the schema name when the label is empty,
	// or "unknown_<ID>" when the registry has no schema for the model ID.
	Name string
	// Schema is the compiled schema for Header.ID, or nil when SchemaKnown is
	// false. It is shared with the registry and must not be modified.
	Schema *registry.ModelMeta
	// SchemaKnown reports whether the registry has a schema for Header.ID.
	SchemaKnown bool
	// DecodingSupported reports whether Device.ReadModel decodes this model
	// into points. Models without it are returned as raw registers only.
	DecodingSupported bool
}

// ProbeAttempt holds one base-address probe during SunSpec detection.
type ProbeAttempt struct {
	// BaseAddress is the register address that was probed for the marker.
	BaseAddress uint16
	// RegType is the register type that was read.
	RegType modbus.RegType
	// Registers holds the two registers read at BaseAddress, or nil when the
	// read failed or returned a short response.
	Registers []uint16
	// Matched reports whether Registers holds the "SunS" marker.
	Matched bool
	// Error is the read error for this probe, or nil. It is not marshalled to
	// JSON; use ErrorString for that.
	Error error `json:"-"`
	// ErrorString is Error.Error(), or empty when the probe did not fail.
	ErrorString string `json:"error,omitempty"`
}

// DetectionResult holds the result of SunSpec marker detection.
type DetectionResult struct {
	// Detected reports whether the "SunS" marker was found.
	Detected bool
	// UnitID is the Modbus unit ID that was probed.
	UnitID uint8
	// RegType is the register type that was probed.
	RegType modbus.RegType
	// BaseAddress is the address of the marker. It is only meaningful when
	// Detected is true.
	BaseAddress uint16
	// Marker holds the two marker registers as read from the device. It is
	// only meaningful when Detected is true.
	Marker [2]uint16
	// Attempts lists every probe in the order it was made. Probing stops at
	// the first match, so a matching attempt is always the last one.
	Attempts []ProbeAttempt
}

// DiscoveryResult holds the enriched discovery output.
type DiscoveryResult struct {
	// BaseAddress is the address of the "SunS" marker; the first model header
	// is at BaseAddress+2.
	BaseAddress uint16
	// RegType is the register type the marker was found in. Model data is
	// read with the same type.
	RegType modbus.RegType
	// Models lists the models in device order, without the end-of-chain
	// marker. It is empty for a device that exposes only the marker.
	Models []ModelInstance
	// Warnings holds human-readable notes, for example one entry per model
	// without a local schema.
	Warnings []string
	// Raw is the low-level discovery result, including the detection
	// attempts and the end-of-chain header. It is kept for debugging and may
	// be nil for a DiscoveryResult that was not produced by Discover.
	Raw *gmsunspec.DiscoveryResult
}

// Device holds a modbus client and discovery result, ready for reading.
//
// The caller owns Client: Device does not open or close the connection.
// Concurrent Device use is safe to the same extent as the underlying client.
type Device struct {
	// Client is the Modbus client used for every read. It must be open.
	Client *modbus.Client
	// UnitID is the Modbus unit ID addressed by every read.
	UnitID uint8
	// RegType is the register type model data is read from.
	RegType modbus.RegType
	// Discovery is the model chain the Device reads from. A nil Discovery
	// behaves like a device without models.
	Discovery *DiscoveryResult
}

// NewDevice creates a Device from an existing client and discovery result.
//
// The Device reads with discovery.RegType. A nil discovery yields a Device
// without models that reads holding registers. NewDevice performs no I/O and
// does not take ownership of client.
func NewDevice(client *modbus.Client, unitID uint8, discovery *DiscoveryResult) *Device {
	d := &Device{
		Client:    client,
		UnitID:    unitID,
		Discovery: discovery,
	}
	if discovery != nil {
		d.RegType = discovery.RegType
	}
	return d
}

// ModelByID returns the first ModelInstance with the given model ID, or nil
// when the device does not expose that model.
//
// The returned pointer refers to the element in d.Discovery.Models, so changes
// made through it are visible to later calls.
func (d *Device) ModelByID(id uint16) *ModelInstance {
	if d.Discovery == nil {
		return nil
	}
	for i := range d.Discovery.Models {
		if d.Discovery.Models[i].Header.ID == id {
			return &d.Discovery.Models[i]
		}
	}
	return nil
}

// DiscoverOptions configures detection and discovery. A nil *DiscoverOptions
// selects the defaults described on each field.
type DiscoverOptions struct {
	// UnitID is the Modbus unit ID to address. Zero selects unit 1.
	UnitID uint8
	// RegType is the register type to probe and read. The zero value is
	// modbus.HoldingRegister, which is what nearly all SunSpec devices use.
	RegType modbus.RegType
	// BaseAddresses lists the register addresses probed for the "SunS"
	// marker, in order. Nil selects SunSpecDefaultBaseAddresses; an empty
	// non-nil slice is rejected.
	BaseAddresses []uint16
	// MaxModels caps the number of model headers read from the chain,
	// including the end marker. Zero or negative selects 256.
	MaxModels int
	// MaxAddressSpan caps how many registers past the base address the model
	// chain may extend; a longer chain fails with ErrModelChainLimitExceeded.
	// Zero disables the check.
	MaxAddressSpan uint16
}

// toGMSunspecOptions converts options to the low-level sunspec package options (internal use).
func (o *DiscoverOptions) toGMSunspecOptions() *gmsunspec.Options {
	if o == nil {
		return nil
	}
	return &gmsunspec.Options{
		UnitID:         o.UnitID,
		RegType:        o.RegType,
		BaseAddresses:  o.BaseAddresses,
		MaxModels:      o.MaxModels,
		MaxAddressSpan: o.MaxAddressSpan,
	}
}
