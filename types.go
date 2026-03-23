package sunspec

import (
	"github.com/otfabric/go-modbus"
	gmsunspec "github.com/otfabric/go-modbus/sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// ModelHeader describes one SunSpec model's header (ID, length, address range).
type ModelHeader struct {
	ID           uint16
	Length       uint16
	StartAddress uint16
	EndAddress   uint16
	NextAddress  uint16
	IsEndModel   bool
}

// ModelInstance represents one discovered model instance enriched with schema metadata.
type ModelInstance struct {
	Header            ModelHeader
	Name              string
	Schema            *registry.ModelMeta
	SchemaKnown       bool
	DecodingSupported bool
}

// ProbeAttempt holds one base-address probe during SunSpec detection.
type ProbeAttempt struct {
	BaseAddress uint16
	RegType     modbus.RegType
	Registers   []uint16
	Matched     bool
	Error       error  `json:"-"`
	ErrorString string `json:"error,omitempty"`
}

// DetectionResult holds the result of SunSpec marker detection.
type DetectionResult struct {
	Detected    bool
	UnitID      uint8
	RegType     modbus.RegType
	BaseAddress uint16
	Marker      [2]uint16
	Attempts    []ProbeAttempt
}

// DiscoveryResult holds the enriched discovery output.
type DiscoveryResult struct {
	BaseAddress uint16
	RegType     modbus.RegType
	Models      []ModelInstance
	Warnings    []string
	Raw         *gmsunspec.DiscoveryResult // optional low-level result for debugging
}

// Device holds a modbus client and discovery result, ready for reading.
type Device struct {
	Client    *modbus.Client
	UnitID    uint8
	RegType   modbus.RegType
	Discovery *DiscoveryResult
}

// NewDevice creates a Device from an existing client and discovery result.
func NewDevice(client *modbus.Client, unitID uint8, discovery *DiscoveryResult) *Device {
	return &Device{
		Client:    client,
		UnitID:    unitID,
		RegType:   discovery.RegType,
		Discovery: discovery,
	}
}

// ModelByID returns the first ModelInstance with the given model ID, or nil.
func (d *Device) ModelByID(id uint16) *ModelInstance {
	for i := range d.Discovery.Models {
		if d.Discovery.Models[i].Header.ID == id {
			return &d.Discovery.Models[i]
		}
	}
	return nil
}

// DiscoverOptions configures discovery behavior.
type DiscoverOptions struct {
	UnitID         uint8
	RegType        modbus.RegType
	BaseAddresses  []uint16
	MaxModels      int
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
