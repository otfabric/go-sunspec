// SPDX-License-Identifier: MIT

package devicefile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-sunspec"
	"github.com/otfabric/go-sunspec/registry"
)

// errNilDevice is returned instead of dereferencing a nil *sunspec.Device.
var errNilDevice = fmt.Errorf("devicefile: nil device, or a device without discovery result: %w", modbus.ErrUnexpectedParameters)

// Capture reads every model of device and describes the device as a device
// file that reproduces it when served: the unit ID and base address of the
// device, and each model as FromDecoded describes it.
//
// A model that does not fit its schema (an error matching sunspec.ErrDecode)
// is still captured, from its registers. Any other read error means the
// registers are not all there: Capture then returns no file and the error,
// prefixed with "read model <id>". A device file is all or nothing: with a
// model missing it would describe another device. A nil device is reported
// as an error wrapping modbus.ErrUnexpectedParameters.
func Capture(ctx context.Context, device *sunspec.Device, raw bool) (*File, error) {
	if device == nil || device.Discovery == nil {
		return nil, errNilDevice
	}
	file := New(device.UnitID, device.Discovery.BaseAddress)
	for _, inst := range device.Discovery.Models {
		dm, err := device.ReadModel(ctx, inst)
		if err != nil && !errors.Is(err, sunspec.ErrDecode) {
			return nil, fmt.Errorf("read model %d: %w", inst.Header.ID, err)
		}
		file.Models = append(file.Models, FromDecoded(dm, raw))
	}
	return file, nil
}

// FromDecoded describes a model as it was read from a device. A model that
// decoded cleanly is described by its point values, which are easy to read
// and edit; any other one (no schema, shorter or longer than its schema,
// values that would not read back the same), and every model when raw is
// set, by its registers, so that serving the file reproduces the device
// exactly.
//
// Enum and bitfield points are described by their numbers, so that values
// the schema does not name survive. A nil dm gives a Model without ID.
func FromDecoded(dm *sunspec.DecodedModel, raw bool) Model {
	if dm == nil {
		return Model{}
	}
	id := dm.ModelID
	data := dm.RawRegisters
	// A model that went through the decoder carries its two header
	// registers; one that did not holds the data registers only.
	if dm.Schema != nil && len(data) >= 2 {
		data = data[2:]
	}
	rawModel := Model{ID: &id, Name: dm.Name, Registers: append([]uint16{}, data...)}
	if raw || dm.Group == nil || dm.Schema == nil || dm.Schema.Group == nil || len(dm.Warnings) != 0 {
		return rawModel
	}
	m := Model{ID: &id, Name: dm.Name, Group: groupFromValues(dm.Schema.Group, dm.Values())}
	if !reproduces(&m, dm) {
		return rawModel
	}
	return m
}

// groupFromValues lists the values of one group instance in schema order.
func groupFromValues(meta *registry.GroupMeta, v *sunspec.Values) Group {
	var g Group
	for _, pm := range meta.Points {
		if value, ok := v.Points[pm.Name]; ok {
			g.Points = append(g.Points, Point{Name: pm.Name, Value: value})
		}
	}
	for _, child := range meta.Groups {
		for _, inst := range v.Groups[child.Name] {
			if g.Groups == nil {
				g.Groups = make(map[string][]*Group)
			}
			cg := groupFromValues(child, inst)
			g.Groups[child.Name] = append(g.Groups[child.Name], &cg)
		}
	}
	return g
}

// reproduces reports whether m, written to a file and loaded again, encodes
// to a model that decodes to the same points as dm.
func reproduces(m *Model, dm *sunspec.DecodedModel) bool {
	text, err := json.Marshal(m)
	if err != nil {
		return false
	}
	file, err := Parse([]byte(`{"models":[` + string(text) + `]}`))
	if err != nil {
		return false
	}
	values, err := file.Models[0].values("")
	if err != nil {
		return false
	}
	regs, err := sunspec.EncodeModel(dm.Schema, values)
	if err != nil || len(regs) != len(dm.RawRegisters) {
		return false
	}
	// The registers have the length of the device's, so they decode; the
	// decoded groups always marshal.
	again, _ := sunspec.DecodeModel(regs, dm.Schema, dm.InstanceAddress)
	want, _ := json.Marshal(dm.Group)
	got, _ := json.Marshal(again.Group)
	return bytes.Equal(want, got)
}

// Template describes a model with every point present and not implemented
// (null), and one instance of each nested group, as a starting point for a
// hand-written file. The model's ID and length points and its padding are
// left out: they are not values. A nil meta gives a Model without ID.
func Template(meta *registry.ModelMeta) Model {
	if meta == nil || meta.Group == nil {
		return Model{}
	}
	id := meta.ID
	return Model{ID: &id, Name: meta.Label, Group: templateGroup(meta.Group, true)}
}

func templateGroup(meta *registry.GroupMeta, top bool) Group {
	g := Group{Points: Points{}}
	for _, pm := range meta.Points {
		// The ID and length registers are not values, and padding has none.
		if (top && pm.Offset < 2) || pm.Type == "pad" {
			continue
		}
		g.Points = append(g.Points, Point{Name: pm.Name})
	}
	for _, child := range meta.Groups {
		if g.Groups == nil {
			g.Groups = make(map[string][]*Group)
		}
		cg := templateGroup(child, false)
		g.Groups[child.Name] = []*Group{&cg}
	}
	return g
}
